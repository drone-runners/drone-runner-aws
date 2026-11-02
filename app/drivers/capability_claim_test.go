package drivers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

type fakeCatalogPool struct {
	name, tier, os, arch string
}

type fakeCapabilityCatalog struct {
	pools  []fakeCatalogPool
	groups map[string][]string // tenant/variant -> pools
}

func (c *fakeCapabilityCatalog) Platform(pool string) (tier, os, arch string, ok bool) {
	for _, p := range c.pools {
		if p.name == pool {
			return p.tier, p.os, p.arch, true
		}
	}
	return "", "", "", false
}

func (c *fakeCapabilityCatalog) PlatformPools(tier, os, arch string) []string {
	var out []string
	for _, p := range c.pools {
		if p.tier == tier && p.os == os && p.arch == arch {
			out = append(out, p.name)
		}
	}
	return out
}

func (c *fakeCapabilityCatalog) ClaimPools(pool, tenantID, variantID string) (tier string, pools []string, ok bool) {
	pools, ok = c.groups[tenantID+"/"+variantID]
	if !ok {
		return "", nil, false
	}
	for _, p := range pools {
		if p == pool {
			tier, _, _, _ = c.Platform(pool)
			return tier, pools, true
		}
	}
	return "", nil, false
}

type fakeOutboxStore struct {
	jobs []*types.OutboxJob
}

func (s *fakeOutboxStore) Create(_ context.Context, job *types.OutboxJob) error {
	s.jobs = append(s.jobs, job)
	return nil
}

func (s *fakeOutboxStore) FindAndClaimPending(context.Context, string, []types.OutboxJobType, int, time.Duration) ([]*types.OutboxJob, error) {
	return nil, nil
}
func (s *fakeOutboxStore) UpdateStatus(context.Context, int64, types.OutboxJobStatus, string) error {
	return nil
}
func (s *fakeOutboxStore) Delete(context.Context, int64) error                   { return nil }
func (s *fakeOutboxStore) DeleteOlderThan(context.Context, int64) (int64, error) { return 0, nil }
func (s *fakeOutboxStore) FindScaleJobForWindow(context.Context, string, int64) (*types.OutboxJob, error) {
	return nil, nil
}
func (s *fakeOutboxStore) CountSetupJobs(context.Context, string, string, string) (int, error) {
	return 0, nil
}

func largeVariant(machineType string, diskSize int64) types.PoolVariant {
	return types.PoolVariant{SetupInstanceParams: types.SetupInstanceParams{
		VariantID: "large", ResourceClass: "large", MachineType: machineType, DiskSize: diskSize,
	}}
}

// newCapabilityTestManager has paid linux/amd64 pools pool1 and pool2 serving variant "large",
// pool3 and pool4 placing account "acct" in tenant "t1", pool5 placing it in tenant "t2", free
// pool free1, and arm1, a paid linux/arm64 pool with no free counterpart.
func newCapabilityTestManager(instances *mockInstanceStore) (*DistributedManager, *fakeOutboxStore, *fakePurgerMetrics) {
	newPool := func(name string, variants ...types.PoolVariant) *poolEntry {
		return &poolEntry{Pool: Pool{Name: name, Driver: &flexibleMockDriver{}, PoolVariants: variants}}
	}
	tenantPool := func(name, tenant, machineType string) *poolEntry {
		p := newPool(name, largeVariant(machineType, 0))
		p.AccountToTenant = map[string]string{"acct": tenant}
		return p
	}
	outbox := &fakeOutboxStore{}
	metrics := &fakePurgerMetrics{}
	d := &DistributedManager{
		Manager: Manager{
			poolMap: map[string]*poolEntry{
				"pool1":  newPool("pool1", largeVariant("c4d-standard-8", 100)),
				"pool2":  newPool("pool2", largeVariant("c4d-standard-8-lssd", 200)),
				"pool3":  tenantPool("pool3", "t1", "c4d-standard-8"),
				"pool4":  tenantPool("pool4", "t1", "n2-standard-8"),
				"pool5":  tenantPool("pool5", "t2", "c4d-standard-8"),
				"free1":  newPool("free1"),
				"arm1":   newPool("arm1"),
				"mapped": newPool("mapped", largeVariant("c4d-standard-8", 100)),
			},
			instanceStore: instances,
			runnerName:    "test-runner",
			metrics:       metrics,
		},
		outboxStore: outbox,
	}
	d.SetCapabilityCatalog(&fakeCapabilityCatalog{
		pools: []fakeCatalogPool{
			{"pool1", types.TierPaid, "linux", "amd64"},
			{"free1", types.TierFree, "linux", "amd64"},
			{"pool2", types.TierPaid, "linux", "amd64"},
			{"pool3", types.TierPaid, "linux", "amd64"},
			{"pool5", types.TierPaid, "linux", "amd64"},
			{"pool4", types.TierPaid, "linux", "amd64"},
			{"arm1", types.TierPaid, "linux", "arm64"},
		},
		groups: map[string][]string{
			"default/large":   {"pool1", "pool2", "pool3", "pool4"},
			"default/default": {"pool1", "free1", "pool2", "pool3", "pool4"},
		},
	}, types.SchedulerAlgorithmCapability)
	return d, outbox, metrics
}

