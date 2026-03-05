package drivers

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/drone/runner-go/logger"
	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/types"
)

// stuckProvisioningMaxAge is defined in distributed_manager.go

// StartInstancePurger starts the background instance purger.
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
							busy, free, hibernating, provisioning, err := m.list(ctx, pool, queryParams)
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
							for _, inst := range provisioning {
								startedAt := time.Unix(inst.Started, 0)
								if time.Since(startedAt) > stuckProvisioningMaxAge {
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

// cleanPool cleans up instances in a pool.
func (m *Manager) cleanPool(ctx context.Context, pool *poolEntry, query *types.QueryParams, destroyBusy, destroyFree bool) error {
	pool.Lock()
	defer pool.Unlock()
	busy, free, hibernating, provisioning, err := m.list(ctx, pool, query)
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
		instances = append(instances, provisioning...)
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

// CleanPools cleans up all pools.
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
