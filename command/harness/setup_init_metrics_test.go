package harness

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestComputeInitSource(t *testing.T) {
	reserved := &types.CapacityReservation{PoolName: "pool1"}
	tests := []struct {
		name             string
		reservedCapacity *types.CapacityReservation
		warmed           bool
		hibernated       bool
		want             string
	}{
		{"on-demand default", nil, false, false, InitSourceOnDemand},
		{"hotpool claim", nil, true, false, InitSourceHotpool},
		{"reserved capacity", reserved, false, false, InitSourceReserved},
		{"reserved and hotpool - reserved wins", reserved, true, false, InitSourceReserved},
		{"hibernated wins over reserved", reserved, false, true, InitSourceHibernated},
		{"hibernated wins over hotpool", nil, true, true, InitSourceHibernated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeInitSource(tt.reservedCapacity, tt.warmed, tt.hibernated)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestClassifyPhaseOutcome_Success(t *testing.T) {
	outcome, reason := classifyPhaseOutcome(context.Background(), nil, InitReasonHealthFailed)
	assert.Equal(t, InitOutcomeSuccess, outcome)
	assert.Equal(t, InitReasonNone, reason)
}

func TestClassifyPhaseOutcome_UsesFallbackReason(t *testing.T) {
	outcome, reason := classifyPhaseOutcome(context.Background(), errors.New("lite-engine unreachable"), InitReasonHealthFailed)
	assert.Equal(t, InitOutcomeError, outcome)
	assert.Equal(t, InitReasonHealthFailed, reason)

	outcome, reason = classifyPhaseOutcome(context.Background(), errors.New("setup failed"), InitReasonSetupFailed)
	assert.Equal(t, InitOutcomeError, outcome)
	assert.Equal(t, InitReasonSetupFailed, reason)
}

func TestClassifyPhaseOutcome_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome, reason := classifyPhaseOutcome(ctx, errors.New("context canceled"), InitReasonSetupFailed)
	assert.Equal(t, InitOutcomeCancelled, outcome)
	assert.Equal(t, InitReasonNone, reason)
}

func TestClassifyPhaseOutcome_ContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)
	outcome, reason := classifyPhaseOutcome(ctx, errors.New("context deadline exceeded"), InitReasonHealthFailed)
	assert.Equal(t, InitOutcomeError, outcome)
	assert.Equal(t, InitReasonTimeout, reason)
}

func TestClassifyProvisionReason_PoolExhausted(t *testing.T) {
	reason := classifyProvisionReason(drivers.ErrorNoInstanceAvailable)
	assert.Equal(t, InitReasonPoolExhausted, reason)

	wrapped := fmt.Errorf("provision: failed to create instance: %w", drivers.ErrorNoInstanceAvailable)
	assert.Equal(t, InitReasonPoolExhausted, classifyProvisionReason(wrapped))
}

func TestClassifyProvisionReason_DefaultsToCloudCreateFailed(t *testing.T) {
	reason := classifyProvisionReason(errors.New("provision: failed to create instance: some driver error"))
	assert.Equal(t, InitReasonCloudCreateFailed, reason)
}

// TestClassifyCtxOutcome_NoError ensures a live context doesn't match either branch.
func TestClassifyCtxOutcome_NoError(t *testing.T) {
	_, _, matched := classifyCtxOutcome(context.Background())
	assert.False(t, matched)
}

// TestInitMetrics_NilMetricsSafe ensures the new Record* wrappers used by handleSetup never
// panic when metrics is nil (e.g. standalone commands that don't wire up metrics).
func TestInitMetrics_NilMetricsSafe(t *testing.T) {
	var m *metric.Metrics
	assert.NotPanics(t, func() {
		m.RecordVMHealthCheckAttempt("pool1", "us-east1-b", "n1-standard-2", InitSourceHotpool, InitOutcomeSuccess, InitReasonNone)
		m.RecordVMHealthCheckDuration("pool1", "us-east1-b", "n1-standard-2", InitSourceHotpool, InitOutcomeSuccess, time.Second)
		m.RecordVMSetupAttempt("pool1", "us-east1-b", "n1-standard-2", InitSourceHotpool, InitOutcomeSuccess, InitReasonNone)
		m.RecordVMSetupDuration("pool1", "us-east1-b", "n1-standard-2", InitSourceHotpool, InitOutcomeSuccess, time.Second)
		m.RecordVMInitAttempt("pool1", "us-east1-b", "n1-standard-2", InitSourceHotpool, InitOutcomeSuccess, InitReasonNone)
		m.RecordVMInitDuration("pool1", "us-east1-b", "n1-standard-2", InitSourceHotpool, InitOutcomeSuccess, time.Second)
	})
}
