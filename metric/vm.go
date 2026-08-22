package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// vmCreationDurationBuckets mirrors the buckets used by WaitDurationCount/TotalVMInitDurationCount:
// VM creation is a sub-phase of the same overall init path, so the same bucket boundaries make
// panels comparable across the three metrics.
var vmCreationDurationBuckets = []float64{0.5, 1, 3, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60, 70, 80, 90, 100, 110, 120, 180, 300, 600}

// VMCreationAttemptsCount counts calls to a driver's Create() method - the point where Runner
// asks the cloud provider for a new VM, separate from any subsequent health/setup work.
func VMCreationAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_vm_creation_attempts_total",
			Help: "Total number of VM creation attempts via a driver's Create() call",
		},
		[]string{"pool_id", "zone", "vm_type", "source", "outcome", "reason"},
	)
}

// VMCreationDurationCount observes how long a single driver Create() call took, from
// immediately before invocation to immediately after it returns (success or error). This is
// narrower than TotalVMInitDurationCount, which also includes post-create health/setup time.
func VMCreationDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_vm_creation_duration_seconds",
			Help:    "Time taken by a single driver Create() call to provision a VM",
			Buckets: vmCreationDurationBuckets,
		},
		[]string{"pool_id", "zone", "vm_type", "source", "outcome"},
	)
}

// VMUsageDurationCount observes how long a VM was in the inuse state before being terminated.
// Buckets mirror HotpoolStateDuration's long-tail range since a build can run for hours.
func VMUsageDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_vm_usage_duration_seconds",
			Help:    "Time a VM spent in the inuse state before being terminated",
			Buckets: []float64{5, 10, 20, 40, 60, 120, 300, 600, 1200, 1800, 3600, 7200, 14400, 28800},
		},
		[]string{"pool_id", "zone", "vm_type", "source", "termination_reason"},
	)
}

// VMsCurrent reports the current number of VMs by pool, zone, VM type, source, and lifecycle
// state. Additive to RunningCount: this gauge exists to add zone/vm_type granularity (RunningCount
// already carries os/arch/driver/owner_id/hibernate instead), not to replace it.
func VMsCurrent() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "runner_vms_current",
			Help: "Current number of VMs by pool, zone, VM type, source, and lifecycle state",
		},
		[]string{"pool_id", "zone", "vm_type", "source", "state"},
	)
}

// RecordVMCreationAttempt increments the creation-attempts counter. Safe to call on a nil
// *Metrics (e.g. standalone commands that don't have metrics wired up).
func (m *Metrics) RecordVMCreationAttempt(poolID, zone, vmType, source, outcome, reason string) {
	if m == nil {
		return
	}
	m.VMCreationAttemptsCount.WithLabelValues(poolID, zone, vmType, source, outcome, reason).Inc()
}

// RecordVMCreationDuration observes the duration of a driver Create() call. Negative durations
// (clock skew) are dropped rather than recorded.
func (m *Metrics) RecordVMCreationDuration(poolID, zone, vmType, source, outcome string, duration time.Duration) {
	if m == nil || duration < 0 {
		return
	}
	m.VMCreationDurationCount.WithLabelValues(poolID, zone, vmType, source, outcome).Observe(duration.Seconds())
}

// RecordVMUsageDuration observes how long a VM was in the inuse state. Negative dwell times are
// dropped rather than recorded, since they'd otherwise silently corrupt the histogram - this
// guards against the best-effort "became inuse at" timestamp (inst.Updated, read before the
// destroy path overwrites it) being stale or missing (zero-valued) at the caller.
func (m *Metrics) RecordVMUsageDuration(poolID, zone, vmType, source, terminationReason string, dwell time.Duration) {
	if m == nil || dwell < 0 {
		return
	}
	m.VMUsageDurationCount.WithLabelValues(poolID, zone, vmType, source, terminationReason).Observe(dwell.Seconds())
}
