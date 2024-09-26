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
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/internal/certs"
	itypes "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	lehttp "github.com/harness/lite-engine/cli/client"
	"github.com/pkg/errors"

	"github.com/sirupsen/logrus"
)

type (
	Manager struct {
		globalCtx              context.Context
		poolMap                map[string]*poolEntry
		strategy               Strategy
		cleanupTimer           *time.Ticker
		runnerName             string
		liteEnginePath         string
		instanceStore          store.InstanceStore
		stageOwnerStore        store.StageOwnerStore
		harnessTestBinaryURI   string
		pluginBinaryURI        string
		tmate                  types.Tmate
		autoInjectionBinaryURI string
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
		globalCtx:              globalContext,
		instanceStore:          instanceStore,
		runnerName:             env.Runner.Name,
		liteEnginePath:         env.LiteEngine.Path,
		harnessTestBinaryURI:   env.Settings.HarnessTestBinaryURI,
		pluginBinaryURI:        env.Settings.PluginBinaryURI,
		autoInjectionBinaryURI: env.Settings.AutoInjectionBinaryURI,
	}
}

func NewManager(
	globalContext context.Context,
	instanceStore store.InstanceStore,
	stageOwnerStore store.StageOwnerStore,
	env *config.EnvConfig,
) *Manager {
	return &Manager{
		globalCtx:              globalContext,
		instanceStore:          instanceStore,
		stageOwnerStore:        stageOwnerStore,
		runnerName:             env.Runner.Name,
		liteEnginePath:         env.LiteEngine.Path,
		harnessTestBinaryURI:   env.Settings.HarnessTestBinaryURI,
		pluginBinaryURI:        env.Settings.PluginBinaryURI,
		autoInjectionBinaryURI: env.Settings.AutoInjectionBinaryURI,
	}
}

// Inspect returns OS, root directory and driver for a pool.
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

func (m *Manager) List(ctx context.Context, pool *poolEntry, queryParams *types.QueryParams) (busy, free, hibernating []*types.Instance, err error) {
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

func (m *Manager) AddTmate(env *config.EnvConfig) error {
	m.tmate = types.Tmate(env.Tmate)
	return nil
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

func (m *Manager) StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree, purgerTime time.Duration) error {
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
							busy, free, hibernating, err := m.List(ctx, pool, queryParams)
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

							err = m.buildPool(ctx, pool, serverName, nil)
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
func (m *Manager) Provision(ctx context.Context, poolName, runnerName, serverName, ownerID, resourceClass string, env *config.EnvConfig, query *types.QueryParams, gitspaceAgentConfig *types.GitspaceAgentConfig, storageConfig *types.StorageConfig) (*types.Instance, error) { //nolint
	m.runnerName = runnerName
	m.liteEnginePath = env.LiteEngine.Path
	m.tmate = types.Tmate(env.Tmate)

	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, fmt.Errorf("provision: pool name %q not found", poolName)
	}

	if gitspaceAgentConfig != nil && len(gitspaceAgentConfig.Ports) > 0 {
		if pool.Driver.DriverName() != "nomad" {
			return nil, fmt.Errorf("incorrect pool, gitspaces is only supported on nomad")
		}
		inst, err := m.setupInstance(ctx, pool, serverName, ownerID, resourceClass, true, gitspaceAgentConfig, storageConfig)
		return inst, err
	}

	strategy := m.strategy
	if strategy == nil {
		strategy = Greedy{}
	}

	pool.Lock()

	busy, free, _, err := m.List(ctx, pool, query)
	if err != nil {
		pool.Unlock()
		return nil, fmt.Errorf("provision: failed to list instances of %q pool: %w", poolName, err)
	}

	if len(free) == 0 {
		pool.Unlock()
		if canCreate := strategy.CanCreate(pool.MinSize, pool.MaxSize, len(busy), len(free)); !canCreate {
			return nil, ErrorNoInstanceAvailable
		}
		var inst *types.Instance
		inst, err = m.setupInstance(ctx, pool, serverName, ownerID, resourceClass, true, gitspaceAgentConfig, storageConfig)
		if err != nil {
			return nil, fmt.Errorf("provision: failed to create instance: %w", err)
		}
		return inst, nil
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
		return nil, fmt.Errorf("provision: failed to tag an instance in %q pool: %w", poolName, err)
	}
	pool.Unlock()

	// the go routine here uses the global context because this function is called
	// from setup API call (and we can't use HTTP request context for async tasks)
	go func(ctx context.Context) {
		_, _ = m.setupInstance(ctx, pool, serverName, "", "", false, nil, nil)
	}(m.globalCtx)

	return inst, nil
}

// Destroy destroys an instance in a pool.
func (m *Manager) Destroy(ctx context.Context, poolName, instanceID string, storageCleanupType *storage.CleanupType) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("provision: pool name %q not found", poolName)
	}

	instance, err := m.Find(ctx, instanceID)
	if err != nil {
		return err
	}

	err = pool.Driver.DestroyInstanceAndStorage(ctx, []*types.Instance{instance}, storageCleanupType)
	if err != nil {
		return fmt.Errorf("provision: failed to destroy an instance of %q pool: %w", poolName, err)
	}

	if derr := m.Delete(ctx, instanceID); derr != nil {
		logrus.Warnf("failed to delete instance %s from store with err: %s", instanceID, derr)
	}
	logrus.WithField("instance", instanceID).Infof("instance destroyed")
	return nil
}

