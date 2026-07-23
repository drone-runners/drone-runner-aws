package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestHotpoolInstancesCurrent(t *testing.T) {
	g := HotpoolInstancesCurrent()
	assert.Contains(t, g.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "ready").Desc().String(),
		"runner_hotpool_instances_current")
}

func TestHotpoolClaimAttemptsCount(t *testing.T) {
	c := HotpoolClaimAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "claimed", "none").Desc().String(),
		"runner_hotpool_claim_attempts_total")
}

func TestHotpoolStateDuration(t *testing.T) {
	h := HotpoolStateDuration()
	observer := h.WithLabelValues("pool1", "us-east1-a", "n1-standard-2", "provisioning")
	assert.Contains(t, observer.(prometheus.Metric).Desc().String(), "runner_hotpool_state_duration_seconds")
}

func TestMetrics_RecordHotpoolMethods_NilSafe(t *testing.T) {
	// All Record* methods must be safe to call on a nil *Metrics (e.g. standalone commands that
	// never wire up metrics), matching the guard used throughout the rest of this package.
	var m *Metrics
	assert.NotPanics(t, func() {
		m.RecordHotpoolClaimAttempt("pool1", "us-east1-a", "n1-standard-2", "claimed", "none")
		m.RecordHotpoolStateDuration("pool1", "us-east1-a", "n1-standard-2", "provisioning", 42)
	})
}

func TestMetrics_RecordHotpoolStateDuration_NegativeDwellDropped(t *testing.T) {
	m := &Metrics{HotpoolStateDuration: HotpoolStateDuration()}
	m.RecordHotpoolStateDuration("pool1", "us-east1-a", "n1-standard-2", "provisioning", -1)
	assert.Equal(t, 0, testutil.CollectAndCount(m.HotpoolStateDuration))

	m.RecordHotpoolStateDuration("pool1", "us-east1-a", "n1-standard-2", "provisioning", 5)
	assert.Equal(t, 1, testutil.CollectAndCount(m.HotpoolStateDuration))
}

// TestUpdateHotpoolInstancesCurrent covers the zone/vm_type-aware bucketing that
// updateWarmPoolCount delegates to: busy/hibernating/provisioning map straight through, and free
// splits into ready vs hibernated depending on each instance's IsHibernated flag.
func TestUpdateHotpoolInstancesCurrent(t *testing.T) {
	m := &Metrics{HotpoolInstancesCurrent: HotpoolInstancesCurrent()}

	busy := []*types.Instance{
		{Zone: "us-east1-a", Size: "n1-standard-2"},
		{Zone: "us-east1-a", Size: "n1-standard-2"},
		{Zone: "us-east1-b", Size: "n1-standard-4"},
	}
	free := []*types.Instance{
		{Zone: "us-east1-a", Size: "n1-standard-2", IsHibernated: false},
		{Zone: "us-east1-a", Size: "n1-standard-2", IsHibernated: true},
	}
	hibernating := []*types.Instance{
		{Zone: "us-east1-b", Size: "n1-standard-4"},
	}
	provisioning := []*types.Instance{
		{Zone: "us-east1-a", Size: "n1-standard-2"},
	}

	m.updateHotpoolInstancesCurrent("pool1", busy, free, hibernating, provisioning)

	assert.InDelta(t, 2, testutil.ToFloat64(m.HotpoolInstancesCurrent.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", drivers.HotpoolStateBusy)), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.HotpoolInstancesCurrent.WithLabelValues(
		"pool1", "us-east1-b", "n1-standard-4", drivers.HotpoolStateBusy)), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.HotpoolInstancesCurrent.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", drivers.HotpoolStateReady)), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.HotpoolInstancesCurrent.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", drivers.HotpoolStateHibernated)), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.HotpoolInstancesCurrent.WithLabelValues(
		"pool1", "us-east1-b", "n1-standard-4", drivers.HotpoolStateHibernating)), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.HotpoolInstancesCurrent.WithLabelValues(
		"pool1", "us-east1-a", "n1-standard-2", drivers.HotpoolStateProvisioning)), 0.0001)
}

func TestUpdateHotpoolInstancesCurrent_Empty_NoSeries(t *testing.T) {
	m := &Metrics{HotpoolInstancesCurrent: HotpoolInstancesCurrent()}
	m.updateHotpoolInstancesCurrent("pool1", nil, nil, nil, nil)
	assert.Equal(t, 0, testutil.CollectAndCount(m.HotpoolInstancesCurrent))
}
