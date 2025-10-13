package nomad

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/hashicorp/nomad/api"
	"golang.org/x/exp/slices"
)

var _ drivers.Driver = (*config)(nil)

//go:embed gitspace/scripts/delete_ceph_storage.sh
var deleteCephStorageScript string

//go:embed gitspace/scripts/detach_ceph_storage.sh
var detachCephStorageScript string

type config struct {
	address           string
	vmImage           string
	vmMemoryGB        string
	vmCpus            string
	vmDiskSize        string
	caCertPath        string
	clientCertPath    string
	clientKeyPath     string
	insecure          bool
	noop              bool
	enablePinning     map[string]string
	client            *api.Client
	virtualizer       Virtualizer
	resource          map[string]cf.NomadResource
	username          string
	password          string
	userData          string
	virtualizerEngine string
	machinePassword   string
	nomadToken        string
	nomadConfig       *types.NomadConfig
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

	// Load configuration from environment
	envConfig, err := cf.FromEnviron()
	if err != nil {
		return nil, fmt.Errorf("failed to load environment config: %w", err)
	}

	// Set NomadConfig from environment
	p.nomadConfig = (&envConfig).NomadConfig()

	for _, opt := range opts {
		opt(p)
	}
	if p.client == nil {
		client, err := NewClient(p.address, p.insecure, p.caCertPath, p.clientCertPath, p.clientKeyPath, p.nomadToken)
		if err != nil {
			return nil, err
		}
		p.client = client
	}
	if p.virtualizerEngine == "tart" {
		p.virtualizer = NewMacVirtualizer(p.nomadConfig)
	} else {
		p.virtualizer = NewLinuxVirtualizer(p.nomadConfig)
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

func (p *config) GetFullyQualifiedImage(_ context.Context, config *types.VMImageConfig) (string, error) {
	// If no image name is provided, return the default VM image
	if config.ImageName == "" {
		return p.vmImage, nil
	}

	// For Nomad, the image name is the VM image path or identifier
	return config.ImageName, nil
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
func (p *config) Create(ctx context.Context, opts *types.InstanceCreateOpts) (*types.Instance, error) { //nolint:gocyclo,funlen
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
				class = p.nomadConfig.LargeBaremetalClass
			}
		}
	}

	if class == "" {
		class = p.virtualizer.GetGlobalAccountID()
	}

	vmImageConfig := opts.VMImageConfig
	if vmImageConfig.ImageName == "" {
		vmImageConfig = types.VMImageConfig{
			ImageName: p.vmImage,
			Username:  p.username,
			Password:  p.password,
		}
	}

	// Create a resource job which occupies resources until the VM is alive to avoid
	// oversubscribing the node
	var resourceJob *api.Job
	var resourceJobID string
	if p.noop {
		resourceJob, resourceJobID = p.resourceJobNoop(cpus, memGB, vm, len(opts.GitspaceOpts.Ports))
	} else {
		resourceJob, resourceJobID = p.resourceJob(cpus, memGB, p.virtualizer.GetMachineFrequency(), len(opts.GitspaceOpts.Ports), vm, class, vmImageConfig, p.virtualizer.GetHealthCheckupGenerator())
	}

	logr := logger.FromContext(ctx).WithField("vm", vm).WithField("node_class", class).WithField("resource_job_id", resourceJobID)
	logr.Infoln("scheduler: finding a node which has available resources ... ")

	_, _, err = p.client.Jobs().Register(resourceJob, nil)
	if err != nil {
		defer func() {
			go p.getAllocationsForJob(logr, resourceJobID)
		}()
		return nil, fmt.Errorf("scheduler: could not register job, err: %w", err)
	}
	// If resources don't become available in `p.nomadConfig.ResourceJobTimeout`, we fail the step
	job, err := p.pollForJob(ctx, resourceJobID, logr, p.nomadConfig.ResourceJobTimeout, true, []JobStatus{Running, Dead})
	if err != nil {
		defer func() {
			go p.getAllocationsForJob(logr, resourceJobID)
		}()
		return nil, fmt.Errorf("scheduler: could not find a node with available resources, err: %w", err)
	}

	if job == nil || isTerminal(job) {
		defer func() {
			go p.getAllocationsForJob(logr, resourceJobID)
		}()
		return nil, fmt.Errorf("scheduler: resource job reached terminal state before starting")
	}
	logr.Infoln("scheduler: found a node with available resources")

	// get the machine details where the resource job was allocated
	ip, id, liteEngineHostPort, gitspacesPorts, err := p.fetchMachine(logr, resourceJobID)
	if err != nil {
		defer func() {
			go p.deregisterJob(logr, resourceJobID, false) //nolint:errcheck
		}()
		return nil, err
	}

	var gitspacesPortMappingsString string
	gitspacesPortMappings := make(map[int]int)

	if len(opts.GitspaceOpts.Ports) != len(gitspacesPorts) {
		return nil, fmt.Errorf("scheduler: could not allocate required number of ports for gitspaces")
	}

	for i, vmPort := range opts.GitspaceOpts.Ports {
		gitspacesPortMappings[vmPort] = gitspacesPorts[i]
		gitspacesPortMappingsString += fmt.Sprintf("%d->%d;", gitspacesPorts[i], vmPort)
	}

	// create a VM on the same machine where the resource job was allocated
	var initJob *api.Job
	var initJobID, initTaskGroup string
	if p.noop {
		initJob, initJobID, initTaskGroup = p.initJobNoop(vm, id, liteEngineHostPort)
	} else {
		initJob, initJobID, initTaskGroup, err = p.virtualizer.GetInitJob(vm, id, p.userData, p.machinePassword, p.vmImage, vmImageConfig, liteEngineHostPort, resource, opts, gitspacesPortMappings, opts.Timeout) //nolint
		if err != nil {
			defer func() {
				go p.deregisterJob(logr, resourceJobID, false) //nolint:errcheck
			}()
			return nil, err
		}
	}

	logr = logr.WithField("init_job_id", initJobID).
		WithField("node_ip", ip).
		WithField("node_port", liteEngineHostPort).
		WithField("vm_image", vmImageConfig.ImageName).
		WithField("vm_image_version", vmImageConfig.ImageVersion)

	if gitspacesPortMappingsString != "" {
		logr = logr.WithField("gitspaces_port_mapping", gitspacesPortMappingsString)
	}

	labelsBytes, marshalErr := json.Marshal(opts.Labels)
	if marshalErr != nil {
		defer func() {
			go p.deregisterJob(logr, resourceJobID, false) //nolint:errcheck
		}()
		return nil, fmt.Errorf("scheduler: could not marshal labels: %v, err: %w", opts.Labels, marshalErr)
	}
	instance := &types.Instance{
		ID:                   vm,
		NodeID:               id,
		Name:                 vm,
		Platform:             opts.Platform,
		State:                types.StateCreated,
		CACert:               opts.CACert,
		CAKey:                opts.CAKey,
		TLSCert:              opts.TLSCert,
		TLSKey:               opts.TLSKey,
		Provider:             types.Nomad,
		Pool:                 opts.PoolName,
		Started:              time.Now().Unix(),
		Updated:              time.Now().Unix(),
		Port:                 int64(liteEngineHostPort),
		GitspacePortMappings: gitspacesPortMappings,
		Address:              ip,
		Labels:               labelsBytes,
	}
	if opts.StorageOpts.Identifier != "" {
		instance.StorageIdentifier = opts.StorageOpts.CephPoolIdentifier + "/" + opts.StorageOpts.Identifier
	}

	logr.Infoln("scheduler: submitting VM creation job")
	_, _, err = p.client.Jobs().Register(initJob, nil)
	if err != nil {
		defer func() {
			go p.getAllocationsForJob(logr, initJobID)
			go p.deregisterJob(logr, resourceJobID, false) //nolint:errcheck
		}()
		return nil, fmt.Errorf("scheduler: could not register job, err: %w ip: %s, resource_job_id: %s, init_job_id: %s, vm: %s", err, ip, resourceJobID, initJobID, vm)
	}
	logr.Infoln("scheduler: successfully submitted init job, started polling for job status")
	_, err = p.pollForJob(ctx, initJobID, logr, p.virtualizer.GetInitJobTimeout(vmImageConfig), true, []JobStatus{Dead})
	if err != nil {
		defer func() {
			go p.getAllocationsForJob(logr, initJobID)
			// Destroy the VM if it's in a partially created state
			go p.Destroy(context.Background(), []*types.Instance{instance}) //nolint:errcheck
		}()
		return nil, fmt.Errorf("scheduler: could not poll for init job status, failed with error: %s on ip: %s, resource_job_id: %s, init_job_id: %s, vm: %s", err, ip, resourceJobID, initJobID, vm)
	}

	// Make sure all subtasks in the init job passed
	err = p.checkTaskGroupStatus(initJobID, initTaskGroup)
	if err != nil {
		defer func() {
			go p.getAllocationsForJob(logr, initJobID)
			go p.Destroy(context.Background(), []*types.Instance{instance}) //nolint:errcheck
		}()
		return nil, fmt.Errorf("scheduler: init job failed with error: %s on ip: %s, resource_job_id: %s, init_job_id: %s, vm: %s", err, ip, resourceJobID, initJobID, vm)
	}
	logr.Infoln("scheduler: Successfully submitted polled job")

	// Check status of the resource job. If it reached a terminal state, destroy the VM and remove the resource job
	job, _, err = p.client.Jobs().Info(resourceJobID, &api.QueryOptions{})
	if err != nil {
		defer func() {
			go p.getAllocationsForJob(logr, resourceJobID)
		}()
		return nil, fmt.Errorf("scheduler: could not query resource job, err: %w, resource_job_id: %s, init_job_id: %s, vm: %s", err, resourceJobID, initJobID, vm)
	}
	if job == nil || isTerminal(job) {
		defer func() {
			go p.getAllocationsForJob(logr, resourceJobID)
			go p.Destroy(context.Background(), []*types.Instance{instance}) //nolint:errcheck
		}()
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
func (p *config) resourceJob(cpus, memGB, machineFrequencyMhz, gitspacesPortCount int, vm, accountID string, vmImageConfig types.VMImageConfig, healthCheckGenerator func(time.Duration, string, string) string) (job *api.Job, id string) { //nolint
	id = resourceJobID(vm)
	portLabel := vm

	sleepTime := p.nomadConfig.ResourceJobTimeout + p.virtualizer.GetInitJobTimeout(vmImageConfig) + 2*time.Minute // add 2 minutes for a buffer

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
				Networks:                  getNetworkResources(portLabel, gitspacesPortCount),
				StopAfterClientDisconnect: &p.nomadConfig.ClientDisconnectTimeout,
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
							"command": p.virtualizer.GetEntryPoint(),
							"args":    []string{"-c", healthCheckGenerator(sleepTime, vm, p.virtualizer.GetHealthCheckPort(portLabel))},
						},
					},
				},
			},
		},
	}
	return job, id
}

