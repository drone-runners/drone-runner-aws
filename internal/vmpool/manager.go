package vmpool

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone/runner-go/logger"
)

type (
	Manager struct {
		poolMap      map[string]*poolEntry
		cleanupTimer *time.Ticker
	}

	poolEntry struct {
		sync.Mutex
		Pool
	}
)

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

						busy, free, err := pool.List(ctx)
						if err != nil {
							return fmt.Errorf("failed to list instances of pool=%q error: %w", pool.GetName(), err)
						}

						var ids []string

						for _, inst := range busy {
							if time.Since(inst.StartedAt) > maxAgeBusy {
								ids = append(ids, inst.ID)
							}
						}
						for _, inst := range free {
							if time.Since(inst.StartedAt) > maxAgeFree {
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

// Get returns a Pool or nil if a pool with this name isn't defined.
// TODO: It would be nice if we could remove this function. Rest of the code should work only with the PoolManager and not directly with Pool objects.
func (m *Manager) Get(name string) Pool {
	entry := m.poolMap[name]
	if entry == nil {
		return nil
	}

	return entry.Pool
}

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
// This method and Provision method contain logic for maintaining pool size.
// The current implementation simply guaranties that there are always GetMinSize instances available for jobs
// and will not terminate any instances if there are more available than it's necessary.
func (m *Manager) buildPool(ctx context.Context, pool *poolEntry) error {
	_, freeIDs, err := pool.List(ctx)
	if err != nil {
		return err
	}

	instanceCount := len(freeIDs)
	needToCreate := pool.GetMinSize() - instanceCount

	if needToCreate <= 0 {
		return nil
	}

	wg := &sync.WaitGroup{}
	wg.Add(needToCreate)

	for i := 0; i < needToCreate; i++ {
		logr := logger.FromContext(ctx).
			WithField("provider", pool.GetProviderName()).
			WithField("pool", pool.GetName())

		go func(ctx context.Context, logr logger.Logger) {
			defer wg.Done()

			instance, err := pool.Provision(ctx, false)
			if err != nil {
				logr.WithError(err).Errorln("build pool: failed to create an instance")
				return
			}

			logr.
				WithField("pool", pool.GetName()).
				WithField("id", instance.ID).
				Infoln("build pool: created new instance")
		}(context.Background(), logr)

		instanceCount++
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
func (m *Manager) Provision(ctx context.Context, poolName string) (*Instance, error) {
	pool := m.poolMap[poolName]
	if pool == nil {
		return nil, fmt.Errorf("provision: pool name %q not found", poolName)
	}

	pool.Lock()
	defer pool.Unlock()

	_, free, err := pool.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("provision: failed to list instances of %q pool: %w", poolName, err)
	}

	if len(free) == 0 {
		var inst *Instance

		inst, err = pool.Provision(ctx, true)
		if err != nil {
			return nil, fmt.Errorf("provision: failed to provision a new instance in %q pool: %w", poolName, err)
		}

		return inst, nil
	}

	inst := &free[0]

	err = pool.TagAsInUse(ctx, inst.ID)
	if err != nil {
		return nil, fmt.Errorf("provision: failed to tag an instance in %q pool: %w", poolName, err)
	}

	go func(ctx context.Context) {
		_ = m.buildPoolWithMutex(ctx, pool)
	}(context.Background())

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

	go func(ctx context.Context) {
		_ = m.buildPoolWithMutex(ctx, pool)
	}(context.Background())

	return nil
}

func (m *Manager) BuildPools(ctx context.Context) error {
	return m.forEach(ctx, m.buildPoolWithMutex)
}

func (m *Manager) Tag(ctx context.Context, poolName, instanceID string, tags map[string]string) error {
	pool := m.poolMap[poolName]
	if pool == nil {
		return fmt.Errorf("tag: pool name %q not found", poolName)
	}

	return pool.Tag(ctx, instanceID, tags)
}

func (m *Manager) CleanPools(ctx context.Context, destroyBusy, destroyFree bool) error {
	for _, pool := range m.poolMap {
		busy, free, err := pool.List(ctx)
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
	}

	return nil
}

func (m *Manager) Ping(ctx context.Context) error {
	for _, pool := range m.poolMap {
		err := pool.Ping(ctx)
		if err != nil {
			return err
		}

		const pauseBetweenChecks = 500 * time.Millisecond
		time.Sleep(pauseBetweenChecks)
	}

	return nil
}
