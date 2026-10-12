package jobs

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

type mockCapabilityHistoryStore struct {
	records   []types.CapabilityUtilizationRecord
	createErr error
	batches   int

	lockHeld   bool
	lockCalls  int
	released   int
	backfilled []string
	notAfter   []int64
	backfillN  int64
}

func (m *mockCapabilityHistoryStore) CreateBatch(_ context.Context, records []*types.CapabilityUtilizationRecord) error {
	m.batches++
	if m.createErr != nil {
		return m.createErr
	}
	for _, r := range records {
		m.records = append(m.records, *r)
	}
	return nil
}

func (m *mockCapabilityHistoryStore) GetHistoryBatch(context.Context, *types.CapabilityKey, []store.TimeRange) ([][]types.CapabilityUtilizationRecord, error) {
	return nil, nil
}

func (m *mockCapabilityHistoryStore) DeleteOlderThan(_ context.Context, ts int64) (int64, error) {
	var kept []types.CapabilityUtilizationRecord
	var deleted int64
	for i := range m.records {
		if m.records[i].RecordedAt < ts {
			deleted++
			continue
		}
		kept = append(kept, m.records[i])
	}
	m.records = kept
	return deleted, nil
}

func (m *mockCapabilityHistoryStore) TryLockBackfill(context.Context) (release func(), ok bool, err error) {
	m.lockCalls++
	if m.lockHeld {
		return nil, false, nil
	}
	return func() { m.released++ }, true, nil
}

func (m *mockCapabilityHistoryStore) BackfillPool(_ context.Context, pool, tier, os, arch string, notAfter int64) (int64, error) {
	m.backfilled = append(m.backfilled, pool+"|"+tier+"|"+os+"|"+arch)
	m.notAfter = append(m.notAfter, notAfter)
	return m.backfillN, nil
}

// fixedCountStore returns preset grouped counts, including tenant ids the generic mock ignores.
type fixedCountStore struct {
	*MockInstanceStore
	counts []types.InstanceCount
	err    error
}

func (s *fixedCountStore) CountGroupedInstances(context.Context, types.InstanceState) ([]types.InstanceCount, error) {
	return s.counts, s.err
}

const trackerPools = `
version: "1"
instances:
  - name: linux-amd64
    type: google
    platform: {os: linux, arch: amd64}
    spec: {image: img}
    variants:
      - {variant_id: variant_medium, pool: 1}
    tenants:
      - ids: [egress-acct]
  - name: linux-amd64-free
    type: google
    tier: free
    platform: {os: linux, arch: amd64}
    spec: {image: img}
  - name: linux-arm64-aws
    type: amazon
    platform: {os: linux, arch: arm64}
    spec:
      account: {region: us-east-1}
      ami: ami-1
  - name: macos
    type: nomad
    platform: {os: darwin, arch: arm64}
    spec: {}
`

func trackerCatalog(t *testing.T, drivers []string) *capability.Catalog {
	t.Helper()
	pf, err := config.Parse(strings.NewReader(trackerPools))
	if err != nil {
		t.Fatalf("parse pools: %v", err)
	}
	c, err := capability.NewCatalog(pf, drivers, nil)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return c
}

