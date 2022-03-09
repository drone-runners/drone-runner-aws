package google

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"reflect"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone/runner-go/logger"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

const (
	cloud = "google"
)

func (p *provider) GetName() string {
	return p.name
}

func (p *provider) GetOS() string {
	return p.os
}

func (p *provider) GetRootDir() string {
	return "/"
}

func (p *provider) GetMaxSize() int {
	return p.limit
}

func (p *provider) GetMinSize() int {
	return p.pool
}

func (p *provider) GetZone() string {
	/* #nosec */
	return p.zones[rand.Intn(len(p.zones))]
}

func (p *provider) CheckProvider(ctx context.Context) error {
	client := p.service
	healthCheck := client.Regions.List(p.projectID).Context(ctx)
	response, err := healthCheck.Do()
	if err != nil {
		return err
	}
	if response.ServerResponse.HTTPStatusCode == http.StatusOK {
		return nil
	}
	return errors.New("unable to ping google")
}

func (p *provider) List(ctx context.Context) (busy, free []drivers.Instance, err error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("cloud", cloud).
		WithField("pool", p.name)

	list, err := client.Instances.List(p.projectID, p.GetZone()).Context(ctx).
		Filter(fmt.Sprintf("labels.%s=%s", drivers.TagCreator, p.runnerName)).
		Filter(fmt.Sprintf("labels.%s=%s", drivers.TagRunner, drivers.RunnerName)).
		Filter(fmt.Sprintf("labels.%s=%s", drivers.TagPool, p.name)).
		Do()

	if list.Items == nil {
		return
	}

	for _, vm := range list.Items {
		if vm.Status == "RUNNING" || vm.Status == "PROVISIONING" {
			inst := p.mapToInstance(vm)
			var isBusy bool
			for key, value := range vm.Labels {
				if key == drivers.TagStatus {
					isBusy = value == drivers.TagStatusValue
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

func (p *provider) GetUsedInstanceByTag(ctx context.Context, tag, value string) (inst *drivers.Instance, err error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("cloud", cloud).
		WithField("pool", p.name).
		WithField("label", tag).
		WithField("label-value", value)

	list, err := client.Instances.List(p.projectID, p.GetZone()).Context(ctx).
		Filter(fmt.Sprintf("labels.%s=%s", drivers.TagCreator, p.runnerName)).
		Filter(fmt.Sprintf("labels.%s=%s", drivers.TagRunner, drivers.RunnerName)).
		Filter(fmt.Sprintf("labels.%s=%s", drivers.TagPool, p.name)).
		Filter(fmt.Sprintf("labels.%s=%s", drivers.TagStatus, drivers.TagStatusValue)).
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

func (p *provider) Tag(ctx context.Context, instanceID string, tags map[string]string) (err error) {
	client := p.service

	logr := logger.FromContext(ctx).
		WithField("id", instanceID).
		WithField("cloud", cloud)

	vm, err := p.getInstanceByID(ctx, instanceID)
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

	_, err = client.Instances.SetLabels(p.projectID, p.GetZone(), instanceID, &labels).Context(ctx).Do()
	if err != nil {
		logr.WithError(err).Errorln("gcp: failed to tag VM")
	}
	// required as there's a delay in labels getting set on GCP side
	for {
		updatedVM, _ := p.getInstanceByID(ctx, instanceID)
		if len(updatedVM.Labels) == len(vm.Labels) {
			break
		}
	}

	logr.Traceln("gcp: VM tagged")
	return
}

func (p *provider) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	if len(instanceIDs) == 0 {
		return
	}

	client := p.service

	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("cloud", cloud)

	for _, instanceID := range instanceIDs {
		_, err = client.Instances.Delete(p.projectID, p.GetZone(), instanceID).Context(ctx).Do()
		if err != nil {
			// https://github.com/googleapis/google-api-go-client/blob/master/googleapi/googleapi.go#L135
			if gerr, ok := err.(*googleapi.Error); ok &&
				gerr.Code == http.StatusNotFound {
				logr.WithError(err).Errorln("gcp: VM not found")
			}
		}
		_ = p.waitZoneOperation(ctx, instanceID, p.GetZone())
	}
	return
}

func (p *provider) GetProviderName() string {
	return cloud
}

func (p *provider) GetInstanceType() string {
	return p.image
}

func (p *provider) Create(ctx context.Context, tagAsInUse bool) (instance *drivers.Instance, err error) {
	p.init.Do(func() {
		_ = p.setup(ctx)
	})

	zone := p.GetZone()

	var name = fmt.Sprintf(p.runnerName+"-"+p.name+"-%d", time.Now().Unix())

	logr := logger.FromContext(ctx).
		WithField("cloud", cloud).
		WithField("name", name).
		WithField("image", p.GetInstanceType()).
		WithField("pool", p.name).
		WithField("zone", zone).
		WithField("image", p.image).
		WithField("size", p.size)

	labels := createCopy(p.labels)
	labels[drivers.TagRunner] = drivers.RunnerName
	labels[drivers.TagPool] = p.name
	labels[drivers.TagCreator] = p.runnerName
	if tagAsInUse {
		labels[drivers.TagStatus] = drivers.TagStatusValue
		logr.Debugln("gcp: tagging VM as in use", name)
	}

	// create the instance
	startTime := time.Now()

	logr.Traceln("gcp: creating VM")

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
		Zone:           fmt.Sprintf("projects/%s/zones/%s", p.projectID, zone),
		MinCpuPlatform: "Automatic",
		MachineType:    fmt.Sprintf("projects/%s/zones/%s/machineTypes/%s", p.projectID, zone, p.size),
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
					DiskType:    fmt.Sprintf("projects/%s/zones/%s/diskTypes/%s", p.projectID, zone, p.diskType),
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

	op, err := p.service.Instances.Insert(p.projectID, zone, in).Context(ctx).Do()
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

	vm, err := p.service.Instances.Get(p.projectID, zone, name).Context(ctx).Do()
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

func (p *provider) mapToInstance(vm *compute.Instance) drivers.Instance {
	network := vm.NetworkInterfaces[0]
	accessConfigs := network.AccessConfigs[0]
	instanceIP := accessConfigs.NatIP
	creationTime, _ := time.Parse(time.RFC3339, vm.CreationTimestamp)

	return drivers.Instance{
		ID:        vm.Name,
		IP:        instanceIP,
		Tags:      vm.Labels,
		StartedAt: creationTime,
	}
}

func (p *provider) waitZoneOperation(ctx context.Context, name, zone string) error {
	for {
		client := p.service
		op, err := client.ZoneOperations.Get(p.projectID, zone, name).Context(ctx).Do()
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

func (p *provider) setup(ctx context.Context) error {
	if reflect.DeepEqual(p.tags, defaultTags) {
		return p.setupFirewall(ctx)
	}
	return nil
}

func (p *provider) setupFirewall(ctx context.Context) error {
	logr := logger.FromContext(ctx)

	logr.Debugln("finding default firewall rules")

	_, err := p.service.Firewalls.Get(p.projectID, "default-allow-docker").Context(ctx).Do()
	if err == nil {
		logr.Debugln("found default firewall rule")
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

	op, err := p.service.Firewalls.Insert(p.projectID, rule).Context(ctx).Do()
	if err != nil {
		logr.WithError(err).
			Errorln("cannot create firewall operation")
		return err
	}

	err = p.waitGlobalOperation(ctx, op.Name)
	if err != nil {
		logr.WithError(err).
			Errorln("cannot create firewall rule")
	}

	return err
}

func (p *provider) waitGlobalOperation(ctx context.Context, name string) error {
	for {
		op, err := p.service.GlobalOperations.Get(p.projectID, name).Context(ctx).Do()
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

func (p *provider) getInstanceByID(ctx context.Context, instanceID string) (*compute.Instance, error) {
	client := p.service
	vm, err := client.Instances.Get(p.projectID, p.GetZone(), instanceID).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return vm, nil
}
