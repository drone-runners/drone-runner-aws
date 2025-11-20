package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/drone-runners/drone-runner-aws/app/certs"
	itypes "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	lehttp "github.com/harness/lite-engine/cli/client"
	"github.com/harness/lite-engine/engine/spec"
	"github.com/pkg/errors"

	"github.com/sirupsen/logrus"
)

var _ IManager = (*Manager)(nil)

type (
	Manager struct {
		globalCtx                    context.Context
		poolMap                      map[string]*poolEntry
		strategy                     Strategy
		cleanupTimer                 *time.Ticker
		runnerName                   string
		liteEnginePath               string
		instanceStore                store.InstanceStore
		stageOwnerStore              store.StageOwnerStore
		capacityReservationStore     store.CapacityReservationStore
		harnessTestBinaryURI         string
		pluginBinaryURI              string
		tmate                        types.Tmate
		autoInjectionBinaryURI       string
		liteEngineFallbackPath       string
		pluginBinaryFallbackURI      string
		runnerConfig                 types.RunnerConfig
		annotationsBinaryURI         string
		annotationsBinaryFallbackURI string
	}

	poolEntry struct {
		sync.Mutex
		Pool
	}
)

func New(
	globalContext context.Context,
	instanceStore store.InstanceStore,
	env *config.EnvConfig,
) *Manager {
	return &Manager{
		globalCtx:                    globalContext,
		instanceStore:                instanceStore,
		tmate:                        types.Tmate(env.Tmate),
		runnerName:                   env.Runner.Name,
		liteEnginePath:               env.LiteEngine.Path,
		harnessTestBinaryURI:         env.Settings.HarnessTestBinaryURI,
		pluginBinaryURI:              env.Settings.PluginBinaryURI,
		autoInjectionBinaryURI:       env.Settings.AutoInjectionBinaryURI,
		liteEngineFallbackPath:       env.LiteEngine.FallbackPath,
		pluginBinaryFallbackURI:      env.Settings.PluginBinaryFallbackURI,
		runnerConfig:                 types.RunnerConfig(env.RunnerConfig),
		annotationsBinaryURI:         env.Settings.AnnotationsBinaryURI,
		annotationsBinaryFallbackURI: env.Settings.AnnotationsBinaryFallbackURI,
	}
}

//nolint:gocritic
func NewManager(
	globalContext context.Context,
	instanceStore store.InstanceStore,
	stageOwnerStore store.StageOwnerStore,
	capacityReservationStore store.CapacityReservationStore,
	tmate types.Tmate,
	runnerName,
	liteEnginePath,
	harnessTestBinaryURI,
	pluginBinaryURI,
	autoInjectionBinaryURI,
	liteEngineFallbackPath,
	pluginBinaryFallbackURI string, runnerConfig types.RunnerConfig,
	annotationsBinaryURI string, annotationsBinaryFallbackURI string,
) *Manager {
	return &Manager{
		globalCtx:                    globalContext,
		instanceStore:                instanceStore,
		tmate:                        tmate,
		stageOwnerStore:              stageOwnerStore,
		capacityReservationStore:     capacityReservationStore,
		runnerName:                   runnerName,
		liteEnginePath:               liteEnginePath,
		harnessTestBinaryURI:         harnessTestBinaryURI,
		pluginBinaryURI:              pluginBinaryURI,
		autoInjectionBinaryURI:       autoInjectionBinaryURI,
		liteEngineFallbackPath:       liteEngineFallbackPath,
		pluginBinaryFallbackURI:      pluginBinaryFallbackURI,
		runnerConfig:                 runnerConfig,
		annotationsBinaryURI:         annotationsBinaryURI,
		annotationsBinaryFallbackURI: annotationsBinaryFallbackURI,
	}
}

func (m *Manager) GetPoolSpec(poolName string) (interface{}, error) {
	entry := m.poolMap[poolName]
	if entry == nil {
		return nil, fmt.Errorf("manager: pool %s not found", poolName)
	}
	if entry.Spec == nil {
		return nil, fmt.Errorf("manager: pool %s does not have a stored spec", poolName)
	}
	return entry.Spec, nil
}

func (m *Manager) Inspect(name string) (platform types.Platform, rootDir, driver string) {
	entry := m.poolMap[name]
	if entry == nil {
		return
	}

	platform = entry.Platform
	rootDir = entry.Driver.RootDir()
	driver = entry.Driver.DriverName()

	return
}

