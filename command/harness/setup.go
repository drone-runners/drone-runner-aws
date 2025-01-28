package harness

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/metric"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	errors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"
	"github.com/harness/lite-engine/logger"

	"github.com/sirupsen/logrus"
)

type SetupVMRequest struct {
	ID                  string            `json:"id"` // stage runtime ID
	PoolID              string            `json:"pool_id"`
	FallbackPoolIDs     []string          `json:"fallback_pool_ids"`
	Tags                map[string]string `json:"tags"`
	CorrelationID       string            `json:"correlation_id"`
	LogKey              string            `json:"log_key"`
	Context             Context           `json:"context,omitempty"`
	ResourceClass       string            `json:"resource_class"`
	ImageName           string            `json:"image_name"`
	api.SetupRequest    `json:"setup_request"`
	GitspaceAgentConfig types.GitspaceAgentConfig `json:"gitspace_agent_config"`
	StorageConfig       types.StorageConfig       `json:"storage_config"`
	Zone                string                    `json:"zone"`
}

type SetupVMResponse struct {
	IPAddress             string       `json:"ip_address"`
	InstanceID            string       `json:"instance_id"`
	GitspacesPortMappings map[int]int  `json:"gitspaces_port_mappings"`
	InstanceInfo          InstanceInfo `json:"instance_info"`
}

var (
	healthCheckTimeout = 5 * time.Minute
	freeAccount        = "free"
	noContext          = context.Background()
)

