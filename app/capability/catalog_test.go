package capability

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

const egressTenant = "CbUXX5rlTcuIsV8aAOjmuw"

// prodLikePools mirrors the shape of the prod pool file: paid primary + fallback pools sharing
// variants and an egress tenant, a free pool, an arm64 pool without the tenant's variant, a mac
// (nomad) pool, and an AWS pool.
const prodLikePools = `
version: "1"
instances:
  - name: linux-amd64
    type: google
    pool: 10
    limit: 100
    platform: {os: linux, arch: amd64}
    spec: {image: base-img, machine_type: c4d-standard-4}
    variants:
      - {variant_id: variant_medium, pool: 5, limit: 50, machine_type: c4d-standard-8}
      - {variant_id: variant_medium_ubuntu24, pool: 1, machine_type: c4d-standard-8, image_name: ubuntu24-img}
    tenants:
      - ids: [` + egressTenant + `]
        pool: 2
        variants:
          - {variant_id: variant_medium, pool: 3}
  - name: linux-amd64-fallback
    type: google
    pool: 4
    limit: 40
    spec: {image: base-img, machine_type: c4d-standard-4}
    variants:
      - {variant_id: variant_medium, pool: 2, machine_type: c4d-standard-8-lssd}
    tenants:
      - ids: [` + egressTenant + `]
  - name: linux-amd64-free
    type: google
    tier: free
    pool: 6
    platform: {os: linux, arch: amd64}
    spec: {image: free-img}
    variants:
      - {variant_id: variant_medium, pool: 1}
  - name: linux-arm64
    type: google
    pool: 3
    platform: {os: linux, arch: arm64}
    spec: {image: arm-img}
  - name: macos-arm64
    type: nomad
    pool: 2
    platform: {os: darwin, arch: arm64}
    spec: {}
  - name: linux-amd64-aws
    type: amazon
    pool: 1
    platform: {os: linux, arch: amd64}
    spec:
      account: {region: us-east-1}
      ami: ami-1
      size: t3.large
`

func parsePools(t *testing.T) *config.PoolFile {
	t.Helper()
	pf, err := config.Parse(strings.NewReader(prodLikePools))
	if err != nil {
		t.Fatalf("parse pools: %v", err)
	}
	return pf
}

// platformOf mimics the pool manager, which applies platform defaults the pool file omits.
func platformOf(pool string) types.Platform {
	if pool == "linux-amd64-fallback" {
		return types.Platform{OS: "linux", Arch: "amd64"}
	}
	return types.Platform{}
}

func key(tier, arch, variant, tenant string) types.CapabilityKey {
	return types.CapabilityKey{Tier: tier, OS: "linux", Arch: arch, VariantID: variant, TenantID: tenant}
}

func ptr(k types.CapabilityKey) *types.CapabilityKey { return &k } //nolint:gocritic

func memberPools(g *Group) []string {
	var out []string
	for _, m := range g.Members {
		out = append(out, m.Pool)
	}
	return out
}

func TestNewCatalog_GroupsAcrossPoolsInFileOrder(t *testing.T) {
	c, err := NewCatalog(parsePools(t), nil, platformOf)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	cases := []struct {
		name string
		key  types.CapabilityKey
		want []string
	}{
		{"paid base", key(types.TierPaid, "amd64", "default", "default"),
			[]string{"linux-amd64", "linux-amd64-fallback", "linux-amd64-aws"}},
		{"paid variant across machine shapes", key(types.TierPaid, "amd64", "variant_medium", "default"),
			[]string{"linux-amd64", "linux-amd64-fallback"}},
		{"variant only in one pool", key(types.TierPaid, "amd64", "variant_medium_ubuntu24", "default"),
			[]string{"linux-amd64"}},
		{"egress tenant only in its pools", key(types.TierPaid, "amd64", "default", egressTenant),
			[]string{"linux-amd64", "linux-amd64-fallback"}},
		{"egress tenant variant", key(types.TierPaid, "amd64", "variant_medium", egressTenant),
			[]string{"linux-amd64", "linux-amd64-fallback"}},
		{"free kept separate", key(types.TierFree, "amd64", "default", "default"),
			[]string{"linux-amd64-free"}},
		{"free variant kept separate", key(types.TierFree, "amd64", "variant_medium", "default"),
			[]string{"linux-amd64-free"}},
		{"arm64 separate", key(types.TierPaid, "arm64", "default", "default"),
			[]string{"linux-arm64"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, ok := c.Group(&tc.key)
			if !ok {
				t.Fatalf("group %s not found", tc.key)
			}
			if got := memberPools(&g); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("members = %v, want %v", got, tc.want)
			}
		})
	}

	// The egress tenant has no arm64 pool, so it gets no arm64 group to borrow from.
	if _, ok := c.Group(ptr(key(types.TierPaid, "arm64", "default", egressTenant))); ok {
		t.Error("egress tenant must not have an arm64 group")
	}
}

