package anka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/dchest/uniuri"
	"github.com/sirupsen/logrus"
)

const BIN = "/usr/local/bin/anka"

type config struct {
	username string
	password string
	rootDir  string
	vmID     string
	userData string
}

type ankaShow struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Created string `json:"created"`
	Memory  string `json:"memory"`
	IP      string `json:"ip"`
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func (p *config) RootDir() string {
	return p.rootDir
}

func (p *config) DriverName() string {
	return string(types.Anka)
}

func (p *config) Ping(_ context.Context) error {
	_, err := exec.LookPath(BIN)
	if err != nil {
		return err
	}
	return nil
}

func (p *config) CanHibernate() bool {
	return false
}

func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	startTime := time.Now()
	uData := lehelper.GenerateUserdata(p.userData, opts)
	machineName := fmt.Sprintf("%s-%s-%s", opts.RunnerName, opts.PoolName, uniuri.NewLen(8)) //nolint:gomnd

	logr := logger.FromContext(ctx).
		WithField("cloud", types.Anka).
		WithField("name", machineName).
		WithField("pool", opts.PoolName)

	var result []byte
	cmdCloneVM := commandCloneVM(ctx, p.vmID, machineName)
	_, err = cmdCloneVM.CombinedOutput()
	if err != nil {
		logr.WithError(err).Error("Failed to clone VM")
		return nil, err
	}

	cmdStartVM := commandAnka(ctx, machineName, "start")
	_, err = cmdStartVM.CombinedOutput()
	if err != nil {
		logr.WithError(err).Error("Failed to start VM")
		return nil, err
	}
	var ip string
	for i := 1; i <= 60; i++ { // loop for 60s until we get an IP
		cmdIP := commandIP(ctx, machineName, "show")
		result, err = cmdIP.CombinedOutput()
		if err != nil {
			logrus.Debugf("Not there yet %d/%d, error: %s", i, 60, err) //nolint
			time.Sleep(2 * time.Second)                                 //nolint
			continue
		}
		ip = strings.TrimSpace(string(result))
		logr.Debugf("got IP %s", ip)
		break
	}
	var createdVM struct {
		Status string   `json:"status"`
		Body   ankaShow `json:"body"`
	}
	cmdShow := commandAnka(ctx, machineName, "show")
	result, err = cmdShow.CombinedOutput()
	if err != nil {
		logr.WithError(err).Errorf("Failed to get VM info")
		return nil, err
	}
	err = json.Unmarshal(result, &createdVM)
	if err != nil {
		logr.WithError(err).Errorf("Failed to parse VM info")
		return nil, err
	}
	createdVM.Body.IP = ip

	f, err := os.CreateTemp("/tmp/", machineName+".sh")
	if err != nil {
		logrus.WithError(err).Warnln("Cannot generate temporary file")
		return nil, err
	}

	defer f.Close()
	defer os.RemoveAll("/tmp/" + machineName + ".sh")

	_, err = f.WriteString(uData)
	if err != nil {
		logrus.WithError(err).Warnln("Cannot write userdata to temporary file")
		return nil, err
	}

	cmdCopy := commandCP(ctx, f.Name(), fmt.Sprintf("%s:%s", createdVM.Body.UUID, f.Name()))
	_, err = cmdCopy.CombinedOutput()
	if err != nil {
		logr.WithError(err).Errorf("Failed to copy userdata to VM")
		return nil, err
	}

	logr.Info("Running script in VM")

	cmdRunScript := commandRunScript(ctx, createdVM.Body.UUID, f.Name())
	_, err = cmdRunScript.CombinedOutput()
	if err != nil {
		logr.WithError(err).Errorf("Failed to run script in VM")
		return nil, err
	}

	instance = &types.Instance{
		ID:       createdVM.Body.UUID,
		Name:     machineName,
		Provider: types.Anka, // this is driver, though its the old legacy name of provider
		State:    types.StateCreated,
		Pool:     opts.PoolName,
		Platform: opts.Platform,
		Address:  ip,
		CACert:   opts.CACert,
		CAKey:    opts.CAKey,
		TLSCert:  opts.TLSCert,
		TLSKey:   opts.TLSKey,
		Started:  startTime.Unix(),
		Updated:  time.Now().Unix(),
		Port:     lehelper.LiteEnginePort,
	}
	logr.
		WithField("ip", ip).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("anka: [creation] complete")

	return instance, nil
}

func (p *config) Destroy(ctx context.Context, instances []*types.Instance, storageCleanupType *storage.CleanupType) (err error) {
	var instanceIDs []string
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}
	if len(instanceIDs) == 0 {
		return
	}
	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("driver", types.Anka)

	for _, id := range instanceIDs {
		// stop & delete VM
		cmdDelete := commandDeleteVM(ctx, id)
		_, err = cmdDelete.CombinedOutput()
		if err != nil {
			logr.WithError(err).Errorln("Anka: error deleting VM")
			return err
		}
	}
	return nil
}

func (p *config) Hibernate(_ context.Context, _, _ string) error {
	return errors.New("unimplemented")
}

func (p *config) Start(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("unimplemented")
}

func (p *config) Logs(ctx context.Context, instance string) (string, error) {
	return "", nil
}

func (p *config) SetTags(ctx context.Context, instance *types.Instance,
	tags map[string]string) error {
	return nil
}

func commandCloneVM(ctx context.Context, vmID, newVMName string) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		BIN, "clone",
		vmID,
		newVMName,
	)
}

func commandAnka(ctx context.Context, vmID, command string) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		BIN,
		"--machine-readable",
		command,
		vmID,
	)
}

func commandIP(ctx context.Context, vmID, command string) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		BIN,
		command,
		vmID,
		"ip",
	)
}

func commandCP(ctx context.Context, src, dest string) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		BIN,
		"cp",
		src,
		dest,
	)
}

func commandRunScript(ctx context.Context, vmID, command string) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		BIN,
		"run",
		vmID,
		"bash",
		command,
	)
}

func commandDeleteVM(ctx context.Context, vmID string) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		BIN,
		"delete",
		"--yes",
		vmID,
	)
}
