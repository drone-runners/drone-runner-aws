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

	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/internal/cloudinit"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/hashicorp/nomad/api"
	"golang.org/x/exp/slices"
)

var (
	ignitePath              = "/usr/local/bin/ignite"
	clientDisconnectTimeout = 4 * time.Minute
	resourceJobTimeout      = 2 * time.Minute
	initTimeout             = 3 * time.Minute
	destroyTimeout          = 3 * time.Minute
	globalAccount           = "GLOBAL_ACCOUNT_ID"
	destroyRetryAttempts    = 1
	minNomadCPUMhz          = 40
	minNomadMemoryMb        = 20
	machineFrequencyMhz     = 3500 // TODO: Find a way to extract this from the node directly
	largebaremetalclass     = "largebaremetal"
)

type config struct {
	address        string
	vmImage        string
	vmMemoryGB     string
	vmCpus         string
	vmDiskSize     string
	caCertPath     string
	clientCertPath string
	clientKeyPath  string
	insecure       bool
	noop           bool
	enablePinning  map[string]string
	client         *api.Client
	resource       map[string]cf.NomadResource
}

// SetPlatformDefaults comes up with default values of the platform
// in case they are not set.
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
// This function is idempotent - any errors in between will cleanup the created VMs.
func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (*types.Instance, error) { //nolint:gocyclo
	startupScript := generateStartupScript(opts)

	vm := strings.ToLower(random(20)) //nolint:gomnd
	class := ""
	for k, v := range p.enablePinning {
		if strings.Contains(v, opts.AccountID) {
			class = k
		}
	}
	cpus, err := strconv.Atoi(p.vmCpus)
	if err != nil {
		return nil, errors.New("could not convert VM cpus to integer")
	}
	memGB, err := strconv.Atoi(p.vmMemoryGB)
	if err != nil {
		return nil, errors.New("could  not convert VM memory to integer")
	}

	resource := cf.NomadResource{
		MemoryGB: p.vmMemoryGB,
		Cpus:     p.vmCpus,
		DiskSize: p.vmDiskSize,
	}

	if opts.ResourceClass != "" && p.resource != nil {
		if v, ok := p.resource[opts.ResourceClass]; ok {
			cpus, err = strconv.Atoi(v.Cpus)
			if err != nil {
				return nil, errors.New("could not convert VM cpus to integer")
			}
			memGB, err = strconv.Atoi(v.MemoryGB)
			if err != nil {
				return nil, errors.New("could  not convert VM memory to integer")
			}
			resource = v

			if opts.ResourceClass == "large" || opts.ResourceClass == "xlarge" {
				// use largebaremetal class if resource class is large or xlarge
				class = largebaremetalclass
			}
		}
	}

	// Create a resource job which occupies resources until the VM is alive to avoid
	// oversubscribing the node
	var resourceJob *api.Job
	var resourceJobID string
	if p.noop {
		resourceJob, resourceJobID = p.resourceJobNoop(cpus, memGB, vm)
	} else {
		if class != "" {
			resourceJob, resourceJobID = p.resourceJob(cpus, memGB, vm, class)
		} else {
			resourceJob, resourceJobID = p.resourceJob(cpus, memGB, vm, globalAccount)
		}
	}

	logr := logger.FromContext(ctx).WithField("vm", vm).WithField("resource_job_id", resourceJobID)

	logr.Infoln("scheduler: finding a node which has available resources ... ")

	_, _, err = p.client.Jobs().Register(resourceJob, nil)
	if err != nil {
		return nil, fmt.Errorf("scheduler: could not register job, err: %w", err)
	}
	// If resources don't become available in `resourceJobTimeout`, we fail the step
	job, err := p.pollForJob(ctx, resourceJobID, logr, resourceJobTimeout, true, []JobStatus{Running, Dead})
	if err != nil {
		return nil, fmt.Errorf("scheduler: could not find a node with available resources, err: %w", err)
	}
	if job == nil || isTerminal(job) {
		return nil, fmt.Errorf("scheduler: resource job reached terminal state before starting")
	}
	logr.Infoln("scheduler: found a node with available resources")

	// get the machine details where the resource job was allocated
	ip, id, hostPort, err := p.fetchMachine(logr, resourceJobID)
	if err != nil {
		defer p.deregisterJob(logr, resourceJobID, false) //nolint:errcheck
		return nil, err
	}

	// create a VM on the same machine where the resource job was allocated
	var initJob *api.Job
	var initJobID, initTaskGroup string
	if p.noop {
		initJob, initJobID, initTaskGroup = p.initJobNoop(vm, startupScript, hostPort, id)
	} else {
		initJob, initJobID, initTaskGroup = p.initJob(vm, startupScript, hostPort, id, resource)
	}

	logr = logr.WithField("init_job_id", initJobID).WithField("node_ip", ip).WithField("node_port", hostPort)

	instance := &types.Instance{
		ID:       vm,
		NodeID:   id,
		Name:     vm,
		Platform: opts.Platform,
		State:    types.StateCreated,
		CACert:   opts.CACert,
		CAKey:    opts.CAKey,
		TLSCert:  opts.TLSCert,
		TLSKey:   opts.TLSKey,
		Provider: types.Nomad,
		Pool:     opts.PoolName,
		Started:  time.Now().Unix(),
		Updated:  time.Now().Unix(),
		Port:     int64(hostPort),
		Address:  ip,
	}

	logr.Debugln("scheduler: submitting VM creation job")
	_, _, err = p.client.Jobs().Register(initJob, nil)
	if err != nil {
		defer p.deregisterJob(logr, resourceJobID, false) //nolint:errcheck
		return nil, fmt.Errorf("scheduler: could not register job, err: %w ip: %s, resource_job_id: %s, init_job_id: %s, vm: %s", err, ip, resourceJobID, initJobID, vm)
	}
	logr.Debugln("scheduler: successfully submitted job, started polling for job status")
	_, err = p.pollForJob(ctx, initJobID, logr, initTimeout, true, []JobStatus{Dead})
	if err != nil {
		// Destroy the VM if it's in a partially created state
		defer p.Destroy(context.Background(), []*types.Instance{instance}) //nolint:errcheck
		return nil, fmt.Errorf("scheduler: could not poll for init job status, failed with error: %s on ip: %s, resource_job_id: %s, init_job_id: %s, vm: %s", err, ip, resourceJobID, initJobID, vm)
	}

	// Make sure all subtasks in the init job passed
	err = p.checkTaskGroupStatus(initJobID, initTaskGroup)
	if err != nil {
		defer p.Destroy(context.Background(), []*types.Instance{instance}) //nolint:errcheck
		return nil, fmt.Errorf("scheduler: init job failed with error: %s on ip: %s, resource_job_id: %s, init_job_id: %s, vm: %s", err, ip, resourceJobID, initJobID, vm)
	}

	// Check status of the resource job. If it reached a terminal state, destroy the VM and remove the resource job
	job, _, err = p.client.Jobs().Info(resourceJobID, &api.QueryOptions{})
	if err != nil {
		return nil, fmt.Errorf("scheduler: could not query resource job, err: %w, resource_job_id: %s, init_job_id: %s, vm: %s", err, resourceJobID, initJobID, vm)
	}
	if job == nil || isTerminal(job) {
		defer p.Destroy(context.Background(), []*types.Instance{instance}) //nolint:errcheck
		return nil, fmt.Errorf("scheduler: resource job reached unexpected terminal status, removing VM, resource_job_id: %s, init_job_id: %s, vm: %s", resourceJobID, initJobID, vm)
	}

	return instance, nil
}

