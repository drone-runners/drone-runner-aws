package sql

import (
	"context"
	"fmt"

	"github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ store.CapabilityHistoryStore = (*CapabilityHistoryStore)(nil)

const (
	capabilityHistoryTable = "capability_utilization_history"
	colRecordedAt          = "recorded_at"
	// capabilityBatchSize keeps a multi-row insert well under driver placeholder limits.
	capabilityBatchSize = 500
	// capabilityBackfillLockKey is the Postgres advisory lock key for the capability backfill.
	capabilityBackfillLockKey int64 = 0x63617062666c6c // "capbfll"
)

type CapabilityHistoryStore struct {
	db *sqlx.DB
}

func NewCapabilityHistoryStore(db *sqlx.DB) *CapabilityHistoryStore {
	return &CapabilityHistoryStore{db: db}
}

func (s *CapabilityHistoryStore) CreateBatch(ctx context.Context, records []*types.CapabilityUtilizationRecord) error {
	for start := 0; start < len(records); start += capabilityBatchSize {
		end := min(start+capabilityBatchSize, len(records))
		query := squirrel.Insert(capabilityHistoryTable).
			Columns("tier", "os", "arch", "variant_id", "tenant_id", "pool_name", "image_name", "in_use_instances", "recorded_at")
		for _, r := range records[start:end] {
			var image interface{}
			if r.ImageName != "" {
				image = r.ImageName
			}
			query = query.Values(
				r.Tier, r.OS, r.Arch,
				orDefaultID(r.VariantID),
				orDefaultID(r.TenantID),
				r.Pool, image, r.InUseInstances, r.RecordedAt,
			)
		}
		if _, err := query.RunWith(s.db).PlaceholderFormat(squirrel.Dollar).ExecContext(ctx); err != nil {
			return fmt.Errorf("error creating capability utilization records: %w", err)
		}
	}
	return nil
}

type capabilitySumWithRangeIdx struct {
	InUseInstances int   `db:"in_use_instances"`
	RecordedAt     int64 `db:"recorded_at"`
	RangeIdx       int   `db:"range_idx"`
}

func (s *CapabilityHistoryStore) GetHistoryBatch(
	ctx context.Context,
	capKey *types.CapabilityKey,
	ranges []store.TimeRange,
) ([][]types.CapabilityUtilizationRecord, error) {
	if len(ranges) == 0 {
		return nil, nil
	}
	key := *capKey
	key.VariantID = orDefaultID(key.VariantID)
	key.TenantID = orDefaultID(key.TenantID)

	var unionParts []string
	var args []interface{}
	argIdx := 1
	for i, r := range ranges {
		//nolint:mnd
		sub := fmt.Sprintf(
			"SELECT recorded_at, SUM(in_use_instances) AS in_use_instances, %d AS range_idx "+
				"FROM %s "+
				"WHERE tier = $%d AND os = $%d AND arch = $%d AND variant_id = $%d AND tenant_id = $%d "+
				"AND recorded_at >= $%d AND recorded_at <= $%d "+
				"GROUP BY recorded_at",
			i, capabilityHistoryTable, argIdx, argIdx+1, argIdx+2, argIdx+3, argIdx+4, argIdx+5, argIdx+6,
		)
		args = append(args, key.Tier, key.OS, key.Arch, key.VariantID, key.TenantID, r.StartTime, r.EndTime)
		argIdx += 7 //nolint:mnd
		unionParts = append(unionParts, fmt.Sprintf("SELECT * FROM (%s) AS r%d", sub, i))
	}
	fullQuery := joinWithUnionAll(unionParts) + " ORDER BY range_idx, recorded_at ASC"

	var rows []capabilitySumWithRangeIdx
	if err := s.db.SelectContext(ctx, &rows, fullQuery, args...); err != nil {
		return nil, fmt.Errorf("error fetching capability utilization history batch: %w", err)
	}

	result := make([][]types.CapabilityUtilizationRecord, len(ranges))
	for i := range result {
		result[i] = []types.CapabilityUtilizationRecord{}
	}
	for _, row := range rows {
		if row.RangeIdx < 0 || row.RangeIdx >= len(ranges) {
			continue
		}
		result[row.RangeIdx] = append(result[row.RangeIdx], types.CapabilityUtilizationRecord{
			CapabilityKey:  key,
			InUseInstances: row.InUseInstances,
			RecordedAt:     row.RecordedAt,
		})
	}
	return result, nil
}

func (s *CapabilityHistoryStore) DeleteOlderThan(ctx context.Context, timestamp int64) (int64, error) {
	result, err := squirrel.Delete(capabilityHistoryTable).
		Where(squirrel.Lt{colRecordedAt: timestamp}).
		RunWith(s.db).
		PlaceholderFormat(squirrel.Dollar).
		ExecContext(ctx)
	if err != nil {
		return 0, fmt.Errorf("error deleting old capability utilization records: %w", err)
	}
	return result.RowsAffected()
}

// TryLockBackfill uses a session-level Postgres advisory lock held on a dedicated connection.
// Other drivers (sqlite) are single-process, so the lock is a no-op there.
func (s *CapabilityHistoryStore) TryLockBackfill(ctx context.Context) (release func(), ok bool, err error) {
	if s.db.DriverName() != "postgres" {
		return func() {}, true, nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("error acquiring connection for backfill lock: %w", err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", capabilityBackfillLockKey).Scan(&ok); err != nil {
		conn.Close()
		return nil, false, fmt.Errorf("error taking backfill lock: %w", err)
	}
	if !ok {
		conn.Close()
		return nil, false, nil
	}
	return func() {
		// Use a fresh context so the unlock still runs if ctx was canceled.
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", capabilityBackfillLockKey)
		conn.Close()
	}, true, nil
}

func (s *CapabilityHistoryStore) BackfillPool(ctx context.Context, pool, tier, os, arch string, notAfter int64) (int64, error) {
	query := `INSERT INTO capability_utilization_history
	(tier, os, arch, variant_id, tenant_id, pool_name, image_name, in_use_instances, recorded_at)
SELECT CAST($1 AS VARCHAR(50)), CAST($2 AS VARCHAR(50)), CAST($3 AS VARCHAR(50)),
	COALESCE(NULLIF(h.variant_id, ''), 'default'),
	COALESCE(NULLIF(h.tenant_id, ''), 'default'),
	h.pool_name, NULLIF(h.image_name, ''), h.in_use_instances, h.recorded_at
FROM instance_utilization_history h
WHERE h.pool_name = $4
	AND h.recorded_at < COALESCE(
		(SELECT MIN(c.recorded_at) FROM capability_utilization_history c WHERE c.pool_name = $5),
		$6)`
	result, err := s.db.ExecContext(ctx, query, tier, os, arch, pool, pool, notAfter)
	if err != nil {
		return 0, fmt.Errorf("error backfilling capability history for pool %s: %w", pool, err)
	}
	return result.RowsAffected()
}

// orDefaultID maps an empty variant or tenant id to "default" (types.DefaultVariantID and
// types.DefaultTenantID share that value).
func orDefaultID(id string) string {
	if id == "" {
		return types.DefaultTenantID
	}
	return id
}
