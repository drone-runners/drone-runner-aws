package database

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/store/database/sql"
	"github.com/drone-runners/drone-runner-aws/types"
)

// sqlite has no instance_utilization_history table (the pool-based scaler is postgres-only),
// so tests create it to exercise the backfill.
const sqliteOldHistoryTable = `CREATE TABLE IF NOT EXISTS instance_utilization_history (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	pool_name TEXT NOT NULL,
	variant_id TEXT NOT NULL DEFAULT 'default',
	in_use_instances INTEGER NOT NULL DEFAULT 0,
	recorded_at INTEGER NOT NULL,
	image_name TEXT NOT NULL DEFAULT '',
	tenant_id TEXT NOT NULL DEFAULT 'default'
)`

// forEachCapabilityDB runs fn against sqlite and, when DLITE_TEST_POSTGRES_DSN is set, postgres.
func forEachCapabilityDB(t *testing.T, fn func(t *testing.T, db *sqlx.DB, s *sql.CapabilityHistoryStore)) {
	t.Run("sqlite", func(t *testing.T) {
		db, err := ConnectSQL("sqlite3", filepath.Join(t.TempDir(), "test.sqlite3"))
		if err != nil {
			t.Fatalf("connect sqlite: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if _, err := db.Exec(sqliteOldHistoryTable); err != nil {
			t.Fatalf("create old history table: %v", err)
		}
		fn(t, db, sql.NewCapabilityHistoryStore(db))
	})
	t.Run("postgres", func(t *testing.T) {
		dsn := os.Getenv("DLITE_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("DLITE_TEST_POSTGRES_DSN not set")
		}
		db, err := ConnectSQL("postgres", dsn)
		if err != nil {
			t.Fatalf("connect postgres: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if _, err := db.Exec("TRUNCATE capability_utilization_history, instance_utilization_history"); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		fn(t, db, sql.NewCapabilityHistoryStore(db))
	})
}

func capKey(tier, arch, variant, tenant string) types.CapabilityKey {
	return types.CapabilityKey{Tier: tier, OS: "linux", Arch: arch, VariantID: variant, TenantID: tenant}
}

func ptrKey(k types.CapabilityKey) *types.CapabilityKey { return &k } //nolint:gocritic

func capRecord(k *types.CapabilityKey, pool, image string, inUse int, at int64) *types.CapabilityUtilizationRecord {
	return &types.CapabilityUtilizationRecord{CapabilityKey: *k, Pool: pool, ImageName: image, InUseInstances: inUse, RecordedAt: at}
}

// Every runner pod records the same global counts; pods ticking in the same second must not
// double the group's sum.
func TestCapabilityHistory_DuplicateRowsFromPodsCountOnce(t *testing.T) {
	forEachCapabilityDB(t, func(t *testing.T, _ *sqlx.DB, s *sql.CapabilityHistoryStore) {
		ctx := context.Background()
		medium := capKey(types.TierPaid, "amd64", "variant_medium", "default")
		var records []*types.CapabilityUtilizationRecord
		for pod := 0; pod < 3; pod++ {
			records = append(records,
				capRecord(&medium, "linux-amd64", "img-a", 4, 100),
				capRecord(&medium, "linux-amd64", "img-b", 1, 100),
				capRecord(&medium, "linux-amd64-fallback", "", 3, 100),
			)
		}
		records = append(records, capRecord(&medium, "linux-amd64", "img-a", 4, 101))
		if err := s.CreateBatch(ctx, records); err != nil {
			t.Fatalf("CreateBatch: %v", err)
		}
		got, err := s.GetHistoryBatch(ctx, &medium, []store.TimeRange{{StartTime: 0, EndTime: 200}})
		if err != nil {
			t.Fatalf("GetHistoryBatch: %v", err)
		}
		assertSums(t, "deduped", got[0], []int64{100, 101}, []int{8, 4})
	})
}

func TestCapabilityHistory_SumsAcrossPoolsPerRange(t *testing.T) {
	forEachCapabilityDB(t, func(t *testing.T, _ *sqlx.DB, s *sql.CapabilityHistoryStore) {
		ctx := context.Background()
		medium := capKey(types.TierPaid, "amd64", "variant_medium", "default")
		records := []*types.CapabilityUtilizationRecord{
			capRecord(&medium, "linux-amd64", "img-a", 4, 100),
			capRecord(&medium, "linux-amd64", "img-b", 1, 100),
			capRecord(&medium, "linux-amd64-fallback", "", 3, 100),
			capRecord(&medium, "linux-amd64", "img-a", 2, 130),
			capRecord(&medium, "linux-amd64", "", 9, 500),
			// Other keys must not leak into the medium sums.
			capRecord(ptrKey(capKey(types.TierFree, "amd64", "variant_medium", "default")), "linux-amd64-free", "", 50, 100),
			capRecord(ptrKey(capKey(types.TierPaid, "arm64", "variant_medium", "default")), "linux-arm64", "", 50, 100),
			capRecord(ptrKey(capKey(types.TierPaid, "amd64", "variant_medium", "acctA")), "linux-amd64", "", 50, 100),
		}
		if err := s.CreateBatch(ctx, records); err != nil {
			t.Fatalf("CreateBatch: %v", err)
		}

		got, err := s.GetHistoryBatch(ctx, &medium, []store.TimeRange{
			{StartTime: 0, EndTime: 200},
			{StartTime: 400, EndTime: 600},
			{StartTime: 1000, EndTime: 2000},
		})
		if err != nil {
			t.Fatalf("GetHistoryBatch: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("expected 3 ranges, got %d", len(got))
		}
		assertSums(t, "range 0", got[0], []int64{100, 130}, []int{8, 2})
		assertSums(t, "range 1", got[1], []int64{500}, []int{9})
		if len(got[2]) != 0 {
			t.Errorf("range 2 should be empty, got %+v", got[2])
		}
		if got[0][0].CapabilityKey != medium || got[0][0].Pool != "" {
			t.Errorf("result should carry the key and no pool: %+v", got[0][0])
		}

		// Empty tenant/variant on lookup resolve to default.
		got, err = s.GetHistoryBatch(ctx, ptrKey(capKey(types.TierFree, "amd64", "variant_medium", "")), []store.TimeRange{{StartTime: 0, EndTime: 200}})
		if err != nil {
			t.Fatalf("GetHistoryBatch: %v", err)
		}
		assertSums(t, "free", got[0], []int64{100}, []int{50})

		none, err := s.GetHistoryBatch(ctx, &medium, nil)
		if err != nil || none != nil {
			t.Errorf("no ranges should return nil, got %v, %v", none, err)
		}
	})
}

func assertSums(t *testing.T, label string, recs []types.CapabilityUtilizationRecord, at []int64, sums []int) {
	t.Helper()
	got := make([][2]int64, 0, len(recs))
	for i := range recs {
		got = append(got, [2]int64{recs[i].RecordedAt, int64(recs[i].InUseInstances)})
	}
	want := make([][2]int64, 0, len(at))
	for i, ts := range at {
		want = append(want, [2]int64{ts, int64(sums[i])})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: (recorded_at, sum) = %v, want %v", label, got, want)
	}
}

func TestCapabilityHistory_DefaultsAndNullImage(t *testing.T) {
	forEachCapabilityDB(t, func(t *testing.T, db *sqlx.DB, s *sql.CapabilityHistoryStore) {
		ctx := context.Background()
		k := types.CapabilityKey{Tier: types.TierPaid, OS: "linux", Arch: "amd64"}
		if err := s.CreateBatch(ctx, []*types.CapabilityUtilizationRecord{capRecord(&k, "p", "", 1, 10), capRecord(&k, "p", "img", 1, 20)}); err != nil {
			t.Fatalf("CreateBatch: %v", err)
		}
		var rows []struct {
			VariantID string  `db:"variant_id"`
			TenantID  string  `db:"tenant_id"`
			Image     *string `db:"image_name"`
		}
		if err := db.Select(&rows, "SELECT variant_id, tenant_id, image_name FROM capability_utilization_history ORDER BY recorded_at"); err != nil {
			t.Fatalf("select: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("expected 2 rows, got %d", len(rows))
		}
		if rows[0].VariantID != "default" || rows[0].TenantID != "default" {
			t.Errorf("empty variant/tenant should be stored as default, got %+v", rows[0])
		}
		if rows[0].Image != nil {
			t.Errorf("empty image should be stored as NULL, got %q", *rows[0].Image)
		}
		if rows[1].Image == nil || *rows[1].Image != "img" {
			t.Errorf("image should be stored, got %v", rows[1].Image)
		}
	})
}

func TestCapabilityHistory_CreateBatchLarge(t *testing.T) {
	forEachCapabilityDB(t, func(t *testing.T, db *sqlx.DB, s *sql.CapabilityHistoryStore) {
		ctx := context.Background()
		k := capKey(types.TierPaid, "amd64", "default", "default")
		var records []*types.CapabilityUtilizationRecord
		for i := 0; i < 1234; i++ {
			records = append(records, capRecord(&k, "p", "", 1, int64(i)))
		}
		if err := s.CreateBatch(ctx, records); err != nil {
			t.Fatalf("CreateBatch: %v", err)
		}
		var n int
		if err := db.Get(&n, "SELECT COUNT(*) FROM capability_utilization_history"); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1234 {
			t.Errorf("expected 1234 rows across insert chunks, got %d", n)
		}
		if err := s.CreateBatch(ctx, nil); err != nil {
			t.Errorf("empty batch should be a no-op: %v", err)
		}
	})
}

func TestCapabilityHistory_DeleteOlderThan(t *testing.T) {
	forEachCapabilityDB(t, func(t *testing.T, _ *sqlx.DB, s *sql.CapabilityHistoryStore) {
		ctx := context.Background()
		k := capKey(types.TierPaid, "amd64", "default", "default")
		if err := s.CreateBatch(ctx, []*types.CapabilityUtilizationRecord{
			capRecord(&k, "p", "", 1, 10), capRecord(&k, "p", "", 1, 20), capRecord(&k, "p", "", 1, 30),
		}); err != nil {
			t.Fatalf("CreateBatch: %v", err)
		}
		n, err := s.DeleteOlderThan(ctx, 30)
		if err != nil {
			t.Fatalf("DeleteOlderThan: %v", err)
		}
		if n != 2 {
			t.Errorf("deleted %d, want 2", n)
		}
		got, _ := s.GetHistoryBatch(ctx, &k, []store.TimeRange{{StartTime: 0, EndTime: 100}})
		assertSums(t, "remaining", got[0], []int64{30}, []int{1})
	})
}

func insertOldHistory(t *testing.T, db *sqlx.DB, pool, tenant, variant, image string, inUse int, at int64) {
	t.Helper()
	_, err := db.Exec(
		"INSERT INTO instance_utilization_history (pool_name, tenant_id, variant_id, image_name, in_use_instances, recorded_at) VALUES ($1, $2, $3, $4, $5, $6)",
		pool, tenant, variant, image, inUse, at)
	if err != nil {
		t.Fatalf("insert old history: %v", err)
	}
}

func TestCapabilityHistory_BackfillPool(t *testing.T) {
	forEachCapabilityDB(t, func(t *testing.T, db *sqlx.DB, s *sql.CapabilityHistoryStore) {
		ctx := context.Background()
		insertOldHistory(t, db, "linux-amd64", "default", "variant_medium", "img", 4, 100)
		insertOldHistory(t, db, "linux-amd64", "acctA", "", "", 2, 100)
		insertOldHistory(t, db, "linux-amd64", "default", "variant_medium", "img", 5, 200)
		// Already dual-written by the new tracker: rows at/after the first capability row are skipped.
		insertOldHistory(t, db, "linux-amd64", "default", "variant_medium", "img", 6, 300)
		insertOldHistory(t, db, "other-pool", "default", "default", "", 7, 100)

		medium := capKey(types.TierPaid, "amd64", "variant_medium", "default")
		if err := s.CreateBatch(ctx, []*types.CapabilityUtilizationRecord{capRecord(&medium, "linux-amd64", "img", 6, 300)}); err != nil {
			t.Fatalf("CreateBatch: %v", err)
		}

		n, err := s.BackfillPool(ctx, "linux-amd64", types.TierPaid, "linux", "amd64", 1000)
		if err != nil {
			t.Fatalf("BackfillPool: %v", err)
		}
		if n != 3 {
			t.Errorf("inserted %d rows, want 3", n)
		}

		got, err := s.GetHistoryBatch(ctx, &medium, []store.TimeRange{{StartTime: 0, EndTime: 1000}})
		if err != nil {
			t.Fatalf("GetHistoryBatch: %v", err)
		}
		assertSums(t, "medium", got[0], []int64{100, 200, 300}, []int{4, 5, 6})

		tenant, _ := s.GetHistoryBatch(ctx, ptrKey(capKey(types.TierPaid, "amd64", "default", "acctA")), []store.TimeRange{{StartTime: 0, EndTime: 1000}})
		assertSums(t, "tenant with empty variant", tenant[0], []int64{100}, []int{2})

		var nullImages int
		if err = db.Get(&nullImages, "SELECT COUNT(*) FROM capability_utilization_history WHERE image_name IS NULL"); err != nil {
			t.Fatalf("count: %v", err)
		}
		if nullImages != 1 {
			t.Errorf("empty image should backfill as NULL, got %d NULL rows", nullImages)
		}

		// Re-running is a no-op.
		n, err = s.BackfillPool(ctx, "linux-amd64", types.TierPaid, "linux", "amd64", 1000)
		if err != nil {
			t.Fatalf("BackfillPool rerun: %v", err)
		}
		if n != 0 {
			t.Errorf("rerun inserted %d rows, want 0", n)
		}
	})
}

func TestCapabilityHistory_BackfillPoolWithoutCapabilityRows(t *testing.T) {
	forEachCapabilityDB(t, func(t *testing.T, db *sqlx.DB, s *sql.CapabilityHistoryStore) {
		ctx := context.Background()
		insertOldHistory(t, db, "linux-amd64-free", "default", "default", "", 1, 100)
		insertOldHistory(t, db, "linux-amd64-free", "default", "default", "", 2, 200)

		// With no capability rows yet, notAfter bounds the copy.
		n, err := s.BackfillPool(ctx, "linux-amd64-free", types.TierFree, "linux", "amd64", 150)
		if err != nil {
			t.Fatalf("BackfillPool: %v", err)
		}
		if n != 1 {
			t.Errorf("inserted %d rows, want 1", n)
		}
		got, _ := s.GetHistoryBatch(ctx, ptrKey(capKey(types.TierFree, "amd64", "default", "default")), []store.TimeRange{{StartTime: 0, EndTime: 1000}})
		assertSums(t, "free", got[0], []int64{100}, []int{1})

		// The copied row is now the oldest, so a later run with a larger cutoff copies nothing.
		n, err = s.BackfillPool(ctx, "linux-amd64-free", types.TierFree, "linux", "amd64", 1000)
		if err != nil {
			t.Fatalf("BackfillPool rerun: %v", err)
		}
		if n != 0 {
			t.Errorf("rerun inserted %d rows, want 0", n)
		}
	})
}

func TestCapabilityHistory_BackfillLock(t *testing.T) {
	forEachCapabilityDB(t, func(t *testing.T, db *sqlx.DB, s *sql.CapabilityHistoryStore) {
		ctx := context.Background()
		release, ok, err := s.TryLockBackfill(ctx)
		if err != nil || !ok {
			t.Fatalf("first lock: ok=%v err=%v", ok, err)
		}
		if db.DriverName() == Postgres {
			if _, ok2, lockErr := s.TryLockBackfill(ctx); lockErr != nil || ok2 {
				t.Errorf("second lock should fail while held: ok=%v err=%v", ok2, lockErr)
			}
		}
		release()
		release2, ok, err := s.TryLockBackfill(ctx)
		if err != nil || !ok {
			t.Fatalf("lock after release: ok=%v err=%v", ok, err)
		}
		release2()
	})
}

func TestCapabilityHistory_HasRecentUsage(t *testing.T) {
	forEachCapabilityDB(t, func(t *testing.T, _ *sqlx.DB, s *sql.CapabilityHistoryStore) {
		ctx := context.Background()
		medium := capKey(types.TierPaid, "amd64", "variant_medium", "default")
		idle := capKey(types.TierPaid, "amd64", "default", "default")
		if err := s.CreateBatch(ctx, []*types.CapabilityUtilizationRecord{
			capRecord(&medium, "linux-amd64-fallback", "", 2, 100),
			capRecord(&idle, "linux-amd64", "", 0, 500),
			capRecord(ptrKey(capKey(types.TierFree, "amd64", "variant_medium", "default")), "linux-amd64-free", "", 9, 500),
		}); err != nil {
			t.Fatalf("CreateBatch: %v", err)
		}
		cases := []struct {
			name  string
			key   types.CapabilityKey
			since int64
			want  bool
		}{
			{"usage in another pool of the group counts", medium, 50, true},
			{"usage before since is ignored", medium, 101, false},
			{"zero in-use rows are not usage", idle, 0, false},
			{"other tenant is ignored", capKey(types.TierPaid, "amd64", "variant_medium", "acctA"), 0, false},
			{"empty tenant resolves to default", capKey(types.TierPaid, "amd64", "variant_medium", ""), 0, true},
		}
		for _, tc := range cases {
			got, err := s.HasRecentUsage(ctx, &tc.key, tc.since)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got != tc.want {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			}
		}
	})
}
