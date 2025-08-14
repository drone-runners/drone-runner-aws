package types

import (
	"github.com/harness/ti-client/types/cache/buildcache"
	"github.com/harness/ti-client/types/cache/dlc"
	"github.com/harness/ti-client/types/cache/gradle"
	"github.com/harness/ti-client/types/cache/maven"
)

type IntelligenceExecutionState string

const (
	FULL_RUN  IntelligenceExecutionState = "FULL_RUN"
	OPTIMIZED IntelligenceExecutionState = "OPTIMIZED"
	DISABLED  IntelligenceExecutionState = "DISABLED"
)

type SavingsFeature string

const (
	BUILD_CACHE SavingsFeature = "build_cache"
	TI          SavingsFeature = "test_intelligence"
	DLC         SavingsFeature = "docker_layer_caching"
)

type SavingsRequest struct {
	GradleMetrics gradle.Metrics     `json:"gradle_metrics"`
	MavenMetrics  maven.MavenMetrics `json:"maven_metrics"`
	DlcMetrics    dlc.Metrics        `json:"dlc_metrics"`
}

type SavingsOverview struct {
	FeatureName  SavingsFeature             `json:"feature_name"`
	TimeTakenMs  int64                      `json:"time_taken_ms"`
	TimeSavedMs  int64                      `json:"time_saved_ms"`
	BaselineMs   int64                      `json:"baseline_ms"`
	FeatureState IntelligenceExecutionState `json:"feature_state"`
}

// This Structure will have the savings overview for each step and also detailed metrics in the future such as dlc metrics and gradle metrics
type SavingsResponse struct {
	Overview           []SavingsOverview    `json:"overview"` // array of SavingsOverview since one step can have multiple savings features enabled
	DlcMetadata        *dlc.Metadata        `json:"dlc_metadata"`
	BuildCacheMetadata *buildcache.Metadata `json:"build_cache_metadata"`
}