// checkTaskGroupStatus verifies whether there were any tasks inside the task group which failed
func (p *config) checkTaskGroupStatus(jobID, taskGroup string) error {
	// Get summary of job to make sure all tasks passed
	summary, _, err := p.client.Jobs().Summary(jobID, &api.QueryOptions{})
	if err != nil {
		return errors.New("could not get summary of the job")
	}

	// If the summary is invalid or any of the tasks have failed, return an error
	if summary == nil || summary.Summary == nil {
		return errors.New("could not get summary of the job")
	}
	if _, ok := summary.Summary[taskGroup]; !ok {
		return errors.New("could not get summary of the task group")
	}
	if summary.Summary[taskGroup].Failed > 0 {
		return fmt.Errorf("found failed tasks")
	}
	return nil
}

// resourceJob creates a job which occupies resources until the VM lifecycle
func (p *config) resourceJob(cpus, memGB int, vm, accountID string) (job *api.Job, id string) {
	id = resourceJobID(vm)
	portLabel := vm

	sleepTime := resourceJobTimeout + initTimeout + 2*time.Minute // add 2 minutes for a buffer

	// TODO: Check if this logic can be made better, although we are bounded by some limitations of Nomad scheduling
	// We want to keep some buffer for other tasks to come in (which require minimum cpu and memory)
	cpu := machineFrequencyMhz*cpus - 109
	mem := convertGigsToMegs(memGB) - 53

	constraintList := []*api.Constraint{}
	if accountID != "" {
		constraintList = constraints(accountID)
	}
	// This job stays alive to keep resources on nomad busy until the VM is destroyed
	// It sleeps until the max VM creation timeout, after which it periodically checks whether the VM is alive or not
	job = &api.Job{
		ID:          &id,
		Name:        stringToPtr(id),
		Type:        stringToPtr("batch"),
		Datacenters: []string{"dc1"},
		// TODO (Vistaar): This can be updated once we have more data points
		Reschedule: &api.ReschedulePolicy{
			Attempts:  intToPtr(0),
			Unlimited: boolToPtr(false),
		},
		Constraints: constraintList,
		TaskGroups: []*api.TaskGroup{
			{
				Networks:                  []*api.NetworkResource{{DynamicPorts: []api.Port{{Label: portLabel}}}},
				StopAfterClientDisconnect: &clientDisconnectTimeout,
				RestartPolicy: &api.RestartPolicy{
					Attempts: intToPtr(0),
				},
				Name:  stringToPtr(fmt.Sprintf("init_task_group_resource_%s", vm)),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{

						Name: "sleep_and_ping",
						Resources: &api.Resources{
							MemoryMB: intToPtr(mem), // to keep resources available for the destroy jobs
							CPU:      intToPtr(cpu), // keep some buffer for destroy and init tasks
						},
						Driver: "raw_exec",
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", generateHealthCheckScript(sleepTime, fmt.Sprintf("$NOMAD_PORT_%s", portLabel))},
						},
					},
				},
			},
		},
	}
	return job, id
}