// HandleSetup tries to setup an instance in any of the pools given in the setup request.
// It calls handleSetup internally for each pool instance trying to complete a setup.
// Instead of passing in the env config, we pass in whatever is needed. This is because
// this same code is being used in the new runner and we want to make sure nothing breaking
// is added here which is not added in the new runner.
func HandleSetup(
	ctx context.Context,
	r *SetupVMRequest,
	s store.StageOwnerStore,
	globalVolumes []string,
	poolMapByAccount map[string]map[string]string,
	runnerName string,
	enableMock bool, // only used for scale testing
	mockTimeout int, // only used for scale testing
	poolManager drivers.IManager,
	metrics *metric.Metrics,
) (*SetupVMResponse, string, error) {
	stageRuntimeID := r.ID
	if stageRuntimeID == "" {
		return nil, "", errors.NewBadRequestError("mandatory field 'id' in the request body is empty")
	}

	if r.PoolID == "" {
		return nil, "", errors.NewBadRequestError("mandatory field 'pool_id' in the request body is empty")
	}

	// Sets up logger to stream the logs in case log config is set
	log := logrus.New()
	var logr *logrus.Entry
	if r.SetupRequest.LogConfig.URL == "" {
		log.Out = os.Stdout
		logr = log.WithField("api", "dlite:setup").WithField("correlationID", r.CorrelationID)
	} else {
		wc := getStreamLogger(r.SetupRequest.LogConfig, r.SetupRequest.MtlsConfig, r.LogKey, r.CorrelationID)
		defer func() {
			if err := wc.Close(); err != nil {
				log.WithError(err).Debugln("failed to close log stream")
			}
		}()

		log.Out = wc
		log.SetLevel(logrus.TraceLevel)
		logr = log.WithField("stage_runtime_id", stageRuntimeID)

		ctx = logger.WithContext(ctx, logr)
	}

	// append global volumes to the setup request.
	for _, pair := range globalVolumes {
		src, _, ro, err := resource.ParseVolume(pair)
		if err != nil {
			log.Warn(err)
			continue
		}
		vol := lespec.Volume{
			HostPath: &lespec.VolumeHostPath{
				ID:       fileID(src),
				Name:     fileID(src),
				Path:     src,
				ReadOnly: ro,
			},
		}
		r.Volumes = append(r.Volumes, &vol)
	}

	logr = AddContext(logr, &r.Context, r.Tags)

	pools := []string{}
	pools = append(pools, r.PoolID)
	pools = append(pools, r.FallbackPoolIDs...)

	var selectedPool, selectedPoolDriver string
	var poolErr error
	var instance *types.Instance
	foundPool := false
	fallback := false

	st := time.Now()
	var owner string

	// TODO: Remove this once we start populating license information.
	if strings.Contains(r.PoolID, freeAccount) || getIsFreeAccount(&r.Context, r.Tags) {
		owner = freeAccount
	} else {
		owner = GetAccountID(&r.Context, r.Tags)
	}

	// try to provision an instance with fallbacks
	for idx, p := range pools {
		if idx > 0 {
			fallback = true
		}
		pool := fetchPool(r.SetupRequest.LogConfig.AccountID, p, poolMapByAccount)
		logr.WithField("pool_id", pool).Traceln("starting the setup process")
		instance, poolErr = handleSetup(ctx, logr, r, runnerName, enableMock, mockTimeout, poolManager, pool, owner)
		if poolErr != nil {
			logr.WithField("pool_id", pool).WithError(poolErr).Errorln("could not setup instance")
			continue
		}
		selectedPool = pool
		foundPool = true
		_, _, selectedPoolDriver = poolManager.Inspect(selectedPool)
		break
	}

	setupTime := time.Since(st) // amount of time it took to provision an instance
	platform, _, driver := poolManager.Inspect(r.PoolID)

	// If a successful fallback happened and we have an instance setup, record it
	if foundPool && instance != nil { // check for instance != nil just in case
		// add an entry in stage pool mapping if instance was created.
		_, findErr := s.Find(noContext, stageRuntimeID)
		if findErr != nil {
			if cerr := s.Create(noContext, &types.StageOwner{StageID: stageRuntimeID, PoolName: selectedPool}); cerr != nil {
				if derr := poolManager.Destroy(noContext, selectedPool, instance.ID, instance, nil); derr != nil {
					logr.WithError(derr).Errorln("failed to cleanup instance on setup failure")
				}
				return nil, "", fmt.Errorf("could not create stage owner entity: %w", cerr)
			}
		}
		if fallback {
			// fallback metric records the first pool ID which was tried and the associated driver.
			// We don't record final pool which was used as this metric is only used to get data about
			// which drivers and pools are causing fallbacks.
			metrics.PoolFallbackCount.WithLabelValues(r.PoolID, instance.OS, instance.Arch, driver, metric.True, strconv.FormatBool(poolManager.IsDistributed()), owner).Inc()
		}
		metrics.WaitDurationCount.WithLabelValues(r.PoolID, instance.OS, instance.Arch,
			driver, metric.ConvertBool(fallback), strconv.FormatBool(poolManager.IsDistributed()), owner).Observe(setupTime.Seconds())
	} else {
		metrics.FailedCount.WithLabelValues(r.PoolID, platform.OS, platform.Arch, driver, strconv.FormatBool(poolManager.IsDistributed()), owner).Inc()
		metrics.BuildCount.WithLabelValues(r.PoolID, platform.OS, platform.Arch, driver, strconv.FormatBool(poolManager.IsDistributed()), "", owner).Inc()
		if fallback {
			metrics.PoolFallbackCount.WithLabelValues(r.PoolID, platform.OS, platform.Arch, driver, metric.False, strconv.FormatBool(poolManager.IsDistributed()), owner).Inc()
		}
		return nil, "", fmt.Errorf("could not provision a VM from the pool: %w", poolErr)
	}

	metrics.BuildCount.WithLabelValues(selectedPool, instance.OS, instance.Arch, string(instance.Provider), strconv.FormatBool(poolManager.IsDistributed()), instance.Zone, owner).Inc()
	instanceInfo := InstanceInfo{
		ID:                instance.ID,
		Name:              instance.Name,
		IPAddress:         instance.Address,
		Port:              instance.Port,
		OS:                platform.OS,
		Arch:              platform.Arch,
		Provider:          string(instance.Provider),
		PoolName:          selectedPool,
		Zone:              instance.Zone,
		StorageIdentifier: instance.StorageIdentifier,
	}
	resp := &SetupVMResponse{InstanceID: instance.ID, IPAddress: instance.Address, GitspacesPortMappings: instance.GitspacePortMappings, InstanceInfo: instanceInfo}

	logr.WithField("selected_pool", selectedPool).
		WithField("ip", instance.Address).
		WithField("id", instance.ID).
		WithField("instance_name", instance.Name).
		WithField("response", fmt.Sprintf("%+v", resp)).
		WithField("tried_pools", pools).
		Traceln("VM setup is complete")

	return resp, selectedPoolDriver, nil
}

