package poolfile

import (
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestProcessPool_AmazonTenants(t *testing.T) {
	yaml := `
version: "1"
instances:
  - name: linux-amd64-aws
    type: amazon
    pool: 1
    limit: 20
    spec:
      account:
        region: us-east-1
        key_pair_name: kp
      ami: ami-base
      size: t3.large
      network:
        security_groups: [sg-base]
    tenants:
      - ids: [acctA, acctB]
        spec:
          network:
            subnet_id: subnet-aaa
      - ids: [acctC]
        spec:
          ami: ami-custom
          network:
            subnet_id: subnet-ccc
`
	pf, err := config.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pools, err := ProcessPool(pf, "runner", types.Passwords{})
	if err != nil {
		t.Fatalf("ProcessPool: %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}
	p := pools[0]
	if !p.IsMultiTenant() {
		t.Fatalf("expected multi-tenant pool")
	}
	// Three tenant drivers: default, acctA, acctC.
	if len(p.TenantDrivers) != 3 {
		t.Errorf("expected 3 tenant drivers, got %d", len(p.TenantDrivers))
	}
	for _, id := range []string{"default", "acctA", "acctC"} {
		if _, ok := p.TenantDrivers[id]; !ok {
			t.Errorf("missing tenant driver %q", id)
		}
	}
	// Account mapping resolves shared tenant.
	if p.ResolveTenant("acctA") != "acctA" || p.ResolveTenant("acctB") != "acctA" {
		t.Errorf("expected acctA and acctB to resolve to acctA, got %q/%q", p.ResolveTenant("acctA"), p.ResolveTenant("acctB"))
	}
	if p.ResolveTenant("acctC") != "acctC" {
		t.Errorf("expected acctC to resolve to acctC, got %q", p.ResolveTenant("acctC"))
	}
	if p.ResolveTenant("unknown") != types.DefaultTenantID {
		t.Errorf("expected unknown account to resolve to default")
	}
	// Default driver is set for tenant-agnostic operations.
	if p.Driver == nil {
		t.Errorf("expected default driver to be set")
	}
}

func TestProcessPool_AmazonSingleTenantUnchanged(t *testing.T) {
	yaml := `
version: "1"
instances:
  - name: linux-amd64-aws
    type: amazon
    pool: 1
    limit: 20
    spec:
      account:
        region: us-east-1
      ami: ami-base
      network:
        subnet_id: subnet-xyz
`
	pf, err := config.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pools, err := ProcessPool(pf, "runner", types.Passwords{})
	if err != nil {
		t.Fatalf("ProcessPool: %v", err)
	}
	p := pools[0]
	if p.IsMultiTenant() {
		t.Fatalf("expected single-tenant pool")
	}
	if p.Driver == nil {
		t.Fatalf("expected pool driver to be set")
	}
	// Single-tenant pools resolve everything to default and use the pool driver.
	if p.ResolveTenant("acctA") != types.DefaultTenantID {
		t.Errorf("expected default tenant for single-tenant pool")
	}
	if p.DriverForTenant("acctA") != p.Driver {
		t.Errorf("expected DriverForTenant to fall back to pool driver")
	}
}

func TestProcessPool_GoogleTenants(t *testing.T) {
	yaml := `
version: "1"
instances:
  - name: linux-amd64-gcp
    type: google
    pool: 1
    limit: 20
    spec:
      account:
        project_id: proj-base
      image: img-base
      network: net-base
      subnetwork: subnet-base
      zone: [us-central1-a]
    tenants:
      - ids: [acctA]
        spec:
          subnetwork: subnet-a
`
	pf, err := config.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pools, err := ProcessPool(pf, "runner", types.Passwords{})
	if err != nil {
		t.Fatalf("ProcessPool: %v", err)
	}
	p := pools[0]
	if !p.IsMultiTenant() {
		t.Fatalf("expected multi-tenant google pool")
	}
	if len(p.TenantDrivers) != 2 {
		t.Errorf("expected 2 tenant drivers, got %d", len(p.TenantDrivers))
	}
	if p.ResolveTenant("acctA") != "acctA" {
		t.Errorf("expected acctA tenant, got %q", p.ResolveTenant("acctA"))
	}
}
