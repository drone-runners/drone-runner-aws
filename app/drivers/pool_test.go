package drivers

import (
	"context"
	"testing"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestPool_ResolveTenant(t *testing.T) {
	p := &Pool{
		Name: "aws",
		AccountToTenant: map[string]string{
			"acctA": "acctA",
			"acctB": "acctA",
		},
		TenantDrivers: map[string]Driver{
			"default": nil,
			"acctA":   nil,
		},
	}

	if got := p.ResolveTenant("acctA"); got != "acctA" {
		t.Errorf("acctA: expected acctA, got %q", got)
	}
	if got := p.ResolveTenant("acctB"); got != "acctA" {
		t.Errorf("acctB: expected acctA, got %q", got)
	}
	if got := p.ResolveTenant("unknown"); got != types.DefaultTenantID {
		t.Errorf("unknown: expected default, got %q", got)
	}
}

func TestPool_ResolveTenant_SingleTenant(t *testing.T) {
	p := &Pool{Name: "aws"}
	if got := p.ResolveTenant("anything"); got != types.DefaultTenantID {
		t.Errorf("expected default tenant for single-tenant pool, got %q", got)
	}
	if p.IsMultiTenant() {
		t.Errorf("expected single-tenant pool")
	}
}

func TestPool_DriverForTenant(t *testing.T) {
	da := &mockDriver{}
	db := &mockDriver{}
	def := &mockDriver{}

	p := &Pool{
		Name:   "aws",
		Driver: def,
		TenantDrivers: map[string]Driver{
			"default": def,
			"acctA":   da,
			"acctB":   db,
		},
	}

	if p.DriverForTenant("acctA") != da {
		t.Errorf("expected acctA driver")
	}
	if p.DriverForTenant("acctB") != db {
		t.Errorf("expected acctB driver")
	}
	// Unknown tenant falls back to the default tenant driver.
	if p.DriverForTenant("acctZ") != def {
		t.Errorf("expected default tenant driver for unknown tenant")
	}
}

func TestPool_DriverForTenant_SingleTenantFallback(t *testing.T) {
	def := &mockDriver{}
	p := &Pool{Name: "aws", Driver: def}
	if p.DriverForTenant("acctA") != def {
		t.Errorf("expected pool driver for single-tenant pool")
	}
	if p.DriverForTenant(types.DefaultTenantID) != def {
		t.Errorf("expected pool driver for default tenant on single-tenant pool")
	}
}

func TestPool_VariantsForTenant(t *testing.T) {
	baseVariants := []types.PoolVariant{{
		SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large", MachineType: "c4d-standard-8-lssd"},
	}}
	tenantVariants := []types.PoolVariant{{
		SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large", MachineType: "c4d-standard-8"},
	}}

	p := &Pool{
		Name:         "gcp",
		PoolVariants: baseVariants,
		AccountToTenant: map[string]string{
			"acctA": "acctA",
		},
		TenantDrivers: map[string]Driver{
			types.DefaultTenantID: &mockDriver{},
			"acctA":               &mockDriver{},
		},
		Tenants: []TenantPool{
			{ID: types.DefaultTenantID, PoolVariants: baseVariants},
			{ID: "acctA", PoolVariants: tenantVariants},
		},
	}

	got := p.VariantsForTenant("acctA")
	if len(got) != 1 || got[0].MachineType != "c4d-standard-8" {
		t.Errorf("acctA: expected tenant machine_type c4d-standard-8, got %+v", got)
	}
	got = p.VariantsForTenant(types.DefaultTenantID)
	if len(got) != 1 || got[0].MachineType != "c4d-standard-8-lssd" {
		t.Errorf("default: expected base machine_type, got %+v", got)
	}
	got = p.VariantsForTenant("unknown")
	if len(got) != 1 || got[0].MachineType != "c4d-standard-8-lssd" {
		t.Errorf("unknown: expected default tenant variants, got %+v", got)
	}
	got = p.VariantsForTenant("")
	if len(got) != 1 || got[0].MachineType != "c4d-standard-8-lssd" {
		t.Errorf("empty tenantID: expected default tenant variants, got %+v", got)
	}
}

func TestPool_VariantsForTenant_SingleTenant(t *testing.T) {
	variants := []types.PoolVariant{{
		SetupInstanceParams: types.SetupInstanceParams{VariantID: "v1", MachineType: "t3.large"},
	}}
	p := &Pool{Name: "aws", PoolVariants: variants}
	got := p.VariantsForTenant("anything")
	if len(got) != 1 || got[0].MachineType != "t3.large" {
		t.Errorf("expected pool variants, got %+v", got)
	}
}

func TestDestroyByTenant_RoutesToTenantDrivers(t *testing.T) {
	var destroyedDefault, destroyedAcctA int
	def := &flexibleMockDriver{
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			destroyedDefault += len(instances)
			return nil, nil
		},
	}
	acct := &flexibleMockDriver{
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			destroyedAcctA += len(instances)
			return nil, nil
		},
	}
	p := &Pool{
		Name:   "gcp",
		Driver: def,
		TenantDrivers: map[string]Driver{
			types.DefaultTenantID: def,
			"acctA":               acct,
		},
	}
	instances := []*types.Instance{
		{ID: "1", TenantID: types.DefaultTenantID},
		{ID: "2", TenantID: "acctA"},
		{ID: "3", TenantID: "acctA"},
		{ID: "4", TenantID: ""}, // empty → default
	}
	failed, err := destroyByTenant(context.Background(), p, instances)
	if err != nil {
		t.Fatalf("destroyByTenant: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("unexpected failed: %v", failed)
	}
	if destroyedDefault != 2 {
		t.Errorf("default driver: destroyed %d want 2", destroyedDefault)
	}
	if destroyedAcctA != 2 {
		t.Errorf("acctA driver: destroyed %d want 2", destroyedAcctA)
	}
}

