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

// TestProcessPool_AmazonTenants_ChartShape runs ProcessPool end-to-end on the AWS pool shape
// produced by the Harness runner helm chart (chart/templates/config.yaml): an instance-level
// YAML anchor for type/platform plus a network block with private_ip / security_groups /
// zone_details, extended with a per-account tenants block. It confirms a driver is built per
// tenant, account routing is correct, each tenant's resolved spec is the base deep-merged with
// its override, and an explicit pool:0 tenant opts out of warm instances.
func TestProcessPool_AmazonTenants_ChartShape(t *testing.T) {
	yaml := `
version: "1"
x-templates:
  aws-linux-amd64: &aws-linux-amd64
    type: amazon
    platform:
      os: linux
      arch: amd64
instances:
  - name: linux-amd64-aws
    <<: *aws-linux-amd64
    pool: 2
    limit: 20
    spec:
      account:
        region: us-east-1
        key_pair_name: kp
      ami: ami-base
      size: t3.large
      disk:
        size: 100
      network:
        private_ip: false
        security_groups:
          - sg-base
        zone_details:
          - availability_zone: us-east-1a
            subnet_id: subnet-base-a
    tenants:
      - ids:
          - acctPrivateLink
        spec:
          network:
            private_ip: true
            security_groups:
              - sg-customer
            zone_details:
              - availability_zone: us-east-1a
                subnet_id: subnet-customer-a
      - ids:
          - freeAcct
        pool: 0
        spec:
          network:
            subnet_id: subnet-free
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
	// default + acctPrivateLink + freeAcct.
	if len(p.TenantDrivers) != 3 {
		t.Errorf("expected 3 tenant drivers, got %d", len(p.TenantDrivers))
	}
	if p.ResolveTenant("acctPrivateLink") != "acctPrivateLink" {
		t.Errorf("expected acctPrivateLink tenant, got %q", p.ResolveTenant("acctPrivateLink"))
	}
	if p.ResolveTenant("freeAcct") != "freeAcct" {
		t.Errorf("expected freeAcct tenant, got %q", p.ResolveTenant("freeAcct"))
	}
	if p.ResolveTenant("someoneElse") != types.DefaultTenantID {
		t.Errorf("expected unknown account to resolve to default")
	}

	specByTenant := map[string]*config.Amazon{}
	sizeByTenant := map[string]int{}
	for i := range p.Tenants {
		tp := p.Tenants[i]
		spec, ok := tp.Spec.(*config.Amazon)
		if !ok {
			t.Fatalf("tenant %q spec not *config.Amazon: %T", tp.ID, tp.Spec)
		}
		specByTenant[tp.ID] = spec
		sizeByTenant[tp.ID] = tp.MinSize
	}

	// PrivateLink tenant: overrides applied, base inherited.
	cust := specByTenant["acctPrivateLink"]
	if cust == nil {
		t.Fatalf("missing acctPrivateLink tenant spec")
	}
	if !cust.Network.PrivateIP {
		t.Errorf("expected private_ip true for acctPrivateLink")
	}
	if len(cust.Network.SecurityGroups) != 1 || cust.Network.SecurityGroups[0] != "sg-customer" {
		t.Errorf("expected sg-customer, got %v", cust.Network.SecurityGroups)
	}
	if len(cust.Network.ZoneDetails) != 1 || cust.Network.ZoneDetails[0].SubnetID != "subnet-customer-a" {
		t.Errorf("expected zone_details subnet-customer-a, got %+v", cust.Network.ZoneDetails)
	}
	if cust.AMI != "ami-base" || cust.Account.Region != "us-east-1" || cust.Disk.Size != 100 {
		t.Errorf("expected inherited ami/region/disk, got ami=%q region=%q disk=%d", cust.AMI, cust.Account.Region, cust.Disk.Size)
	}

	// free tenant: pool:0 opt-out honored, subnet overridden, security_groups + zone_details inherited.
	free := specByTenant["freeAcct"]
	if free == nil {
		t.Fatalf("missing freeAcct tenant spec")
	}
	if sizeByTenant["freeAcct"] != 0 {
		t.Errorf("expected freeAcct min size 0 (explicit pool:0), got %d", sizeByTenant["freeAcct"])
	}
	if free.Network.SubnetID != "subnet-free" {
		t.Errorf("expected subnet-free, got %q", free.Network.SubnetID)
	}
	if len(free.Network.SecurityGroups) != 1 || free.Network.SecurityGroups[0] != "sg-base" {
		t.Errorf("expected inherited [sg-base], got %v", free.Network.SecurityGroups)
	}
	if len(free.Network.ZoneDetails) != 1 || free.Network.ZoneDetails[0].SubnetID != "subnet-base-a" {
		t.Errorf("expected inherited base zone_details, got %+v", free.Network.ZoneDetails)
	}

	// default tenant keeps instance-level sizing.
	if sizeByTenant[types.DefaultTenantID] != 2 {
		t.Errorf("expected default tenant min size 2, got %d", sizeByTenant[types.DefaultTenantID])
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
