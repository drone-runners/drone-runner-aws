package metric

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestVMHealthCheckAttemptsCount(t *testing.T) {
	c := VMHealthCheckAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", "none").Desc().String(),
		"runner_vm_health_check_attempts_total")
}

func TestVMHealthCheckDurationCount(t *testing.T) {
	h := VMHealthCheckDurationCount()
	observer := h.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success")
	assert.Contains(t, observer.(prometheus.Metric).Desc().String(), "runner_vm_health_check_duration_seconds")
}

func TestVMSetupAttemptsCount(t *testing.T) {
	c := VMSetupAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", "none").Desc().String(),
		"runner_vm_setup_attempts_total")
}

func TestVMSetupDurationCount(t *testing.T) {
	h := VMSetupDurationCount()
	observer := h.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success")
	assert.Contains(t, observer.(prometheus.Metric).Desc().String(), "runner_vm_setup_duration_seconds")
}

func TestVMInitAttemptsCount(t *testing.T) {
	c := VMInitAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", "none").Desc().String(),
		"runner_vm_init_attempts_total")
}

func TestVMInitDurationCount(t *testing.T) {
	h := VMInitDurationCount()
	observer := h.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success")
	assert.Contains(t, observer.(prometheus.Metric).Desc().String(), "runner_vm_init_duration_seconds")
}

func TestMetrics_RecordInitHealthSetupMethods_NilSafe(t *testing.T) {
	var m *Metrics
	assert.NotPanics(t, func() {
		m.RecordVMHealthCheckAttempt("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", "none")
		m.RecordVMHealthCheckDuration("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", 5*time.Second)
		m.RecordVMSetupAttempt("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", "none")
		m.RecordVMSetupDuration("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", 5*time.Second)
		m.RecordVMInitAttempt("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", "none")
		m.RecordVMInitDuration("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", 5*time.Second)
	})
}

func TestMetrics_RecordVMHealthCheckDuration_NegativeDurationDropped(t *testing.T) {
	m := &Metrics{VMHealthCheckDurationCount: VMHealthCheckDurationCount()}
	m.RecordVMHealthCheckDuration("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", -1)
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMHealthCheckDurationCount))

	m.RecordVMHealthCheckDuration("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", 5*time.Second)
	assert.Equal(t, 1, testutil.CollectAndCount(m.VMHealthCheckDurationCount))
}

func TestMetrics_RecordVMSetupDuration_NegativeDurationDropped(t *testing.T) {
	m := &Metrics{VMSetupDurationCount: VMSetupDurationCount()}
	m.RecordVMSetupDuration("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", -1)
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMSetupDurationCount))

	m.RecordVMSetupDuration("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", 5*time.Second)
	assert.Equal(t, 1, testutil.CollectAndCount(m.VMSetupDurationCount))
}

func TestMetrics_RecordVMInitDuration_NegativeDurationDropped(t *testing.T) {
	m := &Metrics{VMInitDurationCount: VMInitDurationCount()}
	m.RecordVMInitDuration("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", -1)
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMInitDurationCount))

	m.RecordVMInitDuration("pool1", "us-east1-a", "n1-standard-2", "hotpool", "success", 5*time.Second)
	assert.Equal(t, 1, testutil.CollectAndCount(m.VMInitDurationCount))
}

func TestMetrics_RecordVMInitAttempt_Increments(t *testing.T) {
	m := &Metrics{VMInitAttemptsCount: VMInitAttemptsCount()}
	m.RecordVMInitAttempt("pool1", "us-east1-a", "n1-standard-2", "ondemand", "error", "pool_exhausted")
	assert.InDelta(t, 1, testutil.ToFloat64(m.VMInitAttemptsCount.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", "ondemand", "error", "pool_exhausted")), 0.0001)
}

func TestMetrics_RecordVMHealthCheckAttempt_Increments(t *testing.T) {
	m := &Metrics{VMHealthCheckAttemptsCount: VMHealthCheckAttemptsCount()}
	m.RecordVMHealthCheckAttempt("pool1", "us-east1-a", "n1-standard-2", "hibernated", "error", "health_failed")
	assert.InDelta(t, 1, testutil.ToFloat64(m.VMHealthCheckAttemptsCount.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", "hibernated", "error", "health_failed")), 0.0001)
}

func TestMetrics_RecordVMSetupAttempt_Increments(t *testing.T) {
	m := &Metrics{VMSetupAttemptsCount: VMSetupAttemptsCount()}
	m.RecordVMSetupAttempt("pool1", "us-east1-a", "n1-standard-2", "reserved", "error", "setup_failed")
	assert.InDelta(t, 1, testutil.ToFloat64(m.VMSetupAttemptsCount.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", "reserved", "error", "setup_failed")), 0.0001)
}
