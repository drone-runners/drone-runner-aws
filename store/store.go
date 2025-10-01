package store

import (
	"context"

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
	FindAndClaim(ctx context.Context, params *types.QueryParams, newState types.InstanceState, allowedStates []types.InstanceState) (*types.Instance, error)
}

type StageOwnerStore interface {
	Find(ctx context.Context, id string) (*types.StageOwner, error)
	Create(context.Context, *types.StageOwner) error
	Delete(context.Context, string) error
}
