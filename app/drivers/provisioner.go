package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/drone/runner-go/logger"
	"github.com/harness/lite-engine/engine/spec"
	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/certs"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Provision returns an instance for a job execution and tags it as in use.
// This method and BuildPool method contain logic for maintaining pool size.
func (m *Manager) Provision(
	ctx context.Context,
	poolName,
	serverName,
	ownerID string,
	machineConfig *types.MachineConfig,
	query *types.QueryParams,
	gitspaceAgentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	instanceInfo *common.InstanceInfo,
	timeout int64,
	isMarkedForInfraReset bool,
	reservedCapacity *types.CapacityReservation,
	isCapacityTask bool,
) (*types.Instance, *types.CapacityReservation, bool, error) {
	pool, err := m.validatePool(poolName)
	if err != nil {
		return nil, nil, false, err
	}

	if m.isGitspaceRequest(gitspaceAgentConfig) {
		if gsErr := m.validateGitspaceDriverCompatibility(pool); gsErr != nil {
			return nil, nil, false, gsErr
		}
		existingInstance, _, gsErr := m.processExistingInstance(
			ctx,
			pool,
			instanceInfo,
			serverName,
			ownerID,
			machineConfig,
			gitspaceAgentConfig,
			storageConfig,
			timeout,
			isMarkedForInfraReset,
		)
		return existingInstance, nil, false, gsErr
	}

	instance, _, hotpool, err := m.provisionFromPool(
		ctx,
		pool,
		query,
		serverName,
		ownerID,
		machineConfig,
		gitspaceAgentConfig,
		storageConfig,
		timeout,
		poolName,
		reservedCapacity,
		isCapacityTask,
	)

	// the go routine here uses the global context because this function is called
	// from setup API call (and we can't use HTTP request context for async tasks)
	// TODO: Move to outbox
	if hotpool {
		go func(ctx context.Context) {
			_, _ = m.setupInstanceWithHibernate(ctx, pool, serverName, "", nil, nil, nil, timeout, nil)
		}(m.globalCtx)
	}
	return instance, nil, hotpool, err
}

func (m *Manager) validatePool(poolName string) (*poolEntry, error) {
	if _, ok := m.poolMap[poolName]; !ok {
		return nil, fmt.Errorf("pool %q not found", poolName)
	}
	return m.poolMap[poolName], nil
}

// getStrategy returns the strategy for the manager.
func (m *Manager) getStrategy() Strategy {
	strategy := m.strategy
	if strategy == nil {
		strategy = Greedy{}
	}
	return strategy
}

// provisionFromPool handles provisioning for regular managers using in-memory locks.
//
//nolint:unparam
func (m *Manager) provisionFromPool(
	ctx context.Context,
	pool *poolEntry,
	query *types.QueryParams,
	serverName, ownerID string,
	machineConfig *types.MachineConfig,
	gitspaceAgentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	timeout int64,
	poolName string,
	reservedCapacity *types.CapacityReservation,
	isCapacityTask bool,
) (*types.Instance, *types.CapacityReservation, bool, error) {
	pool.Lock()

	busy, free, _, _, err := m.list(ctx, pool, query)
	if err != nil {
		pool.Unlock()
		return nil, nil, false, fmt.Errorf("provision: failed to list instances of %q pool: %w", poolName, err)
	}

	logger.FromContext(ctx).
		WithField("pool", poolName).
		WithField("busy", len(busy)).
		WithField("free", len(free)).
		WithField("hotpool", len(free) > 0).
		Traceln("provision: hotpool instances")

	strategy := m.getStrategy()

	if len(free) == 0 {
		pool.Unlock()
		if canCreate := strategy.CanCreate(pool.MinSize, pool.MaxSize, len(busy), len(free)); !canCreate {
			return nil, nil, false, ErrorNoInstanceAvailable
		}
		var inst *types.Instance
		inst, _, err = m.setupInstance(ctx, pool, serverName, ownerID, machineConfig, true, gitspaceAgentConfig, storageConfig, timeout, nil, reservedCapacity, isCapacityTask)
		if err != nil {
			return nil, nil, false, fmt.Errorf("provision: failed to create instance: %w", err)
		}
		return inst, nil, false, nil
	}

	sort.Slice(free, func(i, j int) bool {
		iTime := time.Unix(free[i].Started, 0)
		jTime := time.Unix(free[j].Started, 0)
		return iTime.Before(jTime)
	})

	inst := free[0]
	inst.State = types.StateInUse
	inst.OwnerID = ownerID
	if inst.IsHibernated {
		// update started time after bringing instance from hibernate
		// this will make sure that purger only picks it when it is actually used for max age
		inst.Started = time.Now().Unix()
	}
	err = m.instanceStore.Update(ctx, inst)
	if err != nil {
		pool.Unlock()
		return nil, nil, false, fmt.Errorf("provision: failed to tag an instance in %q pool: %w", poolName, err)
	}
	pool.Unlock()
	return inst, nil, true, nil
}