func (m *Manager) BuildPools(ctx context.Context) error {
	return m.forEach(ctx, m.GetTLSServerName(), nil, m.buildPoolWithMutex)
}

func (m *Manager) cleanPool(ctx context.Context, pool *poolEntry, query *types.QueryParams, destroyBusy, destroyFree bool) error {
	pool.Lock()
	defer pool.Unlock()
	busy, free, hibernating, err := m.List(ctx, pool, query)
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
	for _, pool := range m.poolMap {
		err := m.cleanPool(ctx, pool, nil, destroyBusy, destroyFree)
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
func (m *Manager) buildPool(ctx context.Context, pool *poolEntry, tlsServerName string, query *types.QueryParams) error {
	instBusy, instFree, instHibernating, err := m.List(ctx, pool, query)
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
			inst, err := m.setupInstance(ctx, pool, tlsServerName, "", "", false, nil, nil)
			if err != nil {
				logr.WithError(err).Errorln("build pool: failed to create instance")
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

	return m.buildPool(ctx, pool, tlsServerName, query)
}

func (m *Manager) setupInstance(
	ctx context.Context,
	pool *poolEntry,
	tlsServerName,
	ownerID,
	resourceClass string,
	inuse bool,
	agentConfig *types.GitspaceAgentConfig,
	storageConfig *types.StorageConfig,
) (*types.Instance, error) {
	var inst *types.Instance
	retain := "false"

	// generate certs
	createOptions, err := certs.Generate(m.runnerName, tlsServerName)
	createOptions.IsHosted = IsHosted(ctx)
	createOptions.LiteEnginePath = m.liteEnginePath
	createOptions.Platform = pool.Platform
	createOptions.PoolName = pool.Name
	createOptions.Limit = pool.MaxSize
	createOptions.Pool = pool.MinSize
	createOptions.HarnessTestBinaryURI = m.harnessTestBinaryURI
	createOptions.PluginBinaryURI = m.pluginBinaryURI
	createOptions.Tmate = m.tmate
	createOptions.AccountID = ownerID
	createOptions.ResourceClass = resourceClass
	if storageConfig != nil {
		createOptions.StorageOpts = types.StorageOpts{
			CephPoolIdentifier: storageConfig.CephPoolIdentifier,
			Identifier:         storageConfig.Identifier,
			Size:               storageConfig.Size,
		}
	}
	createOptions.AutoInjectionBinaryURI = m.autoInjectionBinaryURI
	if agentConfig != nil && agentConfig.Secret != "" {
		createOptions.GitspaceOpts = types.GitspaceOpts{
			Secret:      agentConfig.Secret,
			AccessToken: agentConfig.AccessToken,
			Ports:       agentConfig.Ports,
		}
		retain = "true"
	}
	createOptions.Labels = map[string]string{"retain": retain}
	if err != nil {
		logrus.WithError(err).
			Errorln("manager: failed to generate certificates")
		return nil, err
	}
	// create instance
	inst, err = pool.Driver.Create(ctx, createOptions)
	if err != nil {
		logrus.WithError(err).
			Errorln("manager: failed to create instance")
		return nil, err
	}

	if inuse {
		inst.State = types.StateInUse
		inst.OwnerID = ownerID
	}

	inst.RunnerName = m.runnerName
	if inst.Labels == nil {
		labelsBytes, marshalErr := json.Marshal(map[string]string{"retain": "false"})
		if marshalErr != nil {
			return nil, fmt.Errorf("manager: could not marshal default labels, err: %w", marshalErr)
		}
		inst.Labels = labelsBytes
	}

	err = m.instanceStore.Create(ctx, inst)
	if err != nil {
		logrus.WithError(err).
			Errorln("manager: failed to store instance")
		_ = pool.Driver.Destroy(ctx, []*types.Instance{inst})
		return nil, err
	}

	if !inuse {
		go func() {
			herr := m.hibernateWithRetries(context.Background(), pool.Name, tlsServerName, inst.ID)
			if herr != nil {
				logrus.WithError(herr).Errorln("failed to hibernate the vm")
			}
		}()
	}
	return inst, nil
}

func (m *Manager) StartInstance(ctx context.Context, poolName, instanceID string) (*types.Instance, error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, fmt.Errorf("start_instance: pool name %q not found", poolName)
	}

	inst, err := m.Find(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("start_instance: failed to find the instance in db %s of %q pool: %w", instanceID, poolName, err)
	}

	if !inst.IsHibernated {
		return inst, nil
	}

	logrus.WithField("instanceID", instanceID).Infoln("Starting vm from hibernate state")
	ipAddress, err := pool.Driver.Start(ctx, instanceID, poolName)
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

func (m *Manager) InstanceLogs(ctx context.Context, poolName, instanceID string) (string, error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return "", fmt.Errorf("instance_logs: pool name %q not found", poolName)
	}

	return pool.Driver.Logs(ctx, instanceID)
}

func (m *Manager) hibernateWithRetries(ctx context.Context, poolName, tlsServerName, instanceID string) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("hibernate: pool name %q not found", poolName)
	}

	if !pool.Driver.CanHibernate() {
		return nil
	}

	m.waitForInstanceConnectivity(ctx, tlsServerName, instanceID)

	retryCount := 1
	const maxRetries = 3
	for {
		err := m.hibernate(ctx, instanceID, poolName, pool)
		if err == nil {
			logrus.WithField("instanceID", instanceID).Infoln("hibernate complete")
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
	if err = pool.Driver.Hibernate(ctx, instanceID, poolName); err != nil {
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

func (m *Manager) waitForInstanceConnectivity(ctx context.Context, tlsServerName, instanceID string) {
	bf := backoff.NewExponentialBackOff()
	for {
		duration := bf.NextBackOff()
		if duration == bf.Stop {
			return
		}

		select {
		case <-ctx.Done():
			logrus.WithField("instanceID", instanceID).Warnln("hibernate: connectivity check deadline exceeded")
			return
		case <-time.After(duration):
			err := m.checkInstanceConnectivity(ctx, tlsServerName, instanceID)
			if err == nil {
				return
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
	return m.runnerName
}

func (m *Manager) IsDistributed() bool {
	return false
}
