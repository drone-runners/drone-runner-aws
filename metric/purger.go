package metric

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// PurgerLastRunTimestamp reports the unix timestamp of the last completed purger sweep, per pool.
func PurgerLastRunTimestamp() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "runner_purger_last_run_timestamp_seconds",
			Help: "Unix timestamp of the last completed background purger sweep for a pool",
		},
		[]string{"pool_id"},
	)
}

// PurgerInstanceDestroyAttemptsCount counts instance destroy attempts made by the background
// purger, one increment per instance per outcome.
func PurgerInstanceDestroyAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_purger_instance_destroy_attempts_total",
			Help: "Total number of instance destroy attempts made by the background purger",
		},
		[]string{"pool_id", "zone", "reason", "outcome"},
	)
}

// PurgerInstancesForceDeletedCount counts instance DB rows force-deleted because the driver
// could never confirm-destroy them within the leak window. This is a cloud-cost leak signal.
func PurgerInstancesForceDeletedCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_purger_instances_force_deleted_total",
			Help: "Total number of instance rows force-deleted by the purger as leak candidates",
		},
		[]string{"pool_id", "cleanup_type"},
	)
}

// PurgerCapacityDestroyAttemptsCount counts capacity reservation destroy attempts made by the
// background purger, one increment per reservation per outcome.
func PurgerCapacityDestroyAttemptsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_purger_capacity_destroy_attempts_total",
			Help: "Total number of capacity reservation destroy attempts made by the background purger",
		},
		[]string{"pool_id", "reason", "outcome"},
	)
}

// RecordPurgerLastRun sets the last-run gauge for a pool to the current unix time. Safe to call
// on a nil *Metrics (e.g. standalone commands that don't have metrics wired up).
func (m *Metrics) RecordPurgerLastRun(poolID string) {
	if m == nil {
		return
	}
	m.PurgerLastRunTimestamp.WithLabelValues(poolID).Set(float64(time.Now().Unix()))
}

// RecordInstanceDestroyAttempt increments the instance destroy attempts counter.
func (m *Metrics) RecordInstanceDestroyAttempt(poolID, zone, reason, outcome string) {
	if m == nil {
		return
	}
	m.PurgerInstanceDestroyAttemptsCount.WithLabelValues(poolID, zone, reason, outcome).Inc()
}

// RecordInstancesForceDeleted increments the force-deleted counter by count.
func (m *Metrics) RecordInstancesForceDeleted(poolID, cleanupType string, count int) {
	if m == nil || count <= 0 {
		return
	}
	m.PurgerInstancesForceDeletedCount.WithLabelValues(poolID, cleanupType).Add(float64(count))
}

// RecordCapacityDestroyAttempt increments the capacity destroy attempts counter.
func (m *Metrics) RecordCapacityDestroyAttempt(poolID, reason, outcome string) {
	if m == nil {
		return
	}
	m.PurgerCapacityDestroyAttemptsCount.WithLabelValues(poolID, reason, outcome).Inc()
}