// fetchMachine returns details of the machine where the job has been allocated
func (p *config) fetchMachine(logr logger.Logger, id string) (ip, nodeID string, port int, err error) {
	// Get the allocation corresponding to this job submission. If this call fails, there is not much we can do in terms
	// of cleanup - as the job has created a virtual machine but we could not parse the node identifier.
	l, _, err := p.client.Jobs().Allocations(id, false, nil)
	if err != nil {
		return ip, nodeID, port, err
	}
	if len(l) == 0 {
		return ip, nodeID, port, errors.New("scheduler: no allocation found for the job")
	}

	nodeID = l[0].NodeID
	allocID := l[0].ID
	if nodeID == "" || allocID == "" {
		return ip, nodeID, port, errors.New("scheduler: could not find an allocation identifier for the job")
	}

	alloc, _, err := p.client.Allocations().Info(allocID, &api.QueryOptions{})
	if err != nil {
		return ip, nodeID, port, err
	}

	// Not expected - if nomad is unable to find a port, it should not run the job at all.
	if alloc.Resources.Networks == nil || len(alloc.Resources.Networks) == 0 {
		err = fmt.Errorf("scheduler: could not allocate network and ports for job")
		logr.Errorln(err)
		return ip, nodeID, port, err
	}

	port = alloc.Resources.Networks[0].DynamicPorts[0].Value

	// sanity check
	if port <= 0 || port > 65535 {
		err = fmt.Errorf("scheduler: port %d generated is not a valid port", port)
		logr.Errorln(err)
		return ip, nodeID, port, err
	}

	n, _, err := p.client.Nodes().Info(nodeID, &api.QueryOptions{})
	if err != nil {
		logr.WithError(err).Errorln("scheduler: could not get information about the node which picked up the resource job")
		return ip, nodeID, port, err
	}

	ip = strings.Split(n.HTTPAddr, ":")[0]
	if net.ParseIP(ip) == nil {
		err = fmt.Errorf("scheduler: could not parse client machine IP: %s", ip)
		logr.Errorln(err)
		return ip, nodeID, port, err
	}

	return ip, nodeID, port, nil
}