func TestNewCatalog_MemberSizing(t *testing.T) {
	c, err := NewCatalog(parsePools(t), nil, platformOf)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	g, _ := c.Group(ptr(key(types.TierPaid, "amd64", "variant_medium", "default")))
	if m := g.Members[0]; m.MinSize != 5 || m.Limit != 50 || m.Variant == nil || m.Variant.MachineType != "c4d-standard-8" {
		t.Errorf("linux-amd64 variant_medium member = %+v", m)
	}
	if m := g.Members[1]; m.MinSize != 2 || m.Variant == nil || m.Variant.MachineType != "c4d-standard-8-lssd" {
		t.Errorf("fallback variant_medium member = %+v", m)
	}

	base, _ := c.Group(ptr(key(types.TierPaid, "amd64", "default", "default")))
	if m := base.Members[0]; m.MinSize != 10 || m.Limit != 100 || m.Variant != nil {
		t.Errorf("linux-amd64 base member = %+v", m)
	}

	tenantBase, _ := c.Group(ptr(key(types.TierPaid, "amd64", "default", egressTenant)))
	if m := tenantBase.Members[0]; m.MinSize != 2 {
		t.Errorf("tenant pool override not applied: %+v", m)
	}
	if m := tenantBase.Members[1]; m.MinSize != 4 {
		t.Errorf("tenant without override should inherit pool size: %+v", m)
	}

	tenantVariant, _ := c.Group(ptr(key(types.TierPaid, "amd64", "variant_medium", egressTenant)))
	if m := tenantVariant.Members[0]; m.MinSize != 3 {
		t.Errorf("tenant variant override not applied: %+v", m)
	}

	ubuntu, _ := c.Group(ptr(key(types.TierPaid, "amd64", "variant_medium_ubuntu24", "default")))
	if m := ubuntu.Members[0]; m.Variant == nil || m.Variant.ImageName != "ubuntu24-img" {
		t.Errorf("variant image should be kept on the member: %+v", m)
	}
}

func TestNewCatalog_ExcludesUnsupportedDrivers(t *testing.T) {
	c, err := NewCatalog(parsePools(t), nil, platformOf)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if _, ok := c.Pool("macos-arm64"); ok {
		t.Error("nomad pool must not be in the catalog")
	}
	for _, g := range c.Groups() {
		if g.Key.OS == "darwin" {
			t.Errorf("unexpected darwin group %s", g.Key)
		}
	}

	var names []string
	for _, p := range c.Pools() {
		names = append(names, p.Name)
	}
	want := []string{"linux-amd64", "linux-amd64-fallback", "linux-amd64-free", "linux-arm64", "linux-amd64-aws"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("pools = %v, want %v", names, want)
	}
}

func TestNewCatalog_DriverFilter(t *testing.T) {
	c, err := NewCatalog(parsePools(t), []string{" Amazon "}, platformOf)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if len(c.Pools()) != 1 || c.Pools()[0].Name != "linux-amd64-aws" {
		t.Fatalf("expected only the AWS pool, got %d pools", len(c.Pools()))
	}
	g, ok := c.Group(ptr(key(types.TierPaid, "amd64", "default", "default")))
	if !ok || !reflect.DeepEqual(memberPools(&g), []string{"linux-amd64-aws"}) {
		t.Errorf("AWS-only base group = %v", memberPools(&g))
	}
	if _, ok := c.Pool("linux-amd64"); ok {
		t.Error("google pool must be excluded when only amazon is configured")
	}
}

func TestNewCatalog_PlatformDefaults(t *testing.T) {
	c, err := NewCatalog(parsePools(t), nil, nil)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	p, ok := c.Pool("linux-amd64-fallback")
	if !ok {
		t.Fatal("fallback pool missing")
	}
	if p.OS != "linux" || p.Arch != "amd64" {
		t.Errorf("platform = %s/%s, want linux/amd64", p.OS, p.Arch)
	}
}

