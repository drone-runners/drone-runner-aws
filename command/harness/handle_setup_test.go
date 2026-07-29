package harness

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// newTestInitMetrics builds a fresh, unregistered *metric.Metrics covering every metric
// handleSetup's instrumented paths can touch. It intentionally avoids metric.RegisterMetrics(),
// which calls prometheus.MustRegister against the global registry and would panic if invoked
// more than once across the test suite (see app/drivers/google/metrics_test.go's newTestMetrics
// for the same pattern).
func newTestInitMetrics() *metric.Metrics {
	return &metric.Metrics{
		VMInitAttemptsCount:          metric.VMInitAttemptsCount(),
		VMInitDurationCount:          metric.VMInitDurationCount(),
		VMHealthCheckAttemptsCount:   metric.VMHealthCheckAttemptsCount(),
		VMHealthCheckDurationCount:   metric.VMHealthCheckDurationCount(),
		VMSetupAttemptsCount:         metric.VMSetupAttemptsCount(),
		VMSetupDurationCount:         metric.VMSetupDurationCount(),
		VMResumeToReadyDurationCount: metric.VMResumeToReadyDurationCount(),
	}
}

// fakeIManager is a minimal, fully-stubbed drivers.IManager for exercising handleSetup's
// early-return paths without a real Manager/DistributedManager or lite-engine transport. Only
// the methods handleSetup actually calls have configurable Func fields; everything else is a
// harmless no-op/zero-value stub.
type fakeIManager struct {
	existsFunc           func(name string) bool
	provisionFunc        func(ctx context.Context) (*types.Instance, *types.CapacityReservation, bool, string, error)
	startInstanceFunc    func(ctx context.Context, poolName, instanceID string) (*types.Instance, error)
	updateFunc           func(ctx context.Context, instance *types.Instance) error
	destroyFunc          func(ctx context.Context, poolName, instanceID string) error
	destroyCapacityFunc  func(ctx context.Context, capacity *types.CapacityReservation) error
	getInstanceByStageID func(ctx context.Context, poolName, stageID string) (*types.Instance, error)
}

//nolint:gocritic // unnamed results mirror drivers.InstanceProvisioner's Provision signature
func (f *fakeIManager) Provision(ctx context.Context, _, _, _ string, _ *types.ProvisionParams, _ *types.QueryParams,
	_ *types.GitspaceAgentConfig, _ *types.StorageConfig, _ *common.InstanceInfo, _ int64, _ bool,
	_ *types.CapacityReservation, _ bool) (*types.Instance, *types.CapacityReservation, bool, string, error) {
	if f.provisionFunc != nil {
		return f.provisionFunc(ctx)
	}
	return nil, nil, false, "", errors.New("provisionFunc not set")
}

func (f *fakeIManager) Destroy(ctx context.Context, poolName, instanceID string, _ *types.Instance, _ *storage.CleanupType) error {
	if f.destroyFunc != nil {
		return f.destroyFunc(ctx, poolName, instanceID)
	}
	return nil
}

func (f *fakeIManager) DestroyCapacity(ctx context.Context, capacity *types.CapacityReservation) error {
	if f.destroyCapacityFunc != nil {
		return f.destroyCapacityFunc(ctx, capacity)
	}
	return nil
}

func (f *fakeIManager) Find(context.Context, string) (*types.Instance, error) { return nil, nil }

//nolint:gocritic // 6 results mirror drivers.InstanceQuerier's List signature
func (f *fakeIManager) List(context.Context, string, *types.QueryParams) (busy, free, hibernating, provisioning, terminating []*types.Instance, err error) {
	return nil, nil, nil, nil, nil, nil
}

func (f *fakeIManager) GetInstanceByStageID(ctx context.Context, poolName, stageID string) (*types.Instance, error) {
	if f.getInstanceByStageID != nil {
		return f.getInstanceByStageID(ctx, poolName, stageID)
	}
	return nil, nil
}
func (f *fakeIManager) Exists(name string) bool {
	if f.existsFunc != nil {
		return f.existsFunc(name)
	}
	return true
}
func (f *fakeIManager) Update(ctx context.Context, instance *types.Instance) error {
	if f.updateFunc != nil {
		return f.updateFunc(ctx, instance)
	}
	return nil
}