// initJob creates a job which is targeted to a specific node. The job does the following:
//  1. Starts a VM with the provided config
//  2. Runs a startup script inside the VM
func (p *config) initJob(vm, startupScript string, hostPort int, nodeID string, resource cf.NomadResource) (job *api.Job, id, group string) {
	id = initJobID(vm)
	group = fmt.Sprintf("init_task_group_%s", vm)
	encodedStartupScript := base64.StdEncoding.EncodeToString([]byte(startupScript))

	hostPath := fmt.Sprintf("/usr/local/bin/%s.sh", vm)
	vmPath := fmt.Sprintf("/usr/bin/%s.sh", vm)

	runCmd := fmt.Sprintf("%s run %s --name %s --cpus %s --memory %sGB --size %s --ssh --runtime=docker --ports %d:%s --copy-files %s:%s",
		ignitePath,
		p.vmImage,
		vm,
		resource.Cpus,
		resource.MemoryGB,
		resource.DiskSize,
		hostPort,
		strconv.Itoa(lehelper.LiteEnginePort),
		hostPath,
		vmPath)
	job = &api.Job{
		ID:          &id,
		Name:        stringToPtr(vm),
		Type:        stringToPtr("batch"),
		Datacenters: []string{"dc1"},
		Constraints: []*api.Constraint{
			{
				LTarget: "${node.unique.id}",
				RTarget: nodeID,
				Operand: "=",
			},
		},
		Reschedule: &api.ReschedulePolicy{
			Attempts:  intToPtr(0),
			Unlimited: boolToPtr(false),
		},
		TaskGroups: []*api.TaskGroup{
			{
				StopAfterClientDisconnect: &clientDisconnectTimeout,
				RestartPolicy: &api.RestartPolicy{
					Attempts: intToPtr(0),
				},
				Name:  stringToPtr(group),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{
						Name:      "create_startup_script_on_host",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
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
						Name:      "enable_port_forwarding",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", "iptables -P FORWARD ACCEPT"},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "prestart",
						},
					},

					{
						Name:      "ignite_run",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", runCmd},
						},
					},

					{
						Name:      "ignite_exec",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", fmt.Sprintf("%s exec %s 'cat %s | base64 --decode | bash'", ignitePath, vm, vmPath)},
						},
						Lifecycle: &api.TaskLifecycle{
							Sidecar: false,
							Hook:    "poststop",
						},
					},
					{
						Name:      "cleanup_startup_script_from_host",
						Driver:    "raw_exec",
						Resources: minNomadResources(),
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
	return job, id, group
}

// destroyJob returns a job targeted to the given node which stops and removes the VM
func (p *config) destroyJob(vm, nodeID string) (job *api.Job, id string) {
	id = destroyJobID(vm)
	constraint := &api.Constraint{
		LTarget: "${node.unique.id}",
		RTarget: nodeID,
		Operand: "=",
	}
	job = &api.Job{
		ID:   &id,
		Name: stringToPtr(random(20)), //nolint:gomnd

		Type:        stringToPtr("batch"),
		Datacenters: []string{"dc1"},
		Constraints: []*api.Constraint{
			constraint,
		},
		TaskGroups: []*api.TaskGroup{
			{
				StopAfterClientDisconnect: &clientDisconnectTimeout,
				RestartPolicy: &api.RestartPolicy{
					Attempts: intToPtr(destroyRetryAttempts),
				},
				Name:  stringToPtr(fmt.Sprintf("delete_task_group_%s", vm)),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{
						Name:      "ignite_stop_and_rm",
						Resources: minNomadResources(),
						Driver:    "raw_exec",
						Config: map[string]interface{}{
							"command": "/usr/bin/su",
							"args":    []string{"-c", generateDestroyCommand(vm)},
						},
					},
				},
			},
		}}
	return job, id
}

