package config

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/drone-runners/drone-runner-aws/types"
)

// DefaultTenantID is the tenant identifier used for the default tenant and for all
// single-tenant (no `tenants` block) pools. It is also the DB default for the tenant_id column.
const DefaultTenantID = "default"

// ResolvedTenant is a fully-resolved tenant: its spec is the default tenant's spec
// deep-merged with the tenant's (possibly partial) override spec, and its sizing/variants
// fall back to the instance-level values when not overridden.
type ResolvedTenant struct {
	ID       string
	Spec     interface{}
	Pool     int
	Limit    int
	Variants []types.PoolVariant
}

// ResolveTenants expands an instance into its list of resolved tenants along with a map from
// customer account ID to tenant ID.
//
// Backward compatibility: when the instance has no `tenants` block, it returns a single
// tenant with ID "default" using the instance-level Spec/Pool/Limit/Variants, and an empty
// account map (so every request resolves to the default tenant).
func ResolveTenants(inst *Instance) (tenants []ResolvedTenant, accountToTenant map[string]string, err error) {
	accountToTenant = map[string]string{}

	if len(inst.Tenants) == 0 {
		return []ResolvedTenant{{
			ID:       DefaultTenantID,
			Spec:     inst.Spec,
			Pool:     inst.Pool,
			Limit:    inst.Limit,
			Variants: inst.Variants,
		}}, accountToTenant, nil
	}

	// Locate the default tenant (exactly one required).
	var defaultTenant *Tenant
	for i := range inst.Tenants {
		t := &inst.Tenants[i]
		if t.Default || t.ID == DefaultTenantID {
			if defaultTenant != nil {
				return nil, nil, fmt.Errorf("pool %q: multiple default tenants defined", inst.Name)
			}
			defaultTenant = t
		}
	}
	if defaultTenant == nil {
		return nil, nil, fmt.Errorf("pool %q: multi-tenant config requires a default tenant (id: default)", inst.Name)
	}
	if defaultTenant.Spec == nil {
		return nil, nil, fmt.Errorf("pool %q: default tenant must define a spec", inst.Name)
	}

	seenTenantIDs := map[string]bool{}
	for i := range inst.Tenants {
		t := &inst.Tenants[i]

		tenantID := tenantIdentifier(t)
		if tenantID == "" {
			return nil, nil, fmt.Errorf("pool %q: tenant must define an id or a non-empty ids list", inst.Name)
		}
		if seenTenantIDs[tenantID] {
			return nil, nil, fmt.Errorf("pool %q: duplicate tenant id %q", inst.Name, tenantID)
		}
		seenTenantIDs[tenantID] = true

		spec := t.Spec
		if t != defaultTenant {
			merged, mergeErr := MergeSpec(defaultTenant.Spec, t.Spec)
			if mergeErr != nil {
				return nil, nil, fmt.Errorf("pool %q: failed to merge tenant %q: %w", inst.Name, tenantID, mergeErr)
			}
			spec = merged
		}

		resolved := ResolvedTenant{
			ID:       tenantID,
			Spec:     spec,
			Pool:     firstNonZero(t.Pool, defaultTenant.Pool, inst.Pool),
			Limit:    firstNonZero(t.Limit, defaultTenant.Limit, inst.Limit),
			Variants: firstNonEmptyVariants(t.Variants, defaultTenant.Variants, inst.Variants),
		}
		tenants = append(tenants, resolved)

		for _, accountID := range t.IDs {
			if accountID == "" {
				continue
			}
			if existing, ok := accountToTenant[accountID]; ok && existing != tenantID {
				return nil, nil, fmt.Errorf("pool %q: account id %q mapped to multiple tenants (%q and %q)", inst.Name, accountID, existing, tenantID)
			}
			accountToTenant[accountID] = tenantID
		}
	}

	return tenants, accountToTenant, nil
}

// tenantIdentifier returns the stable tenant identifier: the explicit ID when set, otherwise
// the first account id in IDs.
func tenantIdentifier(t *Tenant) string {
	if t.ID != "" {
		return t.ID
	}
	if t.Default {
		return DefaultTenantID
	}
	if len(t.IDs) > 0 {
		return t.IDs[0]
	}
	return ""
}

// MergeSpec deep-merges an override spec over a base spec and returns a new spec of the same
// concrete type as base. Fields absent in the override (zero values elided via omitempty) are
// inherited from base; fields present in the override win. Nested objects are merged
// recursively; slices in the override replace the corresponding base slice.
func MergeSpec(base, override interface{}) (interface{}, error) {
	if base == nil {
		return nil, fmt.Errorf("base spec is nil")
	}
	if override == nil {
		return base, nil
	}

	baseMap, err := toMap(base)
	if err != nil {
		return nil, err
	}
	overrideMap, err := toMap(override)
	if err != nil {
		return nil, err
	}

	merged := deepMerge(baseMap, overrideMap)
	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}

	out := reflect.New(reflect.TypeOf(base).Elem()).Interface()
	if err := json.Unmarshal(mergedJSON, out); err != nil {
		return nil, err
	}
	return out, nil
}

func toMap(v interface{}) (map[string]interface{}, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	m := map[string]interface{}{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func deepMerge(base, override map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range override {
		if bv, ok := out[k]; ok {
			bvMap, bok := bv.(map[string]interface{})
			ovMap, ook := ov.(map[string]interface{})
			if bok && ook {
				out[k] = deepMerge(bvMap, ovMap)
				continue
			}
		}
		out[k] = ov
	}
	return out
}

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

func firstNonEmptyVariants(vals ...[]types.PoolVariant) []types.PoolVariant {
	for _, v := range vals {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}
