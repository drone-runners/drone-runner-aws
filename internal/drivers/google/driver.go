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

	"github.com/dchest/uniuri"
	"github.com/google/uuid"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const (
	maxInstanceNameLen = 63
	randStrLen         = 5
	tagRetries         = 3
	getRetries         = 3
	insertRetries      = 3
	deleteRetries      = 3
	secSleep           = 1
	tagRetrySleepMs    = 50
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
	hibernate           bool
	image               string
	network             string
	noServiceAccount    bool
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

func (p *config) RandomZone() string {
	return p.zones[rand.Intn(len(p.zones))] //nolint: gosec
}

func (p *config) GetRegion(zone string) string {
	parts := strings.Split(zone, "-")
	return strings.Join(parts[:len(parts)-1], "-")
}

func (p *config) DriverName() string {
	return string(types.Google)
}

func (p *config) InstanceType() string {
	return p.image
}

func (p *config) CanHibernate() bool {
	return p.hibernate
}

func (p *config) Logs(ctx context.Context, instance string) (string, error) {
	return "", nil
}

func (p *config) Ping(ctx context.Context) error {
	client := p.service
	response, err := client.Regions.List(p.projectID).Context(ctx).Do()
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
	inst, err := p.create(ctx, opts, name)
	if err != nil {
		defer p.Destroy(context.Background(), name) //nolint:errcheck
		return nil, err
	}
	return inst, nil
}

func (p *config) create(ctx context.Context, opts *types.InstanceCreateOpts, name string) (instance *types.Instance, err error) {
	zone := p.RandomZone()

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
	network := ""
	if p.network != "" {
		slash := strings.LastIndex(p.network, "/")
		if slash == -1 {
			network = fmt.Sprintf("projects/%s/global/networks/%s",
				p.projectID, p.network)
		} else {
			network = p.network
		}
	}
	subnet := ""
	if p.subnetwork != "" {
		slash := strings.LastIndex(p.subnetwork, "/")
		if slash == -1 {
			subnet = fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s",
				p.projectID, p.GetRegion(zone), p.subnetwork)
		} else {
			subnet = p.subnetwork
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
				Network:       network,
				Subnetwork:    subnet,
				AccessConfigs: networkConfig,
			},
		},
		Scheduling: &compute.Scheduling{
			Preemptible:       false,
			OnHostMaintenance: "MIGRATE",
			AutomaticRestart:  googleapi.Bool(true),
		},
		DeletionProtection: false,
		Tags: &compute.Tags{
			Items: p.tags,
		},
	}
	if !p.noServiceAccount {
		in.ServiceAccounts = []*compute.ServiceAccount{
			{
				Scopes: p.scopes,
				Email:  p.serviceAccountEmail,
			},
		}
	}

	requestID := uuid.New().String()
	op, err := p.insertInstance(ctx, p.projectID, zone, requestID, in)
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

	vm, err := p.getInstance(ctx, p.projectID, zone, name)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to get VM")
		return nil, err
	}

	instanceMap := p.mapToInstance(vm, zone, opts)
	logr.
		WithField("ip", instanceMap.Address).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("google: [provision] complete")

	return &instanceMap, nil
}

// Set the instance metadata (not network tags)
func (p *config) SetTags(ctx context.Context, instance *types.Instance, tags map[string]string) error {
	logr := logger.FromContext(ctx).
		WithField("id", instance.ID).
		WithField("cloud", types.Google)
	var err error
	for i := 0; i < tagRetries; i++ {
		err = p.setTags(ctx, instance, tags, logr)
		if err == nil {
			return nil
		}

		logr.WithError(err).Warnln("failed to set tags to the instance. retrying")
		time.Sleep(tagRetrySleepMs * time.Millisecond)
	}
	return err
}