func TestClaimScope(t *testing.T) {
	d, _, _ := newCapabilityTestManager(&mockInstanceStore{})
	amd64 := types.Platform{OS: "linux", Arch: "amd64"}
	only := func(pools ...string) *types.ClaimScope {
		return &types.ClaimScope{Pools: pools, Include: pools, Platform: amd64}
	}
	mapTo := func(from, to string) func(string) string {
		return func(p string) string {
			if p == from {
				return to
			}
			return p
		}
	}

	cases := []struct {
		name    string
		pool    string
		params  *types.ProvisionParams
		mapPool func(string) string
		want    *types.ClaimScope
	}{
		{"pool map limits the claim to the mapped pool", "pool1",
			&types.ProvisionParams{AccountID: "acct"}, mapTo("pool1", "mapped"), only("mapped")},
		{"tenant account is limited to the pools of the first tenant it is placed in", "pool1",
			&types.ProvisionParams{AccountID: "acct"}, mapTo("pool9", "mapped"), only("pool3", "pool4")},
		{"tenant account on a platform without its tenant uses the default tenant", "arm1",
			&types.ProvisionParams{AccountID: "acct"}, nil,
			&types.ClaimScope{Pools: []string{"arm1"}, Platform: types.Platform{OS: "linux", Arch: "arm64"}}},
		{"free account is limited to the free pools", "pool2",
			&types.ProvisionParams{AccountID: types.FreeAccountID}, nil, only("free1")},
		{"request to a free pool is limited to the free pools", "free1",
			&types.ProvisionParams{AccountID: "other"}, nil, only("free1")},
		{"free account without free pools uses the default tenant", "arm1",
			&types.ProvisionParams{AccountID: types.FreeAccountID}, nil,
			&types.ClaimScope{Pools: []string{"arm1"}, Platform: types.Platform{OS: "linux", Arch: "arm64"}}},
		{"default tenant claims anywhere but the free pools and creates in the group", "pool2",
			&types.ProvisionParams{AccountID: "other", ResourceClass: "large"}, nil,
			&types.ClaimScope{Pools: []string{"pool2", "pool1", "pool3", "pool4"}, Exclude: []string{"free1"}, Platform: amd64}},
		{"default variant group skips the free pools for create", "pool1",
			&types.ProvisionParams{AccountID: "other"}, nil,
			&types.ClaimScope{Pools: []string{"pool1", "pool2", "pool3", "pool4"}, Exclude: []string{"free1"}, Platform: amd64}},
		{"pool outside the catalog", "mapped", &types.ProvisionParams{AccountID: "other"}, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, d.ClaimScope(context.Background(), tc.pool, tc.params, tc.mapPool))
		})
	}

	assert.Nil(t, d.ClaimScope(context.Background(), "pool1", nil, nil), "nil params")

	d.SetCapabilityCatalog(d.capabilityCatalog, types.SchedulerAlgorithmPool)
	assert.Nil(t, d.ClaimScope(context.Background(), "pool1", &types.ProvisionParams{}, nil),
		"pool algorithm keeps per-pool provisioning")

	d.SetCapabilityCatalog(nil, types.SchedulerAlgorithmCapability)
	assert.Nil(t, d.ClaimScope(context.Background(), "pool1", &types.ProvisionParams{}, nil), "no catalog")
}

