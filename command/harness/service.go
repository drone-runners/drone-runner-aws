package harness

import (
	"context"

	"github.com/harness/lite-engine/api"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Type aliases for store interfaces - exported for use by other packages.
type (
	// StageOwnerStore is an alias for store.StageOwnerStore.
	StageOwnerStore = store.StageOwnerStore
	// CapacityReservationStore is an alias for store.CapacityReservationStore.
	CapacityReservationStore = store.CapacityReservationStore
)

// VMService provides a unified interface for all VM operations.
// It encapsulates the dependencies and configuration needed by handlers,
// eliminating the need to pass many parameters through function calls.
type VMService struct {
	poolManager              drivers.IManager
	stageOwnerStore          store.StageOwnerStore
	capacityReservationStore store.CapacityReservationStore
	metrics                  *metric.Metrics

	// Configuration
	globalVolumes    []string
	poolMapByAccount map[string]map[string]string
	runnerName       string
	enableMock       bool
	mockTimeoutSecs  int
	fallbackPoolIDs  []string
}

// VMServiceConfig holds configuration for creating a VMService.
type VMServiceConfig struct {
	PoolManager              drivers.IManager
	StageOwnerStore          store.StageOwnerStore
	CapacityReservationStore store.CapacityReservationStore
	Metrics                  *metric.Metrics
	GlobalVolumes            []string
	PoolMapByAccount         map[string]map[string]string
	RunnerName               string
	EnableMock               bool
	MockTimeoutSecs          int
	FallbackPoolIDs          []string
}

// NewVMService creates a new VMService with the provided configuration.
func NewVMService(cfg *VMServiceConfig) *VMService {
	return &VMService{
		poolManager:              cfg.PoolManager,
		stageOwnerStore:          cfg.StageOwnerStore,
		capacityReservationStore: cfg.CapacityReservationStore,
		metrics:                  cfg.Metrics,
		globalVolumes:            cfg.GlobalVolumes,
		poolMapByAccount:         cfg.PoolMapByAccount,
		runnerName:               cfg.RunnerName,
		enableMock:               cfg.EnableMock,
		mockTimeoutSecs:          cfg.MockTimeoutSecs,
		fallbackPoolIDs:          cfg.FallbackPoolIDs,
	}
}

// NewVMServiceFromRunner creates a VMService from a Runner instance.
func NewVMServiceFromRunner(r *Runner) *VMService {
	return &VMService{
		poolManager:              r.PoolManager,
		stageOwnerStore:          r.StageOwnerStore,
		capacityReservationStore: r.CapacityReservationStore,
		metrics:                  r.Metrics,
		globalVolumes:            r.Config.Runner.Volumes,
		poolMapByAccount:         r.Config.Dlite.PoolMapByAccount.Convert(),
		runnerName:               r.Config.Runner.Name,
		enableMock:               r.Config.LiteEngine.EnableMock,
		mockTimeoutSecs:          r.Config.LiteEngine.MockStepTimeoutSecs,
		fallbackPoolIDs:          r.Config.Settings.FallbackPoolIDs,
	}
}

// SetupResult contains the result of a VM setup operation.
type SetupResult struct {
	Response   *SetupVMResponse
	PoolDriver string
	Error      error
}

// Setup handles VM setup requests.
func (s *VMService) Setup(ctx context.Context, req *SetupVMRequest) (*SetupVMResponse, string, error) {
	return HandleSetup(
		ctx,
		req,
		s.stageOwnerStore,
		s.capacityReservationStore,
		s.globalVolumes,
		s.poolMapByAccount,
		s.runnerName,
		s.enableMock,
		s.mockTimeoutSecs,
		s.poolManager,
		s.metrics,
		s.fallbackPoolIDs,
	)
}

// Step handles VM step execution requests.
func (s *VMService) Step(ctx context.Context, req *ExecuteVMRequest, async bool) (*api.PollStepResponse, error) {
	return HandleStep(
		ctx,
		req,
		s.stageOwnerStore,
		s.globalVolumes,
		s.enableMock,
		s.mockTimeoutSecs,
		s.poolManager,
		s.metrics,
		async,
	)
}

// Destroy handles VM cleanup/destroy requests.
func (s *VMService) Destroy(ctx context.Context, req *VMCleanupRequest) error {
	return HandleDestroy(
		ctx,
		req,
		s.stageOwnerStore,
		s.capacityReservationStore,
		s.enableMock,
		s.mockTimeoutSecs,
		s.poolManager,
		s.metrics,
	)
}

// Suspend handles VM suspend requests.
func (s *VMService) Suspend(ctx context.Context, req *SuspendVMRequest) error {
	return HandleSuspend(
		ctx,
		req,
		s.enableMock,
		s.mockTimeoutSecs,
		s.poolManager,
	)
}

// ReserveCapacity handles capacity reservation requests.
func (s *VMService) ReserveCapacity(ctx context.Context, req *CapacityReservationRequest) (*types.CapacityReservation, error) {
	return HandleCapacityReservation(
		ctx,
		req,
		s.capacityReservationStore,
		s.poolMapByAccount,
		s.runnerName,
		s.poolManager,
		s.metrics,
		s.fallbackPoolIDs,
	)
}

// PoolExists checks if a pool exists.
func (s *VMService) PoolExists(poolName string) bool {
	return s.poolManager.Exists(poolName)
}

// FindStageOwner finds the stage owner for a given stage ID.
func (s *VMService) FindStageOwner(ctx context.Context, stageID string) (*types.StageOwner, error) {
	return s.stageOwnerStore.Find(ctx, stageID)
}

// PoolManager returns the underlying pool manager.
func (s *VMService) PoolManager() drivers.IManager {
	return s.poolManager
}

// Metrics returns the metrics instance.
func (s *VMService) Metrics() *metric.Metrics {
	return s.metrics
}

// IsDistributed returns whether the pool manager is in distributed mode.
func (s *VMService) IsDistributed() bool {
	return s.poolManager.IsDistributed()
}

// CleanPools cleans up pools.
func (s *VMService) CleanPools(ctx context.Context, destroyBusy, destroyFree bool) error {
	return s.poolManager.CleanPools(ctx, destroyBusy, destroyFree)
}

// VMServiceOption is a functional option for configuring VMService.
type VMServiceOption func(*VMService)

// WithPoolManager sets the pool manager.
func WithPoolManager(pm drivers.IManager) VMServiceOption {
	return func(s *VMService) {
		s.poolManager = pm
	}
}

// WithStageOwnerStore sets the stage owner store.
func WithStageOwnerStore(so store.StageOwnerStore) VMServiceOption {
	return func(s *VMService) {
		s.stageOwnerStore = so
	}
}

// WithCapacityReservationStore sets the capacity reservation store.
func WithCapacityReservationStore(crs store.CapacityReservationStore) VMServiceOption {
	return func(s *VMService) {
		s.capacityReservationStore = crs
	}
}

// WithMetrics sets the metrics.
func WithMetrics(m *metric.Metrics) VMServiceOption {
	return func(s *VMService) {
		s.metrics = m
	}
}

// WithGlobalVolumes sets the global volumes.
func WithGlobalVolumes(volumes []string) VMServiceOption {
	return func(s *VMService) {
		s.globalVolumes = volumes
	}
}

// WithRunnerName sets the runner name.
func WithRunnerName(name string) VMServiceOption {
	return func(s *VMService) {
		s.runnerName = name
	}
}

// WithMockConfig sets the mock configuration.
func WithMockConfig(enabled bool, timeoutSecs int) VMServiceOption {
	return func(s *VMService) {
		s.enableMock = enabled
		s.mockTimeoutSecs = timeoutSecs
	}
}

// WithFallbackPoolIDs sets the fallback pool IDs.
func WithFallbackPoolIDs(ids []string) VMServiceOption {
	return func(s *VMService) {
		s.fallbackPoolIDs = ids
	}
}

// NewVMServiceWithOptions creates a VMService using functional options.
func NewVMServiceWithOptions(opts ...VMServiceOption) *VMService {
	s := &VMService{}
	for _, opt := range opts {
		opt(s)
	}
	return s
}
