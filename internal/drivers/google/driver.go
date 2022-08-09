package google

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	maxInstanceNameLen = 63
	randStrLen         = 5
)

var (
	defaultTags = []string{
		"allow-docker",
	}

	defaultScopes = []string{
		"https://www.googleapis.com/auth/devstorage.read_only",
		"https://www.googleapis.com/auth/logging.write",
		"https://www.googleapis.com/auth/monitoring.write",
		"https://www.googleapis.com/auth/trace.append",
	}
)

type config struct {
	init sync.Once

	projectID string
	JSONPath  string
	JSON      []byte

	rootDir string

	// vm instance data
	diskSize            int64
	diskType            string
	image               string
	network             string
	subnetwork          string
	privateIP           bool
	scopes              []string
	serviceAccountEmail string
	size                string
	tags                []string
	zones               []string
	userData            string
	userDataKey         string
	service             *compute.Service
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}

	ctx := context.Background()
	var err error
	if p.service == nil {
		if p.JSONPath != "" {
			p.service, err = compute.NewService(ctx, option.WithCredentialsFile(p.JSONPath))
		} else {
			p.service, err = compute.NewService(ctx)
		}

		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (p *config) RootDir() string {
	return p.rootDir
}

func (p *config) Zone() string {
	return p.zones[rand.Intn(len(p.zones))] //nolint: gosec
}

func (p *config) DriverName() string {
	return string(types.Google)
}

func (p *config) InstanceType() string {
	return p.image
}

func (p *config) CanHibernate() bool {
	return false
}

func (p *config) Logs(ctx context.Context, instance string) (string, error) {
	return "", nil
}

func (p *config) Ping(ctx context.Context) error {
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

func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	p.init.Do(func() {
		_ = p.setup(ctx)
	})

	var name = getInstanceName(opts.RunnerName, opts.PoolName)
	zone := p.Zone()

	logr := logger.FromContext(ctx).
		WithField("cloud", types.Google).
		WithField("name", name).
		WithField("image", p.InstanceType()).
		WithField("pool", opts.PoolName).
		WithField("zone", zone).
		WithField("image", p.image).
		WithField("size", p.size)

	// create the instance
	startTime := time.Now()

	logr.Traceln("google: creating VM")

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
					Value: googleapi.String(lehelper.GenerateUserdata(p.userData, opts)),
				},
			},
		},
		Disks: []*compute.AttachedDisk{
			{
				Type:       "PERSISTENT",
				Boot:       true,
				Mode:       "READ_WRITE",
				AutoDelete: true,
				DeviceName: opts.PoolName,
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
		logr.WithError(err).Errorln("google: failed to provision VM")
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
		logr.WithError(err).Errorln("google: failed to get VM")
		return nil, err
	}

	instanceMap := p.mapToInstance(vm, opts)
	logr.
		WithField("ip", instanceMap.Address).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("google: [provision] complete")

	return &instanceMap, nil
}

func (p *config) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	if len(instanceIDs) == 0 {
		return
	}

	client := p.service

	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("cloud", types.Google)

	for _, instanceID := range instanceIDs {
		_, err = client.Instances.Delete(p.projectID, p.Zone(), instanceID).Context(ctx).Do()
		if err != nil {
			// https://github.com/googleapis/google-api-go-client/blob/master/googleapi/googleapi.go#L135
			if gerr, ok := err.(*googleapi.Error); ok &&
				gerr.Code == http.StatusNotFound {
				logr.WithError(err).Errorln("google: VM not found")
			}
		}
		_ = p.waitZoneOperation(ctx, instanceID, p.Zone())
	}
	return
}

func (p *config) Hibernate(_ context.Context, _, _ string) error {
	return errors.New("unimplemented")
}

func (p *config) Start(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("unimplemented")
}

func (p *config) mapToInstance(vm *compute.Instance, opts *types.InstanceCreateOpts) types.Instance {
	network := vm.NetworkInterfaces[0]
	accessConfigs := network.AccessConfigs[0]
	instanceIP := accessConfigs.NatIP

	started, _ := time.Parse(time.RFC3339, vm.CreationTimestamp)
	return types.Instance{
		ID:           strconv.FormatUint(vm.Id, 10), //nolint
		Name:         vm.Name,
		Provider:     types.Google, // this is driver, though its the old legacy name of provider
		State:        types.StateCreated,
		Pool:         opts.PoolName,
		Image:        p.image,
		Zone:         p.Zone(),
		Size:         p.size,
		Platform:     opts.Platform,
		Address:      instanceIP,
		CACert:       opts.CACert,
		CAKey:        opts.CAKey,
		TLSCert:      opts.TLSCert,
		TLSKey:       opts.TLSKey,
		Started:      started.Unix(),
		Updated:      time.Now().Unix(),
		IsHibernated: false,
		Port:         lehelper.LiteEnginePort,
	}
}

func (p *config) waitZoneOperation(ctx context.Context, name, zone string) error {
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

func (p *config) setup(ctx context.Context) error {
	if reflect.DeepEqual(p.tags, defaultTags) {
		return p.setupFirewall(ctx)
	}
	return nil
}

func (p *config) setupFirewall(ctx context.Context) error {
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
				Ports:      []string{"2376", fmt.Sprint(lehelper.LiteEnginePort)},
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

func (p *config) waitGlobalOperation(ctx context.Context, name string) error {
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

// instance name must be 1-63 characters long and match the regular expression
// [a-z]([-a-z0-9]*[a-z0-9])?
func getInstanceName(runner, pool string) string {
	namePrefix := strings.ReplaceAll(runner, " ", "")
	randStr, _ := randStringRunes(randStrLen)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%d-%s", namePrefix, pool,
		time.Now().Unix(), randStr))

	return substrSuffix(name, maxInstanceNameLen)
}