// fetchMachine returns details of the machine where the job has been allocated
func (p *config) fetchMachine(logr logger.Logger, id string) (ip, nodeID string, liteEngineHostPort int, ports []int, err error) {
	// Get the allocation corresponding to this job submission. If this call fails, there is not much we can do in terms
	// of cleanup - as the job has created a virtual machine but we could not parse the node identifier.
	l, _, err := p.client.Jobs().Allocations(id, false, nil)
	if err != nil {
		return ip, nodeID, liteEngineHostPort, ports, err
	}
	if len(l) == 0 {
		return ip, nodeID, liteEngineHostPort, ports, errors.New("scheduler: no allocation found for the job")
	}

	nodeID = l[0].NodeID
	allocID := l[0].ID
	if nodeID == "" || allocID == "" {
		return ip, nodeID, liteEngineHostPort, ports, errors.New("scheduler: could not find an allocation identifier for the job")
	}

	alloc, _, err := p.client.Allocations().Info(allocID, &api.QueryOptions{})
	if err != nil {
		return ip, nodeID, liteEngineHostPort, ports, err
	}

	// Not expected - if nomad is unable to find a port, it should not run the job at all.
	if alloc.Resources.Networks == nil || len(alloc.Resources.Networks) == 0 {
		err = fmt.Errorf("scheduler: could not allocate network and ports for job")
		logr.Errorln(err)
		return ip, nodeID, liteEngineHostPort, ports, err
	}

	liteEngineHostPort = alloc.Resources.Networks[0].DynamicPorts[0].Value
	for i := 1; i < len(alloc.Resources.Networks[0].DynamicPorts); i++ {
		ports = append(ports, alloc.Resources.Networks[0].DynamicPorts[i].Value)
	}

	// sanity check
	if liteEngineHostPort <= 0 || liteEngineHostPort > 65535 {
		err = fmt.Errorf("scheduler: lite engine host port %d generated is not a valid port", liteEngineHostPort)
		logr.Errorln(err)
		return ip, nodeID, liteEngineHostPort, ports, err
	}
	for _, port := range ports {
		if port <= 0 || port > 65535 {
			err = fmt.Errorf("scheduler: gitspace host port %d generated is not a valid port", port)
			logr.Errorln(err)
			return ip, nodeID, liteEngineHostPort, ports, err
		}
	}

	n, _, err := p.client.Nodes().Info(nodeID, &api.QueryOptions{})
	if err != nil {
		logr.WithError(err).Errorln("scheduler: could not get information about the node which picked up the resource job")
		return ip, nodeID, liteEngineHostPort, ports, err
	}

	ip = strings.Split(n.HTTPAddr, ":")[0]
	if net.ParseIP(ip) == nil {
		err = fmt.Errorf("scheduler: could not parse client machine IP: %s", ip)
		logr.Errorln(err)
		return ip, nodeID, liteEngineHostPort, ports, err
	}

	return ip, nodeID, liteEngineHostPort, ports, nil
}