// Destroy destroys the VM in the bare metal machine
func (p *config) Destroy(ctx context.Context, instances []*types.Instance) (err error) {
	for _, instance := range instances {
		var job *api.Job
		var jobID string
		if p.noop {
			job, jobID = p.destroyJobNoop(instance.ID, instance.NodeID)
		} else {
			job, jobID = p.destroyJob(instance.ID, instance.NodeID)
		}

		resourceJobID := resourceJobID(instance.ID)
		logr := logger.FromContext(ctx).
			WithField("instance_id", instance.ID).
			WithField("instance_node_id", instance.NodeID).
			WithField("job_id", jobID).WithField("resource_job_id", resourceJobID)

		logr.Debugln("scheduler: freeing up resources ... ")
		err = p.deregisterJob(logr, resourceJobID, false)
		if err == nil {
			logr.Debugln("scheduler: freed up resources")
		} else {
			logr.WithError(err).Errorln("scheduler: could not free up resources")
		}
		logr.Infoln("scheduler: freed up resources, submitting destroy job")
		_, _, err := p.client.Jobs().Register(job, nil)
		if err != nil {
			logr.WithError(err).Errorln("scheduler: could not register destroy job")
			return err
		}
		logr.Debugln("scheduler: started polling for destroy job")
		_, err = p.pollForJob(ctx, jobID, logr, destroyTimeout, false, []JobStatus{Dead})
		if err != nil {
			logr.WithError(err).Errorln("scheduler: could not complete destroy job")
			return err
		}
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
// note: a dead job is always considered to be in a terminal state
// if remove is set to true, it deregisters the job in case the job hasn't reached a terminal state
// before the timeout or before the context is marked as Done.
// An error is returned if the job did not reach a terminal state
func (p *config) pollForJob(ctx context.Context, id string, logr logger.Logger, timeout time.Duration, remove bool, terminalStates []JobStatus) (*api.Job, error) {
	terminalStates = append(terminalStates, Dead) // we always return from poll if the job is dead
	maxPollTime := time.After(timeout)
	terminal := false
	var job *api.Job
	var err error
	var waitIndex uint64
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
			job, qm, err = p.client.Jobs().Info(id, q)
			if err != nil {
				logr.WithError(err).WithField("job_id", id).Error("could not retrieve job information")
				continue
			}
			if job == nil {
				continue
			}
			waitIndex = qm.LastIndex
			status := Status(*job.Status)

			if slices.Contains(terminalStates, status) {
				logr.WithField("job_id", id).WithField("status", status).Traceln("scheduler: job reached a terminal state")
				terminal = true
				break L
			}
		}
	}
	if job == nil {
		logr.WithField("job_id", id).Errorln("could not poll for job")
		return job, errors.New("could not poll for job")
	}
	// If a terminal state was reached, we return back
	if terminal {
		return job, nil
	}

	// Deregister the job if remove is set as true
	if remove {
		go func() {
			p.deregisterJob(logr, id, false) //nolint:errcheck
		}()
	}

	return job, errors.New("scheduler: job never reached terminal state")
}

