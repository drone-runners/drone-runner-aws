package anka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/drone-runners/drone-runner-aws/internal/userdata"
	"github.com/drone/runner-go/logger"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/types"
)

var (
	ankabin = "/usr/local/bin/anka"
)

type ankaShow struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Created string `json:"created"`
	Memory  string `json:"memory"`
	IP      string `json:"ip"`
}

func (p *provider) RootDir() string {
	return p.rootDir
}

func (p *provider) ProviderName() string {
	return string(types.ProviderAnka)
}

func (p *provider) Ping(_ context.Context) error {
	return nil
}

func (p *provider) CanHibernate() bool {
	return false
}

func (p *provider) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	uData := userdata.Generate(p.userData, opts)
	machineName := fmt.Sprintf(opts.RunnerName+"-"+"-%d", time.Now().Unix())

	logr := logger.FromContext(ctx).
		WithField("cloud", types.ProviderAnka).
		WithField("name", machineName).
		WithField("pool", opts.PoolName)

	var result []byte
	cmdCloneVM := commandCloneVM(p.vmId, machineName)
	result, err = cmdCloneVM.CombinedOutput()
	if err != nil {
		return nil, err
	}

	cmdStartVM := commandAnka(machineName, "start")
	_, err = cmdStartVM.CombinedOutput()
	if err != nil {
		return nil, err
	}
	var ip string
	for i := 1; i <= 60; i++ {
		cmdIP := commandIP(machineName, "show")
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
	cmdShow := commandAnka(machineName, "show")
	result, err = cmdShow.CombinedOutput()
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(result, &createdVM)
	if err != nil {
		return nil, err
	}
	createdVM.Body.IP = ip

	f, err := ioutil.TempFile("/tmp/", machineName+".sh")
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

	cmdCopy := commandCP(createdVM.Body.UUID, f.Name(), f.Name())
	_, err = cmdCopy.CombinedOutput()
	if err != nil {
		return nil, err
	}

	logr.Info("Running script in VM")

	cmdRunScript := commandRunScript(createdVM.Body.UUID, fmt.Sprintf(f.Name()))
	_, err = cmdRunScript.CombinedOutput()
	if err != nil {
		return nil, err
	}

	startTime := time.Now()

	instance = &types.Instance{
		ID:       createdVM.Body.UUID,
		Name:     machineName,
		Provider: types.ProviderAnka,
		State:    types.StateCreated,
		Pool:     opts.PoolName,
		Platform: opts.OS,
		Arch:     opts.Arch,
		Address:  ip,
		CACert:   opts.CACert,
		CAKey:    opts.CAKey,
		TLSCert:  opts.TLSCert,
		TLSKey:   opts.TLSKey,
		Started:  startTime.Unix(),
		Updated:  time.Now().Unix(),
	}
	logr.
		WithField("ip", ip).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("anka: [creation] complete")

	return instance, nil
}

func (p *provider) Destroy(ctx context.Context, instanceIDs ...string) (err error) {
	if len(instanceIDs) == 0 {
		return
	}
	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("provider", types.ProviderAnka)

	for _, id := range instanceIDs {
		// stop & delete VM
		cmdDelete := commandDeleteVM(id)
		_, err = cmdDelete.CombinedOutput()
		if err != nil {
			logr.WithError(err).Errorln("Anka: error deleting VM")
			return err
		}
	}
	return
}

func (p *provider) Hibernate(_ context.Context, _, _ string) error {
	return errors.New("unimplemented")
}

func (p *provider) Start(_ context.Context, _, _ string) (string, error) {
	return "", errors.New("unimplemented")
}

func commandCloneVM(vmId, newVMName string) *exec.Cmd {
	return exec.Command(
		ankabin, "clone",
		vmId,
		newVMName,
	)
}

func commandAnka(vmId, command string) *exec.Cmd {
	return exec.Command(
		ankabin,
		"--machine-readable",
		command,
		vmId,
	)
}

func commandIP(vmId, command string) *exec.Cmd {
	return exec.Command(
		ankabin,
		command,
		vmId,
		"ip",
	)
}

func commandCP(vmId, src, dest string) *exec.Cmd {
	return exec.Command(
		ankabin,
		"cp",
		src,
		fmt.Sprintf("%s:%s", vmId, dest),
	)
}

func commandRunScript(vmId, command string) *exec.Cmd {
	return exec.Command(
		ankabin,
		"run",
		vmId,
		"bash",
		command,
	)
}

func commandDeleteVM(vmId string) *exec.Cmd {
	return exec.Command(
		ankabin,
		"delete",
		"--yes",
		vmId,
	)
}
