package drivers

import (
	"context"
	"testing"

	"github.com/drone-runners/drone-runner-aws/types"
)

// TestTenantLifecycle_VariantMachineTypeE2E asserts provision applies the tenant-resolved
// variant machine_type / hibernate flags, while the default tenant keeps base variants.
func TestTenantLifecycle_VariantMachineTypeE2E(t *testing.T) {
	baseVariants := []types.PoolVariant{
		{SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_large", ResourceClass: "large",
			MachineType: "c4d-standard-8-lssd", DiskType: "hyperdisk-balanced", DiskSize: 100, Hibernate: false,
		}},
		{SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_xlarge", ResourceClass: "xlarge",
			MachineType: "c4d-standard-16-lssd", DiskType: "hyperdisk-balanced", DiskSize: 100,
		}},
	}
	tenantVariants := []types.PoolVariant{
		{SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_large", ResourceClass: "large",
			MachineType: "c4d-standard-8", DiskType: "hyperdisk-balanced", DiskSize: 100, Hibernate: true,
		}},
		{SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_xlarge", ResourceClass: "xlarge",
			MachineType: "c4d-standard-16-lssd", DiskType: "hyperdisk-balanced", DiskSize: 100,
		}},
	}

	def := &flexibleMockDriver{canHibernate: false}
	acct := &flexibleMockDriver{canHibernate: true}
	p := &Pool{
		Name:            "linux-amd64-gcp",
		PoolVariants:    baseVariants,
		Driver:          def,
		AccountToTenant: map[string]string{"acctA": "acctA"},
		TenantDrivers: map[string]Driver{
			types.DefaultTenantID: def,
			"acctA":               acct,
		},
		Tenants: []TenantPool{
			{ID: types.DefaultTenantID, PoolVariants: baseVariants},
			{ID: "acctA", PoolVariants: tenantVariants},
		},
	}

	entry := &poolEntry{Pool: *p}
	dm := &DistributedManager{}
	ctx := context.Background()

	acctID := p.ResolveTenant("acctA")
	matched := dm.filterVariants(ctx, entry, &types.ProvisionParams{ResourceClass: "large"}, p.VariantsForTenant(acctID))
	if len(matched) != 1 {
		t.Fatalf("expected 1 matched variant, got %d", len(matched))
	}
	setup := &types.SetupInstanceParams{TenantID: acctID}
	applyVariantToSetupParams(setup, matched[0])
	if setup.MachineType != "c4d-standard-8" {
		t.Errorf("create machine_type: got %q want c4d-standard-8", setup.MachineType)
	}
	if setup.VariantID != "variant_large" {
		t.Errorf("variant_id: got %q", setup.VariantID)
	}
	if !setup.Hibernate {
		t.Error("create hibernate: expected true from tenant variant")
	}

	defMatched := dm.filterVariants(ctx, entry, &types.ProvisionParams{ResourceClass: "large"}, p.VariantsForTenant(types.DefaultTenantID))
	defSetup := &types.SetupInstanceParams{}
	applyVariantToSetupParams(defSetup, defMatched[0])
	if defSetup.MachineType != "c4d-standard-8-lssd" {
		t.Errorf("default create machine_type: got %q", defSetup.MachineType)
	}

	xlarge := dm.filterVariants(ctx, entry, &types.ProvisionParams{ResourceClass: "xlarge"}, p.VariantsForTenant(acctID))
	if len(xlarge) != 1 || xlarge[0].MachineType != "c4d-standard-16-lssd" {
		t.Errorf("inherited variant_xlarge: %+v", xlarge)
	}

	if p.DriverForTenant("acctA") == p.DriverForTenant(types.DefaultTenantID) {
		t.Error("expected distinct tenant drivers")
	}
	if !p.DriverForTenant("acctA").CanHibernate() {
		t.Error("tenant driver should CanHibernate")
	}
	if p.DriverForTenant(types.DefaultTenantID).CanHibernate() {
		t.Error("default driver should not CanHibernate")
	}
}
