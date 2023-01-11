package nomad

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/hashicorp/nomad/api"
)

type config struct {
	address        string
	vmImage        string
	vmMemory       string
	vmCpus         string
	caCertPath     string
	clientCertPath string
	clientKeyPath  string
	insecure       bool
	client         *api.Client
}

func SetPlatformDefaults(platform *types.Platform) (*types.Platform, error) {
	if platform.Arch == "" {
		platform.Arch = oshelp.ArchAMD64
	}
	if platform.Arch != oshelp.ArchAMD64 && platform.Arch != oshelp.ArchARM64 {
		return platform, fmt.Errorf("invalid arch %s, has to be '%s/%s'", platform.Arch, oshelp.ArchAMD64, oshelp.ArchARM64)
	}
	// verify that we are using sane values for OS
	if platform.OS == "" {
		platform.OS = oshelp.OSLinux
	}
	if platform.OS != oshelp.OSLinux && platform.OS != oshelp.OSWindows {
		return platform, fmt.Errorf("invalid OS %s, has to be either'%s/%s'", platform.OS, oshelp.OSLinux, oshelp.OSWindows)
	}

	return platform, nil
}

func New(opts ...Option) (drivers.Driver, error) {
	p := new(config)
	for _, opt := range opts {
		opt(p)
	}
	if p.client == nil {
		client, err := NewClient(p.address, p.insecure, p.caCertPath, p.clientCertPath, p.clientKeyPath)
		if err != nil {
			return nil, err
		}
		p.client = client
	}
	return p, nil
}

func (p *config) DriverName() string {
	return "nomad"
}

func (p *config) RootDir() string {
	return ""
}

func (p *config) CanHibernate() bool {
	return false
}

// Ping checks that we can ping the machine
func (p *config) Ping(ctx context.Context) error {
	if p.client != nil {
		return nil
	}
	return errors.New("could not create a client to the nomad server")
}

