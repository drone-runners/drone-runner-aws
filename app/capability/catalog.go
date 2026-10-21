// Package capability groups pools that can serve the same request (same tier, platform,
// variant and tenant) so warm capacity can be predicted, scaled and claimed across pools.
package capability

import (
	"fmt"
	"slices"
	"strings"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Pool is a pool that participates in capability scheduling.
type Pool struct {
	Name   string
	Driver string
	Tier   string
	OS     string
	Arch   string
	// Order is the pool's position in the pool file; candidates are tried in this order.
	Order int
	// Tenants are the pool's resolved tenants; the first is always the default tenant.
	Tenants []Tenant
}

// Tenant is one tenant of a pool with its warm-pool sizing.
type Tenant struct {
	ID       string
	MinSize  int
	Limit    int
	Variants []types.PoolVariant
}

// Member is one pool's slot in a capability group.
type Member struct {
	Pool    string
	Order   int
	MinSize int
	Limit   int
	// Variant is nil for the pool's base (default variant) configuration.
	Variant *types.PoolVariant
}

// Group is the ordered set of pools that serve one capability key.
type Group struct {
	Key     types.CapabilityKey
	Members []Member
}

// Catalog is the capability view of a pool file, limited to the configured prediction drivers.
type Catalog struct {
	pools  map[string]*Pool
	order  []*Pool
	groups []Group
	index  map[types.CapabilityKey]int
}

// PlatformFunc returns the effective platform of a pool (after defaults are applied).
type PlatformFunc func(pool string) types.Platform

// NewCatalog builds a catalog from the pool file. Pools whose driver is not in drivers are
// excluded (empty drivers means types.DefaultPredictionDrivers). platformOf may be nil, in
// which case the pool file platform is used, defaulting to linux/amd64.
func NewCatalog(poolFile *config.PoolFile, drivers []string, platformOf PlatformFunc) (*Catalog, error) {
	c := &Catalog{
		pools: map[string]*Pool{},
		index: map[types.CapabilityKey]int{},
	}
	if poolFile == nil {
		return c, nil
	}
	supported := DriverSet(drivers)

	for i := range poolFile.Instances {
		inst := &poolFile.Instances[i]
		if !supported[strings.ToLower(inst.Type)] {
			continue
		}
		tier, err := types.ParseTier(inst.Tier)
		if err != nil {
			return nil, fmt.Errorf("pool %q: %w", inst.Name, err)
		}
		platform := inst.Platform
		if platformOf != nil {
			if p := platformOf(inst.Name); p.OS != "" || p.Arch != "" {
				platform = p
			}
		}
		resolved, _, err := config.ResolveTenants(inst)
		if err != nil {
			return nil, err
		}

		p := &Pool{
			Name:   inst.Name,
			Driver: inst.Type,
			Tier:   tier,
			OS:     defaultIfEmpty(platform.OS, "linux"),
			Arch:   defaultIfEmpty(platform.Arch, "amd64"),
			Order:  len(c.order),
		}
		for j := range resolved {
			rt := &resolved[j]
			p.Tenants = append(p.Tenants, Tenant{ID: rt.ID, MinSize: rt.Pool, Limit: rt.Limit, Variants: rt.Variants})
		}
		c.pools[p.Name] = p
		c.order = append(c.order, p)

		baseVariant := defaultIfEmpty(inst.VariantID, types.DefaultVariantID)
		for j := range p.Tenants {
			t := &p.Tenants[j]
			baseKey := p.key(baseVariant, t.ID)
			c.add(&baseKey, Member{Pool: p.Name, Order: p.Order, MinSize: t.MinSize, Limit: t.Limit})
			for k := range t.Variants {
				v := &t.Variants[k]
				variantKey := p.key(defaultIfEmpty(v.VariantID, types.DefaultVariantID), t.ID)
				c.add(&variantKey, Member{Pool: p.Name, Order: p.Order, MinSize: v.Pool, Limit: v.Limit, Variant: v})
			}
		}
	}
	return c, nil
}

func (c *Catalog) add(key *types.CapabilityKey, m Member) {
	idx, ok := c.index[*key]
	if !ok {
		idx = len(c.groups)
		c.index[*key] = idx
		c.groups = append(c.groups, Group{Key: *key})
	}
	c.groups[idx].Members = append(c.groups[idx].Members, m)
}

func (p *Pool) key(variantID, tenantID string) types.CapabilityKey {
	return types.CapabilityKey{Tier: p.Tier, OS: p.OS, Arch: p.Arch, VariantID: variantID, TenantID: tenantID}
}

// Pool returns the pool by name; ok is false for unknown or unsupported-driver pools.
func (c *Catalog) Pool(name string) (*Pool, bool) {
	p, ok := c.pools[name]
	return p, ok
}

// Pools returns the supported pools in pool-file order.
func (c *Catalog) Pools() []*Pool {
	return c.order
}

// Groups returns all capability groups in first-seen pool-file order.
func (c *Catalog) Groups() []Group {
	return c.groups
}

// Group returns the group for a key.
func (c *Catalog) Group(key *types.CapabilityKey) (Group, bool) {
	idx, ok := c.index[*key]
	if !ok {
		return Group{}, false
	}
	return c.groups[idx], true
}

// GroupByName returns the group whose key String() is name.
func (c *Catalog) GroupByName(name string) (Group, bool) {
	for i := range c.groups {
		if c.groups[i].Key.String() == name {
			return c.groups[i], true
		}
	}
	return Group{}, false
}

// KeyFor returns the capability key of an instance in pool with the given tenant and variant.
// ok is false when the pool is not in the catalog.
func (c *Catalog) KeyFor(pool, tenantID, variantID string) (types.CapabilityKey, bool) {
	p, ok := c.pools[pool]
	if !ok {
		return types.CapabilityKey{}, false
	}
	return p.key(defaultIfEmpty(variantID, types.DefaultVariantID), defaultIfEmpty(tenantID, types.DefaultTenantID)), true
}

// Platform returns the tier, os and arch of pool; ok is false when the pool is not in the catalog.
func (c *Catalog) Platform(pool string) (tier, os, arch string, ok bool) {
	p, ok := c.pools[pool]
	if !ok {
		return "", "", "", false
	}
	return p.Tier, p.OS, p.Arch, true
}

// PlatformPools returns the pools of the tier, os and arch in pool-file order.
func (c *Catalog) PlatformPools(tier, os, arch string) []string {
	var pools []string
	for _, p := range c.order {
		if p.Tier == tier && p.OS == os && p.Arch == arch {
			pools = append(pools, p.Name)
		}
	}
	return pools
}

// ClaimPools returns the tier of pool and the pools, in pool-file order, of the group serving
// pool with the tenant and variant. ok is false when pool is not a member of that group.
func (c *Catalog) ClaimPools(pool, tenantID, variantID string) (tier string, pools []string, ok bool) {
	key, ok := c.KeyFor(pool, tenantID, variantID)
	if !ok {
		return "", nil, false
	}
	group, ok := c.Group(&key)
	if !ok {
		return "", nil, false
	}
	member := false
	for _, m := range group.Members {
		if !slices.Contains(pools, m.Pool) {
			pools = append(pools, m.Pool)
		}
		member = member || m.Pool == pool
	}
	if !member {
		return "", nil, false
	}
	return key.Tier, pools, true
}

// DriverSet normalizes a driver list into a lookup set, falling back to the default drivers.
func DriverSet(drivers []string) map[string]bool {
	set := map[string]bool{}
	for _, d := range drivers {
		if d = strings.ToLower(strings.TrimSpace(d)); d != "" {
			set[d] = true
		}
	}
	if len(set) == 0 {
		for _, d := range types.DefaultPredictionDrivers {
			set[d] = true
		}
	}
	return set
}

func defaultIfEmpty(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
