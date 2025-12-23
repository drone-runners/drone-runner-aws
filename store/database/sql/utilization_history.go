package sql

import (
	"context"
	"fmt"

	"github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ store.UtilizationHistoryStore = (*UtilizationHistoryStore)(nil)

type UtilizationHistoryStore struct {
	db *sqlx.DB
}

func NewUtilizationHistoryStore(db *sqlx.DB) *UtilizationHistoryStore {
	return &UtilizationHistoryStore{db: db}
}

func (s *UtilizationHistoryStore) Create(ctx context.Context, record *types.UtilizationRecord) error {
	query := squirrel.Insert("instance_utilization_history").
		Columns(
			"pool_name",
			"variant_id",
			"in_use_instances",
			"recorded_at",
		).
		Values(
			record.Pool,
			record.VariantID,
			record.InUseInstances,
			record.RecordedAt,
		).
		Suffix("RETURNING id").
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	if err := query.QueryRowContext(ctx).Scan(&record.ID); err != nil {
		return fmt.Errorf("error creating utilization record: %w", err)
	}
	return nil
}

func (s *UtilizationHistoryStore) GetUtilizationHistory(ctx context.Context, pool, variantID string, startTime, endTime int64) ([]types.UtilizationRecord, error) {
	query := squirrel.Select(
		"id",
		"pool_name",
		"variant_id",
		"in_use_instances",
		"recorded_at",
	).
		From("instance_utilization_history").
		Where(squirrel.Eq{"pool_name": pool}).
		Where(squirrel.GtOrEq{"recorded_at": startTime}).
		Where(squirrel.LtOrEq{"recorded_at": endTime}).
		OrderBy("recorded_at ASC").
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	if variantID != "" {
		query = query.Where(squirrel.Eq{"variant_id": variantID})
	}

	sql, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("error building query: %w", err)
	}

	var records []types.UtilizationRecord
	if err := s.db.SelectContext(ctx, &records, sql, args...); err != nil {
		return nil, fmt.Errorf("error fetching utilization history: %w", err)
	}

	return records, nil
}

func (s *UtilizationHistoryStore) DeleteOlderThan(ctx context.Context, timestamp int64) (int64, error) {
	query := squirrel.Delete("instance_utilization_history").
		Where(squirrel.Lt{"recorded_at": timestamp}).
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	result, err := query.ExecContext(ctx)
	if err != nil {
		return 0, fmt.Errorf("error deleting old utilization records: %w", err)
	}

	return result.RowsAffected()
}
