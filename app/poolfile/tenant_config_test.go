package poolfile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

// wantEqual fails the test unless got == want.
func wantEqual[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

// wantStringSlice fails the test unless got deep-equals want.
func wantStringSlice(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %v, want %v", name, got, want)
	}
}

// wantSubnetIDs fails the test unless the subnet ids in got match want (in order).
func wantSubnetIDs(t *testing.T, name string, got []config.ZoneInfo, want ...string) {
	t.Helper()
	subnets := make([]string, len(got))
	for i := range got {
		subnets[i] = got[i].SubnetID
	}
	if !reflect.DeepEqual(subnets, want) {
		t.Errorf("%s subnet ids: got %v, want %v", name, subnets, want)
	}
}

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
	pools, err := ProcessPool(pf, "runner", types.Passwords{}, nil)
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
	pools, err := ProcessPool(pf, "runner", types.Passwords{}, nil)
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
	// Account -> tenant routing (unknown accounts fall back to the default tenant).
	for acct, want := range map[string]string{
		"acctPrivateLink": "acctPrivateLink",
		"freeAcct":        "freeAcct",
		"someoneElse":     types.DefaultTenantID,
	} {
		wantEqual(t, "ResolveTenant("+acct+")", p.ResolveTenant(acct), want)
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

	// PrivateLink tenant: private networking + customer subnet/SGs; ami/region/disk inherited.
	cust := specByTenant["acctPrivateLink"]
	if cust == nil {
		t.Fatalf("missing acctPrivateLink tenant spec")
	}
	if !cust.Network.PrivateIP {
		t.Errorf("expected private_ip true for acctPrivateLink")
	}
	wantStringSlice(t, "acctPrivateLink security_groups", cust.Network.SecurityGroups, []string{"sg-customer"})
	wantSubnetIDs(t, "acctPrivateLink zone_details", cust.Network.ZoneDetails, "subnet-customer-a")
	wantEqual(t, "acctPrivateLink ami", cust.AMI, "ami-base")
	wantEqual(t, "acctPrivateLink region", cust.Account.Region, "us-east-1")
	wantEqual(t, "acctPrivateLink disk size", cust.Disk.Size, int64(100))

	// free tenant: pool:0 opt-out honored, subnet overridden, SGs + zone_details inherited.
	free := specByTenant["freeAcct"]
	if free == nil {
		t.Fatalf("missing freeAcct tenant spec")
	}
	wantEqual(t, "freeAcct min size", sizeByTenant["freeAcct"], 0)
	wantEqual(t, "freeAcct subnet_id", free.Network.SubnetID, "subnet-free")
	wantStringSlice(t, "freeAcct security_groups", free.Network.SecurityGroups, []string{"sg-base"})
	wantSubnetIDs(t, "freeAcct zone_details", free.Network.ZoneDetails, "subnet-base-a")

	// default tenant keeps instance-level sizing.
	wantEqual(t, "default min size", sizeByTenant[types.DefaultTenantID], 2)
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
	pools, err := ProcessPool(pf, "runner", types.Passwords{}, nil)
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
      egress_control: false
      networks:
        - network: net-base
          subnetwork: subnet-base
          zones: [us-central1-a]
          proxy_url: http://base-proxy:3128
    tenants:
      - ids: [acctA]
        spec:
          egress_control: true
          networks:
            - network: net-a
              subnetwork: subnet-a
              zones: [us-central1-b]
              proxy_url: http://tenant-proxy:3128
`
	pf, err := config.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pools, err := ProcessPool(pf, "runner", types.Passwords{}, nil)
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

	var defaultSpec, tenantSpec *config.Google
	for i := range p.Tenants {
		g, ok := p.Tenants[i].Spec.(*config.Google)
		if !ok {
			t.Fatalf("tenant %q: expected *config.Google spec", p.Tenants[i].ID)
		}
		switch p.Tenants[i].ID {
		case types.DefaultTenantID:
			defaultSpec = g
		case "acctA":
			tenantSpec = g
		}
	}
	if defaultSpec == nil || tenantSpec == nil {
		t.Fatalf("missing default or acctA tenant spec")
	}
	if defaultSpec.EgressControl {
		t.Errorf("default tenant: want egress_control=false")
	}
	if !tenantSpec.EgressControl {
		t.Errorf("acctA tenant: want egress_control=true")
	}
	if len(tenantSpec.Networks) != 1 || tenantSpec.Networks[0].ProxyURL != "http://tenant-proxy:3128" {
		t.Errorf("acctA networks proxy_url: got %+v", tenantSpec.Networks)
	}
	if got := networkProxyURLFromDriver(t, p.DriverForTenant("acctA")); got != "http://tenant-proxy:3128" {
		t.Errorf("acctA driver proxyURL: got %q", got)
	}
	if got := networkProxyURLFromDriver(t, p.DriverForTenant(types.DefaultTenantID)); got != "http://base-proxy:3128" {
		t.Errorf("default driver proxyURL: got %q", got)
	}
}

// networkProxyURLFromDriver reads the first networkConfigs[].proxyURL from a Google driver via reflect.
func networkProxyURLFromDriver(t *testing.T, d drivers.Driver) string {
	t.Helper()
	if d == nil {
		t.Fatal("driver is nil")
	}
	v := reflect.ValueOf(d)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	ncs := v.FieldByName("networkConfigs")
	if !ncs.IsValid() || ncs.Len() == 0 {
		return ""
	}
	return ncs.Index(0).FieldByName("proxyURL").String()
}
