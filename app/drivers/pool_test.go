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