func (f *fakeIManager) Add(...drivers.Pool) error                    { return nil }
func (f *fakeIManager) BuildPools(context.Context) error             { return nil }
func (f *fakeIManager) CleanPools(context.Context, bool, bool) error { return nil }

//nolint:gocritic // unnamed results mirror drivers.PoolManager's Inspect signature
func (f *fakeIManager) Inspect(string) (types.Platform, string, string) {
	return types.Platform{}, "", "mock"
}
func (f *fakeIManager) GetPoolSpec(string) (interface{}, error) { return nil, nil }
func (f *fakeIManager) IsEgressPool(string, string) bool        { return false }

func (f *fakeIManager) StartInstance(ctx context.Context, poolName, instanceID string, _ *common.InstanceInfo) (*types.Instance, error) {
	if f.startInstanceFunc != nil {
		return f.startInstanceFunc(ctx, poolName, instanceID)
	}
	return nil, errors.New("startInstanceFunc not set")
}
func (f *fakeIManager) Suspend(context.Context, string, *types.Instance) error { return nil }
func (f *fakeIManager) SetInstanceTags(context.Context, string, *types.Instance, map[string]string) error {
	return nil
}
func (f *fakeIManager) InstanceLogs(context.Context, string, string) (string, error) { return "", nil }

func (f *fakeIManager) PingDriver(context.Context) error { return nil }
func (f *fakeIManager) GetHealthCheckTimeout(string, types.DriverType, bool, bool) time.Duration {
	return time.Second
}
func (f *fakeIManager) GetHealthCheckConnectivityDuration() time.Duration { return 0 }

func (f *fakeIManager) GetInstanceStore() store.InstanceStore                       { return nil }
func (f *fakeIManager) GetStageOwnerStore() store.StageOwnerStore                   { return nil }
func (f *fakeIManager) GetCapacityReservationStore() store.CapacityReservationStore { return nil }

func (f *fakeIManager) GetTLSServerName() string            { return "test-server" }
func (f *fakeIManager) IsDistributed() bool                 { return false }
func (f *fakeIManager) IsHosted() bool                      { return false }
func (f *fakeIManager) GetRunnerConfig() types.RunnerConfig { return types.RunnerConfig{} }
func (f *fakeIManager) GetSetupTimeout() time.Duration      { return time.Minute }
func (f *fakeIManager) GetStartStepTimeout() time.Duration  { return time.Minute }

func (f *fakeIManager) StartInstancePurger(context.Context, time.Duration, time.Duration, time.Duration, time.Duration) error {
	return nil
}
func (f *fakeIManager) SetMetrics(drivers.MetricsRecorder) {}

var _ drivers.IManager = (*fakeIManager)(nil)

func newTestSetupRequest() *SetupVMRequest {
	return &SetupVMRequest{}
}

func discardLogr() *logrus.Entry {
	log := logrus.New()
	log.SetOutput(io.Discard)
	return logrus.NewEntry(log)
}

// TestHandleSetup_PoolNotFound_RecordsInitAttempt covers the very first early return: the pool
// isn't registered with the pool manager at all.
func TestHandleSetup_PoolNotFound_RecordsInitAttempt(t *testing.T) {
	poolManager := &fakeIManager{existsFunc: func(string) bool { return false }}
	metrics := newTestInitMetrics()

	_, _, _, _, err := handleSetup(context.Background(), discardLogr(), discardLogr(), newTestSetupRequest(), //nolint:dogsled
		"runner", false, 0, poolManager, "pool1", "owner1", nil, metrics)

	require.Error(t, err)
	assert.InDelta(t, 1, testutil.ToFloat64(metrics.VMInitAttemptsCount.WithLabelValues(
		"pool1", "", "", InitSourceOnDemand, InitOutcomeError, InitReasonUnknown)), 0.0001)
}

// TestHandleSetup_PoolExhausted_RecordsPoolExhaustedReason covers Provision() failing with the
// non-distributed Manager's ErrorNoInstanceAvailable sentinel.
func TestHandleSetup_PoolExhausted_RecordsPoolExhaustedReason(t *testing.T) {
	poolManager := &fakeIManager{
		provisionFunc: func(context.Context) (*types.Instance, *types.CapacityReservation, bool, string, error) {
			return nil, nil, false, "", drivers.ErrorNoInstanceAvailable
		},
	}
	metrics := newTestInitMetrics()

	_, _, _, _, err := handleSetup(context.Background(), discardLogr(), discardLogr(), newTestSetupRequest(), //nolint:dogsled
		"runner", false, 0, poolManager, "pool1", "owner1", nil, metrics)

	require.Error(t, err)
	assert.InDelta(t, 1, testutil.ToFloat64(metrics.VMInitAttemptsCount.WithLabelValues(
		"pool1", "", "", InitSourceOnDemand, InitOutcomeError, InitReasonPoolExhausted)), 0.0001)
}

