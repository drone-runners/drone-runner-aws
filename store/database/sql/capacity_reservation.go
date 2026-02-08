package sql

import (
	"context"
	"fmt"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/Masterminds/squirrel"
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

// FindAndClaim atomically finds capacity reservations matching the query params that are in
// one of the allowedStates, transitions them to newState, and returns the claimed capacities.
// Uses FOR UPDATE SKIP LOCKED to prevent race conditions.
// Query params can filter by StageID, PoolName, CreatedAtBefore, and Limit.
func (s CapacityReservationStore) FindAndClaim(
	ctx context.Context,
	params *types.CapacityReservationQueryParams,
	newState types.CapacityReservationState,
	allowedStates []types.CapacityReservationState,
) ([]*types.CapacityReservation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting transaction: %w", err)
	}
	defer tx.Rollback() //nolint

	// Build subquery (CTE) to find candidate capacity reservations
	subQuery := builder.Select("stage_id AS cr_stage_id").
		From("capacity_reservation")

	if params != nil {
		if params.StageID != "" {
			subQuery = subQuery.Where(squirrel.Eq{"stage_id": params.StageID})
		}

		if params.PoolName != "" {
			subQuery = subQuery.Where(squirrel.Eq{"pool_name": params.PoolName})
		}

		if params.CreatedAtBefore > 0 {
			subQuery = subQuery.Where(squirrel.Lt{"created_at": params.CreatedAtBefore})
		}

		if params.Limit > 0 {
			subQuery = subQuery.Limit(uint64(params.Limit))
		}
	}

	if len(allowedStates) > 0 {
		stateVals := make([]interface{}, len(allowedStates))
		for i, state := range allowedStates {
			stateVals[i] = state
		}
		subQuery = subQuery.Where(squirrel.Eq{"reservation_state": stateVals})
	}

	subQuery = subQuery.Suffix("FOR UPDATE SKIP LOCKED")

	// Convert subquery to SQL + args
	subSQL, subArgs, err := subQuery.ToSql()
	if err != nil {
		return nil, fmt.Errorf("failed to build CTE subquery: %w", err)
	}

	// Build final CTE UPDATE SQL
	//nolint: gosec
	finalSQL := fmt.Sprintf(`
WITH candidate AS (
	%s
)
UPDATE capacity_reservation
SET reservation_state = $%d
FROM candidate
WHERE capacity_reservation.stage_id = candidate.cr_stage_id
RETURNING capacity_reservation.stage_id, pool_name, instance_id, reservation_id, created_at, reservation_state
`, subSQL, len(subArgs)+1)

	// Append newState to args
	args := append(subArgs, newState)

	// Execute and scan results
	rows, err := tx.QueryContext(ctx, finalSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("error executing update: %w", err)
	}
	defer rows.Close()

	var capacities []*types.CapacityReservation
	for rows.Next() {
		capacity := new(types.CapacityReservation)
		scanErr := rows.Scan(
			&capacity.StageID,
			&capacity.PoolName,
			&capacity.InstanceID,
			&capacity.ReservationID,
			&capacity.CreatedAt,
			&capacity.ReservationState,
		)
		if scanErr != nil {
			return nil, fmt.Errorf("error scanning capacity reservation: %w", scanErr)
		}
		capacities = append(capacities, capacity)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating capacity reservations: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing transaction: %w", err)
	}

	return capacities, nil
}

const capacityReservationBase = `
SELECT
 stage_id
,pool_name
,instance_id
,reservation_id
,created_at
,reservation_state
FROM capacity_reservation
`
const capacityReservationInsert = `
INSERT INTO capacity_reservation (
 stage_id
,pool_name
,instance_id
,reservation_id
,created_at
,reservation_state
) values (
 :stage_id
,:pool_name
,:instance_id
,:reservation_id
,:created_at
,:reservation_state
) RETURNING stage_id
`

const capacityReservationFindByID = capacityReservationBase + `
WHERE stage_id = $1
`

const capacityReservationDelete = `
DELETE FROM capacity_reservation
WHERE stage_id = $1
`
