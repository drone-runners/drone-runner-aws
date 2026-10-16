package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestGCPStockoutAttemptsCount_Name(t *testing.T) {
	c := GCPStockoutAttemptsCount()
	assert.Contains(t, c.WithLabelValues("pool1", "us-central1-a", "c4d-standard-4", "1").Desc().String(),
		"runner_gcp_stockout_attempts_total")
}

func TestMetrics_RecordStockoutAttempt_IncrementsPerAttempt(t *testing.T) {
	m := &Metrics{GCPStockoutAttemptsCount: GCPStockoutAttemptsCount()}
	m.RecordStockoutAttempt("pool1", "us-central1-a", "c4d-standard-4", 1)
	m.RecordStockoutAttempt("pool1", "us-central1-a", "c4d-standard-4", 1)
	m.RecordStockoutAttempt("pool1", "us-central1-b", "c4d-standard-4", 3)

	assert.InDelta(t, 2, testutil.ToFloat64(m.GCPStockoutAttemptsCount.WithLabelValues(
		"pool1", "us-central1-a", "c4d-standard-4", "1")), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.GCPStockoutAttemptsCount.WithLabelValues(
		"pool1", "us-central1-b", "c4d-standard-4", "3")), 0.0001)
	assert.InDelta(t, 0, testutil.ToFloat64(m.GCPStockoutAttemptsCount.WithLabelValues(
		"pool1", "us-central1-a", "c4d-standard-4", "2")), 0.0001)
}

func TestMetrics_RecordStockoutAttempt_NilSafe(t *testing.T) {
	var m *Metrics
	assert.NotPanics(t, func() {
		m.RecordStockoutAttempt("pool1", "us-central1-a", "c4d-standard-4", 1)
	})

	unwired := &Metrics{}
	assert.NotPanics(t, func() {
		unwired.RecordStockoutAttempt("pool1", "us-central1-a", "c4d-standard-4", 1)
	})
}
