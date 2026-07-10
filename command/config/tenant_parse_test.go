package config

import (
	"strings"
	"testing"
)

func TestParse_TenantsYAML(t *testing.T) {
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
            security_groups: [sg-aaa]
      - ids: [acctC]
        pool: 2
        spec:
          ami: ami-custom
          network:
            subnet_id: subnet-ccc
`
	pf, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(pf.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(pf.Instances))
	}
	inst := pf.Instances[0]
	if len(inst.Tenants) != 2 {
		t.Fatalf("expected 2 tenant overrides, got %d", len(inst.Tenants))
	}

	// Base (default) spec parsed from the top-level spec.
	amz, ok := inst.Spec.(*Amazon)
	if !ok {
		t.Fatalf("expected base spec *Amazon, got %T", inst.Spec)
	}
	if amz.AMI != "ami-base" || amz.Account.Region != "us-east-1" {
		t.Errorf("base spec parsed wrong: %+v", amz)
	}

	// Customer tenant with multiple ids.
	cust := inst.Tenants[0]
	if len(cust.IDs) != 2 || cust.IDs[0] != "acctA" {
		t.Errorf("expected customer ids [acctA acctB], got %v", cust.IDs)
	}
	custSpec := cust.Spec.(*Amazon)
	if custSpec.Network.SubnetID != "subnet-aaa" {
		t.Errorf("expected subnet-aaa, got %q", custSpec.Network.SubnetID)
	}

	// Per-tenant sizing override.
	if inst.Tenants[1].Pool == nil || *inst.Tenants[1].Pool != 2 {
		t.Errorf("expected acctC pool 2, got %v", inst.Tenants[1].Pool)
	}

	// End-to-end resolve: base default + 2 overrides = 3 resolved tenants.
	resolved, accountMap, err := ResolveTenants(&inst)
	if err != nil {
		t.Fatalf("ResolveTenants error: %v", err)
	}
	if len(resolved) != 3 {
		t.Fatalf("expected 3 resolved tenants, got %d", len(resolved))
	}
	if resolved[0].ID != DefaultTenantID {
		t.Errorf("expected first resolved tenant to be default, got %q", resolved[0].ID)
	}
	if accountMap["acctB"] != "acctA" {
		t.Errorf("expected acctB->acctA, got %v", accountMap)
	}
}

// TestParse_AWSChartAnchorsWithTenants mirrors the exact shape produced by the Harness runner
// helm chart's AWS pool (chart/templates/config.yaml): an instance-level YAML anchor/merge-key
// (`<<: *aws-linux-amd64`) that supplies type + platform, a network block with private_ip /
// security_groups / zone_details, plus a per-account `tenants` override. It proves anchors and
// merge-keys compose with the tenants block (they are resolved by ghodss/yaml -> YAMLToJSON
// before the config structs are decoded) and that the AWS spec merges correctly end-to-end.
func TestParse_AWSChartAnchorsWithTenants(t *testing.T) {
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
          - availability_zone: us-east-1b
            subnet_id: subnet-base-b
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
`
	pf, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(pf.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(pf.Instances))
	}
	inst := pf.Instances[0]

	// type + platform came from the merged YAML anchor.
	if inst.Type != "amazon" {
		t.Errorf("expected type amazon from anchor, got %q", inst.Type)
	}
	if inst.Platform.OS != "linux" || inst.Platform.Arch != "amd64" {
		t.Errorf("expected platform linux/amd64 from anchor, got %s/%s", inst.Platform.OS, inst.Platform.Arch)
	}

	// Base spec parsed into a typed *Amazon.
	base, ok := inst.Spec.(*Amazon)
	if !ok {
		t.Fatalf("expected base spec *Amazon, got %T", inst.Spec)
	}
	if base.Network.PrivateIP {
		t.Errorf("expected base private_ip false")
	}
	if len(base.Network.ZoneDetails) != 2 {
		t.Errorf("expected 2 base zone_details, got %d", len(base.Network.ZoneDetails))
	}

	// Resolve: base default + 1 override.
	resolved, accountMap, err := ResolveTenants(&inst)
	if err != nil {
		t.Fatalf("ResolveTenants error: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("expected 2 resolved tenants, got %d", len(resolved))
	}
	if accountMap["acctPrivateLink"] != "acctPrivateLink" {
		t.Errorf("expected acctPrivateLink to map to itself, got %v", accountMap)
	}

	var cust *Amazon
	for _, rt := range resolved {
		if rt.ID == "acctPrivateLink" {
			cust = rt.Spec.(*Amazon)
		}
	}
	if cust == nil {
		t.Fatalf("resolved tenant acctPrivateLink not found")
	}
	// Overridden by the tenant.
	if !cust.Network.PrivateIP {
		t.Errorf("expected tenant private_ip true")
	}
	if len(cust.Network.SecurityGroups) != 1 || cust.Network.SecurityGroups[0] != "sg-customer" {
		t.Errorf("expected tenant security_groups [sg-customer], got %v", cust.Network.SecurityGroups)
	}
	if len(cust.Network.ZoneDetails) != 1 || cust.Network.ZoneDetails[0].SubnetID != "subnet-customer-a" {
		t.Errorf("expected tenant zone_details [subnet-customer-a], got %+v", cust.Network.ZoneDetails)
	}
	// Inherited from base.
	if cust.AMI != "ami-base" || cust.Size != "t3.large" {
		t.Errorf("expected ami/size inherited, got ami=%q size=%q", cust.AMI, cust.Size)
	}
	if cust.Account.Region != "us-east-1" || cust.Account.KeyPairName != "kp" {
		t.Errorf("expected account inherited, got %+v", cust.Account)
	}
	if cust.Disk.Size != 100 {
		t.Errorf("expected disk size inherited 100, got %d", cust.Disk.Size)
	}
}

func TestParse_NoTenantsBackwardCompatible(t *testing.T) {
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
	pf, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	inst := pf.Instances[0]
	if len(inst.Tenants) != 0 {
		t.Fatalf("expected no tenants, got %d", len(inst.Tenants))
	}
	amz, ok := inst.Spec.(*Amazon)
	if !ok {
		t.Fatalf("expected *Amazon spec, got %T", inst.Spec)
	}
	if amz.Network.SubnetID != "subnet-xyz" {
		t.Errorf("expected subnet-xyz, got %q", amz.Network.SubnetID)
	}

	resolved, accountMap, err := ResolveTenants(&inst)
	if err != nil {
		t.Fatalf("ResolveTenants error: %v", err)
	}
	if len(resolved) != 1 || resolved[0].ID != DefaultTenantID {
		t.Fatalf("expected single default tenant, got %+v", resolved)
	}
	if len(accountMap) != 0 {
		t.Errorf("expected empty account map, got %v", accountMap)
	}
}