// Create creates a VM using port forwarding inside a bare metal machine assigned by nomad.
// TODO (Vistaar): Handle cleanup in cases of partial execution of tasks and errors - idempotency is required
func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	startupScript := generateStartupScript(opts)
	encodedStartupScript := base64.StdEncoding.EncodeToString([]byte(startupScript))
	vm := strings.ToLower(random(20))

	hostPath := fmt.Sprintf("/usr/local/bin/%s.sh", vm)
	vmPath := fmt.Sprintf("/usr/bin/%s.sh", vm)
	jobID := fmt.Sprintf("init_job_%s", vm)
	port := fmt.Sprintf("NOMAD_PORT_%s", vm)

	logr := logger.FromContext(ctx).WithField("vm", vm).WithField("job_id", jobID)

	runCmd := fmt.Sprintf("/usr/local/bin/ignite run %s --name %s --cpus %s --memory %s --ssh --ports $%s:%s --copy-files %s:%s",
		p.vmImage,
		vm,
		p.vmCpus,
		p.vmMemory,
		port,
		strconv.Itoa(lehelper.LiteEnginePort),
		hostPath,
		vmPath)
	job := &api.Job{
		ID:          &jobID,
		Name:        stringToPtr(vm),
		Type:        stringToPtr("batch"),
		Datacenters: []string{"dc1"},
		TaskGroups: []*api.TaskGroup{
			{
				Networks:                  []*api.NetworkResource{{DynamicPorts: []api.Port{{Label: vm}}}},
				StopAfterClientDisconnect: durationToPtr(4 * time.Minute),
				RestartPolicy: &api.RestartPolicy{
					Attempts: intToPtr(0),
				},
				Name:  stringToPtr(fmt.Sprintf("init_task_group_%s", vm)),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{
						Name:   "create_startup_script_on_host",
						Driver: "raw_exec",
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", fmt.Sprintf("echo %s >> %s", encodedStartupScript, hostPath)},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "prestart",
						},
					},

					{
						Name:   "ignite_run",
						Driver: "raw_exec",
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", runCmd},
						},
					},
					{
						Name:   "ignite_exec",
						Driver: "raw_exec",
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", fmt.Sprintf("/usr/local/bin/ignite exec %s 'cat %s | base64 --decode | bash'", vm, vmPath)},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "poststop",
						},
					},
					{
						Name:   "cleanup_startup_script_from_host",
						Driver: "raw_exec",
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", fmt.Sprintf("rm %s", hostPath)},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "poststop",
						},
					},
				},
			},
		},
	}
	logr.Debugln("nomad: submitting job to nomad")
	_, _, err = p.client.Jobs().Register(job, nil)
	if err != nil {
		return nil, fmt.Errorf("nomad: could not register job, err: %w", err)
	}
	logr.Debugln("nomad: successfully submitted job to nomad, started polling for job status")
	_, err = pollForJob(ctx, jobID, p.client, logr, 5*time.Minute, true) // TODO (Vistaar): validate this timeout
	if err != nil {
		return nil, err
	}
	logr.Debugln("nomad: job marked as finished")

	// Get the allocation corresponding to this job submission
	l, _, err := p.client.Jobs().Allocations(jobID, false, nil)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, errors.New("nomad: no allocation found for the job")
	}

	id := l[0].NodeID
	allocID := l[0].ID
	if id == "" || allocID == "" {
		return nil, errors.New("nomad: could not find an allocation identifier for the job")
	}
	alloc, _, err := p.client.Allocations().Info(allocID, &api.QueryOptions{})
	if err != nil {
		logr.WithError(err).Errorln("nomad: could not get allocation information")
		return nil, err
	}

	n, _, err := p.client.Nodes().Info(id, &api.QueryOptions{})
	if err != nil {
		logr.WithError(err).Errorln("nomad: could not get information about the node which picked up the init job")
		return nil, err
	}

	ip := strings.Split(n.HTTPAddr, ":")[0]
	if net.ParseIP(ip) == nil {
		return nil, fmt.Errorf("nomad: could not parse client machine IP: %s", ip)
	}

	// Not expected - if nomad is unable to find a port, it should not run the job at all.
	if alloc.Resources.Networks == nil || len(alloc.Resources.Networks) == 0 {
		return nil, errors.New("could not assign an available port as part of the job")
	}

	liteEnginePort := alloc.Resources.Networks[0].DynamicPorts[0].Value
	// sanity check
	if liteEnginePort <= 0 || liteEnginePort > 65535 {
		err := fmt.Errorf("nomad: port %d generated is not a valid port", liteEnginePort)
		logr.Errorln(err)
		return nil, err
	}

	logr.WithField("node_ip", ip).WithField("node_port", liteEnginePort).Traceln("nomad: successfully created instance")

	return &types.Instance{
		ID:       vm,
		NodeID:   id,
		Name:     id, // TODO: Move this to a separate field
		Platform: opts.Platform,
		State:    types.StateCreated,
		Address:  ip,
		CACert:   opts.CACert,
		CAKey:    opts.CAKey,
		TLSCert:  opts.TLSCert,
		TLSKey:   opts.TLSKey,
		Provider: types.Nomad,
		Pool:     opts.PoolName,
		Port:     int64(liteEnginePort),
	}, nil
}

// Destroy destroys the VM in the bare metal machine
func (p *config) Destroy(ctx context.Context, instances []*types.Instance) (err error) {
	for _, instance := range instances {
		jobID := fmt.Sprintf("destroy_job_%s", instance.ID)
		logr := logger.FromContext(ctx).
			WithField("instance_id", instance.ID).
			WithField("instance_node_id", instance.NodeID).
			WithField("job_id", jobID)
		constraint := &api.Constraint{
			LTarget: "${node.unique.id}",
			RTarget: instance.NodeID,
			Operand: "=",
		}
		job := &api.Job{
			ID:          &jobID,
			Name:        stringToPtr(random(20)),
			Type:        stringToPtr("batch"),
			Datacenters: []string{"dc1"},
			Constraints: []*api.Constraint{
				constraint,
			},
			TaskGroups: []*api.TaskGroup{
				{
					RestartPolicy: &api.RestartPolicy{
						Attempts: intToPtr(3),
					},
					Name:  stringToPtr("delete_vm_grp"),
					Count: intToPtr(1),
					Tasks: []*api.Task{
						{
							Name:   "ignite_delete",
							Driver: "raw_exec",
							Config: map[string]interface{}{
								"command": "/usr/bin/su",
								"args":    []string{"-c", fmt.Sprintf("/usr/local/bin/ignite kill %s", instance.ID)},
							},
						},
					},
				},
			}}
		_, _, err := p.client.Jobs().Register(job, nil)
		if err != nil {
			logr.WithError(err).Errorln("nomad: could not register destroy job")
			return err
		}
		logr.Debugln("nomad: started polling for destroy job")
		_, err = pollForJob(ctx, jobID, p.client, logr, 10*time.Minute, true)
		if err != nil {
			logr.WithError(err).Errorln("nomad: could not complete destroy job")
			return err
		}
		logr.Debugln("nomad: destroy task finished")
	}
	return nil
}

