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

// utilizationRecordWithRangeIdx is used internally to track which time range a record belongs to.
type utilizationRecordWithRangeIdx struct {
	types.UtilizationRecord
	RangeIdx int `db:"range_idx"`
}

func (s *UtilizationHistoryStore) GetUtilizationHistoryBatch(
	ctx context.Context,
	pool, variantID string,
	ranges []store.TimeRange,
) ([][]types.UtilizationRecord, error) {
	if len(ranges) == 0 {
		return nil, nil
	}

	// Build UNION ALL query to fetch all ranges in one round trip
	// Each sub-query adds a range_idx to identify which range the record belongs to
	var unionParts []string
	var allArgs []interface{}
	argIdx := 1

	for i, r := range ranges {
		var subQuery string
		if variantID != "" {
			subQuery = fmt.Sprintf(
				"SELECT id, pool_name, variant_id, in_use_instances, recorded_at, %d as range_idx "+
					"FROM instance_utilization_history "+
					"WHERE pool_name = $%d AND variant_id = $%d AND recorded_at >= $%d AND recorded_at <= $%d",
				i, argIdx, argIdx+1, argIdx+2, argIdx+3,
			)
			allArgs = append(allArgs, pool, variantID, r.StartTime, r.EndTime)
			argIdx += 4
		} else {
			subQuery = fmt.Sprintf(
				"SELECT id, pool_name, variant_id, in_use_instances, recorded_at, %d as range_idx "+
					"FROM instance_utilization_history "+
					"WHERE pool_name = $%d AND recorded_at >= $%d AND recorded_at <= $%d",
				i, argIdx, argIdx+1, argIdx+2,
			)
			allArgs = append(allArgs, pool, r.StartTime, r.EndTime)
			argIdx += 3
		}
		unionParts = append(unionParts, "("+subQuery+")")
	}

	fullQuery := fmt.Sprintf("%s ORDER BY range_idx, recorded_at ASC", joinWithUnionAll(unionParts))

	var records []utilizationRecordWithRangeIdx
	if err := s.db.SelectContext(ctx, &records, fullQuery, allArgs...); err != nil {
		return nil, fmt.Errorf("error fetching utilization history batch: %w", err)
	}

	// Group records by range index
	result := make([][]types.UtilizationRecord, len(ranges))
	for i := range result {
		result[i] = []types.UtilizationRecord{}
	}

	for _, rec := range records {
		if rec.RangeIdx >= 0 && rec.RangeIdx < len(ranges) {
			result[rec.RangeIdx] = append(result[rec.RangeIdx], rec.UtilizationRecord)
		}
	}

	return result, nil
}

func joinWithUnionAll(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += " UNION ALL " + parts[i]
	}
	return result
}