func TestUtilizationTracker_DualWritesCapabilityHistory(t *testing.T) {
	counts := []types.InstanceCount{
		{Pool: "linux-amd64", TenantID: "default", VariantID: "variant_medium", ImageName: "img-a", Count: 4},
		{Pool: "linux-amd64", TenantID: "egress-acct", VariantID: "", ImageName: "", Count: 2},
		{Pool: "linux-amd64-free", TenantID: "", VariantID: "default", Count: 3},
		{Pool: "linux-arm64-aws", TenantID: "default", VariantID: "default", Count: 1},
		{Pool: "macos", TenantID: "default", VariantID: "default", Count: 5},
		{Pool: "removed-pool", TenantID: "default", VariantID: "default", Count: 7},
	}
	instances := &fixedCountStore{MockInstanceStore: NewMockInstanceStore(), counts: counts}
	history := NewMockUtilizationHistoryStore()
	capStore := &mockCapabilityHistoryStore{}

	job := NewUtilizationTrackerJob(instances, history, time.Minute).
		WithCapabilityHistory(capStore, trackerCatalog(t, nil))
	if err := job.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(history.records) != len(counts) {
		t.Errorf("old table should keep every pool: got %d rows, want %d", len(history.records), len(counts))
	}
	if capStore.batches != 1 {
		t.Errorf("expected one batch insert, got %d", capStore.batches)
	}

	got := map[string]types.CapabilityUtilizationRecord{}
	for _, r := range capStore.records {
		got[r.Pool+"|"+r.TenantID+"|"+r.VariantID] = r
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 capability rows (macos and unknown pools skipped), got %d: %+v", len(got), capStore.records)
	}

	want := map[string]types.CapabilityUtilizationRecord{
		"linux-amd64|default|variant_medium": {
			CapabilityKey: types.CapabilityKey{Tier: "paid", OS: "linux", Arch: "amd64", VariantID: "variant_medium", TenantID: "default"},
			Pool:          "linux-amd64", ImageName: "img-a", InUseInstances: 4,
		},
		"linux-amd64|egress-acct|default": {
			CapabilityKey: types.CapabilityKey{Tier: "paid", OS: "linux", Arch: "amd64", VariantID: "default", TenantID: "egress-acct"},
			Pool:          "linux-amd64", InUseInstances: 2,
		},
		"linux-amd64-free|default|default": {
			CapabilityKey: types.CapabilityKey{Tier: "free", OS: "linux", Arch: "amd64", VariantID: "default", TenantID: "default"},
			Pool:          "linux-amd64-free", InUseInstances: 3,
		},
		"linux-arm64-aws|default|default": {
			CapabilityKey: types.CapabilityKey{Tier: "paid", OS: "linux", Arch: "arm64", VariantID: "default", TenantID: "default"},
			Pool:          "linux-arm64-aws", InUseInstances: 1,
		},
	}
	var recordedAt int64
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("missing capability row %s", k)
			continue
		}
		if recordedAt == 0 {
			recordedAt = g.RecordedAt
		}
		if g.RecordedAt != recordedAt || g.RecordedAt != history.records[0].RecordedAt {
			t.Errorf("%s: all rows of a snapshot must share recorded_at", k)
		}
		g.RecordedAt = 0
		if g != w {
			t.Errorf("%s:\n got  %+v\n want %+v", k, g, w)
		}
	}
}

func TestUtilizationTracker_CapabilityDriverFilter(t *testing.T) {
	counts := []types.InstanceCount{
		{Pool: "linux-amd64", TenantID: "default", VariantID: "default", Count: 4},
		{Pool: "linux-arm64-aws", TenantID: "default", VariantID: "default", Count: 1},
	}
	instances := &fixedCountStore{MockInstanceStore: NewMockInstanceStore(), counts: counts}
	capStore := &mockCapabilityHistoryStore{}

	job := NewUtilizationTrackerJob(instances, NewMockUtilizationHistoryStore(), time.Minute).
		WithCapabilityHistory(capStore, trackerCatalog(t, []string{"amazon"}))
	if err := job.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(capStore.records) != 1 || capStore.records[0].Pool != "linux-arm64-aws" {
		t.Errorf("expected only the amazon pool, got %+v", capStore.records)
	}
}

func TestUtilizationTracker_CapabilityErrorDoesNotFailOldWrites(t *testing.T) {
	counts := []types.InstanceCount{{Pool: "linux-amd64", TenantID: "default", VariantID: "default", Count: 4}}
	instances := &fixedCountStore{MockInstanceStore: NewMockInstanceStore(), counts: counts}
	history := NewMockUtilizationHistoryStore()
	capStore := &mockCapabilityHistoryStore{createErr: errors.New("db down")}

	job := NewUtilizationTrackerJob(instances, history, time.Minute).
		WithCapabilityHistory(capStore, trackerCatalog(t, nil))
	if err := job.Execute(context.Background()); err != nil {
		t.Fatalf("capability write errors must not fail the job: %v", err)
	}
	if len(history.records) != 1 {
		t.Errorf("old table write should still happen, got %d", len(history.records))
	}
}

func TestUtilizationTracker_NoCapabilityStoreOrNoRows(t *testing.T) {
	counts := []types.InstanceCount{{Pool: "macos", TenantID: "default", VariantID: "default", Count: 4}}
	instances := &fixedCountStore{MockInstanceStore: NewMockInstanceStore(), counts: counts}

	// Without WithCapabilityHistory the job behaves exactly as before.
	history := NewMockUtilizationHistoryStore()
	if err := NewUtilizationTrackerJob(instances, history, time.Minute).Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(history.records) != 1 {
		t.Errorf("expected 1 old-table row, got %d", len(history.records))
	}

	// Only unsupported pools in use: no empty batch insert.
	capStore := &mockCapabilityHistoryStore{}
	job := NewUtilizationTrackerJob(instances, NewMockUtilizationHistoryStore(), time.Minute).
		WithCapabilityHistory(capStore, trackerCatalog(t, nil))
	if err := job.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if capStore.batches != 0 {
		t.Errorf("expected no capability insert, got %d batches", capStore.batches)
	}
}