// realCatalogPools has google primary and fallback pools and an AWS pool serving variant
// "large", a tenant on the google pools, a free pool, an arm64 pool and a windows pool.
const realCatalogPools = `
version: "1"
instances:
  - name: linux-amd64
    type: google
    platform: {os: linux, arch: amd64}
    spec: {}
    variants:
      - {variant_id: large, resource_class: large, machine_type: c4d-standard-8}
    tenants:
      - {ids: [acct-t], group_id: t1}
  - name: linux-amd64-fallback
    type: google
    platform: {os: linux, arch: amd64}
    spec: {}
    variants:
      - {variant_id: large, resource_class: large, machine_type: c4d-standard-8-lssd}
    tenants:
      - {ids: [acct-t], group_id: t1}
  - name: linux-amd64-free
    type: google
    tier: free
    platform: {os: linux, arch: amd64}
    spec: {}
  - name: linux-amd64-aws
    type: amazon
    platform: {os: linux, arch: amd64}
    spec: {}
    variants:
      - {variant_id: large, resource_class: large, machine_type: m7i.2xlarge}
  - name: linux-arm64
    type: google
    platform: {os: linux, arch: arm64}
    spec: {}
  - name: windows-amd64
    type: google
    platform: {os: windows, arch: amd64}
    spec: {}
`

// newRealCatalogManager builds the catalog from realCatalogPools for the given drivers, with
// pool entries shaped as BuildPools leaves them.
func newRealCatalogManager(t *testing.T, catalogDrivers ...string) *DistributedManager {
	t.Helper()
	pf, err := config.Parse(strings.NewReader(realCatalogPools))
	require.NoError(t, err)
	catalog, err := capability.NewCatalog(pf, catalogDrivers, nil)
	require.NoError(t, err)

	poolMap := map[string]*poolEntry{}
	for i := range pf.Instances {
		inst := &pf.Instances[i]
		p := &poolEntry{Pool: Pool{Name: inst.Name, Driver: &flexibleMockDriver{}, PoolVariants: inst.Variants}}
		for _, tenant := range inst.Tenants {
			p.AccountToTenant = map[string]string{}
			for _, id := range tenant.IDs {
				p.AccountToTenant[id] = tenant.GroupID
			}
		}
		poolMap[inst.Name] = p
	}
	d := &DistributedManager{Manager: Manager{poolMap: poolMap}}
	d.SetCapabilityCatalog(catalog, types.SchedulerAlgorithmCapability)
	return d
}