// handleSetup tries to setup an instance in a given pool. It tries to provision an instance and
// run a health check on the lite engine. It returns information about the setup
// VM and an error if setup failed.
// It is idempotent so in case there was a setup failure, it cleans up any intermediate state.
func handleSetup(
	ctx context.Context,
	logr *logrus.Entry,
	r *SetupVMRequest,
	runnerName string,
	enableMock bool,
	mockTimeout int,
	poolManager drivers.IManager,
	pool,
	owner string,
) (*types.Instance, error) {
	// check if the pool exists in the pool manager.
	if !poolManager.Exists(pool) {
		return nil, fmt.Errorf("could not find pool: %s", pool)
	}

	stageRuntimeID := r.ID

	// try to provision an instance from the pool manager.
	var query *types.QueryParams
	if poolManager.IsDistributed() {
		query = &types.QueryParams{
			RunnerName: runnerName,
		}
	}
	instance, err := poolManager.Provision(ctx, pool, poolManager.GetTLSServerName(), owner, r.ResourceClass, r.ImageName, query, &r.GitspaceAgentConfig, &r.StorageConfig, r.Zone)
	if err != nil {
		return nil, fmt.Errorf("failed to provision instance: %w", err)
	}

	logr = logr.WithField("pool_id", pool).
		WithField("ip", instance.Address).
		WithField("id", instance.ID).
		WithField("instance_name", instance.Name)

	// Since we are enabling Hardware acceleration for GCP VMs so adding this log for GCP VMs only. Might be changed later.
	if instance.Provider == types.Google {
		logr.Traceln(fmt.Sprintf("creating VM instance with hardware acceleration as %t", instance.EnableNestedVirtualization))
	}
	logr.Traceln("successfully provisioned VM in pool")

	instanceID := instance.ID
	instanceName := instance.Name
	instanceIP := instance.Address

	// cleanUpInstanceFn is a function to terminate the instance if an error occurs later in the handleSetup function
	cleanUpInstanceFn := func(consoleLogs bool) {
		if consoleLogs {
			out, logErr := poolManager.InstanceLogs(context.Background(), pool, instanceID)
			if logErr != nil {
				logr.WithError(logErr).Errorln("failed to fetch console output logs")
			} else {
				// Serial console output is limited to 60000 characters since stackdriver only supports 64KB per log entry
				const maxLogLength = 60000
				totalLength := len(out)
				for start := 0; start < totalLength; start += maxLogLength {
					end := start + maxLogLength
					if end > totalLength {
						end = totalLength
					}
					logIndex := start / maxLogLength
					logrus.WithField("id", instanceID).
						WithField("instance_name", instanceName).
						WithField("ip", instanceIP).
						WithField("pool_id", pool).
						WithField("stage_runtime_id", stageRuntimeID).
						WithField("log_index", logIndex).
						Infof("serial console output: %s", out[start:end])
				}
			}
		}
		err = poolManager.Destroy(context.Background(), pool, instance.ID, instance, nil)
		if err != nil {
			logr.WithError(err).Errorln("failed to cleanup instance on setup failure")
		}
	}

	if instance.IsHibernated {
		instance, err = poolManager.StartInstance(ctx, pool, instance.ID)
		if err != nil {
			go cleanUpInstanceFn(false)
			return nil, fmt.Errorf("failed to start the instance up: %w", err)
		}
	}

	instance.Stage = stageRuntimeID
	instance.Updated = time.Now().Unix()
	err = poolManager.Update(ctx, instance)
	if err != nil {
		go cleanUpInstanceFn(false)
		return nil, fmt.Errorf("failed to tag: %w", err)
	}

	err = poolManager.SetInstanceTags(ctx, pool, instance, r.Tags)
	if err != nil {
		go cleanUpInstanceFn(false)
		return nil, fmt.Errorf("failed to add tags to the instance: %w", err)
	}

	client, err := lehelper.GetClient(instance, poolManager.GetTLSServerName(), instance.Port,
		enableMock, mockTimeout)
	if err != nil {
		go cleanUpInstanceFn(false)
		return nil, fmt.Errorf("failed to create LE client: %w", err)
	}

	// try the healthcheck api on the lite-engine until it responds ok
	logr.Traceln("running healthcheck and waiting for an ok response")
	performDNSLookup := drivers.ShouldPerformDNSLookup(ctx, instance.Platform.OS)

	if _, err = client.RetryHealth(ctx, healthCheckTimeout, performDNSLookup); err != nil {
		go cleanUpInstanceFn(true)
		return nil, fmt.Errorf("failed to call lite-engine retry health: %w", err)
	}

	logr.Traceln("retry health check complete")

	// Currently m1 architecture does not enable nested virtualisation, so we disable docker.
	if instance.Platform.OS == oshelp.OSMac {
		b := false
		r.SetupRequest.MountDockerSocket = &b
	}

	_, err = client.Setup(ctx, &r.SetupRequest)
	if err != nil {
		go cleanUpInstanceFn(true)
		return nil, fmt.Errorf("failed to call setup lite-engine: %w", err)
	}

	return instance, nil
}
