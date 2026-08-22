package metric

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestVMCreationAttemptsCount(t *testing.T) {
	c := VMCreationAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "pool", "success", "none").Desc().String(),
		"runner_vm_creation_attempts_total")
}

func TestVMCreationDurationCount(t *testing.T) {
	h := VMCreationDurationCount()
	observer := h.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "pool", "success")
	assert.Contains(t, observer.(prometheus.Metric).Desc().String(), "runner_vm_creation_duration_seconds")
}

func TestVMUsageDurationCount(t *testing.T) {
	h := VMUsageDurationCount()
	observer := h.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "pool", "normal_cleanup")
	assert.Contains(t, observer.(prometheus.Metric).Desc().String(), "runner_vm_usage_duration_seconds")
}

func TestVMsCurrent(t *testing.T) {
	g := VMsCurrent()
	assert.Contains(t, g.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "pool", "inuse").Desc().String(),
		"runner_vms_current")
}

func TestMetrics_RecordVMMethods_NilSafe(t *testing.T) {
	// All Record* methods must be safe to call on a nil *Metrics (e.g. standalone commands that
	// never wire up metrics), matching the guard used throughout the rest of this package.
	var m *Metrics
	assert.NotPanics(t, func() {
		m.RecordVMCreationAttempt("pool1", "us-east1-a", "n1-standard-2", "pool", "success", "none")
		m.RecordVMCreationDuration("pool1", "us-east1-a", "n1-standard-2", "pool", "success", 5*time.Second)
		m.RecordVMUsageDuration("pool1", "us-east1-a", "n1-standard-2", "pool", "normal_cleanup", time.Hour)
	})
}

func TestMetrics_RecordVMCreationDuration_NegativeDurationDropped(t *testing.T) {
	m := &Metrics{VMCreationDurationCount: VMCreationDurationCount()}
	m.RecordVMCreationDuration("pool1", "us-east1-a", "n1-standard-2", "pool", "success", -1)
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMCreationDurationCount))

	m.RecordVMCreationDuration("pool1", "us-east1-a", "n1-standard-2", "pool", "success", 5*time.Second)
	assert.Equal(t, 1, testutil.CollectAndCount(m.VMCreationDurationCount))
}

func TestMetrics_RecordVMUsageDuration_NegativeDwellDropped(t *testing.T) {
	m := &Metrics{VMUsageDurationCount: VMUsageDurationCount()}
	m.RecordVMUsageDuration("pool1", "us-east1-a", "n1-standard-2", "pool", "normal_cleanup", -1)
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMUsageDurationCount))

	m.RecordVMUsageDuration("pool1", "us-east1-a", "n1-standard-2", "pool", "normal_cleanup", time.Minute)
	assert.Equal(t, 1, testutil.CollectAndCount(m.VMUsageDurationCount))
}

func TestMetrics_RecordVMCreationAttempt_Increments(t *testing.T) {
	m := &Metrics{VMCreationAttemptsCount: VMCreationAttemptsCount()}
	m.RecordVMCreationAttempt("pool1", "us-east1-a", "n1-standard-2", "pool", "success", "none")
	assert.InDelta(t, 1, testutil.ToFloat64(m.VMCreationAttemptsCount.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", "pool", "success", "none")), 0.0001)
}

// TestUpdateVMsCurrent covers the zone/vm_type/source-aware bucketing runner_vms_current uses,
// grouping the same instance list updateRunningCount fetches for RunningCount by raw
// types.InstanceState (not the ready/busy/hibernated naming HotpoolInstancesCurrent uses).
func TestUpdateVMsCurrent(t *testing.T) {
	m := &Metrics{VMsCurrent: VMsCurrent()}

	instances := []*types.Instance{
		{Pool: "pool1", Zone: "us-east1-a", Size: "n1-standard-2", Source: types.InstanceSourcePool, State: types.StateInUse},
		{Pool: "pool1", Zone: "us-east1-a", Size: "n1-standard-2", Source: types.InstanceSourcePool, State: types.StateInUse},
		{Pool: "pool1", Zone: "us-east1-a", Size: "n1-standard-2", Source: types.InstanceSourcePool, State: types.StateCreated},
		{Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-4", Source: types.InstanceSourcePredictor, State: types.StateInUse},
	}

	m.updateVMsCurrent(instances)

	assert.InDelta(t, 2, testutil.ToFloat64(m.VMsCurrent.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", "pool", "inuse")), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.VMsCurrent.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", "pool", "created")), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.VMsCurrent.WithLabelValues(
		"pool1", "us-east1-b", "n1-standard-4", "predictor", "inuse")), 0.0001)
}

func TestUpdateVMsCurrent_Empty_NoSeries(t *testing.T) {
	m := &Metrics{VMsCurrent: VMsCurrent()}
	m.updateVMsCurrent(nil)
	assert.Equal(t, 0, testutil.CollectAndCount(m.VMsCurrent))
}