// deregisterJob stops the job in Nomad
// if purge is set to true, it gc's it from nomad state as well
func (p *config) deregisterJob(logr logger.Logger, id string, purge bool) error { //nolint:unparam
	logr.WithField("job_id", id).WithField("purge", purge).Traceln("scheduler: trying to deregister job")
	_, _, err := p.client.Jobs().Deregister(id, purge, &api.WriteOptions{})
	if err != nil {
		logr.WithField("job_id", id).WithField("purge", purge).WithError(err).Errorln("scheduler: could not deregister job")
		return err
	}
	logr.WithField("job_id", id).WithField("purge", purge).Infoln("scheduler: successfully deregistered job")
	return nil
}

func generateStartupScript(opts *types.InstanceCreateOpts) string {
	params := &cloudinit.Params{
		Platform:             opts.Platform,
		CACert:               string(opts.CACert),
		LiteEngineLogsPath:   oshelp.GetLiteEngineLogsPath(opts.OS),
		TLSCert:              string(opts.TLSCert),
		TLSKey:               string(opts.TLSKey),
		LiteEnginePath:       opts.LiteEnginePath,
		HarnessTestBinaryURI: opts.HarnessTestBinaryURI,
		PluginBinaryURI:      opts.PluginBinaryURI,
		Tmate:                opts.Tmate,
	}
	return cloudinit.LinuxBash(params)
}

// generate a job ID for a destroy job
func destroyJobID(s string) string {
	return fmt.Sprintf("destroy_job_%s", s)
}

// geenrate a job ID for a init job
func initJobID(s string) string {
	return fmt.Sprintf("init_job_%s", s)
}

// generate a job ID for a resource job
func resourceJobID(s string) string {
	return fmt.Sprintf("init_job_resources_%s", s)
}

func constraints(accountID string) []*api.Constraint {
	constraintList := []*api.Constraint{}

	constraint := &api.Constraint{
		LTarget: "${node.class}",
		RTarget: accountID,
		Operand: "=",
	}

	constraintList = append(constraintList, constraint)
	return constraintList
}

func minNomadResources() *api.Resources {
	return &api.Resources{
		CPU:      intToPtr(minNomadCPUMhz),
		MemoryMB: intToPtr(minNomadMemoryMb),
	}
}

// To make nomad keep resources occupied until the VM is alive, we do a periodic health check
// by checking whether the lite engine port on the VM is open or not.
func generateHealthCheckScript(sleep time.Duration, port string) string {
	sleepSecs := sleep.Seconds()
	return fmt.Sprintf(`
#!/usr/bin/bash
sleep %f
echo "done sleeping, port is: %s"
cntr=0
while true
	do
		nc -vz localhost %s
		if [ $? -eq 1 ]; then
		    echo "port check failed, incrementing counter:"
			echo "cntr: "$cntr
			((cntr++))
			if [ $cntr -eq 3 ]; then
				echo "port check failed three times. output of ignite command:"
				ignite ps
				echo "output of iptables:"
				iptables -L -v -n
				exit 1
			fi
		else
			cntr=0
			echo "Port check passed..."
		fi
		sleep 20
	done`, sleepSecs, port, port)
}

func generateDestroyCommand(vm string) string {
	// Try to force stop and remove if graceful case doesn't work
	return fmt.Sprintf(`
	    %s stop %s; %s rm %s
		if [ $? -ne 0 ]; then
		  %s stop -f %s; %s rm -f %s
		fi
	`, ignitePath, vm, ignitePath, vm, ignitePath, vm, ignitePath, vm)
}
