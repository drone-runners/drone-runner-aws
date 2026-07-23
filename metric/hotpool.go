package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HotpoolInstancesCurrent reports the current number of hot-pool instances per pool, broken
// down by zone, VM type (machine size), and lifecycle state. Additive to WarmPoolCount: this
// gauge exists to add zone/vm_type granularity, not to replace the existing metric.
func HotpoolInstancesCurrent() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "runner_hotpool_instances_current",
			Help: "Current number of hot-pool instances by pool, zone, VM type, and lifecycle state",
		},
		[]string{"pool_id", "zone", "vm_type", "state"},
	)
}

// HotpoolClaimAttemptsCount counts attempts to claim a warm instance from a hot pool, one
// increment per provisioning attempt, by outcome.
func HotpoolClaimAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_hotpool_claim_attempts_total",
			Help: "Total number of attempts to claim a warm instance from a hot pool",
		},
		[]string{"pool_id", "zone", "vm_type", "outcome", "reason"},
	)
}

// HotpoolStateDuration reports how long a hot-pool instance dwelled in a lifecycle state before
// transitioning out of it (e.g. how long it sat "ready" before being claimed, or how long it
// spent "provisioning" before becoming ready).
func HotpoolStateDuration() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_hotpool_state_duration_seconds",
			Help:    "Time a hot-pool instance spent in a lifecycle state before transitioning out of it",
			Buckets: []float64{5, 10, 20, 40, 60, 120, 300, 600, 1200, 1800, 3600, 7200, 14400, 28800},
		},
		[]string{"pool_id", "zone", "vm_type", "state"},
	)
}

// RecordHotpoolClaimAttempt increments the claim-attempts counter. Safe to call on a nil
// *Metrics (e.g. standalone commands that don't have metrics wired up).
func (m *Metrics) RecordHotpoolClaimAttempt(poolID, zone, vmType, outcome, reason string) {
	if m == nil {
		return
	}
	m.HotpoolClaimAttemptsCount.WithLabelValues(poolID, zone, vmType, outcome, reason).Inc()
}

// RecordHotpoolStateDuration observes how long an instance dwelled in a state before leaving it.
// Negative dwell times (clock skew, or a bug in the caller's start-time bookkeeping) are dropped
// rather than recorded, since they'd otherwise silently corrupt the histogram.
func (m *Metrics) RecordHotpoolStateDuration(poolID, zone, vmType, state string, dwell time.Duration) {
	if m == nil || dwell < 0 {
		return
	}
	m.HotpoolStateDuration.WithLabelValues(poolID, zone, vmType, state).Observe(dwell.Seconds())
}
