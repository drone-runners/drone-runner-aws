package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// vmHibernateResumeDurationBuckets covers a hibernate/resume operation, which is expected to be
// much faster than a cold VM creation (no full boot, just a cloud-level suspend/resume call plus
// retries) but can still take a couple of minutes under retry backoff.
var vmHibernateResumeDurationBuckets = []float64{1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300, 600}

// VMHibernateAttemptsCount counts logical hibernate operations - one increment per
// hibernateOrStopWithRetries call, after any internal retries have concluded.
func VMHibernateAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_vm_hibernate_attempts_total",
			Help: "Total number of VM hibernate attempts",
		},
		[]string{"pool_id", "zone", "vm_type", "outcome", "reason"},
	)
}

// VMHibernateDurationCount observes how long one logical hibernate operation took, including any
// internal retry backoff sleeps (there is no separate raw-attempt layer for this metric area).
func VMHibernateDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_vm_hibernate_duration_seconds",
			Help:    "Time taken to hibernate a VM, including internal retries",
			Buckets: vmHibernateResumeDurationBuckets,
		},
		[]string{"pool_id", "zone", "vm_type", "outcome"},
	)
}

// VMResumeAttemptsCount counts logical resume operations - the cloud-level start/resume call
// plus the subsequent state write that clears IsHibernated.
func VMResumeAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_vm_resume_attempts_total",
			Help: "Total number of VM resume attempts from hibernation",
		},
		[]string{"pool_id", "zone", "vm_type", "outcome", "reason"},
	)
}

// VMResumeDurationCount observes how long just the cloud-level start/resume call took - narrower
// than VMResumeAttemptsCount's scope, and narrower still than VMResumeToReadyDurationCount, which
// also includes the health-check and setup phases.
func VMResumeDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_vm_resume_duration_seconds",
			Help:    "Time taken by the cloud-level resume/start call to wake a hibernated VM",
			Buckets: vmHibernateResumeDurationBuckets,
		},
		[]string{"pool_id", "zone", "vm_type", "outcome"},
	)
}

// resumeToReadyBuckets is sized against the hibernated health-check timeout
// (RunnerConfig.HealthCheckHibernatedTimeout, see app/drivers/health.go's GetHealthCheckTimeout)
// since that's the expected upper bound for a successful resume-to-ready.
var resumeToReadyBuckets = []float64{5, 10, 20, 30, 45, 60, 90, 120, 180, 240, 300, 420, 600, 900, 1200}

// VMResumeToReadyDurationCount observes the customer-relevant latency from resume start to a
// resumed VM being confirmed healthy and set up for a stage. No `reason` label: it can only fail
// after a successful cloud resume (during health-check/setup), and that finer-grained taxonomy
// isn't wired to a label here yet - see the VMLifecycleReasonHealthFailed/SetupFailed comment in
// app/drivers/vm_lifecycle_metrics.go.
func VMResumeToReadyDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_vm_resume_to_ready_duration_seconds",
			Help:    "Time from resume start to a resumed VM being confirmed healthy and set up for a stage",
			Buckets: resumeToReadyBuckets,
		},
		[]string{"pool_id", "zone", "vm_type", "outcome"},
	)
}

// RecordVMHibernateAttempt increments the hibernate-attempts counter. Safe to call on a nil
// *Metrics (e.g. standalone commands that don't have metrics wired up).
func (m *Metrics) RecordVMHibernateAttempt(poolID, zone, vmType, outcome, reason string) {
	if m == nil {
		return
	}
	m.VMHibernateAttemptsCount.WithLabelValues(poolID, zone, vmType, outcome, reason).Inc()
}

// RecordVMHibernateDuration observes the duration of one logical hibernate operation. Negative
// durations (clock skew) are dropped rather than recorded.
func (m *Metrics) RecordVMHibernateDuration(poolID, zone, vmType, outcome string, duration time.Duration) {
	if m == nil || duration < 0 {
		return
	}
	m.VMHibernateDurationCount.WithLabelValues(poolID, zone, vmType, outcome).Observe(duration.Seconds())
}

// RecordVMResumeAttempt increments the resume-attempts counter.
func (m *Metrics) RecordVMResumeAttempt(poolID, zone, vmType, outcome, reason string) {
	if m == nil {
		return
	}
	m.VMResumeAttemptsCount.WithLabelValues(poolID, zone, vmType, outcome, reason).Inc()
}

// RecordVMResumeDuration observes the duration of the cloud-level start/resume call. Negative
// durations (clock skew) are dropped rather than recorded.
func (m *Metrics) RecordVMResumeDuration(poolID, zone, vmType, outcome string, duration time.Duration) {
	if m == nil || duration < 0 {
		return
	}
	m.VMResumeDurationCount.WithLabelValues(poolID, zone, vmType, outcome).Observe(duration.Seconds())
}

// RecordVMResumeToReadyDuration observes the resume-to-ready latency. Negative durations (clock
// skew) are dropped rather than recorded.
func (m *Metrics) RecordVMResumeToReadyDuration(poolID, zone, vmType, outcome string, duration time.Duration) {
	if m == nil || duration < 0 {
		return
	}
	m.VMResumeToReadyDurationCount.WithLabelValues(poolID, zone, vmType, outcome).Observe(duration.Seconds())
}