func TestCatalog_KeyFor(t *testing.T) {
	c, err := NewCatalog(parsePools(t), nil, platformOf)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	got, ok := c.KeyFor("linux-amd64-free", "", "")
	if !ok || got != key(types.TierFree, "amd64", "default", "default") {
		t.Errorf("KeyFor(free, '', '') = %v, %v", got, ok)
	}
	got, ok = c.KeyFor("linux-amd64-fallback", egressTenant, "variant_medium")
	if !ok || got != key(types.TierPaid, "amd64", "variant_medium", egressTenant) {
		t.Errorf("KeyFor(fallback, tenant, medium) = %v, %v", got, ok)
	}
	if _, ok := c.KeyFor("macos-arm64", "", ""); ok {
		t.Error("KeyFor must reject unsupported-driver pools")
	}
	if _, ok := c.KeyFor("unknown", "", ""); ok {
		t.Error("KeyFor must reject unknown pools")
	}
}

func TestCatalog_ClaimPools(t *testing.T) {
	c, err := NewCatalog(parsePools(t), nil, platformOf)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}

	cases := []struct {
		name                  string
		pool, tenant, variant string
		wantTier              string
		wantPools             []string
		wantOK                bool
	}{
		{"fallback pool sees the whole group in file order", "linux-amd64-fallback", "", "variant_medium",
			types.TierPaid, []string{"linux-amd64", "linux-amd64-fallback"}, true},
		{"tenant group", "linux-amd64", egressTenant, "", types.TierPaid, []string{"linux-amd64", "linux-amd64-fallback"}, true},
		{"free tier kept apart", "linux-amd64-free", "", "variant_medium", types.TierFree, []string{"linux-amd64-free"}, true},
		{"variant the pool does not define", "linux-amd64-fallback", "", "variant_medium_ubuntu24", "", nil, false},
		{"tenant the pool does not define", "linux-amd64-free", egressTenant, "", "", nil, false},
		{"unsupported driver", "macos-arm64", "", "", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tier, pools, ok := c.ClaimPools(tc.pool, tc.tenant, tc.variant)
			if ok != tc.wantOK || tier != tc.wantTier || !reflect.DeepEqual(pools, tc.wantPools) {
				t.Errorf("ClaimPools = %q, %v, %v; want %q, %v, %v", tier, pools, ok, tc.wantTier, tc.wantPools, tc.wantOK)
			}
		})
	}
}

func TestCatalog_PlatformAndPlatformPools(t *testing.T) {
	c, err := NewCatalog(parsePools(t), nil, platformOf)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if tier, os, arch, ok := c.Platform("linux-amd64-free"); !ok || tier != types.TierFree || os != "linux" || arch != "amd64" {
		t.Errorf("Platform(free) = %q %q %q %v", tier, os, arch, ok)
	}
	if _, _, _, ok := c.Platform("macos-arm64"); ok {
		t.Error("Platform must reject unsupported-driver pools")
	}
	if got := c.PlatformPools(types.TierPaid, "linux", "amd64"); !reflect.DeepEqual(got,
		[]string{"linux-amd64", "linux-amd64-fallback", "linux-amd64-aws"}) {
		t.Errorf("paid linux/amd64 pools = %v", got)
	}
	if got := c.PlatformPools(types.TierFree, "linux", "amd64"); !reflect.DeepEqual(got, []string{"linux-amd64-free"}) {
		t.Errorf("free linux/amd64 pools = %v", got)
	}
	if got := c.PlatformPools(types.TierFree, "linux", "arm64"); got != nil {
		t.Errorf("free linux/arm64 pools = %v, want none", got)
	}
}

func TestNewCatalog_NilPoolFile(t *testing.T) {
	c, err := NewCatalog(nil, nil, nil)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if len(c.Pools()) != 0 || len(c.Groups()) != 0 {
		t.Error("expected empty catalog")
	}
}

func TestDriverSet(t *testing.T) {
	if got := DriverSet(nil); !got["google"] || !got["amazon"] || len(got) != 2 {
		t.Errorf("default driver set = %v", got)
	}
	if got := DriverSet([]string{"", " "}); len(got) != 2 {
		t.Errorf("blank entries should fall back to defaults, got %v", got)
	}
	if got := DriverSet([]string{"Amazon"}); !got["amazon"] || got["google"] {
		t.Errorf("explicit driver set = %v", got)
	}
}