func TestClaimScope_RealCatalog(t *testing.T) {
	d := newRealCatalogManager(t, "google", "amazon")
	amd64 := types.Platform{OS: "linux", Arch: "amd64"}
	paidAmd64 := func(pools ...string) *types.ClaimScope {
		return &types.ClaimScope{Pools: pools, Exclude: []string{"linux-amd64-free"}, Platform: amd64}
	}
	only := func(pools ...string) *types.ClaimScope {
		return &types.ClaimScope{Pools: pools, Include: pools, Platform: amd64}
	}

	cases := []struct {
		name    string
		pool    string
		account string
		class   string
		want    *types.ClaimScope
	}{
		{"paid account claims across google and AWS pools, never free", "linux-amd64", "acct-x", "large",
			paidAmd64("linux-amd64", "linux-amd64-fallback", "linux-amd64-aws")},
		{"request to the AWS pool creates there first", "linux-amd64-aws", "acct-x", "large",
			paidAmd64("linux-amd64-aws", "linux-amd64", "linux-amd64-fallback")},
		{"unmatched resource class uses the default variant group", "linux-amd64", "acct-x", "xlarge",
			paidAmd64("linux-amd64", "linux-amd64-fallback", "linux-amd64-aws")},
		{"tenant account is limited to its tenant's pools", "linux-amd64-aws", "acct-t", "large",
			only("linux-amd64", "linux-amd64-fallback")},
		{"tenant account on arm64 has no tenant there", "linux-arm64", "acct-t", "",
			&types.ClaimScope{Pools: []string{"linux-arm64"}, Platform: types.Platform{OS: "linux", Arch: "arm64"}}},
		{"free account is limited to the free pool", "linux-amd64", types.FreeAccountID, "large",
			only("linux-amd64-free")},
		{"paid account requesting the free pool is limited to it", "linux-amd64-free", "acct-x", "",
			only("linux-amd64-free")},
		{"windows request stays on windows", "windows-amd64", "acct-x", "",
			&types.ClaimScope{Pools: []string{"windows-amd64"}, Platform: types.Platform{OS: "windows", Arch: "amd64"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.ClaimScope(context.Background(), tc.pool, &types.ProvisionParams{AccountID: tc.account, ResourceClass: tc.class}, nil)
			assert.Equal(t, tc.want, got)
		})
	}

	googleOnly := newRealCatalogManager(t, "google")
	assert.Nil(t, googleOnly.ClaimScope(context.Background(), "linux-amd64-aws", &types.ProvisionParams{AccountID: "acct-x"}, nil),
		"AWS pool outside the prediction drivers keeps per-pool provisioning")
	assert.Equal(t, paidAmd64("linux-amd64", "linux-amd64-fallback"),
		googleOnly.ClaimScope(context.Background(), "linux-amd64", &types.ProvisionParams{AccountID: "acct-x", ResourceClass: "large"}, nil),
		"AWS pool outside the catalog is not a create target")
}

func TestApplyClaimScope(t *testing.T) {
	amd64 := types.Platform{OS: "linux", Arch: "amd64"}
	cases := []struct {
		name  string
		scope *types.ClaimScope
		want  types.QueryParams
	}{
		{"nil scope leaves the per-pool query", nil,
			types.QueryParams{PoolName: "pool1", MachineType: "m1"}},
		{"default tenant claims from any pool but the excluded ones",
			&types.ClaimScope{Pools: []string{"pool1"}, Exclude: []string{"free1"}, Platform: amd64},
			types.QueryParams{PoolName: "pool1", AnyPool: true, ExcludePoolNames: []string{"free1"}, Platform: &amd64}},
		{"included pools replace the pool and drop the machine type",
			&types.ClaimScope{Pools: []string{"pool3", "pool4"}, Include: []string{"pool3", "pool4"}, Platform: amd64},
			types.QueryParams{PoolName: "pool1", PoolNames: []string{"pool3", "pool4"}, Platform: &amd64}},
		{"scope of only the provisioned pool keeps the machine type",
			&types.ClaimScope{Pools: []string{"pool1"}, Include: []string{"pool1"}, Platform: amd64},
			types.QueryParams{PoolName: "pool1", PoolNames: []string{"pool1"}, MachineType: "m1", Platform: &amd64}},
		{"scope of another single pool drops the machine type",
			&types.ClaimScope{Pools: []string{"mapped"}, Include: []string{"mapped"}, Platform: amd64},
			types.QueryParams{PoolName: "pool1", PoolNames: []string{"mapped"}, Platform: &amd64}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query := types.QueryParams{PoolName: "pool1", MachineType: "m1"}
			applyClaimScope(&query, tc.scope, "pool1")
			assert.Equal(t, tc.want, query)
		})
	}
}