// Exists returns true if a pool with given name exists.
func (m *Manager) Exists(name string) bool {
	return m.poolMap[name] != nil
}

func (m *Manager) Count() int {
	return len(m.poolMap)
}

func (m *Manager) MatchPoolNameFromPlatform(requested *types.Platform) string {
	for _, pool := range m.poolMap {
		if pool.Platform.OS == requested.OS && pool.Platform.Arch == requested.Arch {
			return pool.Name
		}
	}
	return ""
}

func (m *Manager) Find(ctx context.Context, instanceID string) (*types.Instance, error) {
	return m.instanceStore.Find(ctx, instanceID)
}

func (m *Manager) GetInstanceByStageID(ctx context.Context, poolName, stage string) (*types.Instance, error) {
	if stage == "" {
		logger.FromContext(ctx).
			Errorln("manager: GetInstanceByStageID stage runtime ID is not set")
		return nil, fmt.Errorf("stage runtime ID is not set")
	}

	pool := m.poolMap[poolName]
	if pool == nil {
		err := fmt.Errorf("GetInstanceByStageID: pool name %s not found", poolName)
		logger.FromContext(ctx).WithError(err).WithField("stage_runtime_id", stage).
			Errorln("manager: GetInstanceByStageID failed find pool")
		return nil, err
	}
	query := types.QueryParams{Status: types.StateInUse, Stage: stage}
	list, err := m.instanceStore.List(ctx, pool.Name, &query)
	if err != nil {
		logger.FromContext(ctx).WithError(err).WithField("stage_runtime_id", stage).
			Errorln("manager: GetInstanceByStageID failed to list instances")
		return nil, err
	}

	if len(list) == 0 {
		return nil, fmt.Errorf("manager: instance for stage runtime ID %s not found", stage)
	}
	return list[0], nil
}

func (m *Manager) List(ctx context.Context, poolName string, queryParams *types.QueryParams) (busy, free, hibernating []*types.Instance, err error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, nil, nil, fmt.Errorf("manager: pool %s not found", poolName)
	}
	return m.list(ctx, pool, queryParams)
}

func (m *Manager) list(ctx context.Context, pool *poolEntry, queryParams *types.QueryParams) (busy, free, hibernating []*types.Instance, err error) {
	list, err := m.instanceStore.List(ctx, pool.Name, queryParams)
	if err != nil {
		logger.FromContext(ctx).WithError(err).
			Errorln("manager: failed to list instances")
		return
	}

	for _, instance := range list {
		// required to append instance not pointer
		loopInstance := instance
		if instance.State == types.StateInUse {
			busy = append(busy, loopInstance)
		} else if instance.State == types.StateHibernating {
			hibernating = append(hibernating, loopInstance)
		} else {
			free = append(free, loopInstance)
		}
	}

	return busy, free, hibernating, nil
}

func (m *Manager) Delete(ctx context.Context, instanceID string) error {
	return m.instanceStore.Delete(ctx, instanceID)
}

func (m *Manager) Update(ctx context.Context, instance *types.Instance) error {
	return m.instanceStore.Update(ctx, instance)
}

func (m *Manager) Add(pools ...Pool) error {
	if len(pools) == 0 {
		return nil
	}

	if m.poolMap == nil {
		m.poolMap = map[string]*poolEntry{}
	}

	for i := range pools {
		name := pools[i].Name
		if name == "" {
			return errors.New("pool must have a name")
		}

		if _, alreadyExists := m.poolMap[name]; alreadyExists {
			return fmt.Errorf("pool %q already defined", name)
		}

		m.poolMap[name] = &poolEntry{
			Mutex: sync.Mutex{},
			Pool:  pools[i],
		}
	}

	return nil
}

