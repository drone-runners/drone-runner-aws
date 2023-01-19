package ankabuild

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/dchest/uniuri"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/sirupsen/logrus"
)

type config struct {
	username    string
	password    string
	rootDir     string
	vmID        string
	userData    string
	nodeID      string
	registryURL string
	authToken   string
	tag         string
	ankaClient  Client
}

const (
	startupScriptCondition = 0
	scriptFailHandler      = 2
	scriptTimeout          = 200
	ankaRunning            = "Running"
)

func New(opts ...Option) (drivers.Driver, error) {
	c := new(config)
	for _, opt := range opts {
		opt(c)
	}
	if c.ankaClient == nil {
		c.ankaClient = NewClient(c.registryURL, c.authToken)
	}
	return c, nil
}

func (c *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	startTime := time.Now()
	uData := base64.StdEncoding.EncodeToString([]byte(lehelper.GenerateUserdata(c.userData, opts)))
	machineName := fmt.Sprintf("%s-%s-%s", opts.RunnerName, opts.PoolName, uniuri.NewLen(8)) //nolint:gomnd
	logr := logger.FromContext(ctx).
		WithField("cloud", types.AnkaBuild).
		WithField("name", machineName).
		WithField("pool", opts.PoolName)

	logr.Info("starting Anka Build Setup")

	request := &createVMParams{
		Name:                   machineName,
		StartupScript:          uData,
		StartupScriptCondition: startupScriptCondition,
		ScriptMonitoring:       true,
		ScriptFailHandler:      scriptFailHandler,
		VMID:                   c.vmID,
		ScriptTimeout:          scriptTimeout,
		Tag:                    c.tag,
	}
	if c.nodeID != "" {
		request.NodeID = c.nodeID
	}

	maxRetries := 3
	retryInterval := 5 * time.Second
	retryIntervalFind := 5 * time.Second
	vm, err := c.CreateVM(ctx, request, maxRetries, retryInterval, retryIntervalFind)
	if err != nil {
		return nil, err
	}

	inst := vm.Body

	if inst.Vminfo.PortForwarding == nil {
		return nil, errors.New("ankabuild: port forwarding is not set on vm template")
	}
	port := inst.Vminfo.PortForwarding[0].HostPort
	instance = &types.Instance{
		ID:       inst.InstanceID,
		Name:     machineName,
		Provider: types.AnkaBuild,
		State:    types.StateCreated,
		Pool:     opts.PoolName,
		Platform: opts.Platform,
		Address:  inst.Vminfo.HostIP,
		CACert:   opts.CACert,
		CAKey:    opts.CAKey,
		TLSCert:  opts.TLSCert,
		TLSKey:   opts.TLSKey,
		Started:  inst.TS.Unix(),
		Updated:  time.Now().Unix(),
		Port:     int64(port),
	}
	logr.
		WithField("ip", inst.Vminfo.HostIP).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("anka build: [creation] complete")

	return instance, nil
}

func (c *config) CreateVM(ctx context.Context, request *createVMParams, maxRetries int, retryInterval, retryIntervalFind time.Duration) (*vmResponse, error) {
	var vm *vmResponse
	retry := 0
	for retry < maxRetries {
		response, err := c.ankaClient.VMCreate(ctx, request)
		if err != nil {
			if retry < maxRetries-1 {
				logrus.Infof("ankabuild: failed to create vm, retrying in %v seconds", retryInterval.Seconds())
				retry++
				time.Sleep(retryInterval)
				retryInterval *= 2
				continue
			} else {
				return nil, fmt.Errorf("failed to create vm after %d retries: %v", maxRetries, err)
			}
		}
		var id = response.Body[0]
		vm, _ = c.FindVM(ctx, id, retryIntervalFind)
		if err != nil {
			return nil, err
		}
		if vm.Body.InstanceState != "Started" {
			logrus.Errorf("ankabuild: deleting vm: %s", vm.Body.InstanceID)
			deleteErr := c.ankaClient.VMDelete(ctx, vm.Body.InstanceID)
			if deleteErr != nil {
				return nil, fmt.Errorf("failed to delete vm: %v", deleteErr)
			}
			retry++
		} else {
			break
		}
	}
	if retry == maxRetries {
		return nil, fmt.Errorf("failed to create vm after %d retries", maxRetries)
	}
	logrus.Infof("ankabuild: vm %s has started on node %s", vm.Body.InstanceID, vm.Body.NodeID)
	return vm, nil
}

func (c *config) FindVM(ctx context.Context, id string, retryInterval time.Duration) (*vmResponse, error) {
	tick := time.NewTicker(retryInterval)
	for {
		select {
		case <-ctx.Done():
			tick.Stop()
			return nil, ctx.Err()
		case <-tick.C:
			vm, err := c.ankaClient.VMFind(ctx, id)
			if err != nil {
				logrus.Infof("Failed to find vm, retrying in %v seconds", retryInterval.Seconds())
				continue
			}
			if vm.Body.InstanceState == "Scheduling" {
				logrus.Infof("ankabuild: vm %s is scheduling, retrying in %v seconds...", vm.Body.InstanceID, retryInterval.Seconds())
				continue
			}
			if vm.Body.InstanceState == "Pulling" {
				logrus.Infof("ankabuild: template tag: %s is pulling, retrying in %v seconds...", vm.Body.Tag, retryInterval.Seconds())
				continue
			}
			return vm, nil
		}
	}
}

func (c *config) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	if len(instanceIDs) == 0 {
		return
	}
	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("driver", types.AnkaBuild)

	for _, id := range instanceIDs {
		err = c.ankaClient.VMDelete(ctx, id)
		if err != nil {
			logr.WithError(err).Errorln("Anka Build: error deleting VM")
			return err
		}
	}
	return nil
}

func (c *config) Hibernate(_ context.Context, _, _ string) error {
	return errors.New("unimplemented")
}

func (c *config) Start(_ context.Context, _, _ string) (ipAddress string, err error) {
	return "", errors.New("unimplemented")
}

func (c *config) Ping(ctx context.Context) error {
	response, err := c.ankaClient.Status(ctx)
	if err != nil {
		return err
	}
	if response.Body.License == "" {
		return errors.New("license is not set")
	}
	if response.Body.RegistryAddress == "" {
		return errors.New("registry address is not set")
	}
	if response.Body.Status == ankaRunning {
		return nil
	}
	return errors.New("anka registry/controller is not running")
}

func (c *config) Logs(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (c *config) SetTags(ctx context.Context, instance *types.Instance,
	tags map[string]string) error {
	return nil
}

func (c *config) RootDir() string {
	return c.rootDir
}

func (c *config) DriverName() string {
	return string(types.AnkaBuild)
}

func (c *config) CanHibernate() bool {
	return false
}
