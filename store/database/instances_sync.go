package database

import (
	"context"

	"github.com/drone-runners/drone-runner-aws/store/database/mutex"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ store.InstanceStore = (*InstanceStoreSync)(nil)

func NewInstanceStoreSync(store *InstanceStore) *InstanceStoreSync {
	return &InstanceStoreSync{store}
}

type InstanceStoreSync struct{ base *InstanceStore }

func (i InstanceStoreSync) Find(ctx context.Context, s string) (*types.Instance, error) {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.Find(ctx, s)
}

func (i InstanceStoreSync) List(ctx context.Context, pool string, params *types.QueryParams) ([]*types.Instance, error) {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.List(ctx, pool, params)
}

func (i InstanceStoreSync) Create(ctx context.Context, instance *types.Instance) error {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.Create(ctx, instance)
}

func (i InstanceStoreSync) Delete(ctx context.Context, s string) error {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.Delete(ctx, s)
}

func (i InstanceStoreSync) Update(ctx context.Context, instance *types.Instance) error {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.Update(ctx, instance)
}

func (i InstanceStoreSync) Purge(ctx context.Context) error {
	mutex.Lock()
	defer mutex.Unlock()
	panic("implement me")
}