func (m *Manager) StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree, freeCapacityMaxAge, purgerTime time.Duration) error {
	const minMaxAge = 5 * time.Minute
	if maxAgeBusy < minMaxAge || maxAgeFree < minMaxAge {
		return fmt.Errorf("minimum value of max age is %.2f minutes", minMaxAge.Minutes())
	}
	if maxAgeBusy > maxAgeFree {
		return fmt.Errorf(
			"max age of used instances (set to %.2fmin) should be less than max age of free instances (set to %.2fmin)",
			maxAgeBusy.Minutes(), maxAgeFree.Minutes())
	}

	if m.cleanupTimer != nil {
		panic("purger already started")
	}

	d := time.Duration(maxAgeBusy.Minutes() * 0.9 * float64(time.Minute))
	m.cleanupTimer = time.NewTicker(d)

	logrus.Infof("Instance purger started. It will run every %.2f minutes", d.Minutes())

	go func() {
		for {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logrus.Errorf("PANIC %v\n%s", r, debug.Stack())
					}
				}()

				select {
				case <-ctx.Done():
					return
				case <-m.cleanupTimer.C:
					logrus.Traceln("Launching instance purger")

					err := m.forEach(ctx,
						m.GetTLSServerName(),
						nil,
						func(ctx context.Context, pool *poolEntry, serverName string, query *types.QueryParams) error {
							logr := logger.FromContext(ctx).
								WithField("driver", pool.Driver.DriverName()).
								WithField("pool", pool.Name)

							pool.Lock()
							defer pool.Unlock()

							queryParams := &types.QueryParams{MatchLabels: map[string]string{"retain": "false"}}
							busy, free, hibernating, err := m.list(ctx, pool, queryParams)
							if err != nil {
								return fmt.Errorf("failed to list instances of pool=%q error: %w", pool.Name, err)
							}
							free = append(free, hibernating...)

							var instances []*types.Instance
							for _, inst := range busy {
								startedAt := time.Unix(inst.Started, 0)
								if time.Since(startedAt) > maxAgeBusy {
									instances = append(instances, inst)
								}
							}
							for _, inst := range free {
								startedAt := time.Unix(inst.Started, 0)
								if time.Since(startedAt) > maxAgeFree {
									instances = append(instances, inst)
								}
							}

							if len(instances) == 0 {
								return nil
							}

							logr.Infof("purger: Terminating %d stale instances\n", len(instances))

							err = pool.Driver.Destroy(ctx, instances)
							if err != nil {
								return fmt.Errorf("failed to delete instances of pool=%q error: %w", pool.Name, err)
							}
							for _, instance := range instances {
								derr := m.Delete(ctx, instance.ID)
								if derr != nil {
									return fmt.Errorf("failed to delete %s from instance store with err: %s", instance.ID, derr)
								}
							}

							err = m.buildPool(ctx, pool, serverName, nil, m.setupInstanceWithHibernate, nil)
							if err != nil {
								return fmt.Errorf("failed to rebuld pool=%q error: %w", pool.Name, err)
							}

							return nil
						})
					if err != nil {
						logger.FromContext(ctx).WithError(err).
							Errorln("purger: Failed to purge stale instances")
					}
				}
			}()
		}
	}()

	return nil
}

// Provision returns an instance for a job execution and tags it as in use.
// This method and BuildPool method contain logic for maintaining pool size.
func (m *Manager) Provision(
	ctx context.Context,
	poolName,
	serverName,
	ownerID,
	resourceClass string,
	vmImageConfig *spec.VMImageConfig,
	query *types.QueryParams,
	gitspaceAgentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	zone string,
	machineType string,
	shouldUseGoogleDNS bool,
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
			resourceClass,
			vmImageConfig,
			gitspaceAgentConfig,
			storageConfig,
			zone,
			machineType,
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
		resourceClass,
		vmImageConfig,
		gitspaceAgentConfig,
		storageConfig,
		zone,
		machineType,
		shouldUseGoogleDNS,
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
			_, _ = m.setupInstanceWithHibernate(ctx, pool, serverName, "", "", nil, nil, nil, zone, machineType, false, timeout, nil)
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

// getStrategy returns the strategy for the manager
func (m *Manager) getStrategy() Strategy {
	strategy := m.strategy
	if strategy == nil {
		strategy = Greedy{}
	}
	return strategy
}

// provisionFromPool handles provisioning for regular managers using in-memory locks
//
//nolint:unparam
func (m *Manager) provisionFromPool(
	ctx context.Context,
	pool *poolEntry,
	query *types.QueryParams,
	serverName, ownerID, resourceClass string,
	vmImageConfig *spec.VMImageConfig,
	gitspaceAgentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	zone, machineType string,
	shouldUseGoogleDNS bool,
	timeout int64,
	poolName string,
	reservedCapacity *types.CapacityReservation,
	isCapacityTask bool,
) (*types.Instance, *types.CapacityReservation, bool, error) {
	pool.Lock()

	busy, free, _, err := m.list(ctx, pool, query)
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
		inst, _, err = m.setupInstance(ctx, pool, serverName, ownerID, resourceClass, vmImageConfig, true, gitspaceAgentConfig, storageConfig, zone, machineType, shouldUseGoogleDNS, timeout, nil, reservedCapacity, isCapacityTask) //nolint:lll
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

// Destroy destroys an instance in a pool.
func (m *Manager) Destroy(ctx context.Context, poolName, instanceID string, instance *types.Instance, storageCleanupType *storage.CleanupType) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("provision: pool name %q not found", poolName)
	}

	if instance == nil {
		instanceFromStore, err := m.Find(ctx, instanceID)
		if err != nil || instanceFromStore == nil {
			return fmt.Errorf("provision: failed to find instance %q: %w", instanceID, err)
		}
		instance = instanceFromStore
	}

	err := pool.Driver.DestroyInstanceAndStorage(ctx, []*types.Instance{instance}, storageCleanupType)
	if err != nil {
		return fmt.Errorf("provision: failed to destroy an instance of %q pool: %w", poolName, err)
	}

	if derr := m.Delete(ctx, instance.ID); derr != nil {
		logrus.Warnf("failed to delete instance %s from store with err: %s", instance.ID, derr)
	}
	logrus.WithField("instance", instance.ID).Infof("instance destroyed")
	return nil
}