// TestHandleSetup_CloudCreateFailed_RecordsCloudCreateFailedReason covers Provision() failing
// for any other reason (e.g. the driver's Create() call failing), which is bucketed as
// cloud_create_failed since Provision is treated as a single opaque call from handleSetup.
func TestHandleSetup_CloudCreateFailed_RecordsCloudCreateFailedReason(t *testing.T) {
	poolManager := &fakeIManager{
		provisionFunc: func(context.Context) (*types.Instance, *types.CapacityReservation, bool, string, error) {
			return nil, nil, false, "", errors.New("provision: failed to create instance: stockout")
		},
	}
	metrics := newTestInitMetrics()

	_, _, _, _, err := handleSetup(context.Background(), discardLogr(), discardLogr(), newTestSetupRequest(), //nolint:dogsled
		"runner", false, 0, poolManager, "pool1", "owner1", nil, metrics)

	require.Error(t, err)
	assert.InDelta(t, 1, testutil.ToFloat64(metrics.VMInitAttemptsCount.WithLabelValues(
		"pool1", "", "", InitSourceOnDemand, InitOutcomeError, InitReasonCloudCreateFailed)), 0.0001)
}

// TestHandleSetup_ResumeFailed_RecordsResumeFailedReason covers a hibernated instance whose
// StartInstance (cloud-level resume) call fails.
func TestHandleSetup_ResumeFailed_RecordsResumeFailedReason(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Zone: "us-east1-b", Size: "n1-standard-2", IsHibernated: true}
	poolManager := &fakeIManager{
		provisionFunc: func(context.Context) (*types.Instance, *types.CapacityReservation, bool, string, error) {
			return inst, nil, true, "default", nil
		},
		startInstanceFunc: func(context.Context, string, string) (*types.Instance, error) {
			return nil, errors.New("cloud provider rejected resume")
		},
	}
	metrics := newTestInitMetrics()

	_, _, _, _, err := handleSetup(context.Background(), discardLogr(), discardLogr(), newTestSetupRequest(), //nolint:dogsled
		"runner", false, 0, poolManager, "pool1", "owner1", nil, metrics)

	require.Error(t, err)
	// Note: cleanUpInstanceFn runs in a fire-and-forget goroutine (`go cleanUpInstanceFn(...)`),
	// so its side effects (destroyCalled) aren't deterministically observable here; this test
	// focuses on the synchronously-recorded init-attempt metric instead.
	assert.InDelta(t, 1, testutil.ToFloat64(metrics.VMInitAttemptsCount.WithLabelValues(
		"pool1", "us-east1-b", "n1-standard-2", InitSourceHibernated, InitOutcomeError, InitReasonResumeFailed)), 0.0001)
}

// TestHandleSetup_StateFailed_RecordsStateFailedReason covers the instance-tagging DB write
// (poolManager.Update) failing after a successful provision.
func TestHandleSetup_StateFailed_RecordsStateFailedReason(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Zone: "us-east1-b", Size: "n1-standard-2"}
	poolManager := &fakeIManager{
		provisionFunc: func(context.Context) (*types.Instance, *types.CapacityReservation, bool, string, error) {
			return inst, nil, true, "default", nil
		},
		updateFunc: func(context.Context, *types.Instance) error {
			return errors.New("db unavailable")
		},
	}
	metrics := newTestInitMetrics()

	_, _, _, _, err := handleSetup(context.Background(), discardLogr(), discardLogr(), newTestSetupRequest(), //nolint:dogsled
		"runner", false, 0, poolManager, "pool1", "owner1", nil, metrics)

	require.Error(t, err)
	assert.InDelta(t, 1, testutil.ToFloat64(metrics.VMInitAttemptsCount.WithLabelValues(
		"pool1", "us-east1-b", "n1-standard-2", InitSourceHotpool, InitOutcomeError, InitReasonStateFailed)), 0.0001)
}
