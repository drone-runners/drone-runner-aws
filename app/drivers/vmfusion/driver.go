package vmfusion

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/sirupsen/logrus"
)

var _ drivers.Driver = (*config)(nil)

var (
	vmrunbin    = setVmwareCmd("vmrun")
	vdiskmanbin = setVmwareCmd("vmware-vdiskmanager")
)

var (
	ErrVMRUNNotFound = errors.New("VMRUN not found")
)

type VmxTemplateData struct {
	ISO         string
	MachineName string
	CPU         int64
	Memory      int64
	VDiskPath   string
	StorePath   string
	Version     string
}

type config struct {
	username string
	password string

	rootDir string

	ISO         string
	MachineName string
	CPU         int64
	Memory      int64
	VDiskPath   string
	StorePath   string

	userData string
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}
	if p.CPU == 0 {
		p.CPU = 1
	}
	if p.Memory == 0 {
		p.Memory = 1024
	}
	return p, nil
}

func (p *config) RootDir() string {
	return p.rootDir
}

func (p *config) DriverName() string {
	return string(types.VMFusion)
}

func (p *config) Ping(_ context.Context) error {
	return nil
}

func (p *config) CanHibernate() bool {
	return false
}

func (p *config) Logs(ctx context.Context, instance string) (string, error) {
	return "", errors.New("Unimplemented")
}

func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	machineName := fmt.Sprintf(opts.RunnerName+"-"+"-%d", time.Now().Unix())

	p.MachineName = machineName

	logr := logger.FromContext(ctx).
		WithField("cloud", types.VMFusion).
		WithField("name", machineName).
		WithField("pool", opts.PoolName)

	uData, err := lehelper.GenerateUserdata(p.userData, opts)
	if err != nil {
		logr.WithError(err).
			Errorln("VMFusion: failed to generate user data")
		return nil, err
	}

	if err = os.MkdirAll(p.ResolveStorePath("."), 0755); err != nil { //nolint
		return nil, err
	}

	vmxt := template.Must(template.New("vmx").Parse(vmx))
	vmxFile, err := os.Create(p.vmxPath())
	if err != nil {
		return nil, err
	}
	err = vmxt.Execute(vmxFile, VmxTemplateData{
		ISO:         p.ISO,
		MachineName: p.MachineName,
		CPU:         p.CPU,
		Memory:      p.Memory,
		VDiskPath:   p.VDiskPath,
		StorePath:   p.StorePath,
		Version:     opts.Version,
	})
	if err != nil {
		return nil, err
	}
	diskImg := p.ResolveStorePath(fmt.Sprintf("%s.vmdk", p.MachineName))
	if _, err = os.Stat(diskImg); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		vDiskCopy := fmt.Sprintf("%s/%s.vmdk", filepath.Dir(p.VDiskPath), p.MachineName)
		cpCmd := commandCp(ctx, p.VDiskPath, vDiskCopy)
		var raw []byte
		raw, err = cpCmd.CombinedOutput()
		if err != nil {
			logrus.Debug(string(raw))
			return nil, err
		}
		copyVDiskCmd := commandCopyVDisk(ctx, vDiskCopy, p.ResolveStorePath(fmt.Sprintf("%s.vmdk", p.MachineName)))
		raw, err = copyVDiskCmd.CombinedOutput()
		if err != nil {
			logrus.Debug(string(raw))
			return nil, err
		}
		os.RemoveAll(vDiskCopy)
	}

	// start VM
	_, _, err = vmrun("start", p.vmxPath(), "nogui")
	if err != nil {
		return nil, err
	}

	var instanceIP string
	for i := 1; i <= 60; i++ {
		instanceIP, err = p.GetIP()
		if err != nil {
			logrus.Debugf("Not there yet %d/%d, error: %s", i, 60, err) //nolint
			time.Sleep(2 * time.Second)                                 //nolint
			continue
		}
	}

	f, err := os.CreateTemp("/tmp/", p.MachineName+".sh")
	if err != nil {
		logrus.WithError(err).Warnln("Cannot generate temporary file")
		return nil, err
	}

	defer f.Close()
	defer os.RemoveAll("/tmp/" + p.MachineName + ".sh")

	_, err = f.WriteString(uData)
	if err != nil {
		logrus.WithError(err).Warnln("Cannot write userdata to temporary file")
		return nil, err
	}

	cmdCopyFile := commandCopyFileToGuest(ctx, f.Name(), f.Name(), p.username, p.password, p.vmxPath())
	_, err = cmdCopyFile.CombinedOutput()
	if err != nil {
		return nil, err
	}

	cmdCheckFileExists := commandCheckFileExists(ctx, p.username, p.password, p.vmxPath(), f.Name())
	_, err = cmdCheckFileExists.CombinedOutput()
	if err != nil {
		return nil, err
	}
	cmdRunScript := commandRunScriptInGuest(ctx, p.username, p.password, p.vmxPath(), fmt.Sprintf("bash %s", f.Name()))
	_, err = cmdRunScript.CombinedOutput()
	if err != nil {
		return nil, err
	}
	startTime := time.Now()

	instance = &types.Instance{
		ID:       p.vmxPath(),
		Name:     machineName,
		Provider: types.VMFusion, // this is driver, though its the old legacy name of provider
		State:    types.StateCreated,
		Pool:     opts.PoolName,
		Image:    p.ISO,
		Platform: opts.Platform,
		Address:  instanceIP,
		CACert:   opts.CACert,
		CAKey:    opts.CAKey,
		TLSCert:  opts.TLSCert,
		TLSKey:   opts.TLSKey,
		Started:  startTime.Unix(),
		Updated:  time.Now().Unix(),
		Port:     lehelper.LiteEnginePort,
	}
	logr.
		WithField("ip", instanceIP).
		WithField("time", fmt.Sprintf("%.2fs", time.Since(startTime).Seconds())).
		Debugln("vmfusion: [creation] complete")

	return instance, nil
}

