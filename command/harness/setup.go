package harness

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/metric"

	"github.com/drone/runner-go/logger"
	"github.com/harness/lite-engine/api"
	lespec "github.com/harness/lite-engine/engine/spec"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/drivers/google"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	errors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/harness/egress"
	"github.com/drone-runners/drone-runner-aws/engine/resource"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/sirupsen/logrus"
)

type SetupVMRequest struct {
	CapacityReservationRequest
	api.SetupRequest      `json:"setup_request"`
	LogKey                string                    `json:"log_key"`
	CorrelationID         string                    `json:"correlation_id"`
	GitspaceAgentConfig   types.GitspaceAgentConfig `json:"gitspace_agent_config"`
	InstanceInfo          common.InstanceInfo       `json:"instance_info"`
	IsMarkedForInfraReset bool                      `json:"is_marked_for_infra_reset,omitempty"`
	// SkipCloudVMCleanup, when true, instructs the runner to label the VM
	// retain=true so the background purger leaves it alone. Set by CI Manager
	// when the CI_SKIP_CLOUD_VM_CLEANUP FF is on for the target.
	SkipCloudVMCleanup bool `json:"skip_cloud_vm_cleanup,omitempty"`
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

// longRunningTimeoutCutoffSec is the boundary used to set the
// harness-long-running label on a CI VM. Requests whose stage timeout
// exceeds this value are tagged so the reaper applies the extended
// grace window (7 days) instead of the default maxAge.
const longRunningTimeoutCutoffSec = int64(24 * 60 * 60)

// HandleSetup tries to setup an instance in any of the pools given in the setup request.
// It calls handleSetup internally for each pool instance trying to complete a setup.
// Instead of passing in the env config, we pass in whatever is needed. This is because
// this same code is being used in the new runner and we want to make sure nothing breaking
// is added here which is not added in the new runner.
//
//nolint:gocyclo
func HandleSetup(
	ctx context.Context,
	r *SetupVMRequest,
	s store.StageOwnerStore,
	crs store.CapacityReservationStore,
	globalVolumes []string,
	poolMapByAccount map[string]map[string]string,
	runnerName string,
	enableMock bool, // only used for scale testing
	mockTimeout int, // only used for scale testing
	poolManager drivers.IManager,
	metrics *metric.Metrics,
	envFallbackPoolIDs []string,
	egressDefaultIPs []string,
	firewallStore store.FirewallStore,
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
		wc := getStreamLogger(&r.SetupRequest.LogConfig, r.SetupRequest.MtlsConfig, r.LogKey, r.CorrelationID)
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
	// If request has no fallback pools, use the env config fallback pools if available
	if len(r.FallbackPoolIDs) == 0 && len(envFallbackPoolIDs) > 0 {
		pools = append(pools, envFallbackPoolIDs...)
	} else {
		pools = append(pools, r.FallbackPoolIDs...)
	}

	var selectedPool, selectedPoolDriver string
	var poolErr error
	var instance *types.Instance

	var (
		foundPool  bool
		fallback   bool
		warmed     bool
		hibernated bool
		variantID  string
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

	logRequestedMachine(logr, poolManager, fetchPool(r.SetupRequest.LogConfig.AccountID, r.PoolID, poolMapByAccount),
		&platform, r.ResourceClass, r.VMImageConfig.ImageVersion, r.VMImageConfig.ImageName, stageRuntimeID, r.NestedVirtualization)

	// try to provision an instance with fallbacks
	setupTime := time.Duration(0)

	var capacity *types.CapacityReservation
	if crs != nil {
		// Atomically find and claim the capacity reservation, transitioning from "created" to "inuse"
		// This prevents race conditions where multiple requests could claim the same capacity
		capacities, capClaimErr := crs.FindAndClaim(
			noContext,
			&types.CapacityReservationQueryParams{StageID: stageRuntimeID, Limit: 1},
			types.CapacityReservationStateInUse,
			[]types.CapacityReservationState{types.CapacityReservationStateCreated},
		)

		if capClaimErr != nil {
			// sql.ErrNoRows means either the capacity doesn't exist or it's not in "created" state
			internalLogr.WithError(capClaimErr).Debug("could not find or claim capacity reservation")
		} else if len(capacities) > 0 && capacities[0].PoolName != "" {
			capacity = capacities[0]
		}
	}

	// if capacity was reserved in any pool then that pool should be tried first
	if capacity != nil && capacity.PoolName != "" {
		for i, p := range pools {
			if p == capacity.PoolName {
				// Move matched pool to the front
				if i != 0 {
					pools = append([]string{p}, append(pools[:i], pools[i+1:]...)...)
				}
				break
			}
		}
	}

	for idx, p := range pools {
		st := time.Now()
		if idx > 0 {
			fallback = true
		}
		pool := fetchPool(r.SetupRequest.LogConfig.AccountID, p, poolMapByAccount)
		internalLogr.WithField("pool_id", pool).Traceln("starting the setup process")
		_, _, poolDriver := poolManager.Inspect(p)
		instance, warmed, hibernated, variantID, poolErr = handleSetup(ctx, logr, internalLogr, r, runnerName, enableMock, mockTimeout, poolManager, pool, owner, capacity, egressDefaultIPs, firewallStore)
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
			variantID,
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
				internalLogr.WithFields(logrus.Fields{
					"instance_id":    instance.ID,
					"pool":           selectedPool,
					"destroy_caller": "setup:stage_owner_create_failed",
				}).Infoln("destroy: cleaning up instance and capacity after stage owner create failure")
				if derr := poolManager.Destroy(noContext, selectedPool, instance.ID, instance, nil); derr != nil {
					internalLogr.WithError(derr).Errorln("failed to cleanup instance on setup failure")
				}
				if derr := poolManager.DestroyCapacity(noContext, capacity); derr != nil {
					internalLogr.WithError(derr).Errorln("failed to cleanup capacity reservation on setup failure")
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
				instance.VariantID,
			).Inc()
		}
		internalLogr.WithField("os", instance.OS).
			WithField("arch", instance.Arch).
			WithField("selected_pool", selectedPool).
			WithField("requested_pool", r.PoolID).
			WithField("instance_address", instance.Address).
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
			"",
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
				"",
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
		instance.VariantID,
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

	printKV(logr, "Machine IP", instance.Address)
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
		WithField("hibernated", hibernated).
		WithField("hotpool", warmed).
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
		instance.VariantID,
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
	reservedCapacity *types.CapacityReservation,
	egressDefaultIPs []string,
	firewallStore store.FirewallStore,
) (
	instance *types.Instance,
	warmed bool,
	hibernated bool,
	variantID string,
	err error,
) {
	// check if the pool exists in the pool manager.
	if !poolManager.Exists(pool) {
		return nil, false, false, "", fmt.Errorf("could not find pool: %s", pool)
	}

	if reservedCapacity != nil {
		if pool != reservedCapacity.PoolName {
			// capacity has not been reserved in this pool
			reservedCapacity = nil
		}
	}

	stageRuntimeID := r.ID

	// try to provision an instance from the pool manager.
	query := &types.QueryParams{
		RunnerName: runnerName,
	}

	provisionParams := &types.ProvisionParams{
		VMImageConfig:        &r.VMImageConfig,
		NestedVirtualization: r.NestedVirtualization,
		ResourceClass:        r.ResourceClass,
		SkipCloudVMCleanup:   r.SkipCloudVMCleanup,
		AccountID:            owner,
		StageRuntimeID:       stageRuntimeID,
		PipelineExecutionID:  getPipelineExecutionID(&r.Context, r.Tags),
		LongRunning:          r.Timeout > longRunningTimeoutCutoffSec,
	}
	if r.Zone != "" {
		provisionParams.Zones = []string{r.Zone}
	}

	instance, _, warmed, variantID, err = poolManager.Provision(
		ctx,
		pool,
		poolManager.GetTLSServerName(),
		owner,
		provisionParams,
		query,
		&r.GitspaceAgentConfig,
		&r.StorageConfig,
		&r.InstanceInfo,
		r.Timeout,
		r.IsMarkedForInfraReset,
		reservedCapacity,
		false,
	)
	if err != nil {
		return nil, false, false, variantID, fmt.Errorf("failed to provision instance: %w", err)
	}

	ilog := internalLogr.WithField("pool_id", pool).
		WithField("ip", instance.Address).
		WithField("id", instance.ID).
		WithField("instance_name", instance.Name).
		WithField("image_name", useNonEmpty(r.VMImageConfig.ImageName, instance.Image)).
		WithField("image_version", r.VMImageConfig.ImageVersion)

	// Since we are enabling Hardware acceleration for GCP VMs so adding this log for GCP VMs only. Might be changed later.
	if instance.Provider == types.Google {
		ilog.Traceln(fmt.Sprintf("creating VM instance with hardware acceleration as %t", instance.EnableNestedVirtualization))
	}

	ilog.Traceln("successfully provisioned VM in pool")
	printOK(buildLog, "Machine provisioned successfully")
	printTitle(buildLog, "Preparing the machine to execute this stage...")

	ilog.WithFields(logrus.Fields{
		"pool_id":       pool,
		"ip":            instance.Address,
		"instance_id":   instance.ID,
		"instance_name": instance.Name,
	}).Infoln("Machine provisioned successfully")

	instanceID := instance.ID
	instanceName := instance.Name
	instanceIP := instance.Address

	cleanUpInstanceFn := func(consoleLogs bool) {
		if consoleLogs {
			logSerialConsoleOutput(poolManager, pool, instanceID, instanceName, instanceIP, stageRuntimeID)
		}
		ilog.WithFields(logrus.Fields{
			"instance_id":    instanceID,
			"pool":           pool,
			"destroy_caller": "setup:le_health_check_failed",
		}).Infoln("destroy: cleaning up instance and capacity after LE health check failure")
		if dErr := poolManager.Destroy(context.Background(), pool, instanceID, instance, nil); dErr != nil {
			ilog.WithError(dErr).Errorln("failed to cleanup instance on setup failure")
		}
		if dErr := poolManager.DestroyCapacity(context.Background(), reservedCapacity); dErr != nil {
			ilog.WithError(dErr).Errorln("failed to cleanup capacity reservation on setup failure")
		}
	}

	if instance.IsHibernated {
		ilog.Tracef("instance %s is hibernated", instance.ID)
		instance, err = poolManager.StartInstance(ctx, pool, instance.ID, &r.InstanceInfo)
		if err != nil {
			go cleanUpInstanceFn(false)
			return nil, false, false, variantID, fmt.Errorf("failed to start the instance up: %w", err)
		}
		ilog.Tracef("instance %s is started", instance.ID)
		hibernated = true
	}

	instance.Stage = stageRuntimeID
	instance.Updated = time.Now().Unix()
	err = poolManager.Update(ctx, instance)
	if err != nil {
		go cleanUpInstanceFn(false)
		return nil, false, false, variantID, fmt.Errorf("failed to tag: %w", err)
	}

	err = poolManager.SetInstanceTags(ctx, pool, instance, r.Tags)
	if err != nil {
		go cleanUpInstanceFn(false)
		return nil, false, false, variantID, fmt.Errorf("failed to add tags to the instance: %w", err)
	}

	client, err := lehelper.GetClient(instance, poolManager.GetTLSServerName(), instance.Port,
		enableMock, mockTimeout)
	if err != nil {
		go cleanUpInstanceFn(false)
		return nil, false, false, variantID, fmt.Errorf("failed to create LE client: %w", err)
	}
	// try the healthcheck api on the lite-engine until it responds ok
	ilog.Traceln("running healthcheck and waiting for an ok response")
	performDNSLookup := drivers.ShouldPerformDNSLookup(poolManager.IsHosted(), instance.Platform.OS, warmed)

	// Get the health check timeout based on the instance OS, provider, warmed status, and hibernated status
	healthCheckTimeout := poolManager.GetHealthCheckTimeout(instance.Platform.OS, instance.Provider, warmed, hibernated)

	if _, err = client.RetryHealth(ctx, &api.HealthRequest{
		PerformDNSLookup:                performDNSLookup,
		Timeout:                         healthCheckTimeout,
		HealthCheckConnectivityDuration: poolManager.GetHealthCheckConnectivityDuration(),
	}); err != nil {
		printError(buildLog, "Machine health check failed")
		go cleanUpInstanceFn(true)
		return nil, false, false, variantID, fmt.Errorf("failed to call lite-engine retry health: %w", err)
	}

	printOK(buildLog, "Machine health check passed")
	internalLogr.Infoln("Machine health check passed")

	// Currently m1 architecture does not enable nested virtualisation, so we disable docker.
	if instance.Platform.OS == oshelp.OSMac {
		b := false
		r.SetupRequest.MountDockerSocket = &b
	}

	if poolManager.IsEgressPool(pool) {
		r.Volumes = appendEgressCAVolume(r.Volumes, instance.Platform.OS)
	}

	// If enabled, merge default Harness IPs with customer IPs into a new slice (don't mutate the request).
	var mergedAllowedIPs []string
	if r.SetupRequest.EgressPolicy != nil && r.SetupRequest.EgressPolicy.Enabled {
		defaultPolicy := egress.DefaultEgressPolicy(egressDefaultIPs)
		mergedAllowedIPs = make([]string, 0, len(defaultPolicy.AllowedIPs)+len(r.SetupRequest.EgressPolicy.AllowedIPs))
		mergedAllowedIPs = append(mergedAllowedIPs, defaultPolicy.AllowedIPs...)
		mergedAllowedIPs = append(mergedAllowedIPs, r.SetupRequest.EgressPolicy.AllowedIPs...)
		r.SetupRequest.EgressPolicy.AllowedIPs = mergedAllowedIPs
	}

	_, err = client.RetrySetup(ctx, &r.SetupRequest, poolManager.GetSetupTimeout())
	if err != nil {
		printError(buildLog, "Machine setup failed")
		go cleanUpInstanceFn(true)
		return nil, false, false, variantID, fmt.Errorf("failed to call setup lite-engine: %w", err)
	}

	// Apply cloud-level egress firewall rules async and save to firewall store
	if r.SetupRequest.EgressPolicy != nil && r.SetupRequest.EgressPolicy.Enabled {
		go applyAndSaveEgressRules(poolManager, firewallStore, instance,
			mergedAllowedIPs, stageRuntimeID, ilog)
	}

	return instance, warmed, hibernated, variantID, nil
}

// applyAndSaveEgressRules creates cloud-level egress firewall rules and saves references to the firewall store.
// It pre-saves rules with provisioning state before cloud creation, then updates to active on success.
func applyAndSaveEgressRules(
	poolManager drivers.IManager,
	firewallStore store.FirewallStore,
	instance *types.Instance,
	allowedIPs []string,
	stageRuntimeID string,
	ilog *logrus.Entry,
) {
	ctx := context.Background()

	// Pre-compute rule names (deterministic based on instance ID).
	allowRuleName := google.EgressRuleName(google.EgressAllowPrefix, instance.ID)
	denyRuleName := google.EgressRuleName(google.EgressDenyPrefix, instance.ID)

	// Resolve the firewall project from the instance's network so the purger can delete rules
	// from the correct project later (Shared VPC firewalls live in the host project, not the VM project).
	firewallProject := google.ProjectFromNetwork(instance.Network, "")

	// Pre-save rules with provisioning state and actual rule names so destroy/purger can always find and clean them.
	if firewallStore != nil {
		now := time.Now().Unix()
		rules := []*types.FirewallRule{
			{
				StageID: stageRuntimeID, InstanceID: instance.ID,
				ResourceID: allowRuleName, CloudProvider: string(instance.Provider),
				ProjectID: firewallProject, State: types.FirewallStateProvisioning, CreatedAt: now,
			},
			{
				StageID: stageRuntimeID, InstanceID: instance.ID,
				ResourceID: denyRuleName, CloudProvider: string(instance.Provider),
				ProjectID: firewallProject, State: types.FirewallStateProvisioning, CreatedAt: now,
			},
		}
		if saveErr := firewallStore.CreateBatch(ctx, rules); saveErr != nil {
			ilog.WithError(saveErr).Warnln("egress: failed to pre-save firewall rules to DB")
		}
	}

	_, egressErr := poolManager.ApplyEgressPolicy(ctx, instance, allowedIPs)
	if egressErr != nil {
		ilog.WithError(egressErr).Warnln("failed to apply cloud egress firewall rules")
		// Clean up the provisioning records
		if firewallStore != nil {
			_ = firewallStore.DeleteByStageID(ctx, stageRuntimeID)
		}
		return
	}

	// Update rules to active state after successful cloud creation.
	if firewallStore != nil {
		if updateErr := firewallStore.UpdateState(ctx, stageRuntimeID, types.FirewallStateActive); updateErr != nil {
			ilog.WithError(updateErr).Warnln("egress: failed to update firewall rules state to active")
		}
	}
}

// appendEgressCAVolume registers the host-path Volume so step containers can bind-mount it.
func appendEgressCAVolume(volumes []*lespec.Volume, osName string) []*lespec.Volume {
	if osName == oshelp.OSLinux {
		return append(volumes, &lespec.Volume{
			HostPath: &lespec.VolumeHostPath{
				ID:       fileID("ca.crt"),
				Name:     fileID("ca.crt"),
				Path:     egressCAHostPath,
				ReadOnly: true,
			},
		})
	} else if osName == oshelp.OSWindows {
		return append(volumes, &lespec.Volume{
			HostPath: &lespec.VolumeHostPath{
				ID:       fileID("ca.crt"),
				Name:     fileID("ca.crt"),
				Path:     "C:\\harness-certs",
				ReadOnly: true,
			},
		})
	}
	return volumes
}

// logSerialConsoleOutput fetches and logs the serial console output for an instance.
func logSerialConsoleOutput(
	poolManager drivers.IManager,
	pool, instanceID, instanceName, instanceIP, stageRuntimeID string,
) {
	out, logErr := poolManager.InstanceLogs(context.Background(), pool, instanceID)
	if logErr != nil {
		logrus.WithField("id", instanceID).WithError(logErr).Errorln("failed to fetch console output logs")
		return
	}
	const maxLogLength = 60000
	totalLength := len(out)
	for start := 0; start < totalLength; start += maxLogLength {
		end := start + maxLogLength
		if end > totalLength {
			end = totalLength
		}
		logrus.WithField("id", instanceID).
			WithField("instance_name", instanceName).
			WithField("ip", instanceIP).
			WithField("pool_id", pool).
			WithField("stage_runtime_id", stageRuntimeID).
			WithField("log_index", start/maxLogLength).
			Infof("serial console output: %s", out[start:end])
	}
}
