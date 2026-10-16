package predictor

import (
	"context"
	"errors"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// HistoryReader returns the utilization history a prediction is based on: for each time range,
// the in-use records ordered by recorded_at.
type HistoryReader interface {
	ReadHistory(ctx context.Context, input *PredictionInput, ranges []store.TimeRange) ([][]types.UtilizationRecord, error)
}

var errNoCapabilityKey = errors.New("predictor: capability history needs a capability key")

// PoolHistoryReader reads a pool's history for input.PoolName, TenantID, VariantID and ImageName.
type PoolHistoryReader struct {
	store store.UtilizationHistoryStore
}

// NewPoolHistoryReader returns a HistoryReader over the pool utilization history table.
func NewPoolHistoryReader(s store.UtilizationHistoryStore) *PoolHistoryReader {
	return &PoolHistoryReader{store: s}
}

// ReadHistory implements HistoryReader.
func (r *PoolHistoryReader) ReadHistory(ctx context.Context, input *PredictionInput, ranges []store.TimeRange) ([][]types.UtilizationRecord, error) {
	return r.store.GetUtilizationHistoryBatch(ctx, input.PoolName, input.TenantID, input.VariantID, input.ImageName, ranges)
}

// CapabilityHistoryReader reads input.Capability's history summed across the group's pools.
type CapabilityHistoryReader struct {
	store store.CapabilityHistoryStore
}

// NewCapabilityHistoryReader returns a HistoryReader over the capability utilization history table.
func NewCapabilityHistoryReader(s store.CapabilityHistoryStore) *CapabilityHistoryReader {
	return &CapabilityHistoryReader{store: s}
}

// ReadHistory implements HistoryReader.
func (r *CapabilityHistoryReader) ReadHistory(ctx context.Context, input *PredictionInput, ranges []store.TimeRange) ([][]types.UtilizationRecord, error) {
	if input.Capability == nil {
		return nil, errNoCapabilityKey
	}
	batches, err := r.store.GetHistoryBatch(ctx, input.Capability, ranges)
	if err != nil {
		return nil, err
	}
	out := make([][]types.UtilizationRecord, len(batches))
	for i, recs := range batches {
		out[i] = make([]types.UtilizationRecord, 0, len(recs))
		for j := range recs {
			out[i] = append(out[i], types.UtilizationRecord{
				TenantID:       recs[j].TenantID,
				VariantID:      recs[j].VariantID,
				InUseInstances: recs[j].InUseInstances,
				RecordedAt:     recs[j].RecordedAt,
			})
		}
	}
	return out, nil
}
