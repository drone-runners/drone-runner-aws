package store

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/types"
)

type InstanceStore interface {
	Find(context.Context, string) (*types.Instance, error)
	List(context.Context, string, *types.QueryParams) ([]*types.Instance, error)
	Create(context.Context, *types.Instance) error
	Delete(context.Context, string) error
	Update(context.Context, *types.Instance) error
	Purge(context.Context) error
	DeleteAndReturn(ctx context.Context, query string, args ...any) ([]*types.Instance, error)
	FindAndClaim(ctx context.Context, params *types.QueryParams, newState types.InstanceState, allowedStates []types.InstanceState, updateStartTime bool) (*types.Instance, error)
	CountByPoolAndVariant(ctx context.Context, status types.InstanceState) (map[string]map[string]int, error)
}

type StageOwnerStore interface {
	Find(ctx context.Context, id string) (*types.StageOwner, error)
	Create(context.Context, *types.StageOwner) error
	Delete(context.Context, string) error
}

type OutboxStore interface {
	Create(context.Context, *types.OutboxJob) error
	// FindAndClaimPending finds and claims pending jobs.
	// If runnerName is non-empty, only jobs matching that runner_name are returned.
	// If runnerName is empty, only jobs with empty runner_name are returned (global jobs).
	FindAndClaimPending(ctx context.Context, runnerName string, jobTypes []types.OutboxJobType, limit int, retryInterval time.Duration) ([]*types.OutboxJob, error)
	UpdateStatus(context.Context, int64, types.OutboxJobStatus, string) error
	Delete(context.Context, int64) error
	DeleteOlderThan(context.Context, int64) (int64, error)
	// FindScaleJobForWindow checks if a scale job already exists for the given pool and window
	FindScaleJobForWindow(ctx context.Context, poolName string, windowStart int64) (*types.OutboxJob, error)
}

type CapacityReservationStore interface {
	Find(ctx context.Context, id string) (*types.CapacityReservation, error)
	Create(context.Context, *types.CapacityReservation) error
	Delete(context.Context, string) error
	// FindAndClaim atomically finds capacity reservations matching the query params that are in
	// one of the allowedStates, transitions them to newState, and returns the claimed capacities.
	// Uses FOR UPDATE SKIP LOCKED to prevent race conditions.
	// Query params can filter by StageID, PoolName, CreatedAtBefore, and Limit.
	FindAndClaim(ctx context.Context, params *types.CapacityReservationQueryParams, newState types.CapacityReservationState, allowedStates []types.CapacityReservationState) ([]*types.CapacityReservation, error)
}

// TimeRange represents a time window for querying utilization history.
type TimeRange struct {
	StartTime int64
	EndTime   int64
}

type UtilizationHistoryStore interface {
	Create(ctx context.Context, record *types.UtilizationRecord) error
	// GetUtilizationHistoryBatch fetches records for multiple time ranges in a single query.
	// Returns a slice of record slices, where each inner slice corresponds to the time range at the same index.
	GetUtilizationHistoryBatch(ctx context.Context, pool, variantID string, ranges []TimeRange) ([][]types.UtilizationRecord, error)
	DeleteOlderThan(ctx context.Context, timestamp int64) (int64, error)
}
