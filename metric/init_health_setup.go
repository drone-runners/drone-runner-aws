package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// initPhaseDurationBuckets is shared by the health-check and setup phase histograms: both are
// sub-phases of the same overall init path as WaitDurationCount/TotalVMInitDurationCount, sized
// against GetHealthCheckTimeout's largest bucket (HealthCheckColdstartTimeout) and typical
// lite-engine setup call durations, which are expected to fall well under the overall init span.
var initPhaseDurationBuckets = []float64{1, 2, 5, 10, 20, 30, 45, 60, 90, 120, 180, 240, 300, 420, 600, 900, 1200}

// initOverallDurationBuckets mirrors WaitDurationCount/TotalVMInitDurationCount's buckets so
// runner_vm_init_duration_seconds is directly comparable to those pre-existing metrics.
var initOverallDurationBuckets = []float64{0.5, 1, 3, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60, 70, 80, 90, 100, 110, 120, 180, 300, 600}

// VMHealthCheckAttemptsCount counts calls to the lite-engine health-check API from handleSetup,
// isolated from the surrounding provision/tag/setup work.
func VMHealthCheckAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_vm_health_check_attempts_total",
			Help: "Total number of VM health-check attempts during setup",
		},
		[]string{"pool_id", "zone", "vm_type", "source", "outcome", "reason"},
	)
}

// VMHealthCheckDurationCount observes how long the lite-engine health-check call took.
func VMHealthCheckDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_vm_health_check_duration_seconds",
			Help:    "Time taken by the lite-engine health-check call during setup",
			Buckets: initPhaseDurationBuckets,
		},
		[]string{"pool_id", "zone", "vm_type", "source", "outcome"},
	)
}

// VMSetupAttemptsCount counts calls to the lite-engine setup API from handleSetup, isolated from
// the health-check phase that precedes it.
func VMSetupAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_vm_setup_attempts_total",
			Help: "Total number of VM (lite-engine) setup attempts during setup",
		},
		[]string{"pool_id", "zone", "vm_type", "source", "outcome", "reason"},
	)
}

// VMSetupDurationCount observes how long the lite-engine setup call took.
func VMSetupDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_vm_setup_duration_seconds",
			Help:    "Time taken by the lite-engine setup call during setup",
			Buckets: initPhaseDurationBuckets,
		},
		[]string{"pool_id", "zone", "vm_type", "source", "outcome"},
	)
}

// VMInitAttemptsCount is the canonical "did an init attempt succeed" signal for the Runner
// availability SLO: it is incremented once per handleSetup call (i.e. once per pool-fallback
// attempt), on every exit path - including early returns - not just the terminal
// success/failure branches. Compare to TotalVMInitDurationCount, which is recorded once per
// top-level HandleSetup request and only on success.
func VMInitAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_vm_init_attempts_total",
			Help: "Total number of VM init attempts (one per pool-fallback attempt), covering every exit path out of setup",
		},
		[]string{"pool_id", "zone", "vm_type", "source", "outcome", "reason"},
	)
}

// VMInitDurationCount observes the overall duration of a single handleSetup call (provision
// through health-check and setup), on both success and failure.
func VMInitDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_vm_init_duration_seconds",
			Help:    "Overall duration of a single VM init attempt (one per pool-fallback attempt), including failures",
			Buckets: initOverallDurationBuckets,
		},
		[]string{"pool_id", "zone", "vm_type", "source", "outcome"},
	)
}

// RecordVMHealthCheckAttempt increments the health-check-attempts counter. Safe to call on a
// nil *Metrics (e.g. standalone commands that don't have metrics wired up).
func (m *Metrics) RecordVMHealthCheckAttempt(poolID, zone, vmType, source, outcome, reason string) {
	if m == nil {
		return
	}
	m.VMHealthCheckAttemptsCount.WithLabelValues(poolID, zone, vmType, source, outcome, reason).Inc()
}

// RecordVMHealthCheckDuration observes the duration of a health-check call. Negative durations
// (clock skew) are dropped rather than recorded.
func (m *Metrics) RecordVMHealthCheckDuration(poolID, zone, vmType, source, outcome string, duration time.Duration) {
	if m == nil || duration < 0 {
		return
	}
	m.VMHealthCheckDurationCount.WithLabelValues(poolID, zone, vmType, source, outcome).Observe(duration.Seconds())
}

// RecordVMSetupAttempt increments the setup-attempts counter.
func (m *Metrics) RecordVMSetupAttempt(poolID, zone, vmType, source, outcome, reason string) {
	if m == nil {
		return
	}
	m.VMSetupAttemptsCount.WithLabelValues(poolID, zone, vmType, source, outcome, reason).Inc()
}

// RecordVMSetupDuration observes the duration of a lite-engine setup call. Negative durations
// (clock skew) are dropped rather than recorded.
func (m *Metrics) RecordVMSetupDuration(poolID, zone, vmType, source, outcome string, duration time.Duration) {
	if m == nil || duration < 0 {
		return
	}
	m.VMSetupDurationCount.WithLabelValues(poolID, zone, vmType, source, outcome).Observe(duration.Seconds())
}

// RecordVMInitAttempt increments the overall init-attempts counter.
func (m *Metrics) RecordVMInitAttempt(poolID, zone, vmType, source, outcome, reason string) {
	if m == nil {
		return
	}
	m.VMInitAttemptsCount.WithLabelValues(poolID, zone, vmType, source, outcome, reason).Inc()
}

// RecordVMInitDuration observes the overall duration of a single handleSetup call. Negative
// durations (clock skew) are dropped rather than recorded.
func (m *Metrics) RecordVMInitDuration(poolID, zone, vmType, source, outcome string, duration time.Duration) {
	if m == nil || duration < 0 {
		return
	}
	m.VMInitDurationCount.WithLabelValues(poolID, zone, vmType, source, outcome).Observe(duration.Seconds())
}
