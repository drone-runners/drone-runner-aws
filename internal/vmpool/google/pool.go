package google

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone/runner-go/logger"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

const (
	provider = "google"
)

type googlePool struct {
	init sync.Once

	name        string
	runnerName  string
	credentials Credentials

	os      string
	rootDir string

	// vm instance data
	diskSize            int64
	diskType            string
	image               string
	labels              map[string]string
	network             string
	subnetwork          string
	project             string
	privateIP           bool
	scopes              []string
	serviceAccountEmail string
	size                string
	tags                []string
	zones               []string
	userData            string
	userDataKey         string

	// pool size data
	sizeMin int
	sizeMax int

	service *compute.Service
}

func (p *googlePool) GetName() string {
	return p.name
}

func (p *googlePool) GetOS() string {
	return p.os
}

func (p *googlePool) GetRootDir() string {
	return p.rootDir
}

func (p *googlePool) GetMaxSize() int {
	return p.sizeMax
}

func (p *googlePool) GetMinSize() int {
	return p.sizeMin
}

func (p *googlePool) GetZone() string {
	return p.zones[rand.Intn(len(p.zones))]
}

func (p *googlePool) Ping(ctx context.Context) error {
	client := p.credentials.getService()
	healthCheck := client.Regions.List(p.project).Context(ctx)
	response, err := healthCheck.Do()
	if err != nil {
		return err
	}
	if response.ServerResponse.HTTPStatusCode == http.StatusOK {
		return nil
	}
	return errors.New("unable to ping google")
}

func (p *googlePool) List(ctx context.Context) (busy, free []vmpool.Instance, err error) {
	client := p.credentials.getService()

	logr := logger.FromContext(ctx).
		WithField("provider", provider).
		WithField("pool", p.name)

	list, err := client.Instances.List(p.project, p.GetZone()).Context(ctx).
		Filter(fmt.Sprintf("labels.%s=%s", vmpool.TagCreator, p.runnerName)).
		Filter(fmt.Sprintf("labels.%s=%s", vmpool.TagRunner, vmpool.RunnerName)).
		Filter(fmt.Sprintf("labels.%s=%s", vmpool.TagPool, p.name)).
		Do()

	if list.Items == nil {
		return
	}

	for _, vm := range list.Items {
		logr.
			WithField("machine", vm.Name).
			WithField("id", vm.Id).
			WithField("status", vm.Status).
			Traceln("-------- list machines -----------")
		if vm.Status == "RUNNING" || vm.Status == "PROVISIONING" {
			inst := p.mapToInstance(vm)
			var isBusy bool
			for key, value := range vm.Labels {
				if key == vmpool.TagStatus {
					isBusy = value == vmpool.TagStatusValue
					break
				}
			}
			if isBusy {
				busy = append(busy, inst)
			} else {
				free = append(free, inst)
			}
		}

		if err != nil {
			logr.WithError(err).Error("unable to list zones")
			return
		}

		logr.
			WithField("free", len(free)).
			WithField("busy", len(busy)).
			Traceln("gcp: list VMs")
	}
	return
}

func (p *googlePool) GetUsedInstanceByTag(ctx context.Context, tag, value string) (inst *vmpool.Instance, err error) {
	client := p.credentials.getService()

	logr := logger.FromContext(ctx).
		WithField("provider", provider).
		WithField("pool", p.name).
		WithField("label", tag).
		WithField("label-value", value)

	list, err := client.Instances.List(p.project, p.GetZone()).Context(ctx).
		Filter(fmt.Sprintf("labels.%s=%s", vmpool.TagCreator, p.runnerName)).
		Filter(fmt.Sprintf("labels.%s=%s", vmpool.TagRunner, vmpool.RunnerName)).
		Filter(fmt.Sprintf("labels.%s=%s", vmpool.TagPool, p.name)).
		Filter(fmt.Sprintf("labels.%s=%s", vmpool.TagStatus, vmpool.TagStatusValue)).
		Filter(fmt.Sprintf("labels.%s=%s", tag, value)).
		Do()

	if err != nil {
		logr.WithError(err).
			Errorln("gcp: failed to get VM by tag")
		return
	}
	if len(list.Items) == 0 {
		err = errors.New("no VM found")
		return
	}
	vm := list.Items[0]
	if vm.Status == "RUNNING" {
		instanceMap := p.mapToInstance(vm)
		logr.
			WithField("id", inst.ID).
			WithField("ip", inst.IP).
			Traceln("gcp: found VM by tag")
		return &instanceMap, nil
	}

	return
}

