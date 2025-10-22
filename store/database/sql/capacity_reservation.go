package sql

import (
	"context"
	"time"

	"github.com/Masterminds/squirrel"
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

func (s CapacityReservationStore) Create(ctx context.Context, capacityReservation *types.CapacityReservation) error {
	now := time.Now().Unix()

	query := squirrel.Insert("capacity_reservation").
		Columns(
			"stage_id",
			"pool_name",
			"instance_id",
			"reservation_id",
			"created_at",
		).
		Values(
			capacityReservation.StageID,
			capacityReservation.PoolName,
			capacityReservation.InstanceID,
			capacityReservation.ReservationID,
			now,
		).
		Suffix("RETURNING stage_id").
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	if err := query.QueryRowContext(ctx).Scan(&capacityReservation.StageID); err != nil {
		return err
	}

	return nil
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

func (s CapacityReservationStore) Purge(ctx context.Context) error {
	panic("implement me")
}

const capacityReservationBase = `
SELECT
 stage_id
,pool_name
,instance_id
,reservation_id
FROM capacity_reservation
`

const capacityReservationFindByID = capacityReservationBase + `
WHERE stage_id = $1
`

const capacityReservationDelete = `
DELETE FROM capacity_reservation
WHERE stage_id = $1
`
