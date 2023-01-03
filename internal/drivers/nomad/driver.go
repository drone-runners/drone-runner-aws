package nomad

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"math/rand"
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

// Create a VM in the bare metal machine
func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (instance *types.Instance, err error) {
	val := opts.Arch
	opts.Arch = "amd64"
	startupScript := generateStartupScript(opts)
	opts.Arch = val
	encodedStartupScript := base64.StdEncoding.EncodeToString([]byte(startupScript))
	vm := strings.ToLower(random(20))
	hostPath := fmt.Sprintf("/usr/local/bin/%s.sh", vm)
	vmPath := fmt.Sprintf("/usr/bin/%s.sh", vm)
	jobID := fmt.Sprintf("init_job_%s", vm)
	logr := logger.FromContext(ctx).WithField("vm", vm).WithField("job_id", jobID)
	port := fmt.Sprintf("NOMAD_PORT_%s", vm)
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
				Networks: []*api.NetworkResource{{DynamicPorts: []api.Port{{Label: vm}}}},
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
		return nil, err
	}
	logr.Debugln("nomad: successfully submitted job to nomad, started polling for job status")
	j := pollForJob(jobID, p.client)
	logr.Debugln("nomad: job marked as finished")

	// Get the allocation corresponding to this job submission
	l, _, err := p.client.Jobs().Allocations(*j.ID, false, nil)
	if err != nil {
		return nil, err
	}
	if len(l) == 0 {
		return nil, errors.New("no allocation found for the job")
	}

	id := l[0].NodeID
	allocID := l[0].ID
	alloc, _, err := p.client.Allocations().Info(allocID, &api.QueryOptions{})
	if err != nil {
		return nil, err
	}

	n, _, err := p.client.Nodes().Info(id, &api.QueryOptions{})
	if err != nil {
		fmt.Println("err: ", err)
		return nil, err
	}

	fmt.Printf("node IP is: %s", n.HTTPAddr)
	ip := strings.Split(n.HTTPAddr, ":")[0]

	if alloc.Resources.Networks == nil || len(alloc.Resources.Networks) == 0 {
		return nil, errors.New("could not assign an available port as part of the job")
	}

	liteEnginePort := alloc.Resources.Networks[0].DynamicPorts[0].Value
	// sanity check
	if liteEnginePort <= 0 || liteEnginePort > 65535 {
		return nil, fmt.Errorf("port %d is not a valid port", liteEnginePort)
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
						Attempts: intToPtr(0),
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
		_, _, err = p.client.Jobs().Register(job, nil)
		if err != nil {
			return err
		}
		logr.Debugln("nomad: started polling for destroy job")
		pollForJob(jobID, p.client)
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

// Helper function to convert an int to a pointer to an int
func intToPtr(i int) *int {
	return &i
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func random(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Helper function to convert an int to a pointer to an int
func stringToPtr(i string) *string {
	return &i
}

func pollForJob(id string, client *api.Client) *api.Job {
	var job *api.Job
	var err error
	// Poll for the response
	for {
		q := &api.QueryOptions{WaitTime: 5 * time.Minute}
		// Get the job status
		job, _, err = client.Jobs().Info(id, q)
		if job == nil {
			fmt.Println("job was nil.... continuing")
			continue
		}
		fmt.Printf("job: %+v", job)
		fmt.Printf("err: %+v", err)
		fmt.Println("create index: ", *job.CreateIndex)
		fmt.Println("job modify index: ", *job.JobModifyIndex)
		fmt.Println("modify index: ", *job.ModifyIndex)
		fmt.Println("job status: ", *job.Status)
		if err != nil {
			fmt.Println("error: ", err)
			log.Fatal(err)
		}
		fmt.Printf("job is %+v", job)

		// Check the job status
		if *job.Status == "running" {
			fmt.Println("Job is running")
		} else if *job.Status == "pending" {
			fmt.Println("job is pending")
		} else if *job.Status == "failed" {
			fmt.Println("Job failed")
			break
		} else if *job.Status == "dead" {
			fmt.Println("Job is dead")
			break
		}
	}
	return job
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
