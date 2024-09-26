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
	query := types.QueryParams{RunnerName: d.runnerName, MatchLabels: map[string]string{"retain": "false"}}
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
func (d *DistributedManager) StartInstancePurger(ctx context.Context, maxAgeBusy, maxAgeFree, purgerTime time.Duration) error {
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

	d.cleanupTimer = time.NewTicker(purgerTime)

	logrus.Infof("distributed dlite: Instance purger started. It will run every %.2f minutes", purgerTime.Minutes())

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

					queryParams := types.QueryParams{MatchLabels: map[string]string{"retain": "false"}}
					// All instances are labeled with retain: true/false
					// If retain is true, instance is not cleaned up while we clean the pools or run the instance purger
					// These instances are only cleaned up when there's a cleanup request from client explicitly.
					// This is the case for VMs created for CDE
					// If retain is false, the instance is cleaned up as earlier. This is the case for CI VMs
					// MatchLabels in the query params are used in a generic manner to match it against the labels stored in the instance
					// This is similar to how K8s matchLabels and labels work.
					for _, pool := range d.poolMap {
						if err := d.startInstancePurger(ctx, pool, maxAgeBusy, queryParams); err != nil {
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

func (d *DistributedManager) startInstancePurger(ctx context.Context, pool *poolEntry, maxAgeBusy time.Duration, queryParams types.QueryParams) error {
	logr := logger.FromContext(ctx).
		WithField("driver", pool.Driver.DriverName()).
		WithField("pool", pool.Name)

	pool.Lock()
	defer pool.Unlock()

	conditions := squirrel.Or{}
	currentTime := time.Now()

	if maxAgeBusy != 0 {
		busyCondition := squirrel.And{
			squirrel.Eq{"instance_pool": pool.Name},
			squirrel.Eq{"instance_state": types.StateInUse},
			squirrel.Lt{"instance_started": currentTime.Add(-maxAgeBusy).Unix()},
		}
		for key, value := range queryParams.MatchLabels {
			condition := squirrel.Expr("(instance_labels->>?) = ?", key, value)
			busyCondition = append(busyCondition, condition)
		}
		conditions = append(conditions, busyCondition)
	}

	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)
	deleteSQL, args, err := builder.Delete("instances").Where(conditions).Suffix("RETURNING instance_id, instance_name, instance_node_id").ToSql()
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

	var instanceNames []string

	for _, instance := range instances {
		instanceNames = append(instanceNames, instance.Name)
	}

	logr.Infof("distributed dlite: purger: Terminating stale instances\n%s", instanceNames)

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
