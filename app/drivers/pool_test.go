package drivers

import (
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