// TestPool_DriverForTenant_MemberIDShim verifies that instance rows stamped with a member
// account id (from before the tenant switched to an explicit group_id) still resolve to the
// group driver via the AccountToTenant map.
func TestPool_DriverForTenant_MemberIDShim(t *testing.T) {
	grp := &mockDriver{}
	def := &mockDriver{}
	p := &Pool{
		Name:   "gcp",
		Driver: def,
		AccountToTenant: map[string]string{
			"acctA": "group-egress",
			"acctB": "group-egress",
		},
		TenantDrivers: map[string]Driver{
			types.DefaultTenantID: def,
			"group-egress":        grp,
		},
	}
	if got := p.DriverForTenant("group-egress"); got != grp {
		t.Errorf("group id: expected group driver, got %v", got)
	}
	for _, member := range []string{"acctA", "acctB"} {
		if got := p.DriverForTenant(member); got != grp {
			t.Errorf("member %q: expected group driver via shim, got %v", member, got)
		}
	}
	// Unknown tenants keep the lenient fallback to the default driver (non-destroy call sites).
	if got := p.DriverForTenant("nobody"); got != def {
		t.Errorf("unknown tenant: expected default driver fallback, got %v", got)
	}
}

// TestDestroyByTenant_MemberIDShim is the incident regression test: rows stamped with a member
// account id (e.g. CbUX when it was ids[0]) must be destroyed by the group driver after the
// tenant is renamed to a group_id.
func TestDestroyByTenant_MemberIDShim(t *testing.T) {
	var destroyedGroup, destroyedDefault int
	grp := &flexibleMockDriver{
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			destroyedGroup += len(instances)
			return nil, nil
		},
	}
	def := &flexibleMockDriver{
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			destroyedDefault += len(instances)
			return nil, nil
		},
	}
	p := &Pool{
		Name:   "gcp",
		Driver: def,
		AccountToTenant: map[string]string{
			"acctA": "group-egress",
			"acctB": "group-egress",
		},
		TenantDrivers: map[string]Driver{
			types.DefaultTenantID: def,
			"group-egress":        grp,
		},
	}
	instances := []*types.Instance{
		{ID: "stale-1", TenantID: "acctA"}, // stamped before the group_id rename
		{ID: "stale-2", TenantID: "acctB"},
		{ID: "cur-1", TenantID: "group-egress"},
	}
	failed, err := destroyByTenant(context.Background(), p, instances)
	if err != nil {
		t.Fatalf("destroyByTenant: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("unexpected failed: %v", failed)
	}
	if destroyedGroup != 3 {
		t.Errorf("group driver: destroyed %d, want 3", destroyedGroup)
	}
	if destroyedDefault != 0 {
		t.Errorf("default driver must not be used for tenant rows, destroyed %d", destroyedDefault)
	}
}

// TestDestroyByTenant_UnknownTenantFallsBackToDefault: rows whose tenant is no longer in the
// config fall back to the default tenant's driver (lenient, matching DriverForTenant).
func TestDestroyByTenant_UnknownTenantFallsBackToDefault(t *testing.T) {
	var destroyedDefault, destroyedGroup int
	def := &flexibleMockDriver{
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			destroyedDefault += len(instances)
			return nil, nil
		},
	}
	grp := &flexibleMockDriver{
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			destroyedGroup += len(instances)
			return nil, nil
		},
	}
	p := &Pool{
		Name:   "gcp",
		Driver: def,
		TenantDrivers: map[string]Driver{
			types.DefaultTenantID: def,
			"group-egress":        grp,
		},
	}
	instances := []*types.Instance{
		{ID: "d1", TenantID: types.DefaultTenantID},
		{ID: "g1", TenantID: "group-egress"},
		{ID: "x1", TenantID: "removed-tenant"},
		{ID: "x2", TenantID: "removed-tenant"},
	}
	failed, err := destroyByTenant(context.Background(), p, instances)
	if err != nil {
		t.Fatalf("destroyByTenant: %v", err)
	}
	if len(failed) != 0 {
		t.Errorf("unexpected failed: %v", failed)
	}
	if destroyedDefault != 3 {
		t.Errorf("default driver: destroyed %d, want 3 (default row + 2 removed-tenant rows)", destroyedDefault)
	}
	if destroyedGroup != 1 {
		t.Errorf("group driver: destroyed %d, want 1", destroyedGroup)
	}
}

// TestDestroyByTenant_SingleTenantUnknownIDsPassThrough: a pool without tenant config keeps the
// legacy behavior — all rows go to the pool driver regardless of stamped tenant id.
func TestDestroyByTenant_SingleTenantUnknownIDsPassThrough(t *testing.T) {
	var destroyed int
	def := &flexibleMockDriver{
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			destroyed += len(instances)
			return nil, nil
		},
	}
	p := &Pool{Name: "gcp", Driver: def}
	instances := []*types.Instance{
		{ID: "1", TenantID: "old-tenant"},
		{ID: "2", TenantID: ""},
	}
	failed, err := destroyByTenant(context.Background(), p, instances)
	if err != nil {
		t.Fatalf("destroyByTenant: %v", err)
	}
	if len(failed) != 0 || destroyed != 2 {
		t.Errorf("single-tenant: destroyed %d failed %d, want 2/0", destroyed, len(failed))
	}
}
