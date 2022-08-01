package drivers

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
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
		globalCtx      context.Context
		poolMap        map[string]*poolEntry
		strategy       Strategy
		cleanupTimer   *time.Ticker
		runnerName     string
		liteEnginePath string
		instanceStore  store.InstanceStore
	}

	poolEntry struct {
		sync.Mutex
		Pool
	}
)

func New(
	globalContext context.Context,
	instanceStore store.InstanceStore,
	liteEnginePath string,
	runnerName string,
) *Manager {
	return &Manager{
		globalCtx:      globalContext,
		instanceStore:  instanceStore,
		runnerName:     runnerName,
		liteEnginePath: liteEnginePath,
	}
}

// Inspect returns OS and root directory for a pool.
func (m *Manager) Inspect(name string) (platform types.Platform, rootDir string) {
	entry := m.poolMap[name]
	if entry == nil {
		return
	}

	platform = entry.Platform
	rootDir = entry.Driver.RootDir()

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
	query := types.QueryParams{Status: types.StateInUse, Stage: stage}
	list, err := m.instanceStore.List(ctx, pool.Name, &query)
	if err != nil {
		logger.FromContext(ctx).WithError(err).
			Errorln("manager: GetInstanceByStageID failed to list instances")
		return nil, err
	}

	if len(list) == 0 {
		return nil, errors.New("manager: instance not found")
	}
	return list[0], nil
}

func (m *Manager) List(ctx context.Context, pool *poolEntry) (busy, free []*types.Instance, err error) {
	list, err := m.instanceStore.List(ctx, pool.Name, nil)
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
		} else {
			free = append(free, loopInstance)
		}
	}

	return busy, free, nil
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

func (m *Manager) StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree time.Duration) error {
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

					err := m.forEach(ctx, func(ctx context.Context, pool *poolEntry) error {
						logr := logger.FromContext(ctx).
							WithField("driver", pool.Driver.DriverName()).
							WithField("pool", pool.Name)

						pool.Lock()
						defer pool.Unlock()

						busy, free, err := m.List(ctx, pool)
						if err != nil {
							return fmt.Errorf("failed to list instances of pool=%q error: %w", pool.Name, err)
						}

						var ids []string
						for _, inst := range busy {
							startedAt := time.Unix(inst.Started, 0)
							if time.Since(startedAt) > maxAgeBusy {
								ids = append(ids, inst.ID)
							}
						}
						for _, inst := range free {
							startedAt := time.Unix(inst.Started, 0)
							if time.Since(startedAt) > maxAgeFree {
								ids = append(ids, inst.ID)
							}
						}

						if len(ids) == 0 {
							return nil
						}

						logr.Infof("purger: Terminating %d stale instances\n", len(ids))

						err = pool.Driver.Destroy(ctx, ids...)
						if err != nil {
							return fmt.Errorf("failed to delete instances of pool=%q error: %w", pool.Name, err)
						}
						for _, id := range ids {
							derr := m.Delete(ctx, id)
							if derr != nil {
								return fmt.Errorf("failed to delete %s from instance store with err: %s", id, derr)
							}
						}

						err = m.buildPool(ctx, pool)
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
func (m *Manager) Provision(ctx context.Context, poolName, serverName, liteEnginePath string) (*types.Instance, error) {
	m.runnerName = serverName
	m.liteEnginePath = liteEnginePath

	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, fmt.Errorf("provision: pool name %q not found", poolName)
	}

	pool.Lock()
	defer pool.Unlock()

	strategy := m.strategy
	if strategy == nil {
		strategy = Greedy{}
	}

	busy, free, err := m.List(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("provision: failed to list instances of %q pool: %w", poolName, err)
	}

	if len(free) == 0 {
		if canCreate := strategy.CanCreate(pool.MinSize, pool.MaxSize, len(busy), len(free)); !canCreate {
			return nil, ErrorNoInstanceAvailable
		}
		var inst *types.Instance
		inst, err = m.setupInstance(ctx, pool, true)
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
	err = m.instanceStore.Update(ctx, inst)
	if err != nil {
		return nil, fmt.Errorf("provision: failed to tag an instance in %q pool: %w", poolName, err)
	}

	// the go routine here uses the global context because this function is called
	// from setup API call (and we can't use HTTP request context for async tasks)
	go func(ctx context.Context) {
		_ = m.buildPoolWithMutex(ctx, pool)
	}(m.globalCtx)

	return inst, nil
}

// Destroy destroys an instance in a pool.
func (m *Manager) Destroy(ctx context.Context, poolName, instanceID string) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("provision: pool name %q not found", poolName)
	}

	err := pool.Driver.Destroy(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("provision: failed to destroy an instance of %q pool: %w", poolName, err)
	}

	if derr := m.Delete(ctx, instanceID); derr != nil {
		logrus.Warnf("failed to delete instance %s from store with err: %s", instanceID, derr)
	}
	// the go routine here uses the global context because this function is called
	// from destroy API call (and we can't use HTTP request context for async tasks)
	go func(ctx context.Context) {
		_ = m.buildPoolWithMutex(ctx, pool)
	}(m.globalCtx)

	return nil
}

func (m *Manager) BuildPools(ctx context.Context) error {
	return m.forEach(ctx, m.buildPoolWithMutex)
}

func (m *Manager) CleanPools(ctx context.Context, destroyBusy, destroyFree bool) error {
	for _, pool := range m.poolMap {
		busy, free, err := m.List(ctx, pool)
		if err != nil {
			return err
		}
		var instanceIDs []string

		if destroyBusy {
			for _, inst := range busy {
				instanceIDs = append(instanceIDs, inst.ID)
			}
		}

		if destroyFree {
			for _, inst := range free {
				instanceIDs = append(instanceIDs, inst.ID)
			}
		}

		err = pool.Driver.Destroy(ctx, instanceIDs...)
		if err != nil {
			return err
		}
		for _, inst := range instanceIDs {
			err = m.Delete(ctx, inst)
			if err != nil {
				return err
			}
		}
	}

	return nil
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

// BuildPool populates a pool with as many instances as it's needed for the pool.
func (m *Manager) buildPool(ctx context.Context, pool *poolEntry) error {
	instBusy, instFree, err := m.List(ctx, pool)
	if err != nil {
		return err
	}

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
		ids := make([]string, shouldRemove)
		for i := 0; i < shouldRemove; i++ {
			ids[i] = instFree[i].ID
		}

		err := pool.Driver.Destroy(ctx, ids...)
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
			inst, err := m.setupInstance(ctx, pool, false)
			if err != nil {
				logr.WithError(err).Errorln("build pool: failed to create instance")
				return
			}
			logr.
				WithField("pool", pool.Name).
				WithField("id", inst.ID).
				Infoln("build pool: created new instance")

			go func() {
				herr := m.hibernateWithRetries(context.Background(), pool.Name, inst.ID)
				if herr != nil {
					logr.WithError(herr).Errorln("failed to hibernate the vm")
				}
			}()
		}(ctx, logr)

		shouldCreate--
	}

	wg.Wait()

	return nil
}

