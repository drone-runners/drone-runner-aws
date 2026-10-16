package jobs

import (
	"context"
	"slices"
	"strings"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/types"
)

// ScaleTargetSource lists the scale jobs the trigger creates for each window. Each target is
// stored as the scale job's PoolName, which is also what dedups the job per window.
type ScaleTargetSource interface {
	ScaleTargets() []string
}

// ScaleJobRunner runs one scale job for a target produced by the matching ScaleTargetSource.
type ScaleJobRunner interface {
	RunScaleJob(ctx context.Context, target string, windowStart, windowEnd int64) error
}

// SetupFailureHandler takes over a setup_instance job that failed. It returns true when the
// failure is handled and the job must be deleted instead of retried.
type SetupFailureHandler interface {
	HandleSetupFailure(ctx context.Context, pool string, params *types.SetupInstanceParams, cause error) bool
}

var (
	_ ScaleTargetSource   = (*PoolScaleTargets)(nil)
	_ ScaleTargetSource   = (*CapabilityScaleTargets)(nil)
	_ ScaleJobRunner      = (*Scaler)(nil)
	_ ScaleJobRunner      = (*CapabilityScaler)(nil)
	_ SetupFailureHandler = (*CapabilityScaler)(nil)
)

// PoolScaleTargets creates one scale job per pool whose driver has predictions enabled.
type PoolScaleTargets struct {
	pools   []ScalablePool
	drivers map[string]bool
}

// NewPoolScaleTargets returns the scale targets of the pool algorithm.
func NewPoolScaleTargets(pools []ScalablePool, predictionDrivers []string) *PoolScaleTargets {
	return &PoolScaleTargets{pools: pools, drivers: capability.DriverSet(predictionDrivers)}
}

// ScaleTargets returns the pool names to scale.
func (t *PoolScaleTargets) ScaleTargets() []string {
	var targets []string
	for i := range t.pools {
		if t.drivers[strings.ToLower(t.pools[i].Driver)] {
			targets = append(targets, t.pools[i].Name)
		}
	}
	return targets
}

// CapabilityScaleTargets creates one scale job per capability group with at least one pool
// that is not disabled for scaling.
type CapabilityScaleTargets struct {
	catalog       *capability.Catalog
	disabledPools []string
}

// NewCapabilityScaleTargets returns the scale targets of the capability algorithm.
func NewCapabilityScaleTargets(catalog *capability.Catalog, disabledPools []string) *CapabilityScaleTargets {
	return &CapabilityScaleTargets{catalog: catalog, disabledPools: disabledPools}
}

// ScaleTargets returns the capability scale job names to scale.
func (t *CapabilityScaleTargets) ScaleTargets() []string {
	var targets []string
	groups := t.catalog.Groups()
	for i := range groups {
		if t.anyPoolEnabled(groups[i].Members) {
			targets = append(targets, types.CapabilityScaleJobName(&groups[i].Key))
		}
	}
	return targets
}

func (t *CapabilityScaleTargets) anyPoolEnabled(members []capability.Member) bool {
	for i := range members {
		if !slices.Contains(t.disabledPools, members[i].Pool) {
			return true
		}
	}
	return false
}