func (m *Manager) DestroyCapacity(ctx context.Context, reservedCapacity *types.CapacityReservation) error {
	if reservedCapacity == nil || reservedCapacity.PoolName == "" {
		return nil
	}

	pool, err := m.validatePool(reservedCapacity.PoolName)
	if err != nil {
		logrus.Warnf("provision: pool name %q not found", reservedCapacity.PoolName)
		return fmt.Errorf("provision: pool name %q not found", reservedCapacity.PoolName)
	}

	logr := logger.FromContext(ctx).
		WithField("pool", reservedCapacity.PoolName).
		WithField("runtimeId", reservedCapacity.StageID)

	// Destroy associated instance if exists
	if reservedCapacity.InstanceID != "" {
		if err := m.Destroy(ctx, reservedCapacity.PoolName, reservedCapacity.InstanceID, nil, nil); err != nil {
			logrus.Warnf("failed to destroy instance %s from store with err: %s", reservedCapacity.InstanceID, err)
		}
	}

	// If no reservation ID, just delete the record and return
	if reservedCapacity.ReservationID == "" {
		m.deleteCapacityReservationRecord(ctx, reservedCapacity.StageID, logr)
		return nil
	}

	// Destroy the actual capacity reservation
	if err := pool.Driver.DestroyCapacity(ctx, reservedCapacity); err != nil {
		logr.Warnln("provision: failed to destroy reserved capacity")
		return err
	}

	// Delete the capacity reservation record
	m.deleteCapacityReservationRecord(ctx, reservedCapacity.StageID, logr)
	return nil
}

func (m *Manager) deleteCapacityReservationRecord(ctx context.Context, stageID string, logr logger.Logger) {
	if m.capacityReservationStore == nil {
		return
	}
	if err := m.capacityReservationStore.Delete(ctx, stageID); err != nil {
		logr.Warnln("failed to delete capacity reservation entity")
	}
}

func (m *Manager) BuildPools(ctx context.Context) error {
	query := types.QueryParams{RunnerName: m.runnerName}
	return m.forEach(ctx, m.GetTLSServerName(), &query, m.buildPoolWithMutex)
}