// destroyJob returns a job targeted to the given node which stops and removes the VM
func (p *config) destroyJob(ctx context.Context, vm, nodeID, storageIdentifier string, destroyGenerator func(string, string) string, storageCleanupType *storage.CleanupType) (job *api.Job, id string) { //nolint:lll
	logr := logger.FromContext(ctx).WithField("vm", vm).WithField("destroy_job_id", destroyJobID)
	id = destroyJobID(vm)
	constraint := &api.Constraint{
		LTarget: "${node.unique.id}",
		RTarget: nodeID,
		Operand: "=",
	}
	destroyCmd := destroyGenerator(vm, p.machinePassword)

	var cephStorageScriptEncoded string
	var cephStorageScriptPath string
	var err error
	if storageIdentifier != "" && storageCleanupType != nil {
		cephStorageScriptEncoded, cephStorageScriptPath, err = cleanupStorage(vm, storageIdentifier, storageCleanupType, &destroyCmd)
		if err != nil {
			logr.Errorln(err)
			return job, id
		}
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
				StopAfterClientDisconnect: &p.nomadConfig.ClientDisconnectTimeout,
				RestartPolicy: &api.RestartPolicy{
					Attempts: intToPtr(p.nomadConfig.DestroyRetryAttempts),
				},
				Name:  stringToPtr(fmt.Sprintf("delete_task_group_%s", vm)),
				Count: intToPtr(1),
				Tasks: []*api.Task{
					{
						Name:      "ignite_stop_and_rm",
						Resources: minNomadResources(p.nomadConfig.MinNomadCPUMhz, p.nomadConfig.MinNomadMemoryMb),
						Driver:    "raw_exec",
						Config: map[string]interface{}{
							"command": p.virtualizer.GetEntryPoint(),
							"args":    []string{"-c", destroyCmd},
						},
					},
				},
			},
		},
	}
	if storageIdentifier != "" {
		job.TaskGroups[0].Tasks = append(job.TaskGroups[0].Tasks,
			p.getCephStorageScriptCreateTask(cephStorageScriptEncoded, cephStorageScriptPath),
			p.getCephStorageScriptCleanupTask(cephStorageScriptPath))
	}
	return job, id
}

