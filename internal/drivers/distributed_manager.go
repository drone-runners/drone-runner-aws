package drivers

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

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
