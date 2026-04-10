package database

import (
	"context"
	"time"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// retryInstanceStore wraps an InstanceStore with retry logic for transient DB errors.
type retryInstanceStore struct {
	inner store.InstanceStore
}

// NewRetryInstanceStore wraps the given InstanceStore with transient-error retry logic.
func NewRetryInstanceStore(inner store.InstanceStore) store.InstanceStore {
	return &retryInstanceStore{inner: inner}
}

func (s *retryInstanceStore) Find(ctx context.Context, id string) (*types.Instance, error) {
	return Retry(func() (*types.Instance, error) {
		return s.inner.Find(ctx, id)
	})
}

func (s *retryInstanceStore) List(ctx context.Context, pool string, params *types.QueryParams) ([]*types.Instance, error) {
	return Retry(func() ([]*types.Instance, error) {
		return s.inner.List(ctx, pool, params)
	})
}

func (s *retryInstanceStore) Create(ctx context.Context, instance *types.Instance) error {
	return RetryVoid(func() error {
		return s.inner.Create(ctx, instance)
	})
}

func (s *retryInstanceStore) Delete(ctx context.Context, id string) error {
	return RetryVoid(func() error {
		return s.inner.Delete(ctx, id)
	})
}

func (s *retryInstanceStore) Update(ctx context.Context, instance *types.Instance) error {
	return RetryVoid(func() error {
		return s.inner.Update(ctx, instance)
	})
}

func (s *retryInstanceStore) Purge(ctx context.Context) error {
	return RetryVoid(func() error {
		return s.inner.Purge(ctx)
	})
}

func (s *retryInstanceStore) DeleteAndReturn(ctx context.Context, query string, args ...any) ([]*types.Instance, error) {
	return Retry(func() ([]*types.Instance, error) {
		return s.inner.DeleteAndReturn(ctx, query, args...)
	})
}

func (s *retryInstanceStore) FindAndClaim(
	ctx context.Context, params *types.QueryParams, newState types.InstanceState,
	allowedStates []types.InstanceState, updateStartTime bool,
) (*types.Instance, error) {
	return Retry(func() (*types.Instance, error) {
		return s.inner.FindAndClaim(ctx, params, newState, allowedStates, updateStartTime)
	})
}

func (s *retryInstanceStore) CountGroupedInstances(ctx context.Context, status types.InstanceState) ([]types.InstanceCount, error) {
	return Retry(func() ([]types.InstanceCount, error) {
		return s.inner.CountGroupedInstances(ctx, status)
	})
}

// retryStageOwnerStore wraps a StageOwnerStore with retry logic.
type retryStageOwnerStore struct {
	inner store.StageOwnerStore
}

// NewRetryStageOwnerStore wraps the given StageOwnerStore with transient-error retry logic.
func NewRetryStageOwnerStore(inner store.StageOwnerStore) store.StageOwnerStore {
	return &retryStageOwnerStore{inner: inner}
}

func (s *retryStageOwnerStore) Find(ctx context.Context, id string) (*types.StageOwner, error) {
	return Retry(func() (*types.StageOwner, error) {
		return s.inner.Find(ctx, id)
	})
}

func (s *retryStageOwnerStore) Create(ctx context.Context, stageOwner *types.StageOwner) error {
	return RetryVoid(func() error {
		return s.inner.Create(ctx, stageOwner)
	})
}

func (s *retryStageOwnerStore) Delete(ctx context.Context, id string) error {
	return RetryVoid(func() error {
		return s.inner.Delete(ctx, id)
	})
}

// retryOutboxStore wraps an OutboxStore with retry logic.
type retryOutboxStore struct {
	inner store.OutboxStore
}

// NewRetryOutboxStore wraps the given OutboxStore with transient-error retry logic.
func NewRetryOutboxStore(inner store.OutboxStore) store.OutboxStore {
	return &retryOutboxStore{inner: inner}
}

func (s *retryOutboxStore) Create(ctx context.Context, job *types.OutboxJob) error {
	return RetryVoid(func() error {
		return s.inner.Create(ctx, job)
	})
}

func (s *retryOutboxStore) FindAndClaimPending(ctx context.Context, runnerName string, jobTypes []types.OutboxJobType, limit int, retryInterval time.Duration) ([]*types.OutboxJob, error) {
	return Retry(func() ([]*types.OutboxJob, error) {
		return s.inner.FindAndClaimPending(ctx, runnerName, jobTypes, limit, retryInterval)
	})
}

func (s *retryOutboxStore) UpdateStatus(ctx context.Context, id int64, status types.OutboxJobStatus, errMsg string) error {
	return RetryVoid(func() error {
		return s.inner.UpdateStatus(ctx, id, status, errMsg)
	})
}

func (s *retryOutboxStore) Delete(ctx context.Context, id int64) error {
	return RetryVoid(func() error {
		return s.inner.Delete(ctx, id)
	})
}

func (s *retryOutboxStore) DeleteOlderThan(ctx context.Context, timestamp int64) (int64, error) {
	return Retry(func() (int64, error) {
		return s.inner.DeleteOlderThan(ctx, timestamp)
	})
}

func (s *retryOutboxStore) FindScaleJobForWindow(ctx context.Context, poolName string, windowStart int64) (*types.OutboxJob, error) {
	return Retry(func() (*types.OutboxJob, error) {
		return s.inner.FindScaleJobForWindow(ctx, poolName, windowStart)
	})
}

// retryCapacityReservationStore wraps a CapacityReservationStore with retry logic.
type retryCapacityReservationStore struct {
	inner store.CapacityReservationStore
}

// NewRetryCapacityReservationStore wraps the given CapacityReservationStore with transient-error retry logic.
func NewRetryCapacityReservationStore(inner store.CapacityReservationStore) store.CapacityReservationStore {
	return &retryCapacityReservationStore{inner: inner}
}

func (s *retryCapacityReservationStore) Find(ctx context.Context, id string) (*types.CapacityReservation, error) {
	return Retry(func() (*types.CapacityReservation, error) {
		return s.inner.Find(ctx, id)
	})
}

func (s *retryCapacityReservationStore) Create(ctx context.Context, cr *types.CapacityReservation) error {
	return RetryVoid(func() error {
		return s.inner.Create(ctx, cr)
	})
}

func (s *retryCapacityReservationStore) Delete(ctx context.Context, id string) error {
	return RetryVoid(func() error {
		return s.inner.Delete(ctx, id)
	})
}

func (s *retryCapacityReservationStore) List(ctx context.Context, params *types.CapacityReservationQueryParams, states []types.CapacityReservationState) ([]*types.CapacityReservation, error) {
	return Retry(func() ([]*types.CapacityReservation, error) {
		return s.inner.List(ctx, params, states)
	})
}

func (s *retryCapacityReservationStore) FindAndClaim(
	ctx context.Context, params *types.CapacityReservationQueryParams,
	newState types.CapacityReservationState, allowedStates []types.CapacityReservationState,
) ([]*types.CapacityReservation, error) {
	return Retry(func() ([]*types.CapacityReservation, error) {
		return s.inner.FindAndClaim(ctx, params, newState, allowedStates)
	})
}

// retryUtilizationHistoryStore wraps a UtilizationHistoryStore with retry logic.
type retryUtilizationHistoryStore struct {
	inner store.UtilizationHistoryStore
}

// NewRetryUtilizationHistoryStore wraps the given UtilizationHistoryStore with transient-error retry logic.
func NewRetryUtilizationHistoryStore(inner store.UtilizationHistoryStore) store.UtilizationHistoryStore {
	return &retryUtilizationHistoryStore{inner: inner}
}

func (s *retryUtilizationHistoryStore) Create(ctx context.Context, record *types.UtilizationRecord) error {
	return RetryVoid(func() error {
		return s.inner.Create(ctx, record)
	})
}

func (s *retryUtilizationHistoryStore) GetUtilizationHistoryBatch(ctx context.Context, pool, variantID, imageName string, ranges []store.TimeRange) ([][]types.UtilizationRecord, error) {
	return Retry(func() ([][]types.UtilizationRecord, error) {
		return s.inner.GetUtilizationHistoryBatch(ctx, pool, variantID, imageName, ranges)
	})
}

func (s *retryUtilizationHistoryStore) GetActiveImages(ctx context.Context, pool, variantID string, since int64) ([]string, error) {
	return Retry(func() ([]string, error) {
		return s.inner.GetActiveImages(ctx, pool, variantID, since)
	})
}

func (s *retryUtilizationHistoryStore) DeleteOlderThan(ctx context.Context, timestamp int64) (int64, error) {
	return Retry(func() (int64, error) {
		return s.inner.DeleteOlderThan(ctx, timestamp)
	})
}
