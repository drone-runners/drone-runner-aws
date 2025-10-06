package harness

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/metric"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	errors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"

	"github.com/sirupsen/logrus"
)

type SetupVMRequest struct {
	ID                    string            `json:"id"` // stage runtime ID
	PoolID                string            `json:"pool_id"`
	FallbackPoolIDs       []string          `json:"fallback_pool_ids"`
	Tags                  map[string]string `json:"tags"`
	CorrelationID         string            `json:"correlation_id"`
	LogKey                string            `json:"log_key"`
	Context               Context           `json:"context,omitempty"`
	ResourceClass         string            `json:"resource_class"`
	api.SetupRequest      `json:"setup_request"`
	GitspaceAgentConfig   types.GitspaceAgentConfig `json:"gitspace_agent_config"`
	StorageConfig         types.StorageConfig       `json:"storage_config"`
	Zone                  string                    `json:"zone"`
	MachineType           string                    `json:"machine_type"`
	InstanceInfo          common.InstanceInfo       `json:"instance_info"`
	Timeout               int64                     `json:"timeout,omitempty"`
	IsMarkedForInfraReset bool                      `json:"is_marked_for_infra_reset,omitempty"`
}

// deriveEnableNestedVirtualization reads the nested virtualization flag from the pool YAML config.
// Returns false if not set or not applicable for the provider.
func deriveEnableNestedVirtualization(poolManager drivers.IManager, pool string) bool {
    poolCfg, err := poolManager.GetPoolConfig(pool)
    if err != nil || poolCfg == nil {
        return false
    }
    switch poolCfg.Type {
    case string(types.Google), "gcp":
        if g, ok := poolCfg.Spec.(*config.Google); ok {
            return g.EnableNestedVirtualization
        }
    }
    return false
}

type SetupVMResponse struct {
	IPAddress             string              `json:"ip_address"`
	InstanceID            string              `json:"instance_id"`
	GitspacesPortMappings map[int]int         `json:"gitspaces_port_mappings"`
	InstanceInfo          common.InstanceInfo `json:"instance_info"`
}