// setupInstance creates a new VM instance.
//
//nolint:gocyclo
func (m *Manager) setupInstance(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName, ownerID string,
	machineConfig *types.MachineConfig,
	inuse bool,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	timeout int64,
	platform *types.Platform,
	reservedCapacity *types.CapacityReservation,
	isCapacityTask bool,
) (*types.Instance, *types.CapacityReservation, error) {
	var inst *types.Instance
	retain := "false"

	// generate certs
	createOptions, err := certs.Generate(m.runnerName, tlsServerName)
	createOptions.IsHosted = IsHosted(ctx)
	createOptions.LiteEnginePath = m.liteEnginePath
	createOptions.LiteEngineFallbackPath = m.liteEngineFallbackPath
	createOptions.PoolName = pool.Name
	createOptions.Limit = pool.MaxSize
	createOptions.Pool = pool.MinSize
	createOptions.HarnessTestBinaryURI = m.harnessTestBinaryURI
	createOptions.PluginBinaryURI = m.pluginBinaryURI
	createOptions.PluginBinaryFallbackURI = m.pluginBinaryFallbackURI
	createOptions.Tmate = m.tmate
	createOptions.AccountID = ownerID
	if machineConfig != nil {
		// Use ResourceClass from machineConfig
		createOptions.ResourceClass = machineConfig.ResourceClass
		createOptions.Zones = machineConfig.Zones
		createOptions.MachineType = machineConfig.MachineType
		createOptions.NestedVirtualization = machineConfig.NestedVirtualization
		if machineConfig.VMImageConfig != nil && machineConfig.VMImageConfig.ImageName != "" {
			createOptions.VMImageConfig = types.VMImageConfig{
				ImageName:    machineConfig.VMImageConfig.ImageName,
				Username:     machineConfig.VMImageConfig.Username,
				Password:     machineConfig.VMImageConfig.Password,
				ImageVersion: machineConfig.VMImageConfig.ImageVersion,
			}

			if machineConfig.VMImageConfig.Auth != nil {
				createOptions.VMImageConfig.VMImageAuth = types.VMImageAuth{
					Registry: machineConfig.VMImageConfig.Auth.Address,
					Username: machineConfig.VMImageConfig.Auth.Username,
					Password: machineConfig.VMImageConfig.Auth.Password,
				}
			}
		}
	}
	if storageConfig != nil {
		createOptions.StorageOpts = types.StorageOpts{
			CephPoolIdentifier: storageConfig.CephPoolIdentifier,
			Identifier:         storageConfig.Identifier,
			Size:               storageConfig.Size,
			Type:               storageConfig.Type,
			BootDiskSize:       storageConfig.BootDiskSize,
			BootDiskType:       storageConfig.BootDiskType,
		}
	}
	// Set boot disk settings from machineConfig if provided and not already set
	if machineConfig != nil {
		if machineConfig.DiskSize != 0 && createOptions.StorageOpts.BootDiskSize == "" {
			createOptions.StorageOpts.BootDiskSize = fmt.Sprintf("%d", machineConfig.DiskSize)
		}
		if machineConfig.DiskType != "" && createOptions.StorageOpts.BootDiskType == "" {
			createOptions.StorageOpts.BootDiskType = machineConfig.DiskType
		}
	}
	createOptions.AutoInjectionBinaryURI = m.autoInjectionBinaryURI
	createOptions.AnnotationsBinaryURI = m.annotationsBinaryURI
	createOptions.AnnotationsBinaryFallbackURI = m.annotationsBinaryFallbackURI
	createOptions.EnvmanBinaryURI = m.envmanBinaryURI
	createOptions.EnvmanBinaryFallbackURI = m.envmanBinaryFallbackURI
	createOptions.TmateBinaryURI = m.tmateBinaryURI
	createOptions.TmateBinaryFallbackURI = m.tmateBinaryFallbackURI
	if agentConfig != nil && (agentConfig.Secret != "" || agentConfig.VMInitScript != "") {
		createOptions.GitspaceOpts = types.GitspaceOpts{
			Secret:                   agentConfig.Secret,
			AccessToken:              agentConfig.AccessToken,
			Ports:                    agentConfig.Ports,
			VMInitScript:             agentConfig.VMInitScript,
			GitspaceConfigIdentifier: agentConfig.GitspaceConfigIdentifier,
		}
		retain = "true"
	}
	createOptions.InternalLabels = map[string]string{"retain": retain}
	if createOptions.IsHosted {
		createOptions.VMLabels = map[string]string{
			"pool_id": pool.Name,
		}
		if m.env != "" {
			createOptions.VMLabels["harness_env"] = m.env
		}
	}
	createOptions.DriverName = pool.Driver.DriverName()
	createOptions.Timeout = timeout
	createOptions.CapacityReservation = reservedCapacity
	if err != nil {
		logrus.WithError(err).
			Errorln("manager: failed to generate certificates")
		return nil, nil, err
	}

	if platform != nil {
		createOptions.Platform = *platform
	} else {
		createOptions.Platform = pool.Platform
	}

	if isCapacityTask {
		// create instance
		var capacity *types.CapacityReservation
		capacity, err = pool.Driver.ReserveCapacity(ctx, createOptions)
		if err != nil {
			logrus.WithError(err).
				Errorln("manager: failed to reserve capacity")
			return nil, nil, err
		}
		return nil, capacity, nil
	}

	// create instance
	inst, err = pool.Driver.Create(ctx, createOptions)
	if err != nil {
		logrus.WithError(err).
			Errorln("manager: failed to create instance")
		return nil, nil, err
	}

	if inuse {
		inst.State = types.StateInUse
		inst.OwnerID = ownerID
	}

	inst.RunnerName = m.runnerName

	// Set VariantID from machineConfig ("default" for non-variant instances)
	if machineConfig != nil && machineConfig.VariantID != "" {
		inst.VariantID = machineConfig.VariantID
	} else {
		inst.VariantID = "default"
	}

	if inst.Labels == nil {
		labelsBytes, marshalErr := json.Marshal(map[string]string{"retain": "false"})
		if marshalErr != nil {
			return nil, nil, fmt.Errorf("manager: could not marshal default labels, err: %w", marshalErr)
		}
		inst.Labels = labelsBytes
	}

	err = m.instanceStore.Create(ctx, inst)
	if err != nil {
		logrus.WithError(err).
			Errorln("manager: failed to store instance")
		_, _ = pool.Driver.Destroy(ctx, []*types.Instance{inst})
		return nil, nil, err
	}
	return inst, nil, nil
}

