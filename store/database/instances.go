package database

import (
	"context"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/jmoiron/sqlx"
)

var _ store.InstanceStore = (*InstanceStore)(nil)

func NewInstanceStore(db *sqlx.DB) *InstanceStore {
	return &InstanceStore{db}
}

type InstanceStore struct {
	db *sqlx.DB
}

func (s InstanceStore) Find(_ context.Context, id string) (*types.Instance, error) {
	dst := new(types.Instance)
	err := s.db.Get(dst, instanceFindByID, id)
	return dst, err
}

func (s InstanceStore) List(_ context.Context, pool string) ([]*types.Instance, error) {
	dst := []*types.Instance{}
	err := s.db.Select(&dst, instanceFind, pool)
	return dst, err
}

func (s InstanceStore) Create(_ context.Context, instance *types.Instance) error {
	query, arg, err := s.db.BindNamed(instanceInsert, instance)
	if err != nil {
		return err
	}
	return s.db.QueryRow(query, arg...).Scan(&instance.ID)
}

func (s InstanceStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(instanceDelete, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s InstanceStore) Update(_ context.Context, instance *types.Instance) error {
	query, arg, err := s.db.BindNamed(instanceUpdateState, instance)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(query, arg...)
	return err
}

func (s InstanceStore) Purge(ctx context.Context) error {
	panic("implement me")
}

const instanceBase = `
SELECT
 instance_name
,instance_id
,instance_address
,instance_provider
,instance_state
,instance_pool
,instance_image
,instance_region
,instance_zone
,instance_size
,instance_platform
,instance_ca_key
,instance_ca_cert
,instance_tls_key
,instance_tls_cert
,instance_created
,instance_started
FROM instances
`

const instanceFindByID = instanceBase + `
WHERE instance_id = $1
`

const instanceFind = instanceBase + `
WHERE instance_pool = $1
ORDER BY instance_created ASC
`

const instanceInsert = `
INSERT INTO instances (
 instance_id
,instance_name
,instance_address
,instance_provider
,instance_state
,instance_pool
,instance_image
,instance_region
,instance_zone
,instance_size
,instance_platform
,instance_ca_key
,instance_ca_cert
,instance_tls_key
,instance_tls_cert
,instance_created
,instance_started
) values (
 :instance_id
,:instance_name
,:instance_address
,:instance_provider
,:instance_state
,:instance_pool
,:instance_image
,:instance_region
,:instance_zone
,:instance_size
,:instance_platform
,:instance_ca_key
,:instance_ca_cert
,:instance_tls_key
,:instance_tls_cert
,:instance_created
,:instance_started
) RETURNING instance_id
`

const instanceDelete = `
DELETE FROM instances
WHERE instance_id = $1
`

const instanceUpdateState = `
UPDATE instances
SET
 instance_state   = :instance_state
WHERE instance_id = :instance_id
`
