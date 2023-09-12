package drivers

import (
	"context"
	"fmt"
	"math"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/Masterminds/squirrel"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	minPoolSize int = 2
)

type DistributedManager struct {
	Manager
}

func NewDistributedManager(manager *Manager) *DistributedManager {
	return &DistributedManager{
		*manager,
	}
}

func (d *DistributedManager) Add(pools ...Pool) error {
	if len(pools) == 0 {
		return nil
	}

	if d.poolMap == nil {
		d.poolMap = map[string]*poolEntry{}
	}

	for i := range pools {
		name := pools[i].Name
		if name == "" {
			return errors.New("pool must have a name")
		}

		if _, alreadyExists := d.poolMap[name]; alreadyExists {
			return fmt.Errorf("pool %q already defined", name)
		}

		pools[i].MinSize = int(math.Min(float64(pools[i].MinSize), float64(minPoolSize)))

		d.poolMap[name] = &poolEntry{
			Mutex: sync.Mutex{},
			Pool:  pools[i],
		}
	}

	return nil
}

func (d *DistributedManager) BuildPools(ctx context.Context) error {
	query := types.QueryParams{RunnerName: d.runnerName}
	return d.forEach(ctx, &query, d.buildPool)
}

// This helps in provisiong the VM and maintaining the pool size
func (d *DistributedManager) Provision(ctx context.Context, poolName, serverName, ownerID string, env *config.EnvConfig) (*types.Instance, error) {
	d.liteEnginePath = env.LiteEngine.Path
	d.tmate = types.Tmate(env.Tmate)

	pool := d.poolMap[poolName]
	if pool == nil {
		return nil, fmt.Errorf("provision: pool name %q not found", poolName)
	}

	strategy := d.strategy
	if strategy == nil {
		strategy = Greedy{}
	}

	pool.Lock()

	query := types.QueryParams{RunnerName: d.runnerName}
	busy, free, _, err := d.List(ctx, pool, &query)
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
		inst, err = d.setupInstance(ctx, pool, d.GetTlsServerName(), ownerID, true)
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
	err = d.instanceStore.Update(ctx, inst)
	if err != nil {
		pool.Unlock()
		return nil, fmt.Errorf("provision: failed to tag an instance in %q pool: %w", poolName, err)
	}
	pool.Unlock()

	// the go routine here uses the global context because this function is called
	// from setup API call (and we can't use HTTP request context for async tasks)
	go func(ctx context.Context) {
		_, _ = d.setupInstance(ctx, pool, d.GetTlsServerName(), "", false)
	}(d.globalCtx)

	return inst, nil
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

func (d *DistributedManager) forEach(ctx context.Context,
	queryParams *types.QueryParams,
	f func(ctx context.Context, pool *poolEntry,
		tlsServerName string,
		queryParams *types.QueryParams) error) error {
	for _, pool := range d.poolMap {
		err := f(ctx, pool, d.GetTlsServerName(), queryParams)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *DistributedManager) GetTlsServerName() string {
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
						if err := d.startInstancePurger(ctx, pool, maxAgeBusy, maxAgeFree); err != nil {
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

func (d *DistributedManager) startInstancePurger(ctx context.Context, pool *poolEntry, maxAgeBusy, maxAgeFree time.Duration) error {
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
	if maxAgeFree != 0 {
		freeCondition := squirrel.And{
			squirrel.Eq{"instance_state": []string{string(types.StateCreated), string(types.StateHibernating)}},
			squirrel.Lt{"instance_started": currentTime.Add(-maxAgeFree).Unix()},
		}
		conditions = append(conditions, freeCondition)
	}

	builder := squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)
	deleteSql, args, err := builder.Delete("instances").Where(conditions).Suffix("RETURNING instance_id, instance_node_id").ToSql()
	if err != nil {
		return err
	}

	instances, err := d.instanceStore.DeleteAndReturn(ctx, deleteSql, args...)
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
	for _, instance := range instances {
		derr := d.Delete(ctx, instance.ID)
		if derr != nil {
			return fmt.Errorf("distributed dlite: failed to delete %s from instance store with err: %s", instance.ID, derr)
		}
	}

	err = d.buildPool(ctx, pool, d.GetTlsServerName(), nil)
	if err != nil {
		return fmt.Errorf("distributed dlite: failed to rebuld pool=%q error: %w", pool.Name, err)
	}

	return nil
}
