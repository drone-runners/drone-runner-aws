package metric

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestVMHibernateAttemptsCount(t *testing.T) {
	c := VMHibernateAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "success", "none").Desc().String(),
		"runner_vm_hibernate_attempts_total")
}

func TestVMHibernateDurationCount(t *testing.T) {
	h := VMHibernateDurationCount()
	observer := h.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "success")
	assert.Contains(t, observer.(prometheus.Metric).Desc().String(), "runner_vm_hibernate_duration_seconds")
}

func TestVMResumeAttemptsCount(t *testing.T) {
	c := VMResumeAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "success", "none").Desc().String(),
		"runner_vm_resume_attempts_total")
}

func TestVMResumeDurationCount(t *testing.T) {
	h := VMResumeDurationCount()
	observer := h.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "success")
	assert.Contains(t, observer.(prometheus.Metric).Desc().String(), "runner_vm_resume_duration_seconds")
}

func TestVMResumeToReadyDurationCount(t *testing.T) {
	h := VMResumeToReadyDurationCount()
	observer := h.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "success")
	assert.Contains(t, observer.(prometheus.Metric).Desc().String(), "runner_vm_resume_to_ready_duration_seconds")
}

func TestMetrics_RecordHibernateResumeMethods_NilSafe(t *testing.T) {
	var m *Metrics
	assert.NotPanics(t, func() {
		m.RecordVMHibernateAttempt("pool1", "us-east1-a", "n1-standard-2", "success", "none")
		m.RecordVMHibernateDuration("pool1", "us-east1-a", "n1-standard-2", "success", 5*time.Second)
		m.RecordVMResumeAttempt("pool1", "us-east1-a", "n1-standard-2", "success", "none")
		m.RecordVMResumeDuration("pool1", "us-east1-a", "n1-standard-2", "success", 5*time.Second)
		m.RecordVMResumeToReadyDuration("pool1", "us-east1-a", "n1-standard-2", "success", time.Minute)
	})
}

func TestMetrics_RecordVMHibernateDuration_NegativeDurationDropped(t *testing.T) {
	m := &Metrics{VMHibernateDurationCount: VMHibernateDurationCount()}
	m.RecordVMHibernateDuration("pool1", "us-east1-a", "n1-standard-2", "success", -1)
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMHibernateDurationCount))

	m.RecordVMHibernateDuration("pool1", "us-east1-a", "n1-standard-2", "success", 5*time.Second)
	assert.Equal(t, 1, testutil.CollectAndCount(m.VMHibernateDurationCount))
}

func TestMetrics_RecordVMResumeDuration_NegativeDurationDropped(t *testing.T) {
	m := &Metrics{VMResumeDurationCount: VMResumeDurationCount()}
	m.RecordVMResumeDuration("pool1", "us-east1-a", "n1-standard-2", "success", -1)
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMResumeDurationCount))

	m.RecordVMResumeDuration("pool1", "us-east1-a", "n1-standard-2", "success", 5*time.Second)
	assert.Equal(t, 1, testutil.CollectAndCount(m.VMResumeDurationCount))
}

func TestMetrics_RecordVMResumeToReadyDuration_NegativeDurationDropped(t *testing.T) {
	m := &Metrics{VMResumeToReadyDurationCount: VMResumeToReadyDurationCount()}
	m.RecordVMResumeToReadyDuration("pool1", "us-east1-a", "n1-standard-2", "success", -1)
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMResumeToReadyDurationCount))

	m.RecordVMResumeToReadyDuration("pool1", "us-east1-a", "n1-standard-2", "success", time.Minute)
	assert.Equal(t, 1, testutil.CollectAndCount(m.VMResumeToReadyDurationCount))
}

func TestMetrics_RecordVMHibernateAttempt_Increments(t *testing.T) {
	m := &Metrics{VMHibernateAttemptsCount: VMHibernateAttemptsCount()}
	m.RecordVMHibernateAttempt("pool1", "us-east1-a", "n1-standard-2", "success", "none")
	m.RecordVMHibernateAttempt("pool1", "us-east1-a", "n1-standard-2", "success", "none")
	assert.InDelta(t, 2, testutil.ToFloat64(m.VMHibernateAttemptsCount.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", "success", "none")), 0.0001)
}

func TestMetrics_RecordVMResumeAttempt_Increments(t *testing.T) {
	m := &Metrics{VMResumeAttemptsCount: VMResumeAttemptsCount()}
	m.RecordVMResumeAttempt("pool1", "us-east1-a", "n1-standard-2", "error", "cloud_call_failed")
	assert.InDelta(t, 1, testutil.ToFloat64(m.VMResumeAttemptsCount.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", "error", "cloud_call_failed")), 0.0001)
}