func (m *Manager) cleanPool(ctx context.Context, pool *poolEntry, query *types.QueryParams, destroyBusy, destroyFree bool) error {
	pool.Lock()
	defer pool.Unlock()
	busy, free, hibernating, err := m.list(ctx, pool, query)
	if err != nil {
		return err
	}
	free = append(free, hibernating...)
	var instances []*types.Instance

	if destroyBusy {
		instances = append(instances, busy...)
	}

	if destroyFree {
		instances = append(instances, free...)
	}

	if len(instances) == 0 {
		return nil
	}

	err = pool.Driver.Destroy(ctx, instances)
	if err != nil {
		return err
	}

	for _, inst := range instances {
		err = m.Delete(ctx, inst.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) CleanPools(ctx context.Context, destroyBusy, destroyFree bool) error {
	var returnError error
	query := types.QueryParams{RunnerName: m.runnerName, MatchLabels: map[string]string{"retain": "false"}}
	for _, pool := range m.poolMap {
		err := m.cleanPool(ctx, pool, &query, destroyBusy, destroyFree)
		if err != nil {
			returnError = err
			logrus.Errorf("failed to clean pool %s with error: %s", pool.Name, err)
		}
	}

	return returnError
}

func (m *Manager) PingDriver(ctx context.Context) error {
	for _, pool := range m.poolMap {
		err := pool.Driver.Ping(ctx)
		if err != nil {
			return err
		}

		const pauseBetweenChecks = 500 * time.Millisecond
		time.Sleep(pauseBetweenChecks)
	}

	return nil
}

// SetInstanceTags sets tags on an instance in a pool.
func (m *Manager) SetInstanceTags(ctx context.Context, poolName string, instance *types.Instance,
	tags map[string]string) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("provision: pool name %q not found", poolName)
	}

	if len(tags) == 0 {
		return nil
	}

	if err := pool.Driver.SetTags(ctx, instance, tags); err != nil {
		return fmt.Errorf("provision: failed to label an instance of %q pool: %w", poolName, err)
	}
	return nil
}

// BuildPool populates a pool with as many instances as it's needed for the pool.
func (m *Manager) buildPool(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName string,
	query *types.QueryParams,
	setupInstanceWithHibernate func(
	context.Context,
	*poolEntry,
	string,
	string,
	string,
	*spec.VMImageConfig,
	*types.GitspaceAgentConfig,
	*types.StorageConfig,
	string,
	string,
	bool,
	int64,
	*types.Platform,
) (*types.Instance, error),
	setupInstanceAsync func(context.Context, string, string),
) error {
	instBusy, instFree, instHibernating, err := m.list(ctx, pool, query)
	if err != nil {
		return err
	}
	instFree = append(instFree, instHibernating...)

	strategy := m.strategy
	if strategy == nil {
		strategy = Greedy{}
	}

	logr := logger.FromContext(ctx).
		WithField("driver", pool.Driver.DriverName()).
		WithField("pool", pool.Name)

	shouldCreate, shouldRemove := strategy.CountCreateRemove(
		pool.MinSize, pool.MaxSize,
		len(instBusy), len(instFree))

	if shouldRemove > 0 {
		instances := make([]*types.Instance, shouldRemove)
		for i := 0; i < shouldRemove; i++ {
			instances[i] = instFree[i]
		}

		err := pool.Driver.Destroy(ctx, instances)
		if err != nil {
			logr.WithError(err).Errorln("build pool: failed to destroy excess instances")
		}
	}

	if shouldCreate <= 0 {
		return nil
	}

	wg := &sync.WaitGroup{}
	wg.Add(shouldCreate)

	for shouldCreate > 0 {
		go func(ctx context.Context, logr logger.Logger) {
			defer wg.Done()

			// generate certs cert
			inst, err := setupInstanceWithHibernate(ctx, pool, tlsServerName, "", "", nil, nil, nil, "", "", false, 0, nil)
			if err != nil {
				logr.WithError(err).Errorln("build pool: failed to create instance")
				if setupInstanceAsync != nil {
					logr.WithField("runner_name", m.runnerName).Infoln("build pool: creating instance asynchronously")
					setupInstanceAsync(ctx, pool.Name, m.runnerName)
				}
				return
			}
			logr.
				WithField("pool", pool.Name).
				WithField("id", inst.ID).
				WithField("name", inst.Name).
				Infoln("build pool: created new instance")
		}(ctx, logr)
		shouldCreate--
	}

	wg.Wait()

	return nil
}

func (m *Manager) buildPoolWithMutex(ctx context.Context, pool *poolEntry, tlsServerName string, query *types.QueryParams) error {
	pool.Lock()
	defer pool.Unlock()

	return m.buildPool(ctx, pool, tlsServerName, query, m.setupInstanceWithHibernate, nil)
}

func (m *Manager) setupInstanceWithHibernate(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName, ownerID, resourceClass string,
	vmImageConfig *spec.VMImageConfig,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	zone, machineType string,
	shouldUseGoogleDNS bool,
	timeout int64,
	platform *types.Platform,
) (*types.Instance, error) {
	inst, _, err := m.setupInstance(ctx,
		pool,
		tlsServerName,
		ownerID,
		resourceClass,
		vmImageConfig,
		false,
		agentConfig,
		storageConfig,
		zone,
		machineType,
		shouldUseGoogleDNS,
		timeout,
		platform,
		nil,
		false)
	if err != nil {
		return nil, err
	}
	go func() {
		herr := m.hibernateOrStopWithRetries(context.Background(), pool.Name, tlsServerName, inst, false)
		if herr != nil {
			logrus.WithError(herr).Errorln("failed to hibernate the vm")
		}
	}()
	return inst, nil
}

