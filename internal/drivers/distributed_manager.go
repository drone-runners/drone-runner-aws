package drivers

import (
	"context"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

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
	return d.forEach(ctx, &query, d.buildPool)
}

// This helps in provisiong the VM and maintaining the pool size
func (d *DistributedManager) Provision(ctx context.Context, poolName, serverName, ownerID string, env *config.EnvConfig, queryParams *types.QueryParams) (*types.Instance, error) {
	query := types.QueryParams{RunnerName: d.runnerName}
	return d.Manager.Provision(ctx, poolName, serverName, ownerID, env, &query)
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
		queryParams *types.QueryParams) error) error {
	for _, pool := range d.poolMap {
		err := f(ctx, pool, queryParams)
		if err != nil {
			return err
		}
	}
	return nil
}