func TestRecordClaim(t *testing.T) {
	d, _, metrics := newCapabilityTestManager(&mockInstanceStore{})
	d.recordClaim("pool1", &types.Instance{Pool: "pool2", IsHibernated: true})
	d.recordClaim("free1", &types.Instance{Pool: "free1"})
	d.recordClaim("pool9", nil)
	assert.Equal(t, []hotpoolClaimRecord{
		{"capability", types.TierPaid, "pool1", "pool2", ClaimOutcomeHibernated},
		{"capability", types.TierFree, "free1", "free1", ClaimOutcomeWarm},
		{"capability", "", "pool9", "", ClaimOutcomeMiss},
	}, metrics.claims)

	d.metrics = nil
	assert.NotPanics(t, func() { d.recordClaim("pool1", nil) })
}

func TestVariantSetupParams(t *testing.T) {
	d, _, _ := newCapabilityTestManager(&mockInstanceStore{})
	got := variantSetupParams(d.poolMap["pool2"], &types.Instance{VariantID: "large", TenantID: types.DefaultTenantID})
	assert.Equal(t, "c4d-standard-8-lssd", got.MachineType)
	assert.Equal(t, int64(200), got.DiskSize)

	assert.Empty(t, variantSetupParams(d.poolMap["pool2"], &types.Instance{VariantID: "unknown"}).MachineType,
		"unknown variant keeps the pool's base settings")
}

func TestProvisionFromPool_TenantScopeClaimsFromAnotherTenantPool(t *testing.T) {
	claimed := &types.Instance{
		ID: "inst-4", Pool: "pool4", RunnerName: "test-runner", Size: "n2-standard-8",
		State: types.StateInUse, Source: types.InstanceSourcePool, VariantID: "large", TenantID: "t1",
	}
	instances := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, params *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			assert.False(t, params.AnyPool)
			assert.Equal(t, []string{"pool3", "pool4"}, params.PoolNames)
			assert.Empty(t, params.MachineType)
			assert.Equal(t, "t1", params.TenantID)
			return claimed, nil
		},
		UpdateFunc: func(context.Context, *types.Instance) error { return nil },
	}
	d, outbox, metrics := newCapabilityTestManager(instances)
	pool := d.poolMap["pool3"]
	scope := &types.ClaimScope{Pools: []string{"pool3", "pool4"}, Include: []string{"pool3", "pool4"}, Platform: types.Platform{OS: "linux", Arch: "amd64"}}

	inst, capacity, _, _, err := d.provisionFromPool(
		context.Background(), pool, "tls", "acct",
		&types.ProvisionParams{ResourceClass: "large", AccountID: "acct", Claim: scope},
		nil, nil, 60, pool.Name, nil, false,
	)

	require.NoError(t, err)
	assert.Equal(t, "inst-4", inst.ID)
	assert.Equal(t, "pool4", capacity.PoolName)
	require.Len(t, outbox.jobs, 1)
	assert.Equal(t, "pool4", outbox.jobs[0].PoolName)
	var params types.SetupInstanceParams
	require.NoError(t, json.Unmarshal(*outbox.jobs[0].JobParams, &params))
	assert.Equal(t, "n2-standard-8", params.MachineType, "replenish with the claimed pool's machine type")
	assert.Equal(t, "t1", params.TenantID)
	assert.Equal(t, []hotpoolClaimRecord{{"capability", types.TierPaid, "pool3", "pool4", ClaimOutcomeWarm}}, metrics.claims)
}

