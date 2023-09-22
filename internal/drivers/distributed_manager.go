package drivers

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/Masterminds/squirrel"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/sirupsen/logrus"
)

type DistributedManager struct {
	Manager
}

func NewDistributedManager(manager *Manager) *DistributedManager {
	return &DistributedManager{
		*manager,
	}
}

func (d *DistributedManager) BuildPools(ctx context.Context) error {
	query := types.QueryParams{RunnerName: d.runnerName}
	return d.forEach(ctx, d.GetTLSServerName(), &query, d.buildPoolWithMutex)
}

// This helps in cleaning the pools
func (d *DistributedManager) CleanPools(ctx context.Context, destroyBusy, destroyFree bool) error {
	var returnError error
	query := types.QueryParams{RunnerName: d.runnerName}
	for _, pool := range d.poolMap {
		err := d.cleanPool(ctx, pool, &query, destroyBusy, destroyFree)
		if err != nil {
			returnError = err
			logrus.Errorf("failed to clean pool %s with error: %s", pool.Name, err)
		}
	}

	return returnError
}

func (d *DistributedManager) GetInstanceStore() store.InstanceStore {
	return d.instanceStore
}

func (d *DistributedManager) GetStageOwnerStore() store.StageOwnerStore {
	return d.stageOwnerStore
}

func (d *DistributedManager) GetTLSServerName() string {
	// keep server name constant since any runner should be able to send request to LE
	return "distributed-dlite"
}

func (d *DistributedManager) IsDistributed() bool {
	return true
}

// Instance purger for distributed dlite
// Delete all instances irrespective of runner name
func (d *DistributedManager) StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree time.Duration) error {
	const minMaxAge = 5 * time.Minute
	if maxAgeBusy < minMaxAge || maxAgeFree < minMaxAge {
		return fmt.Errorf("distributed dlite: minimum value of max age is %.2f minutes", minMaxAge.Minutes())
	}
	if maxAgeBusy > maxAgeFree {
		return fmt.Errorf(
			"distributed dlite: max age of used instances (set to %.2fmin) should be less than max age of free instances (set to %.2fmin)",
			maxAgeBusy.Minutes(), maxAgeFree.Minutes())
	}

	if d.cleanupTimer != nil {
		panic("distributed dlite: purger already started")
	}

	t := time.Duration(maxAgeBusy.Minutes() * 0.9 * float64(time.Minute))
	d.cleanupTimer = time.NewTicker(t)

	logrus.Infof("distributed dlite: Instance purger started. It will run every %.2f minutes", t.Minutes())

	go func() {
		defer d.cleanupTimer.Stop()
		for {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logrus.Errorf("distributed dlite: PANIC %v\n%s", r, debug.Stack())
					}
				}()

				select {
				case <-ctx.Done():
					return
				case <-d.cleanupTimer.C:
					logrus.Traceln("distributed dlite: Launching instance purger")

					for _, pool := range d.poolMap {
						if err := d.startInstancePurger(ctx, pool, maxAgeBusy); err != nil {
							logger.FromContext(ctx).WithError(err).
								Errorln("distributed dlite: purger: Failed to purge stale instances")
						}
					}
				}
			}()
		}
	}()

	return nil
}

func (d *DistributedManager) startInstancePurger(ctx context.Context, pool *poolEntry, maxAgeBusy time.Duration) error {
	logr := logger.FromContext(ctx).
		WithField("driver", pool.Driver.DriverName()).
		WithField("pool", pool.Name)

	pool.Lock()
	defer pool.Unlock()

	conditions := squirrel.Or{}
	currentTime := time.Now()

	if maxAgeBusy != 0 {
		busyCondition := squirrel.And{
			squirrel.Eq{"instance_state": types.StateInUse},
			squirrel.Lt{"instance_started": currentTime.Add(-maxAgeBusy).Unix()},
		}
		conditions = append(conditions, busyCondition)
	}

	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)
	deleteSQL, args, err := builder.Delete("instances").Where(conditions).Suffix("RETURNING instance_id, instance_node_id").ToSql()
	if err != nil {
		return err
	}

	instances, err := d.instanceStore.DeleteAndReturn(ctx, deleteSQL, args...)
	if err != nil {
		return err
	}

	if len(instances) == 0 {
		return nil
	}

	logr.Infof("distributed dlite: purger: Terminating %d stale instances\n", len(instances))

	err = pool.Driver.Destroy(ctx, instances)
	if err != nil {
		return fmt.Errorf("distributed dlite: failed to delete instances of pool=%q error: %w", pool.Name, err)
	}

	err = d.buildPool(ctx, pool, d.GetTLSServerName(), nil)
	if err != nil {
		return fmt.Errorf("distributed dlite: failed to rebuld pool=%q error: %w", pool.Name, err)
	}

	return nil
}
