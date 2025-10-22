package sql

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/jmoiron/sqlx"
)

var _ store.CapacityReservationStore = (*CapacityReservationStore)(nil)

func NewCapacityReservationStore(db *sqlx.DB) *CapacityReservationStore {
	return &CapacityReservationStore{db}
}

type CapacityReservationStore struct {
	db *sqlx.DB
}

func (s CapacityReservationStore) Find(_ context.Context, id string) (*types.CapacityReservation, error) {
	dst := new(types.CapacityReservation)
	err := s.db.Get(dst, capacityReservationFindByID, id)
	return dst, err
}

func (s CapacityReservationStore) Create(_ context.Context, capacityReservation *types.CapacityReservation) error {
	capacityReservation.CreatedAt = time.Now().Unix()
	query, arg, err := s.db.BindNamed(capacityReservationInsert, capacityReservation)
	if err != nil {
		return err
	}
	return s.db.QueryRow(query, arg...).Scan(&capacityReservation.StageID)
}

func (s CapacityReservationStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint
	if _, err := tx.Exec(capacityReservationDelete, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s CapacityReservationStore) ListByPoolName(ctx context.Context, poolName string) ([]*types.CapacityReservation, error) {
	query := capacityReservationFindByPoolName

	var reservations []*types.CapacityReservation
	if err := s.db.SelectContext(ctx, &reservations, query, poolName); err != nil {
		return nil, err
	}
	return reservations, nil
}

func (s CapacityReservationStore) Purge(ctx context.Context) error {
	panic("implement me")
}

const capacityReservationBase = `
SELECT
 stage_id
,pool_name
,instance_id
,reservation_id
,created_at
FROM capacity_reservation
`
const capacityReservationInsert = `
INSERT INTO capacity_reservation (
 stage_id
,pool_name
,instance_id
,reservation_id
,created_at
) values (
 :stage_id
,:pool_name
,:instance_id
,:reservation_id
,:created_at
) RETURNING stage_id
`

const capacityReservationFindByID = capacityReservationBase + `
WHERE stage_id = $1
`

const capacityReservationDelete = `
DELETE FROM capacity_reservation
WHERE stage_id = $1
`
const capacityReservationFindByPoolName = capacityReservationBase + `
WHERE pool_name = $1
`
