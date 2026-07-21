package sql

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ store.UtilizationHistoryStore = (*UtilizationHistoryStore)(nil)

const (
	colPoolName  = "pool_name"
	colImageName = "image_name"
)

type UtilizationHistoryStore struct {
	db *sqlx.DB
}

func NewUtilizationHistoryStore(db *sqlx.DB) *UtilizationHistoryStore {
	return &UtilizationHistoryStore{db: db}
}

func (s *UtilizationHistoryStore) Create(ctx context.Context, record *types.UtilizationRecord) error {
	tenantID := record.TenantID
	if tenantID == "" {
		tenantID = types.DefaultTenantID
	}
	query := squirrel.Insert("instance_utilization_history").
		Columns(
			"pool_name",
			"tenant_id",
			"variant_id",
			"image_name",
			"in_use_instances",
			"recorded_at",
		).
		Values(
			record.Pool,
			tenantID,
			record.VariantID,
			record.ImageName,
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
	pool, tenantID, variantID, imageName string,
	ranges []store.TimeRange,
) ([][]types.UtilizationRecord, error) {
	if len(ranges) == 0 {
		return nil, nil
	}
	if tenantID == "" {
		tenantID = types.DefaultTenantID
	}

	// Build UNION ALL query to fetch all ranges in one round trip
	// Each sub-query adds a range_idx to identify which range the record belongs to
	var unionParts []string
	var allArgs []interface{}
	argIdx := 1

	for i, r := range ranges {
		//nolint:mnd
		subQuery := fmt.Sprintf(
			"SELECT id, pool_name, tenant_id, variant_id, image_name, in_use_instances, recorded_at, %d as range_idx "+
				"FROM instance_utilization_history "+
				"WHERE pool_name = $%d AND tenant_id = $%d AND variant_id = $%d AND image_name = $%d AND recorded_at >= $%d AND recorded_at <= $%d",
			i, argIdx, argIdx+1, argIdx+2, argIdx+3, argIdx+4, argIdx+5,
		)
		allArgs = append(allArgs, pool, tenantID, variantID, imageName, r.StartTime, r.EndTime)
		argIdx += 6 //nolint:mnd
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

func (s *UtilizationHistoryStore) GetActiveImages(ctx context.Context, pool, tenantID, variantID string, since int64) ([]string, error) {
	if tenantID == "" {
		tenantID = types.DefaultTenantID
	}
	query := squirrel.Select("DISTINCT image_name").
		From("instance_utilization_history").
		Where(squirrel.Eq{colPoolName: pool}).
		Where(squirrel.Eq{colTenantID: tenantID}).
		Where(squirrel.Eq{colVariantID: variantID}).
		Where(squirrel.GtOrEq{"recorded_at": since}).
		Where(squirrel.Gt{"in_use_instances": 0}).
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	rows, err := query.QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("error fetching active images: %w", err)
	}
	defer rows.Close()

	var images []string
	for rows.Next() {
		var img string
		if err := rows.Scan(&img); err != nil {
			return nil, fmt.Errorf("error scanning image name: %w", err)
		}
		images = append(images, img)
	}

	return images, rows.Err()
}

func (s *UtilizationHistoryStore) HasRecentUsage(ctx context.Context, pool, tenantID, variantID, imageName string, since int64) (bool, error) {
	if tenantID == "" {
		tenantID = types.DefaultTenantID
	}
	query := squirrel.Select("1").
		From("instance_utilization_history").
		Where(squirrel.Eq{colPoolName: pool}).
		Where(squirrel.Eq{colTenantID: tenantID}).
		Where(squirrel.Eq{colVariantID: variantID}).
		Where(squirrel.Eq{colImageName: imageName}).
		Where(squirrel.GtOrEq{"recorded_at": since}).
		Where(squirrel.Gt{"in_use_instances": 0}).
		Limit(1).
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar)

	var exists int
	err := query.QueryRowContext(ctx).Scan(&exists)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("error checking recent usage: %w", err)
	}
	return true, nil
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
