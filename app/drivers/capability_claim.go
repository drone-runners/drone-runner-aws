package drivers

import (
	"context"
	"slices"

	"github.com/drone-runners/drone-runner-aws/types"
)

// Claim outcomes for MetricsRecorder.RecordHotpoolClaim.
const (
	ClaimOutcomeWarm       = "warm"
	ClaimOutcomeHibernated = "hibernated"
	ClaimOutcomeMiss       = "miss"
)

// CapabilityCatalog describes the pools that take part in capability scheduling.
type CapabilityCatalog interface {
	Platform(pool string) (tier, os, arch string, ok bool)
	PlatformPools(tier, os, arch string) []string
	ClaimPools(pool, tenantID, variantID string) (tier string, pools []string, ok bool)
}

// CapabilityProvisioner is implemented by managers that serve a request from pools other than
// the requested one.
type CapabilityProvisioner interface {
	// ClaimScope returns where a request to pool may claim and create an instance, or nil when
	// the request is served by pool and its fallbacks. mapPool applies the account's POOL_MAP.
	ClaimScope(ctx context.Context, pool string, params *types.ProvisionParams, mapPool func(string) string) *types.ClaimScope
}

// SetCapabilityCatalog sets the catalog and the scheduler algorithm. Requests are served beyond
// the requested pool only with types.SchedulerAlgorithmCapability.
func (d *DistributedManager) SetCapabilityCatalog(catalog CapabilityCatalog, algorithm types.SchedulerAlgorithm) {
	d.capabilityCatalog = catalog
	d.algorithm = algorithm
}

// ClaimScope picks the pools for the request's account, in order of precedence:
//   - the pool POOL_MAP maps the requested pool to;
//   - the pools placing the account in a tenant, with that tenant;
//   - the free pools, for the free account or a request to a free pool;
//   - otherwise the default tenant: any pool except the free ones can serve the claim, and new
//     instances are created in the requested pool's capability group.
func (d *DistributedManager) ClaimScope(
	ctx context.Context, poolName string, params *types.ProvisionParams, mapPool func(string) string,
) *types.ClaimScope {
	if d.capabilityCatalog == nil || d.algorithm != types.SchedulerAlgorithmCapability || params == nil {
		return nil
	}
	tier, osName, arch, ok := d.capabilityCatalog.Platform(poolName)
	if !ok {
		return nil
	}
	scope := &types.ClaimScope{Platform: types.Platform{OS: osName, Arch: arch}}
	only := func(pools []string) *types.ClaimScope {
		scope.Pools, scope.Include = pools, pools
		return scope
	}

	if mapPool != nil {
		if mapped := mapPool(poolName); mapped != poolName {
			return only([]string{mapped})
		}
	}
	if pools := d.tenantPools(d.capabilityCatalog.PlatformPools(tier, osName, arch), params.AccountID); len(pools) > 0 {
		return only(pools)
	}
	freePools := d.capabilityCatalog.PlatformPools(types.TierFree, osName, arch)
	if params.AccountID == types.FreeAccountID || tier == types.TierFree {
		if len(freePools) > 0 {
			return only(freePools)
		}
		freePools = nil
	}

	scope.Exclude = freePools
	scope.Pools = []string{poolName}
	pool := d.poolMap[poolName]
	if pool == nil {
		return scope
	}
	variantID := defaultVariantID
	if variants := pool.VariantsForTenant(defaultTenantID); len(variants) > 0 {
		if matched := d.filterVariants(ctx, pool, params, variants); len(matched) > 0 {
			variantID = matched[0].VariantID
		}
	}
	if _, group, ok := d.capabilityCatalog.ClaimPools(poolName, defaultTenantID, variantID); ok {
		for _, p := range group {
			if !slices.Contains(scope.Pools, p) && !slices.Contains(freePools, p) {
				scope.Pools = append(scope.Pools, p)
			}
		}
	}
	return scope
}

// tenantPools returns the pools, in order, that place the account in the same tenant as the
// first pool that places it in a tenant at all.
func (d *DistributedManager) tenantPools(pools []string, accountID string) []string {
	if accountID == "" {
		return nil
	}
	var tenantID string
	var out []string
	for _, p := range pools {
		entry, ok := d.poolMap[p]
		if !ok {
			continue
		}
		t := entry.ResolveTenant(accountID)
		if t == defaultTenantID || (tenantID != "" && t != tenantID) {
			continue
		}
		tenantID = t
		out = append(out, p)
	}
	return out
}

// applyClaimScope widens a single-pool claim query to the scope.
func applyClaimScope(query *types.QueryParams, scope *types.ClaimScope, poolName string) {
	if scope == nil {
		return
	}
	if len(scope.Include) > 0 {
		query.PoolNames = scope.Include
	} else {
		query.AnyPool = true
		query.ExcludePoolNames = scope.Exclude
	}
	platform := scope.Platform
	query.Platform = &platform
	if len(scope.Include) != 1 || scope.Include[0] != poolName {
		// Pools may give the same variant different machine types.
		query.MachineType = ""
	}
}

func (d *DistributedManager) recordClaim(requestedPool string, inst *types.Instance) {
	if d.metrics == nil {
		return
	}
	var tier string
	if d.capabilityCatalog != nil {
		tier, _, _, _ = d.capabilityCatalog.Platform(requestedPool)
	}
	claimedPool, outcome := "", ClaimOutcomeMiss
	if inst != nil {
		claimedPool, outcome = inst.Pool, ClaimOutcomeWarm
		if inst.IsHibernated {
			outcome = ClaimOutcomeHibernated
		}
	}
	d.metrics.RecordHotpoolClaim(string(d.algorithm), tier, requestedPool, claimedPool, outcome)
}

// variantSetupParams returns the machine settings of pool's variant for inst, used to
// replenish an instance claimed from a pool other than the one requested.
func variantSetupParams(pool *poolEntry, inst *types.Instance) *types.SetupInstanceParams {
	params := &types.SetupInstanceParams{}
	variants := pool.VariantsForTenant(inst.TenantID)
	for i := range variants {
		if variants[i].VariantID == inst.VariantID {
			applyVariantToSetupParams(params, &variants[i])
			break
		}
	}
	return params
}