func cleanupStorage(vm, storageIdentifier string, storageCleanupType *storage.CleanupType, destroyCmd *string) (cephStorageScriptEncoded, cephStorageScriptPath string, err error) {
	var cephStorageCleanupScriptTemplate *template.Template
	if *storageCleanupType == storage.Detach {
		cephStorageCleanupScriptTemplate = template.Must(template.New("detach-ceph-storage").Funcs(funcs).Parse(detachCephStorageScript))
	} else {
		cephStorageCleanupScriptTemplate = template.Must(template.New("delete-ceph-storage").Funcs(funcs).Parse(deleteCephStorageScript))
	}

	sb := &strings.Builder{}
	storageIdentifierSplit := strings.Split(storageIdentifier, "/")

	if len(storageIdentifierSplit) != 2 { //nolint:gomnd
		return "", "", fmt.Errorf("scheduler: could not parse storage identifier %s", storageIdentifier)
	}
	params := struct {
		CephPoolIdentifier string
		RBDIdentifier      string
	}{
		CephPoolIdentifier: storageIdentifierSplit[0],
		RBDIdentifier:      storageIdentifierSplit[1],
	}
	err = cephStorageCleanupScriptTemplate.Execute(sb, params)
	if err != nil {
		return "", "", fmt.Errorf("scheduler: failed to execute de-provision-ceph-storage template to get the script: %w", err)
	}
	cephStorageScriptEncoded = base64.StdEncoding.EncodeToString([]byte(sb.String()))
	cephStorageScriptPath = fmt.Sprintf("/usr/local/bin/%s_delete_ceph_storage.sh", vm)
	*destroyCmd += fmt.Sprintf("\ncat %s | base64 --decode | bash", cephStorageScriptPath)
	return cephStorageScriptEncoded, cephStorageScriptPath, nil
}

