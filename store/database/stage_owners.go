package database

import (
	"context"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/jmoiron/sqlx"
)

var _ store.StageOwnerStore = (*StageOwnerStore)(nil)

func NewStageOwnerStore(db *sqlx.DB) *StageOwnerStore {
	return &StageOwnerStore{db}
}

type StageOwnerStore struct {
	db *sqlx.DB
}

func (s StageOwnerStore) Find(_ context.Context, id, poolName string) (*types.StageOwner, error) {
	dst := new(types.StageOwner)
	err := s.db.Get(dst, stageOwnerFindByID, id, poolName)
	return dst, err
}

func (s StageOwnerStore) Create(_ context.Context, stageOwner *types.StageOwner) error {
	query, arg, err := s.db.BindNamed(stageOwnerInsert, stageOwner)
	if err != nil {
		return err
	}
	return s.db.QueryRow(query, arg...).Scan(&stageOwner.ID)
}

func (s StageOwnerStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint
	if _, err := tx.Exec(stageOwnerDelete, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s StageOwnerStore) Purge(ctx context.Context) error {
	panic("implement me")
}

const stageOnwerBase = `
SELECT
 stage_id
,pool_name
FROM stage_owner
`

const stageOwnerFindByID = stageOnwerBase + `
WHERE stage_id = $1
AND pool_name = $2
`

const stageOwnerInsert = `
INSERT INTO stage_owner (
 stage_id
,pool_name
) values (
 :stage_id
,:pool_name
) RETURNING stage_id
`

const stageOwnerDelete = `
DELETE FROM stage_owner
WHERE stage_id = $1
`
