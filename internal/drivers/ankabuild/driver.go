package ankabuild

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

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
	scriptTimeout          = 500
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

func (c config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	startTime := time.Now()
	uData := base64.StdEncoding.EncodeToString([]byte(lehelper.GenerateUserdata(c.userData, opts)))
	machineName := fmt.Sprintf(opts.RunnerName+"-"+"-%d", startTime.Unix()) //nolint

	logr := logger.FromContext(ctx).
		WithField("cloud", types.AnkaBuild).
		WithField("name", machineName).
		WithField("pool", opts.PoolName)

	logr.Info("Starting Anka Build Setup")
	response, err := c.ankaClient.VMCreate(ctx, &createVMParams{
		Name:                   machineName,
		StartupScript:          uData,
		StartupScriptCondition: startupScriptCondition,
		ScriptMonitoring:       true,
		ScriptFailHandler:      scriptFailHandler,
		VmID:                   c.vmID,
		ScriptTimeout:          scriptTimeout,
		Tag:                    c.tag,
	})
	if err != nil {
		return nil, err
	}
	var id = response.Body[0]
	vm := &vmResponse{}
	for i := 1; i <= 60; i++ {
		vm, err = c.ankaClient.VMFind(ctx, id)
		if err != nil {
			return nil, err
		}
		if vm.Body.InstanceState != "Started" {
			logrus.Debugf("VM: %s is starting...", vm.Body.InstanceID)
			time.Sleep(5 * time.Second)
			continue
		}
		logrus.Debugf("VM: %s is running!", vm.Body.InstanceID)
		break
	}

	inst := vm.Body
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
		Started:  inst.Ts.Unix(),
		Updated:  time.Now().Unix(),
		Port:     int64(port),
	}
	logr.
		WithField("ip", inst.Vminfo.HostIP).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("anka build: [creation] complete")

	return instance, nil
}

func (c config) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
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

func (c config) Hibernate(_ context.Context, _, _ string) error {
	return errors.New("unimplemented")
}

func (c config) Start(_ context.Context, _, _ string) (ipAddress string, err error) {
	return "", errors.New("unimplemented")
}

func (c config) Ping(_ context.Context) error {
	return nil
}

func (c config) Logs(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (c config) RootDir() string {
	return c.rootDir
}

func (c config) DriverName() string {
	return string(types.AnkaBuild)
}

func (c config) CanHibernate() bool {
	return false
}