func (p *config) setTags(ctx context.Context, instance *types.Instance,
	tags map[string]string, logr logger.Logger) error {
	vm, err := p.service.Instances.Get(p.projectID, instance.Zone,
		instance.ID).Context(ctx).Do()
	if err != nil {
		logr.WithError(err).Errorln("google: failed to get VM")
		return err
	}

	metadata := &compute.Metadata{
		Fingerprint: vm.Metadata.Fingerprint,
		Items:       vm.Metadata.Items,
	}
	for key, val := range tags {
		metadata.Items = append(metadata.Items, &compute.MetadataItems{
			Key:   key,
			Value: googleapi.String(val),
		})
	}
	_, err = p.service.Instances.SetMetadata(p.projectID, instance.Zone,
		instance.ID, metadata).Context(ctx).Do()
	return err
}

func (p *config) Destroy(ctx context.Context, instances []*types.Instance) (err error) {
	var instanceIDs []string
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}
	if len(instanceIDs) == 0 {
		return errors.New("no instance IDs provided")
	}

	for _, instanceID := range instanceIDs {
		logr := logger.FromContext(ctx).
			WithField("id", instanceIDs).
			WithField("cloud", types.Google)
		zone, err := p.findInstanceZone(ctx, instanceID)
		if err != nil {
			logr.WithError(err).Errorln("google: failed to find instance")
			continue
		}

		requestID := uuid.New().String()

		_, err = p.deleteInstance(ctx, p.projectID, zone, instanceID, requestID)
		if err != nil {
			// https://github.com/googleapis/google-api-go-client/blob/master/googleapi/googleapi.go#L135
			if gerr, ok := err.(*googleapi.Error); ok &&
				gerr.Code == http.StatusNotFound {
				logr.WithError(err).Errorln("google: VM not found")
			} else {
				logr.WithError(err).Errorln("google: failed to delete the VM")
			}
		}
	}
	return
}

func (p *config) Hibernate(ctx context.Context, instanceID, _ string) error {
	logr := logger.FromContext(ctx).
		WithField("id", instanceID).
		WithField("cloud", types.Google)

	zone, err := p.findInstanceZone(ctx, instanceID)
	if err != nil {
		return err
	}

	op, err := p.suspendInstance(ctx, p.projectID, zone, instanceID)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to suspend VM")
		return err
	}

	err = p.waitZoneOperation(ctx, op.Name, zone)
	if err != nil {
		logr.WithError(err).Errorln("instance suspend operation failed")
		return err
	}
	return nil
}

func (p *config) Start(ctx context.Context, instanceID, _ string) (string, error) {
	logr := logger.FromContext(ctx).
		WithField("id", instanceID).
		WithField("cloud", types.Google)

	zone, err := p.findInstanceZone(ctx, instanceID)
	if err != nil {
		return "", err
	}

	vm, err := p.getInstance(ctx, p.projectID, zone, instanceID)
	if err != nil {
		return "", err
	}
	if vm.Status != "SUSPENDED" {
		return p.getInstanceIP(vm), nil
	}

	op, err := p.resumeInstance(ctx, p.projectID, zone, instanceID)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to suspend VM")
		return "", err
	}

	err = p.waitZoneOperation(ctx, op.Name, zone)
	if err != nil {
		logr.WithError(err).Errorln("google: instance suspend operation failed")
		return "", err
	}

	vm, err = p.getInstance(ctx, p.projectID, zone, instanceID)
	if err != nil {
		logr.WithError(err).Errorln("google: failed to retrieve instance data")
		return "", err
	}
	return p.getInstanceIP(vm), nil
}

func (p *config) getInstance(ctx context.Context, projectID, zone, name string) (*compute.Instance, error) {
	return retry(ctx, getRetries, secSleep, func() (*compute.Instance, error) {
		return p.service.Instances.Get(projectID, zone, name).Context(ctx).Do()
	})
}

func (p *config) getInstanceIP(i *compute.Instance) string {
	instanceIP := ""
	network := i.NetworkInterfaces[0]
	if p.privateIP {
		instanceIP = network.NetworkIP
	} else if len(network.AccessConfigs) > 0 {
		instanceIP = network.AccessConfigs[0].NatIP
	}
	return instanceIP
}

func (p *config) suspendInstance(ctx context.Context, projectID, zone, name string) (*compute.Operation, error) {
	return retry(ctx, getRetries, secSleep, func() (*compute.Operation, error) {
		return p.service.Instances.Suspend(projectID, zone, name).Context(ctx).Do()
	})
}

