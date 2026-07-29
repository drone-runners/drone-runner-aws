package metric

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupAttemptsCount_NameAndLabels(t *testing.T) {
	c := CleanupAttemptsCount()
	desc := c.WithLabelValues("vm", "pool1", "us-east1-b", "success", "").Desc()
	assert.Contains(t, desc.String(), "runner_cleanup_attempts_total")
}

func TestCleanupDurationCount_NameAndLabels(t *testing.T) {
	h := CleanupDurationCount()
	observer := h.WithLabelValues("vm", "pool1", "us-east1-b", "success")
	desc := observer.(prometheus.Metric).Desc()
	assert.Contains(t, desc.String(), "runner_cleanup_duration_seconds")
}

func TestRecordCleanupAttempt_NilSafe(t *testing.T) {
	var m *Metrics
	require.NotPanics(t, func() {
		m.RecordCleanupAttempt("vm", "pool1", "us-east1-b", "success", "")
	})
}

func TestRecordCleanupDuration_NilSafe(t *testing.T) {
	var m *Metrics
	require.NotPanics(t, func() {
		m.RecordCleanupDuration("vm", "pool1", "us-east1-b", "success", time.Second)
	})
}

func TestRecordCleanupDuration_DropsNegativeDuration(t *testing.T) {
	m := &Metrics{CleanupDurationCount: CleanupDurationCount()}
	m.RecordCleanupDuration("vm", "pool1", "us-east1-b", "success", -time.Second)
	assert.Equal(t, 0, testutil.CollectAndCount(m.CleanupDurationCount))
}

func TestRecordCleanupAttempt_Increments(t *testing.T) {
	m := &Metrics{CleanupAttemptsCount: CleanupAttemptsCount()}
	m.RecordCleanupAttempt("vm", "pool1", "us-east1-b", "success", "")
	m.RecordCleanupAttempt("vm", "pool1", "us-east1-b", "success", "")
	m.RecordCleanupAttempt("capacity_reservation", "pool1", "us-east1-b", "error", "cloud_call_failed")

	assert.InDelta(t, 2, testutil.ToFloat64(m.CleanupAttemptsCount.WithLabelValues(
		"vm", "pool1", "us-east1-b", "success", "")), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.CleanupAttemptsCount.WithLabelValues(
		"capacity_reservation", "pool1", "us-east1-b", "error", "cloud_call_failed")), 0.0001)
}

func TestRecordCleanupDuration_Observes(t *testing.T) {
	m := &Metrics{CleanupDurationCount: CleanupDurationCount()}
	m.RecordCleanupDuration("stage_owner", "pool1", "us-east1-b", "success", 250*time.Millisecond)
	assert.Equal(t, 1, testutil.CollectAndCount(m.CleanupDurationCount))
}
