package harness

import (
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

// TestBuildScalablePools_TenantVariantReplaceByID verifies the scaler config path picks up
// tenant variant machine_type overrides (full replace by variant_id via ResolveTenants).
func TestBuildScalablePools_TenantVariantReplaceByID(t *testing.T) {
	yaml := `
version: "1"
instances:
  - name: linux-amd64-gcp
    type: google
    pool: 1
    limit: 10
    spec:
      account:
        project_id: proj-base
      image: img-base
      machine_type: e2-standard-4
    variants:
      - variant_id: variant_large
        resource_class: large
        machine_type: c4d-standard-8-lssd
        pool: 1
      - variant_id: variant_xlarge
        resource_class: xlarge
        machine_type: c4d-standard-16-lssd
        pool: 1
    tenants:
      - ids: [acctA]
        pool: 0
        spec:
          machine_type: c4d-standard-8
        variants:
          - variant_id: variant_large
            resource_class: large
            machine_type: c4d-standard-8
            pool: 2
`
	pf, err := config.Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	pools := buildScalablePools(pf)
	if len(pools) != 1 {
		t.Fatalf("expected 1 pool, got %d", len(pools))
	}
	p := pools[0]
	if len(p.Tenants) != 2 {
		t.Fatalf("expected default + acctA tenants, got %d", len(p.Tenants))
	}

	byID := map[string]struct {
		minSize  int
		variants map[string]string // variant_id -> machine_type
	}{}
	for _, tn := range p.Tenants {
		vm := map[string]string{}
		for _, v := range tn.Variants {
			vm[v.Params.VariantID] = v.Params.MachineType
		}
		byID[tn.ID] = struct {
			minSize  int
			variants map[string]string
		}{minSize: tn.MinSize, variants: vm}
	}

	def := byID[types.DefaultTenantID]
	if def.variants["variant_large"] != "c4d-standard-8-lssd" {
		t.Errorf("default variant_large: got %q", def.variants["variant_large"])
	}
	if def.minSize != 1 {
		t.Errorf("default minSize: got %d want 1", def.minSize)
	}

	acct := byID["acctA"]
	if acct.minSize != 0 {
		t.Errorf("acctA pool:0 opt-out: got minSize %d", acct.minSize)
	}
	if acct.variants["variant_large"] != "c4d-standard-8" {
		t.Errorf("acctA variant_large machine_type: got %q want c4d-standard-8", acct.variants["variant_large"])
	}
	if acct.variants["variant_xlarge"] != "c4d-standard-16-lssd" {
		t.Errorf("acctA variant_xlarge should inherit base: got %q", acct.variants["variant_xlarge"])
	}
}
