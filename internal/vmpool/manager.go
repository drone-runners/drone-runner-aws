package vmpool

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
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

func (m *Manager) ForEach(f func(pool Pool) error) error {
	for _, pool := range m.poolMap {
		err := f(pool)
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) Count() int {
	return len(m.poolMap)
}

func (m *Manager) BuildPools(ctx context.Context) error {
	for _, pool := range m.poolMap {
		instanceCount, err := pool.PoolCountFree(ctx)
		if err != nil {
			return err
		}

		if pool.GetMaxSize() == 0 {
			logrus.Warnf("Max pool size is set to zero %s", pool.GetName())
		}

		for instanceCount < pool.GetMaxSize() {
			instance, setupErr := pool.Provision(ctx, false)
			if setupErr != nil {
				return setupErr
			}
			logrus.Infof("BuildPools: created instance %s %s %s", pool.GetName(), instance.ID, instance.IP)
			instanceCount++
		}
	}

	return nil
}

func (m *Manager) CleanPools(ctx context.Context) error {
	for _, pool := range m.poolMap {
		err := pool.CleanPools(ctx)
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