func (m *Manager) setupInstance(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName, ownerID, resourceClass string,
	vmImageConfig *spec.VMImageConfig,
	inuse bool,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	zone, machineType string,
	shouldUseGoogleDNS bool,
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
	createOptions.ResourceClass = resourceClass
	createOptions.ShouldUseGoogleDNS = shouldUseGoogleDNS
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
	createOptions.AutoInjectionBinaryURI = m.autoInjectionBinaryURI
	createOptions.AnnotationsBinaryURI = m.annotationsBinaryURI
	createOptions.AnnotationsBinaryFallbackURI = m.annotationsBinaryFallbackURI
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
	createOptions.Labels = map[string]string{"retain": retain}
	createOptions.Zone = zone
	createOptions.MachineType = machineType
	createOptions.DriverName = pool.Driver.DriverName()
	createOptions.Timeout = timeout
	createOptions.CapacityReservation = reservedCapacity
	if err != nil {
		logrus.WithError(err).
			Errorln("manager: failed to generate certificates")
		return nil, nil, err
	}
	if vmImageConfig != nil && vmImageConfig.ImageName != "" {
		createOptions.VMImageConfig = types.VMImageConfig{
			ImageName:    vmImageConfig.ImageName,
			Username:     vmImageConfig.Username,
			Password:     vmImageConfig.Password,
			ImageVersion: vmImageConfig.ImageVersion,
		}

		if vmImageConfig.Auth != nil {
			createOptions.VMImageConfig.VMImageAuth = types.VMImageAuth{
				Registry: vmImageConfig.Auth.Address,
				Username: vmImageConfig.Auth.Username,
				Password: vmImageConfig.Auth.Password,
			}
		}
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
		_ = pool.Driver.Destroy(ctx, []*types.Instance{inst})
		return nil, nil, err
	}
	return inst, nil, nil
}

func (m *Manager) StartInstance(ctx context.Context, poolName, instanceID string, instanceInfo *common.InstanceInfo) (*types.Instance, error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, fmt.Errorf("start_instance: pool name %q not found", poolName)
	}

	var inst *types.Instance
	var err error
	if instanceInfo.ID != "" {
		if err = common.ValidateStruct(*instanceInfo); err != nil {
			logrus.Warnf("missing information in the instance info: %v", err)
		} else {
			inst = common.BuildInstanceFromRequest(*instanceInfo)
			inst.IsHibernated = true
			logrus.WithField("instanceID", instanceID).Traceln("found instance in request")
		}
	}

	if inst == nil {
		inst, err = m.Find(ctx, instanceID)
		if err != nil {
			return nil, fmt.Errorf("start_instance: failed to find the instance in db %s of %q pool: %w", instanceID, poolName, err)
		}
		logrus.WithField("instanceID", instanceID).Traceln("found instance in DB")
	}

	if !inst.IsHibernated {
		return inst, nil
	}

	logrus.WithField("instanceID", instanceID).Infoln("Starting vm from hibernate state")
	ipAddress, err := pool.Driver.Start(ctx, inst, poolName)
	if err != nil {
		return nil, fmt.Errorf("start_instance: failed to start the instance %s of %q pool: %w", instanceID, poolName, err)
	}

	inst.IsHibernated = false
	inst.Address = ipAddress
	if err := m.instanceStore.Update(ctx, inst); err != nil {
		return nil, fmt.Errorf("start_instance: failed to update instance store %s of %q pool: %w", instanceID, poolName, err)
	}
	return inst, nil
}

func (m *Manager) GetInstanceStore() store.InstanceStore {
	return m.instanceStore
}

func (m *Manager) GetStageOwnerStore() store.StageOwnerStore {
	return m.stageOwnerStore
}

func (m *Manager) GetCapacityReservationStore() store.CapacityReservationStore {
	return m.capacityReservationStore
}

func (m *Manager) InstanceLogs(ctx context.Context, poolName, instanceID string) (string, error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return "", fmt.Errorf("instance_logs: pool name %q not found", poolName)
	}

	return pool.Driver.Logs(ctx, instanceID)
}

