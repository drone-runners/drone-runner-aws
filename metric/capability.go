package metric

import "github.com/prometheus/client_golang/prometheus"

// Spill outcomes for ScalerCreateSpillCount.
const (
	SpillOutcomeSpilled   = "spilled"
	SpillOutcomeExhausted = "exhausted"
)

const (
	labelArch      = "arch"
	labelOutcome   = "outcome"
	labelVariantID = "variant_id"
)

var capabilityLabels = []string{"tier", "os", labelArch, labelVariantID, "tenant_id"}

// ScalerCapabilityPredictedInstances is the warm-instance target for a capability group,
// including any hibernated buffer.
func ScalerCapabilityPredictedInstances() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "harness_ci_scaler_capability_predicted_instances",
			Help: "Warm instances the capability scaler targets for a capability group in the upcoming window",
		},
		capabilityLabels,
	)
}

// ScalerCapabilityTargetInstances is the warm-instance target assigned to one pool of a group.
func ScalerCapabilityTargetInstances() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "harness_ci_scaler_capability_target_instances",
			Help: "Warm instances the capability scaler assigns to a pool of a capability group",
		},
		append(append([]string{}, capabilityLabels...), "pool_id"),
	)
}

// ScalerCapabilityFreeInstances is the free (warm) instance count of one pool of a group,
// observed when scaling.
func ScalerCapabilityFreeInstances() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "harness_ci_scaler_capability_free_instances",
			Help: "Free instances observed by the capability scaler in a pool of a capability group",
		},
		append(append([]string{}, capabilityLabels...), "pool_id"),
	)
}

// ScalerCreateSpillCount counts predictor instance creations moved to another pool of their
// capability group after a failure. to_pool is empty when no pool is left, in which case the
// failed job is retried in its own pool.
func ScalerCreateSpillCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "harness_ci_scaler_create_spill_total",
			Help: "Predictor instance creations moved to the next pool of their capability group after a failure",
		},
		[]string{"from_pool", "to_pool", labelOutcome},
	)
}
