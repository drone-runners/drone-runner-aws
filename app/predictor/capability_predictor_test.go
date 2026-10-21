package predictor

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

var _ store.CapabilityHistoryStore = (*mockCapabilityStore)(nil)

// mockCapabilityStore sums per-pool rows by recorded_at, like the SQL store.
type mockCapabilityStore struct {
	records []types.CapabilityUtilizationRecord
	err     error
}

func (m *mockCapabilityStore) CreateBatch(_ context.Context, records []*types.CapabilityUtilizationRecord) error {
	for _, r := range records {
		m.records = append(m.records, *r)
	}
	return nil
}

func (m *mockCapabilityStore) GetHistoryBatch(_ context.Context, key *types.CapabilityKey, ranges []store.TimeRange) ([][]types.CapabilityUtilizationRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make([][]types.CapabilityUtilizationRecord, len(ranges))
	for i, r := range ranges {
		sums := map[int64]int{}
		for j := range m.records {
			rec := &m.records[j]
			if rec.CapabilityKey == *key && rec.RecordedAt >= r.StartTime && rec.RecordedAt <= r.EndTime {
				sums[rec.RecordedAt] += rec.InUseInstances
			}
		}
		for at, n := range sums {
			out[i] = append(out[i], types.CapabilityUtilizationRecord{CapabilityKey: *key, InUseInstances: n, RecordedAt: at})
		}
		sort.Slice(out[i], func(a, b int) bool { return out[i][a].RecordedAt < out[i][b].RecordedAt })
	}
	return out, nil
}

func (m *mockCapabilityStore) HasRecentUsage(context.Context, *types.CapabilityKey, int64) (bool, error) {
	return false, nil
}

func (m *mockCapabilityStore) DeleteOlderThan(context.Context, int64) (int64, error) { return 0, nil }

func (m *mockCapabilityStore) TryLockBackfill(context.Context) (release func(), ok bool, err error) {
	return func() {}, true, nil
}

func (m *mockCapabilityStore) BackfillPool(context.Context, string, string, string, string, int64) (int64, error) {
	return 0, nil
}

// TestPredict_CapabilityMatchesPoolOnSummedHistory checks that predicting for a capability
// group gives exactly the pool-algorithm result computed on the group's summed history, so the
// EMA/weekend-decay behavior validated per pool carries over unchanged.
func TestPredict_CapabilityMatchesPoolOnSummedHistory(t *testing.T) {
	key := types.CapabilityKey{Tier: types.TierPaid, OS: "linux", Arch: "amd64", VariantID: "variant_medium", TenantID: "default"}

	targets := map[string]time.Time{
		"weekday": time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC), // Wednesday
		"monday":  time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),  // EMA reaches back over a weekend
		"weekend": time.Date(2026, 10, 17, 14, 0, 0, 0, time.UTC), // Saturday
	}
	for name, ref := range targets {
		t.Run(name, func(t *testing.T) {
			primary := GenerateMockHistoryDataWithSpikes("linux-amd64", key.VariantID, ref)
			fallback := GenerateMockHistoryDataGradualIncrease("linux-amd64-fallback", key.VariantID, ref)

			summed := map[int64]int{}
			capStore := &mockCapabilityStore{}
			for _, recs := range [][]types.UtilizationRecord{primary, fallback} {
				for _, r := range recs {
					summed[r.RecordedAt] += r.InUseInstances
					capStore.records = append(capStore.records, types.CapabilityUtilizationRecord{
						CapabilityKey: key, Pool: r.Pool, InUseInstances: r.InUseInstances, RecordedAt: r.RecordedAt,
					})
				}
			}
			poolStore := &MockHistoryStore{}
			for at, n := range summed {
				poolStore.records = append(poolStore.records, types.UtilizationRecord{
					Pool: "summed", TenantID: "default", VariantID: key.VariantID, InUseInstances: n, RecordedAt: at,
				})
			}

			start := ref.Unix()
			end := ref.Add(30 * time.Minute).Unix()
			cfg := DefaultPredictorConfig()

			want, err := NewEMAWeekendDecayPredictor(poolStore, cfg).Predict(context.Background(), &PredictionInput{
				PoolName: "summed", TenantID: "default", VariantID: key.VariantID, StartTimestamp: start, EndTimestamp: end,
			})
			if err != nil {
				t.Fatalf("pool predict: %v", err)
			}
			got, err := NewEMAWeekendDecayPredictorFromHistory(NewCapabilityHistoryReader(capStore), cfg).
				Predict(context.Background(), &PredictionInput{
					Capability: &key, TenantID: key.TenantID, VariantID: key.VariantID, StartTimestamp: start, EndTimestamp: end,
				})
			if err != nil {
				t.Fatalf("capability predict: %v", err)
			}
			if got.PredictedInstances != want.PredictedInstances {
				t.Errorf("capability prediction = %d, pool prediction on summed history = %d", got.PredictedInstances, want.PredictedInstances)
			}
			if want.PredictedInstances == 0 {
				t.Error("fixture should produce a non-zero prediction")
			}
		})
	}
}

func TestPredict_CapabilityIgnoresOtherKeys(t *testing.T) {
	ref := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	key := types.CapabilityKey{Tier: types.TierPaid, OS: "linux", Arch: "amd64", VariantID: "default", TenantID: "default"}
	free := key
	free.Tier = types.TierFree

	capStore := &mockCapabilityStore{}
	for _, r := range GenerateMockHistoryData("linux-amd64-free", "default", ref) {
		capStore.records = append(capStore.records, types.CapabilityUtilizationRecord{CapabilityKey: free, Pool: r.Pool, InUseInstances: r.InUseInstances, RecordedAt: r.RecordedAt})
	}
	got, err := NewEMAWeekendDecayPredictorFromHistory(NewCapabilityHistoryReader(capStore), DefaultPredictorConfig()).
		Predict(context.Background(), &PredictionInput{Capability: &key, StartTimestamp: ref.Unix(), EndTimestamp: ref.Add(30 * time.Minute).Unix()})
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	if got.PredictedInstances != 0 {
		t.Errorf("paid prediction must not use free-tier history, got %d", got.PredictedInstances)
	}
}

func TestPredict_CapabilityErrors(t *testing.T) {
	key := types.CapabilityKey{Tier: types.TierPaid, OS: "linux", Arch: "amd64", VariantID: "default", TenantID: "default"}
	ref := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	input := &PredictionInput{Capability: &key, StartTimestamp: ref.Unix(), EndTimestamp: ref.Add(30 * time.Minute).Unix()}

	noKey := *input
	noKey.Capability = nil
	pred := NewEMAWeekendDecayPredictorFromHistory(NewCapabilityHistoryReader(&mockCapabilityStore{}), DefaultPredictorConfig())
	if _, err := pred.Predict(context.Background(), &noKey); !errors.Is(err, errNoCapabilityKey) {
		t.Errorf("expected errNoCapabilityKey, got %v", err)
	}

	boom := errors.New("db down")
	_, err := NewEMAWeekendDecayPredictorFromHistory(NewCapabilityHistoryReader(&mockCapabilityStore{err: boom}), DefaultPredictorConfig()).
		Predict(context.Background(), input)
	if !errors.Is(err, boom) {
		t.Errorf("expected store error, got %v", err)
	}
}