func (m *Manager) hibernateOrStopWithRetries(
	ctx context.Context,
	poolName, tlsServerName string,
	instance *types.Instance,
	fallbackStop bool,
) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("hibernate: pool name %q not found", poolName)
	}

	if !pool.Driver.CanHibernate() && !fallbackStop {
		return nil
	}

	shouldHibernate := m.waitForInstanceConnectivity(ctx, tlsServerName, instance.ID)
	if !shouldHibernate {
		if derr := m.Destroy(ctx, poolName, instance.ID, instance, nil); derr != nil {
			logrus.WithError(derr).WithField("instanceID", instance.ID).Errorln("failed to cleanup instance after connectivity failure")
		}
		return fmt.Errorf("hibernate: connectivity check deadline exceeded")
	}

	retryCount := 1
	const maxRetries = 3
	for {
		err := m.hibernate(ctx, instance.ID, poolName, pool)
		if err == nil {
			logrus.WithField("instanceID", instance.ID).Infoln("hibernate complete")
			return nil
		}

		logrus.WithError(err).WithField("retryCount", retryCount).Warnln("failed to hibernate the vm")
		var re *itypes.RetryableError
		if !errors.As(err, &re) {
			return err
		}

		if retryCount >= maxRetries {
			return err
		}

		time.Sleep(time.Minute)
		retryCount++
	}
}

func (m *Manager) hibernate(ctx context.Context, instanceID, poolName string, pool *poolEntry) error {
	pool.Lock()
	inst, err := m.Find(ctx, instanceID)
	if err != nil {
		pool.Unlock()
		return fmt.Errorf("hibernate: failed to find the instance in db %s of %q pool: %w", instanceID, poolName, err)
	}

	if inst.State == types.StateInUse {
		pool.Unlock()
		return nil
	}
	inst.State = types.StateHibernating
	if err = m.instanceStore.Update(ctx, inst); err != nil {
		pool.Unlock()
		return fmt.Errorf("hibernate: failed to update instance in db %s of %q pool: %w", instanceID, poolName, err)
	}
	pool.Unlock()

	logrus.WithField("instanceID", instanceID).Infoln("Hibernating vm")
	if err = pool.Driver.Hibernate(ctx, instanceID, poolName, inst.Zone); err != nil {
		if uerr := m.updateInstState(ctx, pool, instanceID, types.StateCreated); uerr != nil {
			logrus.WithError(err).WithField("instanceID", instanceID).Errorln("failed to update state for failed hibernation")
		}
		return fmt.Errorf("hibernate: failed to hibernated an instance %s of %q pool: %w", instanceID, poolName, err)
	}

	pool.Lock()
	if inst, err = m.Find(ctx, instanceID); err != nil {
		pool.Unlock()
		return fmt.Errorf("hibernate: failed to find the instance in db %s of %q pool: %w", instanceID, poolName, err)
	}

	inst.IsHibernated = true
	inst.State = types.StateCreated
	if err = m.instanceStore.Update(ctx, inst); err != nil {
		pool.Unlock()
		return fmt.Errorf("hibernate: failed to update instance in db %s of %q pool: %w", instanceID, poolName, err)
	}
	pool.Unlock()
	return nil
}

func (m *Manager) updateInstState(ctx context.Context, pool *poolEntry, instanceID string, state types.InstanceState) error {
	pool.Lock()
	defer pool.Unlock()

	inst, err := m.Find(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("update state: failed to find the instance in db %s of %q pool: %w", instanceID, pool.Name, err)
	}

	inst.State = state
	if err := m.instanceStore.Update(ctx, inst); err != nil {
		return fmt.Errorf("update state: failed to update instance in db %s of %q pool: %w", instanceID, pool.Name, err)
	}
	return nil
}

