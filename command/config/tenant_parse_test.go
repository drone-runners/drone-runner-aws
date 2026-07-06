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
    tenants:
      - id: default
        spec:
          account:
            region: us-east-1
            key_pair_name: kp
          ami: ami-base
          size: t3.large
          network:
            security_groups: [sg-base]
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
	if len(inst.Tenants) != 3 {
		t.Fatalf("expected 3 tenants, got %d", len(inst.Tenants))
	}

	// Default tenant spec typed correctly.
	def := inst.Tenants[0]
	if def.ID != "default" {
		t.Errorf("expected default id, got %q", def.ID)
	}
	amz, ok := def.Spec.(*Amazon)
	if !ok {
		t.Fatalf("expected default tenant spec *Amazon, got %T", def.Spec)
	}
	if amz.AMI != "ami-base" || amz.Account.Region != "us-east-1" {
		t.Errorf("default tenant spec parsed wrong: %+v", amz)
	}

	// Customer tenant with multiple ids.
	cust := inst.Tenants[1]
	if len(cust.IDs) != 2 || cust.IDs[0] != "acctA" {
		t.Errorf("expected customer ids [acctA acctB], got %v", cust.IDs)
	}
	custSpec := cust.Spec.(*Amazon)
	if custSpec.Network.SubnetID != "subnet-aaa" {
		t.Errorf("expected subnet-aaa, got %q", custSpec.Network.SubnetID)
	}

	// Per-tenant sizing override.
	if inst.Tenants[2].Pool != 2 {
		t.Errorf("expected acctC pool 2, got %d", inst.Tenants[2].Pool)
	}

	// End-to-end resolve.
	resolved, accountMap, err := ResolveTenants(&inst)
	if err != nil {
		t.Fatalf("ResolveTenants error: %v", err)
	}
	if len(resolved) != 3 {
		t.Fatalf("expected 3 resolved tenants, got %d", len(resolved))
	}
	if accountMap["acctB"] != "acctA" {
		t.Errorf("expected acctB->acctA, got %v", accountMap)
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