func (p *config) Destroy(ctx context.Context, instances []*types.Instance) (err error) {
	return p.DestroyInstanceAndStorage(ctx, instances, nil)
}

func (p *config) DestroyInstanceAndStorage(ctx context.Context, instances []*types.Instance, _ *storage.CleanupType) (err error) {
	var instanceIDs []string
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}
	if len(instanceIDs) == 0 {
		return errors.New("no instance IDs provided")
	}
	logr := logger.FromContext(ctx).
		WithField("id", instanceIDs).
		WithField("driver", types.VMFusion)

	for _, vmxPath := range instanceIDs {
		// stop & delete VM
		_, _, _ = vmrun("stop", vmxPath)
		_, _, err = vmrun("deleteVM", vmxPath)
		if err != nil {
			logr.WithError(err).Errorln("VMFusion: error deleting VM")
			return err
		}
	}
	return
}

func (p *config) Hibernate(_ context.Context, _, _, _ string) error {
	return errors.New("unimplemented")
}

func (p *config) Start(_ context.Context, _ *types.Instance, _ string) (string, error) {
	return "", errors.New("unimplemented")
}

func (p *config) SetTags(ctx context.Context, instance *types.Instance,
	tags map[string]string) error {
	return nil
}

func commandCopyFileToGuest(ctx context.Context, src, dest, username, password, path string) *exec.Cmd {
	return exec.CommandContext(ctx, vmrunbin, "-gu", username, "-gp", password, "copyFileFromHostToGuest", path, src, dest)
}

func commandRunScriptInGuest(ctx context.Context, username, password, path, script string) *exec.Cmd {
	return exec.CommandContext(ctx, vmrunbin, "-gu", username, "-gp", password, "runScriptInGuest", path, "-noWait", "/bin/bash", script)
}

func commandCheckFileExists(ctx context.Context, username, password, vmxPath, path string) *exec.Cmd {
	return exec.CommandContext(ctx, vmrunbin, "-gu", username, "-gp", password, "fileExistsInGuest", vmxPath, path)
}

func commandCopyVDisk(ctx context.Context, src, dest string) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		vdiskmanbin, "-n",
		src,
		dest,
	)
}

func commandCp(ctx context.Context, src, dest string) *exec.Cmd {
	return exec.CommandContext(
		ctx,
		"cp",
		src,
		dest,
	)
}
