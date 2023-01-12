package singleinstance

import (
	"context"

	"github.com/drone-runners/drone-runner-vm/types"

	"github.com/drone-runners/drone-runner-vm/store"
	"github.com/jmoiron/sqlx"
)

var (
	_                 store.InstanceStore = (*InstanceStore)(nil)
	singletonInstance types.Instance
)

func NewSingleInstanceStore(db *sqlx.DB) *InstanceStore {
	return &InstanceStore{db}
}

type InstanceStore struct {
	db *sqlx.DB
}

func (s InstanceStore) Find(_ context.Context, id string) (dst *types.Instance, err error) {
	return &singletonInstance, nil
}

func (s InstanceStore) Create(_ context.Context, instance *types.Instance) error {
	if singletonInstance.ID == "" {
		singletonInstance = *instance
	}
	return nil
}

func (s InstanceStore) Update(_ context.Context, instance *types.Instance) error {
	return nil
}

func (s InstanceStore) Delete(_ context.Context, id string) error {
	return nil
}

func (s InstanceStore) List(_ context.Context, pool string, params *types.QueryParams) ([]*types.Instance, error) {
	return nil, nil
}

func (s InstanceStore) Purge(ctx context.Context) error {
	return nil
}