func (p *config) resumeInstance(ctx context.Context, projectID, zone, name string) (*compute.Operation, error) {
	return retry(ctx, getRetries, secSleep, func() (*compute.Operation, error) {
		return p.service.Instances.Resume(projectID, zone, name).Context(ctx).Do()
	})
}

func (p *config) insertInstance(ctx context.Context, projectID, zone, requestID string, in *compute.Instance) (*compute.Operation, error) {
	return retry(ctx, insertRetries, secSleep, func() (*compute.Operation, error) {
		return p.service.Instances.Insert(projectID, zone, in).RequestId(requestID).Context(ctx).Do()
	})
}

func (p *config) deleteInstance(ctx context.Context, projectID, zone, instanceID, requestID string) (*compute.Operation, error) {
	return retry(ctx, deleteRetries, secSleep, func() (*compute.Operation, error) {
		return p.service.Instances.Delete(projectID, zone, instanceID).RequestId(requestID).Context(ctx).Do()
	})
}

func (p *config) mapToInstance(vm *compute.Instance, zone string, opts *types.InstanceCreateOpts) types.Instance {
	network := vm.NetworkInterfaces[0]
	instanceIP := ""
	if p.privateIP {
		instanceIP = network.NetworkIP
	} else {
		instanceIP = network.AccessConfigs[0].NatIP
	}

	started, _ := time.Parse(time.RFC3339, vm.CreationTimestamp)
	return types.Instance{
		ID:           strconv.FormatUint(vm.Id, 10),
		Name:         vm.Name,
		Provider:     types.Google, // this is driver, though its the old legacy name of provider
		State:        types.StateCreated,
		Pool:         opts.PoolName,
		Image:        p.image,
		Zone:         zone,
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

func (p *config) findInstanceZone(ctx context.Context, instanceID string) (
	string, error) {
	for _, zone := range p.zones {
		_, err := p.getInstance(ctx, p.projectID, zone, instanceID)
		if err == nil {
			return zone, nil
		}

		if gerr, ok := err.(*googleapi.Error); ok &&
			gerr.Code == http.StatusNotFound {
			continue
		}
		logger.FromContext(ctx).
			WithField("instance", instanceID).
			WithField("zone", zone).
			Errorln("google: failed to fetch the VM")
	}
	return "", fmt.Errorf("failed to find vm")
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
			if shouldRetry(err) {
				logger.FromContext(ctx).
					WithField("name", name).
					WithField("zone", zone).
					Warnf("google: wait operation failed with retryable error: %s. retrying\n", err)
				time.Sleep(time.Second)
				continue
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
			if shouldRetry(err) {
				time.Sleep(time.Second)
				continue
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

// instance name must be 1-63 characters long and match the regular expression
// [a-z]([-a-z0-9]*[a-z0-9])?
func getInstanceName(runner, pool string) string {
	namePrefix := strings.ReplaceAll(runner, " ", "")
	randStr, _ := randStringRunes(randStrLen)
	name := strings.ToLower(fmt.Sprintf("%s-%s-%s-%s", namePrefix, pool, uniuri.NewLen(8), randStr)) //nolint:gomnd
	return substrSuffix(name, maxInstanceNameLen)
}

func shouldRetry(err error) bool {
	switch e := err.(type) {
	case *googleapi.Error:
		// Retry on 429 and 5xx, according to
		// https://cloud.google.com/storage/docs/exponential-backoff.
		return e.Code == 429 || (e.Code >= 500 && e.Code < 600)
	case interface{ Temporary() bool }:
		return e.Temporary()
	default:
		return false
	}
}

func retry[T any](ctx context.Context, attempts, sleepSecs int, f func() (T, error)) (result T, err error) {
	for i := 0; i < attempts; i++ {
		if i > 0 {
			logger.FromContext(ctx).Warnf("retrying after error: %s\n", err)
			time.Sleep(time.Duration(sleepSecs) * time.Second)
		}
		result, err = f()
		if err == nil {
			return result, nil
		}

		if !shouldRetry(err) {
			return result, err
		}
	}
	return result, err
}