var (
	freeAccount = "free"
	noContext   = context.Background()
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
	initStartTime := time.Now()
	stageRuntimeID := r.ID
	if stageRuntimeID == "" {
		return nil, "", errors.NewBadRequestError("mandatory field 'id' in the request body is empty")
	}

	if r.PoolID == "" {
		return nil, "", errors.NewBadRequestError("mandatory field 'pool_id' in the request body is empty")
	}

	// Sets up logger to stream the logs in case log config is set
	log := logrus.New()
	internalLog := logrus.New()
	internalLog.SetFormatter(&logrus.JSONFormatter{})

	var (
		logr         *logrus.Entry
		internalLogr *logrus.Entry
	)
	if r.SetupRequest.LogConfig.URL == "" {
		log.Out = os.Stdout
		logr = log.WithField("api", "dlite:setup").WithField("correlationID", r.CorrelationID)
		// ensure internal logger is initialized even when streaming is disabled
		internalLog.Out = os.Stdout
		internalLogr = internalLog.WithField("stage_runtime_id", stageRuntimeID)
	} else {
		wc := getStreamLogger(r.SetupRequest.LogConfig, r.SetupRequest.MtlsConfig, r.LogKey, r.CorrelationID)
		defer func() {
			if err := wc.Close(); err != nil {
				log.WithError(err).Debugln("failed to close log stream")
			}
		}()

		log.Out = wc
		log.SetLevel(logrus.TraceLevel)
		internalLog.SetLevel(logrus.TraceLevel)
		usePlainFormatter(log)
		internalLog.Out = os.Stdout
		logr = logrus.NewEntry(log)
		internalLogr = internalLog.WithField("stage_runtime_id", stageRuntimeID)
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

	pools := []string{}
	pools = append(pools, r.PoolID)
	pools = append(pools, r.FallbackPoolIDs...)

	var selectedPool, selectedPoolDriver string
	var poolErr error
	var instance *types.Instance

	var (
		foundPool  bool
		fallback   bool
		warmed     bool
		hibernated bool
	)

	var owner string

	// TODO: Remove this once we start populating license information.
	if strings.Contains(r.PoolID, freeAccount) || getIsFreeAccount(&r.Context, r.Tags) {
		owner = freeAccount
	} else {
		owner = GetAccountID(&r.Context, r.Tags)
	}

	platform, _, driver := poolManager.Inspect(r.PoolID)
	internalLogr = AddContext(internalLogr, &r.Context, r.Tags)
	ctx = logger.WithContext(ctx, logger.Logrus(internalLogr))
	internalLogr = internalLogr.
		WithField("resource_class", r.ResourceClass).
		WithField("os", platform.OS).
		WithField("arch", platform.Arch)
	
		printTitle(logr, "Requested machine:")
		printKV(logr, "Machine Size", r.ResourceClass)
		printKV(logr, "OS", capitalize(platform.OS))
		printKV(logr, "Arch", capitalize(platform.Arch))
		poolImageForLog := derivePoolImageForLog(poolManager, r.PoolID)
		printKV(logr, "Image Version", useNonEmpty(r.VMImageConfig.ImageVersion, poolImageForLog))
		nvFromConfig := deriveEnableNestedVirtualization(poolManager, r.PoolID)
		printKV(logr, "Hardware Acceleration (Nested Virtualization)", nvFromConfig)

	// try to provision an instance with fallbacks
	setupTime := time.Duration(0)
	for idx, p := range pools {
		st := time.Now()
		if idx > 0 {
			fallback = true
		}
		pool := fetchPool(r.SetupRequest.LogConfig.AccountID, p, poolMapByAccount)
		internalLogr.WithField("pool_id", pool).Traceln("starting the setup process")
		_, _, poolDriver := poolManager.Inspect(p)
		instance, warmed, hibernated, poolErr = handleSetup(ctx, logr, internalLogr, r, runnerName, enableMock, mockTimeout, poolManager, pool, owner)
		setupTime = time.Since(st)
		metrics.WaitDurationCount.WithLabelValues(
			pool,
			platform.OS,
			platform.Arch,
			poolDriver,
			metric.ConvertBool(fallback),
			strconv.FormatBool(poolManager.IsDistributed()),
			owner,
			r.VMImageConfig.ImageVersion,
			r.VMImageConfig.ImageName,
			strconv.FormatBool(warmed),
			strconv.FormatBool(hibernated),
		).Observe(setupTime.Seconds())
		if poolErr != nil {
			internalLogr.WithField("pool_id", pool).WithError(poolErr).Errorln("could not setup instance")
			metrics.FailedCount.WithLabelValues(
				pool,
				platform.OS,
				platform.Arch,
				poolDriver,
				strconv.FormatBool(poolManager.IsDistributed()),
				owner,
				r.ResourceClass,
				r.VMImageConfig.ImageVersion,
				r.VMImageConfig.ImageName,
			).Inc()
			continue
		}
		selectedPool = pool
		foundPool = true
		_, _, selectedPoolDriver = poolManager.Inspect(selectedPool)
		break
	}

	// If a successful fallback happened and we have an instance setup, record it
	if foundPool && instance != nil { // check for instance != nil just in case
		// add an entry in stage pool mapping if instance was created.
		_, findErr := s.Find(noContext, stageRuntimeID)
		if findErr != nil {
			if cerr := s.Create(noContext, &types.StageOwner{StageID: stageRuntimeID, PoolName: selectedPool}); cerr != nil {
				if derr := poolManager.Destroy(noContext, selectedPool, instance.ID, instance, nil); derr != nil {
					internalLogr.WithError(derr).Errorln("failed to cleanup instance on setup failure")
				}
				return nil, "", fmt.Errorf("could not create stage owner entity: %w", cerr)
			}
		}
		if fallback {
			// fallback metric records the first pool ID which was tried and the associated driver.
			// We don't record final pool which was used as this metric is only used to get data about
			// which drivers and pools are causing fallbacks.
			metrics.PoolFallbackCount.WithLabelValues(
				r.PoolID,
				instance.OS,
				instance.Arch,
				driver,
				metric.True,
				strconv.FormatBool(poolManager.IsDistributed()),
				owner,
				r.ResourceClass,
				r.VMImageConfig.ImageVersion,
				r.VMImageConfig.ImageName,
			).Inc()
		}
		internalLogr.WithField("os", instance.OS).
			WithField("arch", instance.Arch).
			WithField("selected_pool", selectedPool).
			WithField("requested_pool", r.PoolID).
			WithField("instance_address", instance.Address).
			WithField("tried_pools", pools).
			Tracef("init time for vm setup in pool %s is %.2fs", selectedPool, setupTime.Seconds())
	} else {
		metrics.BuildCount.WithLabelValues(
			r.PoolID,
			platform.OS,
			platform.Arch,
			driver,
			strconv.FormatBool(poolManager.IsDistributed()),
			"",
			owner,
			r.ResourceClass,
			"",
			r.VMImageConfig.ImageVersion,
			r.VMImageConfig.ImageName,
		).Inc()
		if fallback {
			metrics.PoolFallbackCount.WithLabelValues(
				r.PoolID,
				platform.OS,
				platform.Arch,
				driver,
				metric.False,
				strconv.FormatBool(poolManager.IsDistributed()),
				owner,
				r.ResourceClass,
				r.VMImageConfig.ImageVersion,
				r.VMImageConfig.ImageName,
			).Inc()
		}
		printError(logr, "Init step failed")
		internalLogr.WithField("stage_runtime_id", stageRuntimeID).
			Errorln("Init step failed")
		return nil, "", fmt.Errorf("could not provision a VM from the pool: %w", poolErr)
	}

	metrics.BuildCount.WithLabelValues(
		selectedPool,
		instance.OS,
		instance.Arch,
		string(instance.Provider),
		strconv.FormatBool(poolManager.IsDistributed()),
		instance.Zone,
		owner,
		r.ResourceClass,
		instance.Address,
		r.VMImageConfig.ImageVersion,
		r.VMImageConfig.ImageName,
	).Inc()

	instanceInfo := common.InstanceInfo{
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
		CAKey:             instance.CAKey,
		CACert:            instance.CACert,
		TLSKey:            instance.TLSKey,
		TLSCert:           instance.TLSCert,
	}
	resp := &SetupVMResponse{InstanceID: instance.ID, IPAddress: instance.Address, GitspacesPortMappings: instance.GitspacePortMappings, InstanceInfo: instanceInfo}

	printOK(logr, "VM setup is complete")

	internalLogr.WithField("selected_pool", selectedPool).
		WithField("stage_runtime_id", stageRuntimeID).
		WithField("ip", instance.Address).
		WithField("id", instance.ID).
		WithField("instance_name", instance.Name).
		WithField("tried_pools", pools).
		Traceln("Init step completed successfully")

	totalInitTime := time.Since(initStartTime)
	internalLogr.WithField("os", instance.OS).
		WithField("arch", instance.Arch).
		WithField("selected_pool", selectedPool).
		WithField("requested_pool", r.PoolID).
		WithField("instance_address", instance.Address).
		Tracef("total init time for vm setup is %.2fs", totalInitTime.Seconds())
	metrics.TotalVMInitDurationCount.WithLabelValues(
		selectedPool,
		platform.OS,
		platform.Arch,
		selectedPoolDriver,
		metric.ConvertBool(fallback),
		strconv.FormatBool(poolManager.IsDistributed()),
		owner,
		r.VMImageConfig.ImageVersion,
		r.VMImageConfig.ImageName,
		strconv.FormatBool(warmed),
		strconv.FormatBool(hibernated),
	).Observe(totalInitTime.Seconds())

	return resp, selectedPoolDriver, nil
}

// handleSetup tries to setup an instance in a given pool. It tries to provision an instance and
// run a health check on the lite engine. It returns information about the setup
// VM and an error if setup failed.
// It is idempotent so in case there was a setup failure, it cleans up any intermediate state.
func handleSetup(
	ctx context.Context,
	buildLog *logrus.Entry,
	internalLogr *logrus.Entry,
	r *SetupVMRequest,
	runnerName string,
	enableMock bool,
	mockTimeout int,
	poolManager drivers.IManager,
	pool,
	owner string,
) (instance *types.Instance, warmed, hibernated bool, err error) {
	// check if the pool exists in the pool manager.
	if !poolManager.Exists(pool) {
		return nil, false, false, fmt.Errorf("could not find pool: %s", pool)
	}

	stageRuntimeID := r.ID

	// try to provision an instance from the pool manager.
	query := &types.QueryParams{
		RunnerName: runnerName,
	}

	shouldUseGoogleDNS := false
	if len(r.SetupRequest.Envs) != 0 {
		if r.SetupRequest.Envs["CI_HOSTED_USE_GOOGLE_DNS"] == "true" {
			shouldUseGoogleDNS = true
		}
	}

	instance, warmed, err = poolManager.Provision(
		ctx,
		pool,
		poolManager.GetTLSServerName(),
		owner,
		r.ResourceClass,
		&r.VMImageConfig,
		query,
		&r.GitspaceAgentConfig,
		&r.StorageConfig,
		r.Zone,
		r.MachineType,
		shouldUseGoogleDNS,
		&r.InstanceInfo,
		r.Timeout,
		r.IsMarkedForInfraReset,
	)
	if err != nil {
		return nil, false, false, fmt.Errorf("failed to provision instance: %w", err)
	}	

	ilog := internalLogr.WithField("pool_id", pool).
		WithField("ip", instance.Address).
		WithField("id", instance.ID).
		WithField("instance_name", instance.Name).
		WithField("image_name", useNonEmpty(r.VMImageConfig.ImageName, instance.Image)).
		WithField("image_version", r.VMImageConfig.ImageVersion)

	ilog.Traceln("successfully provisioned VM in pool")
	printOK(buildLog, "Machine provisioned successfully")
	printTitle(buildLog, "Preparing a machine to execute this stage...")

	ilog.WithFields(logrus.Fields{
		"pool_id":       pool,
		"ip":            instance.Address,
		"instance_id":   instance.ID,
		"instance_name": instance.Name,
	}).Infoln("Machine provisioned successfully")

	instanceID := instance.ID
	instanceName := instance.Name
	instanceIP := instance.Address

	// cleanUpInstanceFn is a function to terminate the instance if an error occurs later in the handleSetup function
	cleanUpInstanceFn := func(consoleLogs bool) {
		if consoleLogs {
			out, logErr := poolManager.InstanceLogs(context.Background(), pool, instanceID)
			if logErr != nil {
				ilog.WithError(logErr).Errorln("failed to fetch console output logs")
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
		err = poolManager.Destroy(context.Background(), pool, instanceID, instance, nil)
		if err != nil {
			ilog.WithError(err).Errorln("failed to cleanup instance on setup failure")
		}
	}

	if instance.IsHibernated {
		ilog.Tracef("instance %s is hibernated", instance.ID)
		instance, err = poolManager.StartInstance(ctx, pool, instance.ID, &r.InstanceInfo)
		if err != nil {
			go cleanUpInstanceFn(false)
			return nil, false, false, fmt.Errorf("failed to start the instance up: %w", err)
		}
		ilog.Tracef("instance %s is started", instance.ID)
	}

	instance.Stage = stageRuntimeID
	instance.Updated = time.Now().Unix()
	err = poolManager.Update(ctx, instance)
	if err != nil {
		go cleanUpInstanceFn(false)
		return nil, false, false, fmt.Errorf("failed to tag: %w", err)
	}

	err = poolManager.SetInstanceTags(ctx, pool, instance, r.Tags)
	if err != nil {
		go cleanUpInstanceFn(false)
		return nil, false, false, fmt.Errorf("failed to add tags to the instance: %w", err)
	}

	client, err := lehelper.GetClient(instance, poolManager.GetTLSServerName(), instance.Port,
		enableMock, mockTimeout)
	if err != nil {
		go cleanUpInstanceFn(false)
		return nil, false, false, fmt.Errorf("failed to create LE client: %w", err)
	}
	// try the healthcheck api on the lite-engine until it responds ok
	performDNSLookup := drivers.ShouldPerformDNSLookup(ctx, instance.Platform.OS)
	runnerConfig := poolManager.GetRunnerConfig()

	// override the health check timeouts
	healthCheckTimeout := time.Duration(runnerConfig.HealthCheckTimeout) * time.Minute
	if instance.Platform.OS == "windows" {
		healthCheckTimeout = time.Duration(runnerConfig.HealthCheckWindowsTimeout) * time.Minute
	}

	if _, err = client.RetryHealth(ctx, healthCheckTimeout, performDNSLookup); err != nil {
		printError(buildLog, "Machine health check failed")
		go cleanUpInstanceFn(true)
		return nil, false, false, fmt.Errorf("failed to call lite-engine retry health: %w", err)
	}

	printOK(buildLog, "Machine health check passed")
	internalLogr.Infoln("Machine health check passed")

	// Currently m1 architecture does not enable nested virtualisation, so we disable docker.
	if instance.Platform.OS == oshelp.OSMac {
		b := false
		r.SetupRequest.MountDockerSocket = &b
	}

	_, err = client.Setup(ctx, &r.SetupRequest)
	if err != nil {
		printError(buildLog, "Machine setup failed")
		go cleanUpInstanceFn(true)
		return nil, false, false, fmt.Errorf("failed to call setup lite-engine: %w", err)
	}

	hibernated = instance.IsHibernated
	return instance, warmed, hibernated, nil
}

// derivePoolImageForLog extracts an image identifier from the pool YAML config for logging purposes.
// It returns an empty string if the pool config is missing or does not match a known driver type.
func derivePoolImageForLog(poolManager drivers.IManager, pool string) string {
    poolCfg, err := poolManager.GetPoolConfig(pool)
    if err != nil || poolCfg == nil {
        return ""
    }
    switch poolCfg.Type {
    case string(types.Google), "gcp":
        if g, ok := poolCfg.Spec.(*config.Google); ok {
            return g.Image
        }
    case string(types.DigitalOcean):
        if d, ok := poolCfg.Spec.(*config.DigitalOcean); ok {
            return d.Image
        }
    case string(types.Azure):
        if az, ok := poolCfg.Spec.(*config.Azure); ok {
            if az.Image.Version != "" {
                return az.Image.Version
            } else if az.Image.SKU != "" {
                return az.Image.SKU
            } else if az.Image.Offer != "" {
                return az.Image.Offer
            }
        }
    case string(types.Amazon):
        if a, ok := poolCfg.Spec.(*config.Amazon); ok {
            return a.AMI
        }
    case string(types.Nomad):
        if n, ok := poolCfg.Spec.(*config.Nomad); ok {
            return n.VM.Image
        }
    }
    return ""
}
