package vmpool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drone/runner-go/logger"
)

type Manager struct {
	poolMap map[string]Pool
}

func (m *Manager) Add(pools ...Pool) error {
	if len(pools) == 0 {
		return nil
	}

	if m.poolMap == nil {
		m.poolMap = map[string]Pool{}
	}

	for _, pool := range pools {
		name := pool.GetName()
		if _, alreadyExists := m.poolMap[name]; alreadyExists {
			return fmt.Errorf("pool %q already exists", name)
		}

		m.poolMap[name] = pool
	}

	return nil
}

func (m *Manager) Get(name string) Pool {
	return m.poolMap[name]
}

func (m *Manager) ForEach(ctx context.Context, f func(ctx context.Context, pool Pool) error) error {
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
func (m *Manager) BuildPool(ctx context.Context, pool Pool) error {
	_, freeIDs, err := pool.List(ctx)
	if err != nil {
		return err
	}

	instanceCount := len(freeIDs)
	needToCreate := pool.GetMinSize() - instanceCount

	if needToCreate <= 0 {
		return nil
	}

	pool.Lock()
	defer pool.Unlock()

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
		_ = m.BuildPool(ctx, pool)
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
		_ = m.BuildPool(ctx, pool)
	}(context.Background())

	return nil
}

func (m *Manager) BuildPools(ctx context.Context) error {
	return m.ForEach(ctx, m.BuildPool)
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
