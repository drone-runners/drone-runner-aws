package database

import (
	"context"

	"github.com/drone-runners/drone-runner-aws/store/database/mutex"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ store.StageOwnerStore = (*StageOwnerStoreSync)(nil)

func NewStageOwnerStoreSync(stageOwnerStore *StageOwnerStore) *StageOwnerStoreSync {
	return &StageOwnerStoreSync{stageOwnerStore}
}

type StageOwnerStoreSync struct{ base *StageOwnerStore }

func (i StageOwnerStoreSync) Find(ctx context.Context, id, poolName string) (*types.StageOwner, error) {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.Find(ctx, id, poolName)
}

func (i StageOwnerStoreSync) Create(ctx context.Context, stageOwner *types.StageOwner) error {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.Create(ctx, stageOwner)
}

func (i StageOwnerStoreSync) Delete(ctx context.Context, s string) error {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.Delete(ctx, s)
}

func (i StageOwnerStoreSync) Purge(ctx context.Context) error {
	mutex.Lock()
	defer mutex.Unlock()
	panic("implement me")
}