func (p *config) Logs(ctx context.Context, instanceID string) (string, error) {
	return "", nil
}

func (p *config) SetTags(ctx context.Context, instance *types.Instance,
	tags map[string]string) error {
	return nil
}

func (p *config) Hibernate(ctx context.Context, instanceID, poolName string) error {
	return nil
}

func (p *config) Start(ctx context.Context, instanceID, poolName string) (string, error) {
	return "", nil
}

// pollForJob polls on the status of the job and returns back once it is in a terminal state.
// if remove is set to true, it deregisters the job in case it either exceeds the timeout or the context is marked as Done
// it returns an error if the job did not reach the terminal state
func pollForJob(ctx context.Context, id string, client *api.Client, logr logger.Logger, timeout time.Duration, remove bool) (*api.Job, error) {
	maxPollTime := time.After(timeout)
	var job *api.Job
	var err error
	var waitIndex uint64 = 0
L:
	for {
		select {
		case <-ctx.Done():
			break L
		case <-maxPollTime:
			break L
		default:
			q := &api.QueryOptions{WaitTime: 15 * time.Second, WaitIndex: waitIndex}
			var qm *api.QueryMeta
			// Get the job status
			job, qm, err = client.Jobs().Info(id, q)
			if job == nil {
				continue
			}
			if err != nil {
				logr.WithError(err).WithField("job_id", id).Error("could not retrieve job information")
				continue
			}
			waitIndex = qm.LastIndex

			// Check the job status
			if *job.Status == "running" {
				logr.WithField("job_id", id).Traceln("job is running")
			} else if *job.Status == "pending" {
				logr.WithField("job_id", id).Traceln("job is in pending state")
			} else if *job.Status == "dead" {
				logr.WithField("job_id", id).Traceln("job is finished")
				break L
			}
		}
	}
	if job == nil {
		logr.WithField("job_id", id).Errorln("could not poll for job")
		return job, errors.New("could not poll for job")
	}
	if *job.Status == "running" || *job.Status == "pending" {
		// Kill the job as it has reached its timeout and still not completed.
		// Nomad does not offer a way to set a max runtime on jobs: https://github.com/hashicorp/nomad/issues/1782
		if !remove {
			return job, errors.New("job never reached terminal state")
		}
		_, _, err = client.Jobs().Deregister(id, true, &api.WriteOptions{})
		if err != nil {
			logr.WithField("job_id", id).WithError(err).Errorln("job timed out but could still not be deregistered")
		} else {
			logr.WithField("job_id", id).Infoln("successfully deregistered long running job")
		}
		return job, fmt.Errorf("job never reached terminal status, deregister error: %s", err)
	}
	return job, nil
}

func generateStartupScript(opts *types.InstanceCreateOpts) string {
	params := &cloudinit.Params{
		Platform:             opts.Platform,
		CACert:               string(opts.CACert),
		TLSCert:              string(opts.TLSCert),
		TLSKey:               string(opts.TLSKey),
		LiteEnginePath:       opts.LiteEnginePath,
		HarnessTestBinaryURI: opts.HarnessTestBinaryURI,
		PluginBinaryURI:      opts.PluginBinaryURI,
	}

	return cloudinit.LinuxBash(params)
}
