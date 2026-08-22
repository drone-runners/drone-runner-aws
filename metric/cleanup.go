package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// cleanupDurationBuckets covers both fast DB-only cleanups (stage_owner) and slower cloud-level
// destroy calls (vm, capacity_reservation), which run under handleDestroy's own
// liteEngineDestroyTimeout/destroyTimeout ceilings (30s / 10m respectively).
var cleanupDurationBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300, 600}

// CleanupAttemptsCount counts on-demand cleanup attempts made during explicit VM destroy
// requests (handleDestroy: cloud-level instance destroy, capacity reservation release, and
// stage-owner record deletion). Distinct from the background purger's
// PurgerInstanceDestroyAttemptsCount/PurgerCapacityDestroyAttemptsCount (purger.go), which cover
// unrequested, staleness-driven cleanup of the same underlying resource types instead.
func CleanupAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_cleanup_attempts_total",
			Help: "Total number of on-demand cleanup attempts during explicit VM destroy requests",
		},
		[]string{"resource", "pool_id", "zone", "outcome", "reason"},
	)
}

// CleanupDurationCount observes how long a single on-demand cleanup attempt took.
func CleanupDurationCount() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_cleanup_duration_seconds",
			Help:    "Time taken by a single on-demand cleanup attempt during explicit VM destroy requests",
			Buckets: cleanupDurationBuckets,
		},
		[]string{"resource", "pool_id", "zone", "outcome"},
	)
}

// RecordCleanupAttempt increments the cleanup-attempts counter. Safe to call on a nil *Metrics
// (e.g. standalone commands that don't have metrics wired up).
func (m *Metrics) RecordCleanupAttempt(resource, poolID, zone, outcome, reason string) {
	if m == nil {
		return
	}
	m.CleanupAttemptsCount.WithLabelValues(resource, poolID, zone, outcome, reason).Inc()
}

// RecordCleanupDuration observes the duration of a cleanup attempt. Negative durations (clock
// skew) are dropped rather than recorded.
func (m *Metrics) RecordCleanupDuration(resource, poolID, zone, outcome string, duration time.Duration) {
	if m == nil || duration < 0 {
		return
	}
	m.CleanupDurationCount.WithLabelValues(resource, poolID, zone, outcome).Observe(duration.Seconds())
}
