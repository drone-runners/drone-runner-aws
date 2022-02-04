package google

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"

	"github.com/drone-runners/drone-runner-aws/internal/vmpool"
	"github.com/drone/runner-go/logger"
)

const (
	provider = "google"

	tagRunner      = vmpool.TagPrefix + "name"
	tagCreator     = vmpool.TagPrefix + "creator"
	tagPool        = vmpool.TagPrefix + "pool"
	tagStatus      = vmpool.TagPrefix + "status"
	tagStatusValue = "in-use"
)

type googlePool struct {
	name        string
	runnerName  string
	credentials Credentials
	keyPairName string

	region  string
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
	userdataKey         string

	// pool size data
	sizeMin int
	sizeMax int

	service *compute.Service
}

func (p *googlePool) GetName() string {
	panic("implement me")
}

func (p *googlePool) GetOS() string {
	panic("implement me")
}

func (p *googlePool) GetUser() string {
	panic("implement me")
}

func (p *googlePool) GetPrivateKey() string {
	panic("implement me")
}

func (p *googlePool) GetRootDir() string {
	panic("implement me")
}

func (p *googlePool) GetMaxSize() int {
	panic("implement me")
}

func (p *googlePool) GetMinSize() int {
	panic("implement me")
}

func (p *googlePool) Ping(ctx context.Context) error {
	panic("implement me")
}

func (p *googlePool) List(ctx context.Context) (busy, free []vmpool.Instance, err error) {
	panic("implement me")
}

func (p *googlePool) GetUsedInstanceByTag(ctx context.Context, tag, value string) (inst *vmpool.Instance, err error) {
	panic("implement me")
}

func (p *googlePool) Tag(ctx context.Context, instanceID string, tags map[string]string) (err error) {
	panic("implement me")
}

func (p *googlePool) TagAsInUse(ctx context.Context, instanceID string) (err error) {
	panic("implement me")
}

func (p *googlePool) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	panic("implement me")
}

func (p *googlePool) GetProviderName() string {
	return provider
}

func (p *googlePool) GetInstanceType() string {
	return p.image
}

func (p *googlePool) Provision(ctx context.Context, tagAsInUse bool) (instance *vmpool.Instance, err error) {
	p.service = p.credentials.getService()

	zone := p.zones[rand.Intn(len(p.zones))]

	logr := logger.FromContext(ctx).
		WithField("provider", provider).
		WithField("image", p.GetInstanceType()).
		WithField("pool", p.name).
		WithField("region", p.region).
		WithField("image", p.image).
		WithField("size", p.size)

	// create the instance
	startTime := time.Now()

	logr.Traceln("aws: provisioning VM")

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
		Name:           p.name,
		Zone:           fmt.Sprintf("projects/%s/zones/%s", p.credentials.ProjectID, zone),
		MinCpuPlatform: "Automatic",
		MachineType:    fmt.Sprintf("projects/%s/zones/%s/machineTypes/%s", p.credentials.ProjectID, zone, p.size),
		//Metadata: &compute.Metadata{
		//	Items: []*compute.MetadataItems{
		//		{
		//			Key:   p.userdataKey,
		//			Value: googleapi.String(buf.String()),
		//		},
		//	},
		//},
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
		Labels: p.labels,
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
		logr.WithError(err).Errorln("aws: failed to provision VM")
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
		Debugln("aws: [provision] complete")

	return nil, nil
}

func (p *googlePool) waitZoneOperation(ctx context.Context, name string, zone string) error {
	for {
		op, err := p.service.ZoneOperations.Get(p.project, zone, name).Context(ctx).Do()
		if err != nil {
			if gerr, ok := err.(*googleapi.Error); ok &&
				gerr.Code == http.StatusNotFound {
				return errors.New("Not Found")
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
