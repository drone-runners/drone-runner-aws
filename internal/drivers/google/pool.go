package google

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/userdata"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

func (p *provider) RootDir() string {
	return "/"
}

func (p *provider) Zone() string {
	/* #nosec */
	return p.zones[rand.Intn(len(p.zones))]
}

func (p *provider) ProviderName() string {
	return string(types.ProviderGoogle)
}

func (p *provider) InstanceType() string {
	return p.image
}

func (p *provider) CanHibernate() bool {
	return false
}

func (p *provider) OS() string {
	return p.os
}

func (p *provider) Ping(ctx context.Context) error {
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

func (p *provider) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	p.init.Do(func() {
		_ = p.setup(ctx)
	})

	var name = fmt.Sprintf(p.runnerName+"-"+p.name+"-%d", time.Now().Unix())
	zone := p.Zone()

	logr := logger.FromContext(ctx).
		WithField("cloud", types.ProviderGoogle).
		WithField("name", name).
		WithField("image", p.InstanceType()).
		WithField("pool", p.name).
		WithField("zone", zone).
		WithField("image", p.image).
		WithField("size", p.size)

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
					Value: googleapi.String(userdata.Generate(p.userData, p.os, p.arch, opts)),
				},
			},
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
		Tags: &compute.Tags{
			Items: p.tags,
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

	instanceMap := p.mapToInstance(vm, opts)
	logr.
		WithField("ip", instanceMap.Address).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("gcp: [provision] complete")

	return &instanceMap, nil
}

func (p *provider) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	if len(instanceIDs) == 0 {
		return
	}

	client := p.service

	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("cloud", types.ProviderGoogle)

	for _, instanceID := range instanceIDs {
		_, err = client.Instances.Delete(p.projectID, p.Zone(), instanceID).Context(ctx).Do()
		if err != nil {
			// https://github.com/googleapis/google-api-go-client/blob/master/googleapi/googleapi.go#L135
			if gerr, ok := err.(*googleapi.Error); ok &&
				gerr.Code == http.StatusNotFound {
				logr.WithError(err).Errorln("gcp: VM not found")
			}
		}
		_ = p.waitZoneOperation(ctx, instanceID, p.Zone())
	}
	return
}

func (p *provider) Hibernate(_ context.Context, _ string) error {
	return errors.New("Unimplemented")
}

func (p *provider) Start(_ context.Context, _ string) (string, error) {
	return "", errors.New("Unimplemented")
}

func (p *provider) mapToInstance(vm *compute.Instance, opts *types.InstanceCreateOpts) types.Instance {
	network := vm.NetworkInterfaces[0]
	accessConfigs := network.AccessConfigs[0]
	instanceIP := accessConfigs.NatIP

	started, _ := time.Parse(time.RFC3339, vm.CreationTimestamp)
	return types.Instance{
		ID:           strconv.FormatUint(vm.Id, 10), //nolint
		Name:         vm.Name,
		Provider:     types.ProviderGoogle,
		State:        types.StateCreated,
		Pool:         p.name,
		Image:        p.image,
		Zone:         p.Zone(),
		Size:         p.size,
		Platform:     p.os,
		Arch:         p.arch,
		Address:      instanceIP,
		CACert:       opts.CACert,
		CAKey:        opts.CAKey,
		TLSCert:      opts.TLSCert,
		TLSKey:       opts.TLSKey,
		Started:      started.Unix(),
		Updated:      time.Now().Unix(),
		IsHibernated: false,
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