func (p *googlePool) Tag(ctx context.Context, instanceID string, tags map[string]string) (err error) {
	client := p.credentials.getService()

	logr := logger.FromContext(ctx).
		WithField("id", instanceID).
		WithField("provider", provider)

	vm, err := p.getInstanceById(ctx, instanceID)
	if err != nil {
		logr.WithError(err).Errorln("gcp: failed to get VM")
	}

	for k, v := range tags {
		vm.Labels[k] = v
		logr.Traceln("gcp: adding label", k, v)
	}

	var labels = compute.InstancesSetLabelsRequest{
		Labels:           vm.Labels,
		LabelFingerprint: vm.LabelFingerprint,
	}

	_, err = client.Instances.SetLabels(p.project, p.GetZone(), instanceID, &labels).Context(ctx).Do()
	if err != nil {
		logr.WithError(err).Errorln("gcp: failed to tag VM")
	}

	logr.Traceln("gcp: VM tagged")
	return
}

func (p *googlePool) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	if len(instanceIDs) == 0 {
		return
	}

	client := p.credentials.getService()

	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("provider", provider)

	for _, instanceID := range instanceIDs {
		vm, err := p.getInstanceById(ctx, instanceID)
		if err != nil {
			logr.Errorln("gcp: failed to get VM", vm.Id)
			continue
		}
		if vm.Status == "RUNNING" || vm.Status == "PROVISIONING" {
			_, err = client.Instances.Delete(p.project, p.GetZone(), instanceID).Context(ctx).Do()
			if err != nil {
				logr.WithError(err).
					Errorln("gcp: failed to terminate VMs")
				if gerr, ok := err.(*googleapi.Error); ok &&
					gerr.Code == http.StatusNotFound {
					return errors.New("not Found")
				}
				continue
			}
			logr.Traceln("gcp: VMs terminated", vm.Name)
		}
	}
	return
}

func (p *googlePool) GetProviderName() string {
	return provider
}

func (p *googlePool) GetInstanceType() string {
	return p.image
}

func (p *googlePool) Provision(ctx context.Context, tagAsInUse bool) (instance *vmpool.Instance, err error) {
	p.service = p.credentials.getService()

	p.init.Do(func() {
		p.setup(ctx)
	})

	zone := p.GetZone()

	var name = fmt.Sprintf(p.runnerName+"-"+p.name+"-%d", time.Now().Unix())

	logr := logger.FromContext(ctx).
		WithField("provider", provider).
		WithField("name", name).
		WithField("image", p.GetInstanceType()).
		WithField("pool", p.name).
		WithField("zone", zone).
		WithField("image", p.image).
		WithField("size", p.size)

	labels := createCopy(p.labels)
	labels[vmpool.TagRunner] = vmpool.RunnerName
	labels[vmpool.TagPool] = p.name
	labels[vmpool.TagCreator] = p.runnerName
	if tagAsInUse {
		labels[vmpool.TagStatus] = vmpool.TagStatusValue
		logr.Debugln("gcp: tagging VM as in use", name)
	}

	// create the instance
	startTime := time.Now()

	logr.Traceln("gcp: provisioning VM")

	networkConfig := []*compute.AccessConfig{}

	if !p.privateIP {
		networkConfig = []*compute.AccessConfig{
			{
				Name: "External NAT",
				Type: "ONE_TO_ONE_NAT",
			},
		}
	}

	in := &compute.Instance{
		Name:           name,
		Zone:           fmt.Sprintf("projects/%s/zones/%s", p.credentials.ProjectID, zone),
		MinCpuPlatform: "Automatic",
		MachineType:    fmt.Sprintf("projects/%s/zones/%s/machineTypes/%s", p.credentials.ProjectID, zone, p.size),
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   p.userDataKey,
					Value: googleapi.String(p.userData),
				},
			},
		},
		Tags: &compute.Tags{
			Items: p.tags,
		},
		Disks: []*compute.AttachedDisk{
			{
				Type:       "PERSISTENT",
				Boot:       true,
				Mode:       "READ_WRITE",
				AutoDelete: true,
				DeviceName: p.name,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/%s", p.image),
					DiskType:    fmt.Sprintf("projects/%s/zones/%s/diskTypes/%s", p.credentials.ProjectID, zone, p.diskType),
					DiskSizeGb:  p.diskSize,
				},
			},
		},
		CanIpForward: false,
		NetworkInterfaces: []*compute.NetworkInterface{
			{
				Network:       p.network,
				Subnetwork:    p.subnetwork,
				AccessConfigs: networkConfig,
			},
		},
		Labels: labels,
		Scheduling: &compute.Scheduling{
			Preemptible:       false,
			OnHostMaintenance: "MIGRATE",
			AutomaticRestart:  googleapi.Bool(true),
		},
		DeletionProtection: false,
		ServiceAccounts: []*compute.ServiceAccount{
			{
				Scopes: p.scopes,
				Email:  p.serviceAccountEmail,
			},
		},
	}

	op, err := p.service.Instances.Insert(p.project, zone, in).Context(ctx).Do()
	if err != nil {
		logr.WithError(err).Errorln("gcp: failed to provision VM")
		return nil, err
	}

	err = p.waitZoneOperation(ctx, op.Name, zone)
	if err != nil {
		logr.WithError(err).Errorln("instance insert operation failed")
		return nil, err
	}

	logr.Debugln("instance insert operation completed")

	logr.
		WithField("ip", op.Id).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("google: [provision] VM provisioned")

	vm, err := p.service.Instances.Get(p.project, zone, name).Context(ctx).Do()
	if err != nil {
		logr.WithError(err).Errorln("gcp: failed to get VM")
		return nil, err
	}

	instanceMap := p.mapToInstance(vm)

	logr.
		WithField("ip", instanceMap.IP).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("gcp: [provision] complete")

	return &instanceMap, nil
}

