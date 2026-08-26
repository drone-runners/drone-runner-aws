package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/types"
)

// newTestCleanupMetrics builds a fresh, unregistered *metric.Metrics covering the metrics
// handleDestroy's instrumented paths can touch (see newTestInitMetrics in handle_setup_test.go
// for the same avoid-global-registry pattern).
func newTestCleanupMetrics() *metric.Metrics {
	return &metric.Metrics{
		CleanupAttemptsCount: metric.CleanupAttemptsCount(),
		CleanupDurationCount: metric.CleanupDurationCount(),
	}
}

// fakeStageOwnerStore is a minimal store.StageOwnerStore stub for handleDestroy tests.
type fakeStageOwnerStore struct {
	findResult  *types.StageOwner
	findErr     error
	findCalls   int
	deleteFunc  func(ctx context.Context, stageID string) error
	deleteCalls int
}

func (f *fakeStageOwnerStore) Find(context.Context, string) (*types.StageOwner, error) {
	f.findCalls++
	return f.findResult, f.findErr
}
func (f *fakeStageOwnerStore) Create(context.Context, *types.StageOwner) error { return nil }
func (f *fakeStageOwnerStore) Delete(ctx context.Context, stageID string) error {
	f.deleteCalls++
	if f.deleteFunc != nil {
		return f.deleteFunc(ctx, stageID)
	}
	return nil
}

// validCleanupRequest returns a VMCleanupRequest whose InstanceInfo satisfies
// ValidateStructForKeys(..., []string{"ID", "Zone", "PoolName", "StorageIdentifier"}), so
// handleDestroy uses common.BuildInstanceFromRequest instead of fetching from the DB via
// poolManager.GetInstanceByStageID. TLSCert/TLSKey are intentionally left empty: lehelper.GetClient
// (with enableMock=false) fails fast on tls.X509KeyPair([]byte{}, []byte{}) without any real
// network call, which is what's used below to simulate a lite-engine cleanup failure.
func validCleanupRequest() *VMCleanupRequest {
	return &VMCleanupRequest{
		PoolID:         "pool1",
		StageRuntimeID: "stage1",
		InstanceInfo: common.InstanceInfo{
			ID:                "inst-1",
			PoolName:          "pool1",
			Zone:              "us-east1-b",
			StorageIdentifier: "storage-1",
		},
	}
}

// TestHandleDestroy_LiteEngineCleanupFailure_StillRecordsVMCleanupSuccess covers the documented
// "Runner must continue infrastructure destruction even if that [lite-engine] call fails" rule:
// resource=vm's outcome must reflect only the cloud-level poolManager.Destroy result.
func TestHandleDestroy_LiteEngineCleanupFailure_StillRecordsVMCleanupSuccess(t *testing.T) {
	poolManager := &fakeIManager{
		destroyFunc: func(context.Context, string, string) error { return nil },
	}
	stageOwners := &fakeStageOwnerStore{}
	metrics := newTestCleanupMetrics()

	inst, err := handleDestroy(context.Background(), validCleanupRequest(), stageOwners, nil, /* crs */
		false /* enableMock */, 0, poolManager, metrics, 0, discardLogr())

	require.NoError(t, err)
	require.NotNil(t, inst)
	require.Zero(t, stageOwners.findCalls,
		"a complete legacy cleanup request must not add a stage-owner database read")
	assert.InDelta(t, 1, testutil.ToFloat64(metrics.CleanupAttemptsCount.WithLabelValues(
		CleanupResourceVM, "pool1", "us-east1-b", CleanupOutcomeSuccess, "")), 0.0001)
}

func TestHandleDestroy_AlreadyRemovedPCInstance_RecordsStageOwnerCleanup(t *testing.T) {
	poolManager := &fakeIManager{}
	stageOwners := &fakeStageOwnerStore{findResult: &types.StageOwner{
		StageID: "stage1", PoolName: "pool1", InstanceID: "inst-1",
	}}
	metrics := newTestCleanupMetrics()
	request := &VMCleanupRequest{StageRuntimeID: "stage1"}

	inst, err := handleDestroy(context.Background(), request, stageOwners, nil, /* crs */
		false /* enableMock */, 0, poolManager, metrics, 0, discardLogr())

	require.NoError(t, err)
	require.Nil(t, inst)
	require.Equal(t, 1, stageOwners.findCalls)
	require.Equal(t, 1, stageOwners.deleteCalls)
	assert.InDelta(t, 1, testutil.ToFloat64(metrics.CleanupAttemptsCount.WithLabelValues(
		CleanupResourceStageOwner, "pool1", "", CleanupOutcomeSuccess, "")), 0.0001)
}

// TestHandleDestroy_VMDestroyFailure_RecordsCleanupError covers the cloud-level destroy call
// itself failing, which does propagate as an error from handleDestroy (unlike the lite-engine
// and stage-owner cleanup steps).
func TestHandleDestroy_VMDestroyFailure_RecordsCleanupError(t *testing.T) {
	poolManager := &fakeIManager{
		destroyFunc: func(context.Context, string, string) error {
			return errors.New("cloud provider rejected delete")
		},
	}
	stageOwners := &fakeStageOwnerStore{}
	metrics := newTestCleanupMetrics()

	_, err := handleDestroy(context.Background(), validCleanupRequest(), stageOwners, nil, /* crs */
		false /* enableMock */, 0, poolManager, metrics, 0, discardLogr())

	require.Error(t, err)
	assert.InDelta(t, 1, testutil.ToFloat64(metrics.CleanupAttemptsCount.WithLabelValues(
		CleanupResourceVM, "pool1", "us-east1-b", CleanupOutcomeError, CleanupReasonCloudCallFailed)), 0.0001)
}

// TestHandleDestroy_StageOwnerDeleteFailure_RecordedButNonFatal covers the stage-owner record
// deletion failing: today this is swallowed (only logged as a warning) and never fails the
// overall destroy call - this test asserts that failure is now counted rather than fully silent,
// while confirming the non-fatal behavior is unchanged.
func TestHandleDestroy_StageOwnerDeleteFailure_RecordedButNonFatal(t *testing.T) {
	poolManager := &fakeIManager{
		destroyFunc: func(context.Context, string, string) error { return nil },
	}
	stageOwners := &fakeStageOwnerStore{
		deleteFunc: func(context.Context, string) error {
			return errors.New("db unavailable")
		},
	}
	metrics := newTestCleanupMetrics()

	inst, err := handleDestroy(context.Background(), validCleanupRequest(), stageOwners, nil, /* crs */
		false /* enableMock */, 0, poolManager, metrics, 0, discardLogr())

	require.NoError(t, err, "stage-owner delete failure must remain non-fatal to the overall destroy call")
	require.NotNil(t, inst)
	assert.InDelta(t, 1, testutil.ToFloat64(metrics.CleanupAttemptsCount.WithLabelValues(
		CleanupResourceStageOwner, "pool1", "us-east1-b", CleanupOutcomeError, CleanupReasonStoreError)), 0.0001)
}