func (m *Manager) buildPoolWithMutex(ctx context.Context, pool *poolEntry) error {
	pool.Lock()
	defer pool.Unlock()

	return m.buildPool(ctx, pool)
}

func (m *Manager) setupInstance(ctx context.Context, pool *poolEntry, inuse bool) (*types.Instance, error) {
	var inst *types.Instance

	// generate certs
	createOptions, err := certs.Generate(m.runnerName)
	createOptions.LiteEnginePath = m.liteEnginePath
	createOptions.Platform = pool.Platform
	createOptions.PoolName = pool.Name
	createOptions.Limit = pool.MaxSize
	createOptions.Pool = pool.MinSize
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
	}

	err = m.instanceStore.Create(ctx, inst)
	if err != nil {
		logrus.WithError(err).
			Errorln("manager: failed store instance")
		_ = pool.Driver.Destroy(ctx, inst.ID)
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

	ipAddress, err := pool.Driver.Start(ctx, instanceID, poolName)
	if err != nil {
		return nil, fmt.Errorf("start_instance: failed to start the instance %s of %q pool: %w", instanceID, poolName, err)
	}

	pool.Lock()
	defer pool.Unlock()

	inst.IsHibernated = false
	inst.Address = ipAddress
	if err := m.instanceStore.Update(ctx, inst); err != nil {
		return nil, fmt.Errorf("start_instance: failed to update instance store %s of %q pool: %w", instanceID, poolName, err)
	}
	return inst, nil
}

func (m *Manager) InstanceLogs(ctx context.Context, poolName, instanceID string) (string, error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return "", fmt.Errorf("instance_logs: pool name %q not found", poolName)
	}

	return pool.Driver.Logs(ctx, instanceID)
}

func (m *Manager) hibernateWithRetries(ctx context.Context, poolName, instanceID string) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("hibernate: pool name %q not found", poolName)
	}

	if pool.Driver.DriverName() != string(types.Amazon) {
		return nil
	}

	if !pool.Driver.CanHibernate() {
		return nil
	}

	m.waitForInstanceConnectivity(ctx, instanceID)

	retryCount := 1
	const maxRetries = 3
	for {
		err := m.hibernate(ctx, instanceID, poolName, pool)
		if err == nil {
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
	defer pool.Unlock()

	inst, err := m.Find(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("hibernate: failed to find the instance in db %s of %q pool: %w", instanceID, poolName, err)
	}

	if inst.State == types.StateInUse {
		return nil
	}

	err = pool.Driver.Hibernate(ctx, instanceID, poolName)
	if err != nil {
		return fmt.Errorf("hibernate: failed to hibernated an instance %s of %q pool: %w", instanceID, poolName, err)
	}

	inst.IsHibernated = true
	if err := m.instanceStore.Update(ctx, inst); err != nil {
		return fmt.Errorf("hibernate: failed to update instance in db %s of %q pool: %w", instanceID, poolName, err)
	}
	return nil
}

func (m *Manager) forEach(ctx context.Context, f func(ctx context.Context, pool *poolEntry) error) error {
	for _, pool := range m.poolMap {
		err := f(ctx, pool)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) waitForInstanceConnectivity(ctx context.Context, instanceID string) {
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
			err := m.checkInstanceConnectivity(ctx, instanceID)
			if err == nil {
				return
			}
			logrus.WithError(err).WithField("instanceID", instanceID).Traceln("hibernate: instance connectivity check failed")
		}
	}
}

func (m *Manager) checkInstanceConnectivity(ctx context.Context, instanceID string) error {
	instance, err := m.Find(ctx, instanceID)
	if err != nil {
		return errors.Wrap(err, "failed to find the instance in db")
	}

	if instance.Address == "" {
		return errors.New("instance has not received IP address")
	}

	endpoint := fmt.Sprintf("https://%s:9079/", instance.Address)
	client, err := lehttp.NewHTTPClient(endpoint, m.runnerName, string(instance.CACert), string(instance.TLSCert), string(instance.TLSKey))
	if err != nil {
		return errors.Wrap(err, "failed to create client")
	}

	response, err := client.Health(ctx)
	if err != nil {
		return err
	}

	if !response.OK {
		return errors.New("health check call failed")
	}

	return nil
}