func (m *Manager) forEach(ctx context.Context,
	serverName string,
	query *types.QueryParams,
	f func(ctx context.Context, pool *poolEntry, serverName string, query *types.QueryParams) error) error {
	for _, pool := range m.poolMap {
		err := f(ctx, pool, serverName, query)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) waitForInstanceConnectivity(ctx context.Context, tlsServerName, instanceID string) bool {
	bf := backoff.NewExponentialBackOff()
	for {
		duration := bf.NextBackOff()
		if duration == bf.Stop {
			return false
		}

		select {
		case <-ctx.Done():
			logrus.WithField("instanceID", instanceID).Warnln("hibernate: connectivity check deadline exceeded")
			return false
		case <-time.After(duration):
			err := m.checkInstanceConnectivity(ctx, tlsServerName, instanceID)
			if err == nil {
				return true
			}
			logrus.WithError(err).WithField("instanceID", instanceID).Traceln("hibernate: instance connectivity check failed")
		}
	}
}

func (m *Manager) checkInstanceConnectivity(ctx context.Context, tlsServerName, instanceID string) error {
	instance, err := m.Find(ctx, instanceID)
	if err != nil {
		return errors.Wrap(err, "failed to find the instance in db")
	}

	if instance.Address == "" {
		return errors.New("instance has not received IP address")
	}

	endpoint := fmt.Sprintf("https://%s:9079/", instance.Address)
	client, err := lehttp.NewHTTPClient(endpoint, tlsServerName, string(instance.CACert), string(instance.TLSCert), string(instance.TLSKey))
	if err != nil {
		return errors.Wrap(err, "failed to create client")
	}

	response, err := client.Health(ctx, false)
	if err != nil {
		return err
	}

	if !response.OK {
		return errors.New("health check call failed")
	}

	return nil
}

func (m *Manager) GetTLSServerName() string {
	if m.runnerConfig.HA {
		return "drone-runner-ha"
	}
	return m.runnerName
}

func (m *Manager) GetRunnerConfig() types.RunnerConfig {
	return m.runnerConfig
}

// GetHealthCheckTimeout returns the appropriate health check timeout based on the OS and provider
func (m *Manager) GetHealthCheckTimeout(os string, provider types.DriverType) time.Duration {
	// Override for Windows
	if os == "windows" {
		return m.runnerConfig.HealthCheckWindowsTimeout
	}

	// Use hotpool timeout for Nomad
	if provider == types.Nomad {
		return m.runnerConfig.HealthCheckHotpoolTimeout
	}

	return m.runnerConfig.HealthCheckColdstartTimeout
}

// GetSetupTimeout returns the setup timeout
func (m *Manager) GetSetupTimeout() time.Duration {
	return m.runnerConfig.SetupTimeout
}

func (m *Manager) IsDistributed() bool {
	return false
}

func (m *Manager) Suspend(ctx context.Context, poolName string, instance *types.Instance) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("suspend: pool name %q not found", poolName)
	}

	// hibernateOrStopWithRetries assumes that the instance is present in the store
	// and works only if the state is not InUse.
	var err error
	instance, err = m.findOrCreateInstance(ctx, pool, instance)
	if err != nil {
		return fmt.Errorf("suspend failed to find or create instance: %w", err)
	}

	if err := m.hibernateOrStopWithRetries(
		ctx,
		poolName,
		m.GetTLSServerName(),
		instance,
		true,
	); err != nil {
		return fmt.Errorf("suspend: failed to suspend an instance %s of %q pool: %w", instance.ID, poolName, err)
	}

	return nil
}

func (m *Manager) findOrCreateInstance(ctx context.Context, pool *poolEntry, instance *types.Instance) (*types.Instance, error) {
	pool.Lock()
	defer pool.Unlock()

	if instance == nil {
		return nil, fmt.Errorf("instance is nil")
	}

	instanceFromStore, err := m.Find(ctx, instance.ID)
	if err != nil || instanceFromStore == nil {
		logrus.WithField("instanceID", instance.ID).Infoln("Instance not found in db, creating a new entry")
		if err := m.instanceStore.Create(ctx, instance); err != nil {
			return nil, fmt.Errorf("failed to create instance in db %s: %w", instance.ID, err)
		}
	} else {
		instance = instanceFromStore
	}

	instance.State = types.StateCreated
	if err := m.instanceStore.Update(ctx, instance); err != nil {
		return nil, fmt.Errorf(
			"failed to update instance in db %s of %q pool: %w",
			instance.ID,
			pool.Name,
			err,
		)
	}

	return instance, nil
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
	serverName, ownerID, resourceClass string,
	vmImageConfig *spec.VMImageConfig,
	gitspaceAgentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
	zone, machineType string,
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
				destroyInstanceErr := pool.Driver.DestroyInstanceAndStorage(ctx, []*types.Instance{inst}, &storageCleanupType)
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
		resourceClass,
		vmImageConfig,
		true,
		gitspaceAgentConfig,
		storageConfig,
		zone,
		machineType,
		false,
		timeout,
		platform,
		nil,
		false,
	)
}
