package metric

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestCapabilityScalerMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	predicted := ScalerCapabilityPredictedInstances()
	target := ScalerCapabilityTargetInstances()
	free := ScalerCapabilityFreeInstances()
	spill := ScalerCreateSpillCount()
	reg.MustRegister(predicted, target, free, spill)

	predicted.WithLabelValues("paid", "linux", "amd64", "variant_medium", "default").Set(12)
	target.WithLabelValues("paid", "linux", "amd64", "variant_medium", "default", "linux-amd64").Set(8)
	free.WithLabelValues("paid", "linux", "amd64", "variant_medium", "default", "linux-amd64").Set(3)
	spill.WithLabelValues("linux-amd64", "linux-amd64-fallback", SpillOutcomeSpilled).Inc()

	names := map[string]bool{}
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, n := range []string{
		"harness_ci_scaler_capability_predicted_instances",
		"harness_ci_scaler_capability_target_instances",
		"harness_ci_scaler_capability_free_instances",
		"harness_ci_scaler_create_spill_total",
	} {
		if !names[n] {
			t.Errorf("metric %s not registered", n)
		}
	}
	if v := testutil.ToFloat64(spill.WithLabelValues("linux-amd64", "linux-amd64-fallback", SpillOutcomeSpilled)); v != 1 {
		t.Errorf("spill counter = %v", v)
	}
}
