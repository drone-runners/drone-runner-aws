package sql

import (
	"context"

	"github.com/drone-runners/drone-runner-aws/store/database/mutex"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ store.CapacityReservationStore = (*CapacityReservationStoreSync)(nil)

func NewCapacityReservationStoreSync(capacityReservationStore *CapacityReservationStore) *CapacityReservationStoreSync {
	return &CapacityReservationStoreSync{capacityReservationStore}
}

type CapacityReservationStoreSync struct{ base *CapacityReservationStore }

func (i CapacityReservationStoreSync) Find(ctx context.Context, id string) (*types.CapacityReservation, error) {
	mutex.RLock()
	defer mutex.RUnlock()
	return i.base.Find(ctx, id)
}

func (i CapacityReservationStoreSync) Create(ctx context.Context, stageOwner *types.CapacityReservation) error {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.Create(ctx, stageOwner)
}

func (i CapacityReservationStoreSync) Delete(ctx context.Context, s string) error {
	mutex.Lock()
	defer mutex.Unlock()
	return i.base.Delete(ctx, s)
}

func (i CapacityReservationStoreSync) Purge(ctx context.Context) error {
	mutex.Lock()
	defer mutex.Unlock()
	panic("implement me")
}
