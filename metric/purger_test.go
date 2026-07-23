package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestPurgerLastRunTimestamp(t *testing.T) {
	g := PurgerLastRunTimestamp()
	assert.Contains(t, g.WithLabelValues("pool1").Desc().String(), "runner_purger_last_run_timestamp_seconds")
}

func TestPurgerInstanceDestroyAttemptsCount(t *testing.T) {
	c := PurgerInstanceDestroyAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "us-east1-a", "busy_maxage", "destroyed").Desc().String(),
		"runner_purger_instance_destroy_attempts_total")
}

func TestPurgerInstancesForceDeletedCount(t *testing.T) {
	c := PurgerInstancesForceDeletedCount()
	assert.Contains(t, c.WithLabelValues("pool1", "busy").Desc().String(),
		"runner_purger_instances_force_deleted_total")
}

func TestPurgerCapacityDestroyAttemptsCount(t *testing.T) {
	c := PurgerCapacityDestroyAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "orphaned_inuse", "destroyed").Desc().String(),
		"runner_purger_capacity_destroy_attempts_total")
}

func TestMetrics_RecordPurgerMethods_NilSafe(t *testing.T) {
	// All Record* methods must be safe to call on a nil *Metrics (e.g. standalone commands that
	// never wire up metrics), matching the guard used throughout the rest of this package.
	var m *Metrics
	assert.NotPanics(t, func() {
		m.RecordPurgerLastRun("pool1")
		m.RecordInstanceDestroyAttempt("pool1", "us-east1-a", "busy_maxage", "destroyed")
		m.RecordInstancesForceDeleted("pool1", "busy", 2)
		m.RecordCapacityDestroyAttempt("pool1", "orphaned_inuse", "destroyed")
	})
}

func TestMetrics_RecordInstancesForceDeleted_ZeroCountNoop(t *testing.T) {
	m := &Metrics{PurgerInstancesForceDeletedCount: PurgerInstancesForceDeletedCount()}
	m.RecordInstancesForceDeleted("pool1", "busy", 0)
	// A count <= 0 must not create a series at all.
	assert.Equal(t, 0, testutil.CollectAndCount(m.PurgerInstancesForceDeletedCount))

	m.RecordInstancesForceDeleted("pool1", "busy", 3)
	assert.Equal(t, 1, testutil.CollectAndCount(m.PurgerInstancesForceDeletedCount))
	assert.InDelta(t, 3, testutil.ToFloat64(m.PurgerInstancesForceDeletedCount.WithLabelValues("pool1", "busy")), 0.0001)
}
