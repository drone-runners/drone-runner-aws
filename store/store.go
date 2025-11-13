package store

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/types"
)

type InstanceStore interface {
	Find(context.Context, string) (*types.Instance, error)
	List(context.Context, string, *types.QueryParams) ([]*types.Instance, error)
	Create(context.Context, *types.Instance) error
	Delete(context.Context, string) error
	Update(context.Context, *types.Instance) error
	Purge(context.Context) error
	DeleteAndReturn(ctx context.Context, query string, args ...any) ([]*types.Instance, error)
	FindAndClaim(ctx context.Context, params *types.QueryParams, newState types.InstanceState, allowedStates []types.InstanceState, updateStartTime bool) (*types.Instance, error)
}

type StageOwnerStore interface {
	Find(ctx context.Context, id string) (*types.StageOwner, error)
	Create(context.Context, *types.StageOwner) error
	Delete(context.Context, string) error
}

type OutboxStore interface {
	Create(context.Context, *types.OutboxJob) error
	FindAndClaimPending(context.Context, string, []types.OutboxJobType, int, time.Duration) ([]*types.OutboxJob, error)
	UpdateStatus(context.Context, int64, types.OutboxJobStatus, string) error
	Delete(context.Context, int64) error
	DeleteOlderThan(context.Context, int64) (int64, error)
}

type CapacityReservationStore interface {
	Find(ctx context.Context, id string) (*types.CapacityReservation, error)
	Create(context.Context, *types.CapacityReservation) error
	Delete(context.Context, string) error
	ListByPoolName(ctx context.Context, poolName string) ([]*types.CapacityReservation, error)
	MarkForDeletion(ctx context.Context, id string, marked bool) error
}