func TestProvisionFromPool_ClaimsFromAnyPoolInScope(t *testing.T) {
	claimed := &types.Instance{
		ID: "inst-2", Pool: "pool2", RunnerName: "test-runner", Size: "c4d-standard-8-lssd",
		State: types.StateInUse, Source: types.InstanceSourcePool, VariantID: "large", TenantID: types.DefaultTenantID,
	}
	instances := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, params *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			assert.True(t, params.AnyPool)
			assert.Nil(t, params.PoolNames)
			assert.Equal(t, []string{"free1"}, params.ExcludePoolNames)
			assert.Equal(t, &types.Platform{OS: "linux", Arch: "amd64"}, params.Platform)
			assert.Empty(t, params.MachineType, "pools may give the variant different machine types")
			assert.Equal(t, "large", params.VariantID)
			assert.Equal(t, types.DefaultTenantID, params.TenantID)
			return claimed, nil
		},
		UpdateFunc: func(context.Context, *types.Instance) error { return nil },
	}
	d, outbox, metrics := newCapabilityTestManager(instances)
	pool := d.poolMap["pool1"]
	scope := &types.ClaimScope{Pools: []string{"pool1"}, Exclude: []string{"free1"}, Platform: types.Platform{OS: "linux", Arch: "amd64"}}

	inst, capacity, warmed, _, err := d.provisionFromPool(
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{ResourceClass: "large", Claim: scope},
		nil, nil, 60, pool.Name, nil, false,
	)

	require.NoError(t, err)
	assert.True(t, warmed)
	assert.Equal(t, "inst-2", inst.ID)
	assert.Equal(t, "pool2", capacity.PoolName)

	require.Len(t, outbox.jobs, 1)
	assert.Equal(t, "pool2", outbox.jobs[0].PoolName, "replenish the pool the instance came from")
	var params types.SetupInstanceParams
	require.NoError(t, json.Unmarshal(*outbox.jobs[0].JobParams, &params))
	assert.Equal(t, "c4d-standard-8-lssd", params.MachineType)
	assert.Equal(t, int64(200), params.DiskSize)

	assert.Equal(t, []hotpoolClaimRecord{{"capability", types.TierPaid, "pool1", "pool2", ClaimOutcomeWarm}}, metrics.claims)
}

func TestProvisionFromPool_ScopeOfOwnPoolKeepsMachineTypeAndRecordsMiss(t *testing.T) {
	instances := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, params *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			assert.False(t, params.AnyPool)
			assert.Equal(t, []string{"pool1"}, params.PoolNames)
			assert.Equal(t, "c4d-standard-8", params.MachineType)
			return nil, sql.ErrNoRows
		},
	}
	d, _, metrics := newCapabilityTestManager(instances)
	pool := d.poolMap["pool1"]
	pool.Driver = &flexibleMockDriver{
		CreateFunc: func(context.Context, *types.InstanceCreateOpts) (*types.Instance, error) {
			return nil, errors.New("no capacity in test driver")
		},
	}

	_, _, _, _, err := d.provisionFromPool( //nolint:dogsled
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{ResourceClass: "large", Claim: &types.ClaimScope{Pools: []string{"pool1"}, Include: []string{"pool1"}}},
		nil, nil, 60, pool.Name, nil, false,
	)

	require.Error(t, err)
	assert.Equal(t, []hotpoolClaimRecord{{"capability", types.TierPaid, "pool1", "", ClaimOutcomeMiss}}, metrics.claims)
}

func TestProvisionFromPool_ScopeRecordsOneMissPerRequest(t *testing.T) {
	instances := &mockInstanceStore{
		FindAndClaimFunc: func(context.Context, *types.QueryParams, types.InstanceState, []types.InstanceState, bool) (*types.Instance, error) {
			return nil, sql.ErrNoRows
		},
	}
	d, _, metrics := newCapabilityTestManager(instances)
	scope := &types.ClaimScope{Pools: []string{"pool1", "pool2"}, Exclude: []string{"free1"}, Platform: types.Platform{OS: "linux", Arch: "amd64"}}
	failCreate := &flexibleMockDriver{
		CreateFunc: func(context.Context, *types.InstanceCreateOpts) (*types.Instance, error) {
			return nil, errors.New("no capacity in test driver")
		},
	}

	for _, name := range scope.Pools {
		pool := d.poolMap[name]
		pool.Driver = failCreate
		_, _, _, _, err := d.provisionFromPool( //nolint:dogsled
			context.Background(), pool, "tls", "owner1",
			&types.ProvisionParams{ResourceClass: "large", Claim: scope},
			nil, nil, 60, name, nil, false,
		)
		require.Error(t, err)
	}

	assert.Equal(t, []hotpoolClaimRecord{{"capability", types.TierPaid, "pool1", "", ClaimOutcomeMiss}}, metrics.claims)
}
