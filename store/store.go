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
}