func (p *config) Destroy(ctx context.Context, instances []*types.Instance) (err error) {
	storageCleanupType := storage.Delete
	return p.DestroyInstanceAndStorage(ctx, instances, &storageCleanupType)
}

// DestroyInstanceAndStorage destroys the VM in the bare metal machine
func (p *config) DestroyInstanceAndStorage(ctx context.Context, instances []*types.Instance, storageCleanupType *storage.CleanupType) (err error) {
	for _, instance := range instances {
		var job *api.Job
		var jobID string
		if p.noop {
			job, jobID = p.destroyJobNoop(instance.ID, instance.NodeID)
		} else {
			job, jobID = p.destroyJob(ctx, instance.ID, instance.NodeID, instance.StorageIdentifier, p.virtualizer.GetDestroyScriptGenerator(), storageCleanupType)
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
		_, err = p.pollForJob(ctx, jobID, logr, p.nomadConfig.DestroyTimeout, false, []JobStatus{Dead})
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

func (p *config) Hibernate(_ context.Context, _, _, _ string) error {
	return nil
}

func (p *config) Start(_ context.Context, _ *types.Instance, _ string) (string, error) {
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
	waitIndex = 1
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
				time.Sleep(15 * time.Second) //nolint
				continue
			}
			if job == nil {
				time.Sleep(15 * time.Second) //nolint
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

func (p *config) getAllocationsForJob(logr logger.Logger, id string) {
	allocs, _, err := p.client.Jobs().Allocations(id, true, &api.QueryOptions{})
	if err != nil || allocs == nil || len(allocs) == 0 || allocs[0] == nil {
		logr.WithError(err).Errorln("scheduler: unable to get allocations")
		return
	}
	alloc := allocs[0]
	allocState := map[string][]api.TaskEvent{} // Use non-pointer slices

	var (
		allocation *api.Allocation
	)

	for taskName, taskState := range alloc.TaskStates {
		var events []api.TaskEvent
		if taskState != nil {
			for _, event := range taskState.Events {
				if event != nil {
					events = append(events, *event) // Dereference the pointer
				}
			}
			allocState[taskName] = events

			// Check if the task has failed
			if taskState.Failed {
				if allocation == nil {
					if allocation, _, err = p.client.Allocations().Info(alloc.ID, &api.QueryOptions{}); err != nil {
						continue
					}
				}
				p.streamStdLogs(allocation, taskName, logr, "stdout")
				p.streamStdLogs(allocation, taskName, logr, "stderr")
			}
		}
	}
	// Marshal allocState to JSON
	allocStateBytes, err := json.MarshalIndent(allocState, "", "  ")
	if err == nil {
		logr.WithField("alloc_state", string(allocStateBytes)).Infoln("scheduler: successfully fetched job allocations")
	} else {
		// fallback
		logr.WithField("alloc_state", allocState).Infoln("scheduler: successfully fetched job allocations")
	}
}

func (p *config) streamStdLogs(allocation *api.Allocation, taskName string, logr logger.Logger, logType string) {
	const maxRetries = 3
	const sleepBetweenTry = 3 * time.Second
	logReceived := false

	for retryCount := 0; retryCount < maxRetries; retryCount++ {
		cancel := make(chan struct{})

		logs, errCh := p.client.AllocFS().Logs(allocation, false, taskName, logType, "", int64(0), cancel, &api.QueryOptions{})
		if logs == nil {
			time.Sleep(sleepBetweenTry)
			close(cancel)
			continue
		}
		timeout := time.After(twentySecondsTimeout) // Set the timeout duration

		// Handle logs in real-time with a timeout
	streamLoop:
		for {
			select {
			case <-cancel:
				return
			case logLine := <-logs:
				if logLine == nil {
					if logReceived {
						close(cancel)
						return
					}

					time.Sleep(sleepBetweenTry)

					continue
				}
				logReceived = true
				if logType == "stderr" {
					logr.WithField("task_name", taskName).WithField(logType, string(logLine.Data)).Errorln("scheduler: successfully task " + logType + " logs")
				} else {
					logr.WithField("task_name", taskName).WithField(logType, string(logLine.Data)).Infoln("scheduler: successfully task " + logType + " logs")
				}
			case <-timeout:
				logr.WithField("task_name", taskName).Warnln("scheduler: log streaming timed out")
				close(cancel)
				return
			case err := <-errCh:
				if err != nil {
					logr.WithField("task_name", taskName).WithError(err).Errorln("scheduler: failed to stream task stderr logs")
					close(cancel)
					time.Sleep(sleepBetweenTry)
					break streamLoop // Break out of the labeled loop
				}
			}
		}
	}
}

func (p *config) getCephStorageScriptCleanupTask(deProvisionCephStorageScriptPath string) *api.Task {
	return &api.Task{
		Name:      "cleanup_ceph_storage_script_from_host",
		Driver:    "raw_exec",
		Resources: minNomadResources(p.nomadConfig.MinNomadCPUMhz, p.nomadConfig.MinNomadMemoryMb),
		Config: map[string]interface{}{
			"command": p.virtualizer.GetEntryPoint(),
			"args":    []string{"-c", fmt.Sprintf("rm %s", deProvisionCephStorageScriptPath)},
		},
		Lifecycle: &api.TaskLifecycle{
			Sidecar: false,
			Hook:    "poststop",
		},
	}
}

func (p *config) getCephStorageScriptCreateTask(cephStorageScriptEncoded, cephStorageScriptPath string) *api.Task {
	return &api.Task{
		Name:      "create_ceph_storage_cleanup_script_on_host",
		Driver:    "raw_exec",
		Resources: minNomadResources(p.nomadConfig.MinNomadCPUMhz, p.nomadConfig.MinNomadMemoryMb),
		Config: map[string]interface{}{
			"command": p.virtualizer.GetEntryPoint(),
			"args": []string{
				"-c",
				fmt.Sprintf(`echo %s >> %s`, cephStorageScriptEncoded, cephStorageScriptPath),
			},
		},
		Lifecycle: &api.TaskLifecycle{
			Sidecar: false,
			Hook:    "prestart",
		},
	}
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

// Request Nomad to assign available ports dynamically.
// 1 for lite engine and another n ports for gitspaces as requested
// Since the port labels have to be unique, VM ID is used usually.
// For gitspaces port, prefix of _gitspaces_{port_number_count} is added to the label to keep it unique
func getNetworkResources(portLabel string, gitspacesPortCount int) []*api.NetworkResource {
	dynamicPorts := []api.Port{{Label: portLabel}}
	for i := 0; i < gitspacesPortCount; i++ {
		dynamicPorts = append(dynamicPorts, api.Port{Label: fmt.Sprintf("%s_gitspaces_%d", portLabel, i+1)})
	}
	return []*api.NetworkResource{{DynamicPorts: dynamicPorts}}
}