func (p *googlePool) mapToInstance(vm *compute.Instance) vmpool.Instance {
	network := vm.NetworkInterfaces[0]
	accessConfigs := network.AccessConfigs[0]
	instanceIP := accessConfigs.NatIP
	creationTime, _ := time.Parse(time.RFC3339, vm.CreationTimestamp)

	return vmpool.Instance{
		ID:        vm.Name,
		IP:        instanceIP,
		Tags:      vm.Labels,
		StartedAt: creationTime,
	}
}

func (p *googlePool) waitZoneOperation(ctx context.Context, name string, zone string) error {
	for {
		op, err := p.service.ZoneOperations.Get(p.project, zone, name).Context(ctx).Do()
		if err != nil {
			if gerr, ok := err.(*googleapi.Error); ok &&
				gerr.Code == http.StatusNotFound {
				return errors.New("not Found")
			}
			return err
		}
		if op.Error != nil {
			return errors.New(op.Error.Errors[0].Message)
		}
		if op.Status == "DONE" {
			return nil
		}
		time.Sleep(time.Second)
	}
}

// helper function creates a copy of map[string]string
func createCopy(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (p *googlePool) setup(ctx context.Context) error {
	if reflect.DeepEqual(p.tags, defaultTags) {
		return p.setupFirewall(ctx)
	}
	return nil
}

func (p *googlePool) setupFirewall(ctx context.Context) error {
	logger := logger.FromContext(ctx)

	logger.Debugln("finding default firewall rules")

	_, err := p.service.Firewalls.Get(p.project, "default-allow-docker").Context(ctx).Do()
	if err == nil {
		logger.Debugln("found default firewall rule")
		return nil
	}

	rule := &compute.Firewall{
		Allowed: []*compute.FirewallAllowed{
			{
				IPProtocol: "tcp",
				Ports:      []string{"2376", "9079"},
			},
		},
		Direction:    "INGRESS",
		Name:         "default-allow-docker",
		Network:      p.network,
		Priority:     1000,
		SourceRanges: []string{"0.0.0.0/0"},
		TargetTags:   []string{"allow-docker"},
	}

	op, err := p.service.Firewalls.Insert(p.project, rule).Context(ctx).Do()
	if err != nil {
		logger.WithError(err).
			Errorln("cannot create firewall operation")
		return err
	}

	err = p.waitGlobalOperation(ctx, op.Name)
	if err != nil {
		logger.WithError(err).
			Errorln("cannot create firewall rule")
	}

	return err
}

func (p *googlePool) waitGlobalOperation(ctx context.Context, name string) error {
	for {
		op, err := p.service.GlobalOperations.Get(p.project, name).Context(ctx).Do()
		if err != nil {
			return err
		}
		if op.Error != nil {
			return errors.New(op.Error.Errors[0].Message)
		}
		if op.Status == "DONE" {
			return nil
		}
		time.Sleep(time.Second)
	}
}

func (p *googlePool) getInstanceById(ctx context.Context, instanceID string) (*compute.Instance, error) {
	client := p.credentials.getService()
	vm, err := client.Instances.Get(p.project, p.GetZone(), instanceID).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return vm, nil
}
