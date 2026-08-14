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
// The instance-level Spec is the base (default) tenant: it is always returned first with ID
// "default" and the instance-level Pool/Limit/Variants. Each entry in inst.Tenants is a
// per-account override whose (partial) Spec is deep-merged over the base; its tenant ID is the
// entry's GroupID when set, otherwise the first entry of its IDs list (legacy behavior). Every
// account in IDs is routed to that tenant. Tenant
// Variants replace matching instance-level variants by variant_id (full entry replace; new
// variant_ids are appended). Accounts not listed in any override resolve
// to the default tenant.
//
// Backward compatibility: an instance with no `tenants` block resolves to the single default
// tenant and an empty account map (so every request resolves to the default tenant).
func ResolveTenants(inst *Instance) (tenants []ResolvedTenant, accountToTenant map[string]string, err error) {
	accountToTenant = map[string]string{}

	if inst.Spec == nil {
		return nil, nil, fmt.Errorf("pool %q: missing base spec", inst.Name)
	}

	// The base pool spec is always the default tenant.
	tenants = append(tenants, ResolvedTenant{
		ID:       DefaultTenantID,
		Spec:     inst.Spec,
		Pool:     inst.Pool,
		Limit:    inst.Limit,
		Variants: inst.Variants,
	})

	if len(inst.Tenants) == 0 {
		return tenants, accountToTenant, nil
	}

	seenTenantIDs := map[string]bool{DefaultTenantID: true}
	for i := range inst.Tenants {
		t := &inst.Tenants[i]

		if len(t.IDs) == 0 {
			return nil, nil, fmt.Errorf("pool %q: tenant override must define a non-empty ids list", inst.Name)
		}
		// GroupID, when set, is the tenant's stable identity; otherwise fall back to ids[0]
		// (legacy behavior). GroupID may equal a member of the tenant's own ids list (the
		// zero-migration cutover), but must not collide with another tenant's identity or
		// member accounts.
		tenantID := t.GroupID
		if tenantID == "" {
			tenantID = t.IDs[0]
		}
		if tenantID == "" {
			return nil, nil, fmt.Errorf("pool %q: tenant override has an empty account id", inst.Name)
		}
		if seenTenantIDs[tenantID] {
			return nil, nil, fmt.Errorf("pool %q: duplicate tenant id %q", inst.Name, tenantID)
		}
		seenTenantIDs[tenantID] = true

		spec := inst.Spec
		if t.Spec != nil {
			merged, mergeErr := MergeSpec(inst.Spec, t.Spec)
			if mergeErr != nil {
				return nil, nil, fmt.Errorf("pool %q: failed to merge tenant %q: %w", inst.Name, tenantID, mergeErr)
			}
			spec = merged
		}

		tenants = append(tenants, ResolvedTenant{
			ID:       tenantID,
			Spec:     spec,
			Pool:     intOrDefault(t.Pool, inst.Pool),
			Limit:    intOrDefault(t.Limit, inst.Limit),
			Variants: mergeVariants(inst.Variants, t.Variants),
		})

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

	// Post-pass: an explicit group_id must not shadow another tenant's member account id,
	// regardless of tenant order. (Its own members map to the group id itself, which is fine.)
	for i := range inst.Tenants {
		t := &inst.Tenants[i]
		if t.GroupID == "" {
			continue
		}
		if existing, ok := accountToTenant[t.GroupID]; ok && existing != t.GroupID {
			return nil, nil, fmt.Errorf("pool %q: group_id %q collides with a member account of tenant %q", inst.Name, t.GroupID, existing)
		}
	}

	return tenants, accountToTenant, nil
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

// intOrDefault returns *p when p is non-nil (honoring an explicit 0), otherwise def. This lets a
// tenant set `pool: 0`/`limit: 0` to opt out of warm instances instead of inheriting the
// instance-level sizing.
func intOrDefault(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}

// mergeVariants merges tenant variant overrides over the instance-level variants by variant_id.
//
//   - Empty override → return base unchanged (inherit all).
//   - Empty base → return override as-is.
//   - Matching variant_id → the tenant entry fully replaces the base entry.
//   - New variant_id in override → appended after base variants (in override order).
//   - Base variants with no matching override → kept as-is.
func mergeVariants(base, override []types.PoolVariant) []types.PoolVariant {
	if len(override) == 0 {
		return base
	}
	if len(base) == 0 {
		return override
	}

	overrideByID := make(map[string]types.PoolVariant, len(override))
	overrideOrder := make([]string, 0, len(override))
	for i := range override {
		id := override[i].VariantID
		overrideByID[id] = override[i]
		overrideOrder = append(overrideOrder, id)
	}

	seen := make(map[string]bool, len(override))
	out := make([]types.PoolVariant, 0, len(base)+len(override))
	for i := range base {
		id := base[i].VariantID
		if ov, ok := overrideByID[id]; ok {
			out = append(out, ov)
			seen[id] = true
			continue
		}
		out = append(out, base[i])
	}
	for _, id := range overrideOrder {
		if seen[id] {
			continue
		}
		out = append(out, overrideByID[id])
		seen[id] = true
	}
	return out
}