func TestUtilizationTracker_CountErrorPropagates(t *testing.T) {
	instances := &fixedCountStore{MockInstanceStore: NewMockInstanceStore(), err: errors.New("boom")}
	capStore := &mockCapabilityHistoryStore{}
	job := NewUtilizationTrackerJob(instances, NewMockUtilizationHistoryStore(), time.Minute).
		WithCapabilityHistory(capStore, trackerCatalog(t, nil))
	if err := job.Execute(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if capStore.batches != 0 {
		t.Error("nothing should be written when counting fails")
	}
}

func TestHistoryCleanup_AlsoCleansCapabilityHistory(t *testing.T) {
	now := time.Now().Unix()
	old := now - int64((10 * 24 * time.Hour).Seconds())

	history := NewMockUtilizationHistoryStore()
	history.records = []types.UtilizationRecord{{Pool: "p", RecordedAt: old}, {Pool: "p", RecordedAt: now}}
	capStore := &mockCapabilityHistoryStore{records: []types.CapabilityUtilizationRecord{
		{Pool: "p", RecordedAt: old}, {Pool: "p", RecordedAt: old + 1}, {Pool: "p", RecordedAt: now},
	}}

	job := NewHistoryCleanupJob(history, time.Hour, 7*24*time.Hour).WithCapabilityHistory(capStore)
	if err := job.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(history.records) != 1 {
		t.Errorf("old table: expected 1 remaining row, got %d", len(history.records))
	}
	if len(capStore.records) != 1 || capStore.records[0].RecordedAt != now {
		t.Errorf("capability table: expected only the recent row, got %+v", capStore.records)
	}
}

func TestBackfillCapabilityHistory_CopiesSupportedPools(t *testing.T) {
	capStore := &mockCapabilityHistoryStore{backfillN: 10}
	before := time.Now().Unix()

	total, err := BackfillCapabilityHistory(context.Background(), capStore, trackerCatalog(t, nil))
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if total != 30 {
		t.Errorf("total = %d, want 30", total)
	}
	want := []string{
		"linux-amd64|paid|linux|amd64",
		"linux-amd64-free|free|linux|amd64",
		"linux-arm64-aws|paid|linux|arm64",
	}
	if strings.Join(capStore.backfilled, ",") != strings.Join(want, ",") {
		t.Errorf("backfilled = %v, want %v", capStore.backfilled, want)
	}
	for _, n := range capStore.notAfter {
		if n != capStore.notAfter[0] || n < before {
			t.Errorf("all pools must share one cutoff taken at start, got %v", capStore.notAfter)
		}
	}
	if capStore.released != 1 {
		t.Errorf("lock should be released once, got %d", capStore.released)
	}
}

func TestBackfillCapabilityHistory_SkipsWhenLocked(t *testing.T) {
	capStore := &mockCapabilityHistoryStore{lockHeld: true}
	total, err := BackfillCapabilityHistory(context.Background(), capStore, trackerCatalog(t, nil))
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if total != 0 || len(capStore.backfilled) != 0 {
		t.Errorf("backfill should be skipped when another runner holds the lock")
	}
}

func TestScalerTrigger_OnlyConfiguredPredictionDrivers(t *testing.T) {
	pools := []ScalablePool{
		{Name: "gcp", Driver: "google"},
		{Name: "aws", Driver: "amazon"},
		{Name: "mac", Driver: "nomad"},
	}
	cases := []struct {
		name    string
		drivers []string
		want    []string
	}{
		{"default drivers", nil, []string{"aws", "gcp"}},
		{"aws runner", []string{"amazon"}, []string{"aws"}},
		{"case insensitive", []string{"Google"}, []string{"gcp"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outbox := NewMockOutboxStore()
			cfg := types.ScalerConfig{
				Enabled: true,
				// Lead time equal to the window keeps every moment inside the lead window.
				WindowDuration:    time.Hour,
				LeadTime:          time.Hour,
				PredictionDrivers: tc.drivers,
			}
			job := NewScalerTriggerJob(outbox, cfg, "runner", pools)
			if err := job.Execute(context.Background()); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			var got []string
			for _, j := range outbox.GetJobsByType(types.OutboxJobTypeScale) {
				got = append(got, j.PoolName)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("scale jobs for %v, want %v", got, tc.want)
			}
		})
	}
}