// isGitspaceRequest checks if the request is for a GitSpace configuration with ports.
func (m *Manager) isGitspaceRequest(gitspaceAgentConfig *types.GitspaceAgentConfig) bool {
	return gitspaceAgentConfig != nil && len(gitspaceAgentConfig.Ports) > 0
}

// validateGitspaceDriverCompatibility checks if the pool's driver is compatible with gitspace configuration.
// Returns an error if the driver is incompatible.
func (m *Manager) validateGitspaceDriverCompatibility(pool *poolEntry) error {
	if pool.Driver.DriverName() != string(types.Nomad) &&
		pool.Driver.DriverName() != string(types.Google) &&
		pool.Driver.DriverName() != string(types.Amazon) {
		return fmt.Errorf("incorrect pool, gitspaces is only supported on nomad, google, and amazon")
	}
	return nil
}

// processExistingInstance processes an existing instance based on provided instance info.
// It validates the instance info, creates an instance from it, and handles reset or resume operations.
// If no valid existing instance is found, it sets up a new instance.
func (m *Manager) processExistingInstance(
	ctx context.Context,
	pool *poolEntry,
	instanceInfo *common.InstanceInfo,
	serverName, ownerID string,
	machineConfig *types.MachineConfig,
	gitspaceAgentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	timeout int64,
	isMarkedForInfraReset bool,
) (*types.Instance, *types.CapacityReservation, error) {
	if instanceInfo != nil && instanceInfo.ID != "" {
		if validateInstanceInfoErr := common.ValidateStruct(*instanceInfo); validateInstanceInfoErr != nil {
			logrus.Warnf("missing information in the instance info: %v", validateInstanceInfoErr)
		} else {
			inst := common.BuildInstanceFromRequest(*instanceInfo)
			if isMarkedForInfraReset {
				storageCleanupType := storage.Detach
				_, destroyInstanceErr := pool.Driver.DestroyInstanceAndStorage(ctx, []*types.Instance{inst}, &storageCleanupType)
				if destroyInstanceErr != nil {
					logrus.Warnf(
						"failed to destroy instance %s: %v",
						instanceInfo.ID,
						destroyInstanceErr,
					)
				}
				// Continue to create a new instance below
			} else {
				logrus.Tracef("instance is suspend, waking up the instance")
				inst.IsHibernated = true
				inst.State = types.StateInUse
				inst.OwnerID = ownerID
				inst.Started = time.Now().Unix()
				return inst, nil, nil
			}
		}
	}

	logrus.Infof("instance info is not present or reset required, setting up a new instance")

	var platform *types.Platform
	if instanceInfo != nil {
		platform = &types.Platform{
			OS:   instanceInfo.OS,
			Arch: instanceInfo.Arch,
		}
	}

	return m.setupInstance(
		ctx,
		pool,
		serverName,
		ownerID,
		machineConfig,
		true,
		gitspaceAgentConfig,
		storageConfig,
		timeout,
		platform,
		nil,
		false,
	)
}

// setupInstanceParamsToMachineConfig converts SetupInstanceParams to MachineConfig.
func (m *Manager) setupInstanceParamsToMachineConfig(params *types.SetupInstanceParams) *types.MachineConfig {
	if params == nil {
		return nil
	}

	machineConfig := &types.MachineConfig{
		SetupInstanceParams: *params,
	}

	// Deep copy Zones slice to ensure immutability
	if len(params.Zones) > 0 {
		machineConfig.Zones = make([]string, len(params.Zones))
		copy(machineConfig.Zones, params.Zones)
	}

	if params.ImageName != "" {
		machineConfig.VMImageConfig = &spec.VMImageConfig{
			ImageName: params.ImageName,
		}
	}

	return machineConfig
}
