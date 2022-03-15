package drivers

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/internal/certs"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/sirupsen/logrus"
)

type (
	Manager struct {
		globalCtx      context.Context
		poolMap        map[string]*poolEntry
		strategy       Strategy
		cleanupTimer   *time.Ticker
		serverName     string
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
	serverName string,
) *Manager {
	return &Manager{
		globalCtx:      globalContext,
		instanceStore:  instanceStore,
		serverName:     serverName,
		liteEnginePath: liteEnginePath,
	}
}

func (m *Manager) Find(ctx context.Context, instanceID string) (*types.Instance, error) {
	return m.instanceStore.Find(ctx, instanceID)
}

func (m *Manager) List(ctx context.Context, pool *poolEntry) (busy, free []types.Instance, err error) {
	list, err := m.instanceStore.List(ctx, pool.GetName())
	if err != nil {
		logger.FromContext(ctx).WithError(err).
			Errorln("manager: failed to list instances")
		return
	}
	for _, instance := range list {
		if instance.State == types.StateInUse {
			busy = append(busy, *instance)
		} else {
			free = append(free, *instance)
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

	for _, pool := range pools {
		name := pool.GetName()
		if name == "" {
			return errors.New("pool must have a name")
		}

		if _, alreadyExists := m.poolMap[name]; alreadyExists {
			return fmt.Errorf("pool %q already defined", name)
		}

		m.poolMap[name] = &poolEntry{
			Mutex: sync.Mutex{},
			Pool:  pool,
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
							WithField("provider", pool.GetProviderName()).
							WithField("pool", pool.GetName())

						pool.Lock()
						defer pool.Unlock()

						busy, free, err := m.List(ctx, pool)
						if err != nil {
							return fmt.Errorf("failed to list instances of pool=%q error: %w", pool.GetName(), err)
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

						err = pool.Destroy(ctx, ids...)
						if err != nil {
							return fmt.Errorf("failed to delete instances of pool=%q error: %w", pool.GetName(), err)
						}

						err = m.buildPool(ctx, pool)
						if err != nil {
							return fmt.Errorf("failed to rebuld pool=%q error: %w", pool.GetName(), err)
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

// Inspect returns a OS and root directory for a pool.
func (m *Manager) Inspect(name string) (os, rootDir string) {
	entry := m.poolMap[name]
	if entry == nil {
		return
	}

	os = entry.GetOS()
	rootDir = entry.GetRootDir()

	return
}

// Exists returns true if a pool with given name exists.
func (m *Manager) Exists(name string) bool {
	return m.poolMap[name] != nil
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

func (m *Manager) Count() int {
	return len(m.poolMap)
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
		WithField("provider", pool.GetProviderName()).
		WithField("pool", pool.GetName())

	shouldCreate, shouldRemove := strategy.CountCreateRemove(
		pool.GetMinSize(), pool.GetMaxSize(),
		len(instBusy), len(instFree))

	if shouldRemove > 0 {
		ids := make([]string, shouldRemove)
		for i := 0; i < shouldRemove; i++ {
			ids[i] = instFree[i].ID
		}

		err := pool.Destroy(ctx, ids...)
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
				WithField("pool", pool.GetName()).
				WithField("id", inst.ID).
				Infoln("build pool: created new instance")
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

// Provision returns an instance for a job execution and tags it as in use.
// This method and BuildPool method contain logic for maintaining pool size.
func (m *Manager) Provision(ctx context.Context, poolName, serverName, liteEnginePath string) (*types.Instance, error) {
	m.serverName = serverName
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
		if canCreate := strategy.CanCreate(pool.GetMinSize(), pool.GetMaxSize(), len(busy), len(free)); !canCreate {
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

	inst := &free[0]
	inst, err = m.instanceStore.Find(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("provision: failed to find instance %q: %w", inst.ID, err)
	}
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

	pool.Lock()
	defer pool.Unlock()

	err := pool.Destroy(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("provision: failed to destroy an instance of %q pool: %w", poolName, err)
	}
	err = m.Destroy(ctx, poolName, instanceID)
	if err != nil {
		return fmt.Errorf("provision: failed to delete an instance of %q pool: %w", poolName, err)
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

func (m *Manager) GetUsedInstanceByTag(ctx context.Context, poolName, tag, value string) (*types.Instance, error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, fmt.Errorf("get by tag: pool name %q not found", poolName)
	}

	return pool.GetUsedInstanceByTag(ctx, tag, value)
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

		err = pool.Destroy(ctx, instanceIDs...)
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

func (m *Manager) CheckProvider(ctx context.Context) error {
	for _, pool := range m.poolMap {
		err := pool.CheckProvider(ctx)
		if err != nil {
			return err
		}

		const pauseBetweenChecks = 500 * time.Millisecond
		time.Sleep(pauseBetweenChecks)
	}

	return nil
}

func (m *Manager) setupInstance(ctx context.Context, pool *poolEntry, inuse bool) (*types.Instance, error) {
	var inst *types.Instance

	// generate certs
	certOptions, err := certs.Generate(m.serverName)
	certOptions.LiteEnginePath = m.liteEnginePath
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: failed to generate certificates")
		return nil, err
	}

	// create instance
	inst, err = pool.Create(ctx, certOptions)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: failed to create instance")
		return nil, err
	}

	if inuse {
		inst.State = types.StateInUse
	}

	// store instance
	err = m.instanceStore.Create(ctx, inst)
	if err != nil {
		logrus.WithError(err).
			Errorln("delegate: failed store instance")
	}
	return inst, nil
}
