package config

import (
	"testing"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestMergeSpec_AmazonPartialOverride(t *testing.T) {
	base := &Amazon{
		Account: AmazonAccount{Region: "us-east-1", KeyPairName: "kp"},
		AMI:     "ami-base",
		Size:    "t3.large",
		Network: AmazonNetwork{
			SecurityGroups: []string{"sg-base"},
			ZoneDetails:    []ZoneInfo{{AvailabilityZone: "us-east-1a", SubnetID: "subnet-base"}},
		},
	}
	override := &Amazon{
		Network: AmazonNetwork{
			SubnetID:       "subnet-aaa",
			SecurityGroups: []string{"sg-aaa"},
		},
	}

	mergedIface, err := MergeSpec(base, override)
	if err != nil {
		t.Fatalf("MergeSpec returned error: %v", err)
	}
	merged, ok := mergedIface.(*Amazon)
	if !ok {
		t.Fatalf("expected *Amazon, got %T", mergedIface)
	}

	// Inherited from base.
	if merged.Account.Region != "us-east-1" {
		t.Errorf("region: expected inherited us-east-1, got %q", merged.Account.Region)
	}
	if merged.AMI != "ami-base" {
		t.Errorf("ami: expected inherited ami-base, got %q", merged.AMI)
	}
	if merged.Size != "t3.large" {
		t.Errorf("size: expected inherited t3.large, got %q", merged.Size)
	}
	// Overridden.
	if merged.Network.SubnetID != "subnet-aaa" {
		t.Errorf("subnet: expected override subnet-aaa, got %q", merged.Network.SubnetID)
	}
	if len(merged.Network.SecurityGroups) != 1 || merged.Network.SecurityGroups[0] != "sg-aaa" {
		t.Errorf("security_groups: expected override [sg-aaa], got %v", merged.Network.SecurityGroups)
	}
	// Base must not be mutated.
	if base.Network.SubnetID != "" {
		t.Errorf("base mutated: subnet became %q", base.Network.SubnetID)
	}
}

func TestMergeSpec_GooglePartialOverride(t *testing.T) {
	base := &Google{
		Image:      "img-base",
		Size:       "n1",
		Network:    "net-base",
		Subnetwork: "subnet-base",
		Zone:       []string{"us-central1-a"},
		Tags:       []string{"tag-base"},
	}
	override := &Google{
		Subnetwork: "subnet-cust",
		Tags:       []string{"tag-cust"},
	}

	mergedIface, err := MergeSpec(base, override)
	if err != nil {
		t.Fatalf("MergeSpec returned error: %v", err)
	}
	merged := mergedIface.(*Google)

	if merged.Image != "img-base" {
		t.Errorf("image: expected inherited img-base, got %q", merged.Image)
	}
	if merged.Network != "net-base" {
		t.Errorf("network: expected inherited net-base, got %q", merged.Network)
	}
	if merged.Subnetwork != "subnet-cust" {
		t.Errorf("subnetwork: expected override subnet-cust, got %q", merged.Subnetwork)
	}
	if len(merged.Tags) != 1 || merged.Tags[0] != "tag-cust" {
		t.Errorf("tags: expected override [tag-cust], got %v", merged.Tags)
	}
}

func TestResolveTenants_NoTenantsBackwardCompatible(t *testing.T) {
	inst := &Instance{
		Name:     "linux-amd64-aws",
		Type:     "amazon",
		Pool:     3,
		Limit:    10,
		Spec:     &Amazon{AMI: "ami-x"},
		Variants: []types.PoolVariant{{Pool: 1}},
	}

	tenants, accountMap, err := ResolveTenants(inst)
	if err != nil {
		t.Fatalf("ResolveTenants error: %v", err)
	}
	if len(tenants) != 1 {
		t.Fatalf("expected 1 tenant, got %d", len(tenants))
	}
	if tenants[0].ID != DefaultTenantID {
		t.Errorf("expected default tenant id, got %q", tenants[0].ID)
	}
	if tenants[0].Pool != 3 || tenants[0].Limit != 10 {
		t.Errorf("expected pool/limit inherited from instance, got pool=%d limit=%d", tenants[0].Pool, tenants[0].Limit)
	}
	if tenants[0].Spec != inst.Spec {
		t.Errorf("expected instance spec to be reused")
	}
	if len(accountMap) != 0 {
		t.Errorf("expected empty account map, got %v", accountMap)
	}
}

func TestResolveTenants_MultiTenant(t *testing.T) {
	inst := &Instance{
		Name: "linux-amd64-aws",
		Type: "amazon",
		Pool: 1,
		Spec: &Amazon{Account: AmazonAccount{Region: "us-east-1"}, AMI: "ami-base", Network: AmazonNetwork{SecurityGroups: []string{"sg-base"}}},
		Tenants: []Tenant{
			{
				IDs:  []string{"acctA", "acctB"},
				Spec: &Amazon{Network: AmazonNetwork{SubnetID: "subnet-aaa"}},
			},
			{
				IDs:  []string{"acctC"},
				Pool: intPtr(2),
				Spec: &Amazon{AMI: "ami-custom", Network: AmazonNetwork{SubnetID: "subnet-ccc"}},
			},
		},
	}

	tenants, accountMap, err := ResolveTenants(inst)
	if err != nil {
		t.Fatalf("ResolveTenants error: %v", err)
	}
	if len(tenants) != 3 {
		t.Fatalf("expected 3 tenants, got %d", len(tenants))
	}

	byID := map[string]ResolvedTenant{}
	for _, tn := range tenants {
		byID[tn.ID] = tn
	}

	// Multi-id tenant uses first id as its tenant id and both accounts map to it.
	if accountMap["acctA"] != "acctA" || accountMap["acctB"] != "acctA" {
		t.Errorf("expected acctA and acctB to map to tenant acctA, got %v", accountMap)
	}
	if accountMap["acctC"] != "acctC" {
		t.Errorf("expected acctC to map to tenant acctC, got %v", accountMap)
	}

	// Merged spec for acctA tenant: subnet overridden, region+ami+sg inherited.
	ta := byID["acctA"].Spec.(*Amazon)
	if ta.Network.SubnetID != "subnet-aaa" {
		t.Errorf("acctA subnet: got %q", ta.Network.SubnetID)
	}
	if ta.Account.Region != "us-east-1" || ta.AMI != "ami-base" {
		t.Errorf("acctA inherited fields wrong: region=%q ami=%q", ta.Account.Region, ta.AMI)
	}
	if len(ta.Network.SecurityGroups) != 1 || ta.Network.SecurityGroups[0] != "sg-base" {
		t.Errorf("acctA sg: expected inherited [sg-base], got %v", ta.Network.SecurityGroups)
	}

	// acctC overrides ami and has its own pool sizing.
	tc := byID["acctC"]
	if tc.Spec.(*Amazon).AMI != "ami-custom" {
		t.Errorf("acctC ami: got %q", tc.Spec.(*Amazon).AMI)
	}
	if tc.Pool != 2 {
		t.Errorf("acctC pool: expected 2, got %d", tc.Pool)
	}

	// Default tenant sizing falls back to instance pool.
	if byID[DefaultTenantID].Pool != 1 {
		t.Errorf("default pool: expected 1, got %d", byID[DefaultTenantID].Pool)
	}
}

func intPtr(i int) *int { return &i }

// A tenant that sets pool: 0 must resolve to MinSize 0 (no warm pool), not inherit the
// instance-level pool. A tenant that omits pool inherits the instance-level pool.
func TestResolveTenants_ExplicitZeroPool(t *testing.T) {
	inst := &Instance{
		Name:  "p",
		Type:  "amazon",
		Pool:  3,
		Limit: 5,
		Spec:  &Amazon{AMI: "ami-base"},
		Tenants: []Tenant{
			{IDs: []string{"free"}, Pool: intPtr(0), Spec: &Amazon{Network: AmazonNetwork{SubnetID: "s-free"}}},
			{IDs: []string{"acctX"}, Spec: &Amazon{Network: AmazonNetwork{SubnetID: "s-x"}}},
		},
	}
	resolved, _, err := ResolveTenants(inst)
	if err != nil {
		t.Fatalf("ResolveTenants error: %v", err)
	}
	byID := map[string]ResolvedTenant{}
	for _, tn := range resolved {
		byID[tn.ID] = tn
	}
	if byID["free"].Pool != 0 {
		t.Errorf("free tenant: expected pool 0 (explicit), got %d", byID["free"].Pool)
	}
	// Limit omitted -> inherits instance-level 5.
	if byID["free"].Limit != 5 {
		t.Errorf("free tenant: expected inherited limit 5, got %d", byID["free"].Limit)
	}
	// Pool omitted -> inherits instance-level 3.
	if byID["acctX"].Pool != 3 {
		t.Errorf("acctX tenant: expected inherited pool 3, got %d", byID["acctX"].Pool)
	}
}

func TestResolveTenants_MissingBaseSpec(t *testing.T) {
	inst := &Instance{
		Name: "p",
		Type: "amazon",
		Tenants: []Tenant{
			{IDs: []string{"acctA"}, Spec: &Amazon{Network: AmazonNetwork{SubnetID: "subnet-aaa"}}},
		},
	}
	if _, _, err := ResolveTenants(inst); err == nil {
		t.Fatalf("expected error for missing base spec")
	}
}

func TestResolveTenants_OverrideMissingIDs(t *testing.T) {
	inst := &Instance{
		Name: "p",
		Type: "amazon",
		Spec: &Amazon{},
		Tenants: []Tenant{
			{Spec: &Amazon{Network: AmazonNetwork{SubnetID: "subnet-aaa"}}},
		},
	}
	if _, _, err := ResolveTenants(inst); err == nil {
		t.Fatalf("expected error for override without ids")
	}
}

func TestResolveTenants_DuplicateTenantID(t *testing.T) {
	inst := &Instance{
		Name: "p",
		Type: "amazon",
		Spec: &Amazon{},
		Tenants: []Tenant{
			{IDs: []string{"acctX"}, Spec: &Amazon{}},
			{IDs: []string{"acctX"}, Spec: &Amazon{}},
		},
	}
	if _, _, err := ResolveTenants(inst); err == nil {
		t.Fatalf("expected error for duplicate tenant id")
	}
}

func TestResolveTenants_AccountCollision(t *testing.T) {
	inst := &Instance{
		Name: "p",
		Type: "amazon",
		Spec: &Amazon{},
		Tenants: []Tenant{
			{IDs: []string{"t1", "acctX"}, Spec: &Amazon{}},
			{IDs: []string{"t2", "acctX"}, Spec: &Amazon{}},
		},
	}
	if _, _, err := ResolveTenants(inst); err == nil {
		t.Fatalf("expected error for account mapped to multiple tenants")
	}
}

// TestMergeSpec_AmazonChartShape mirrors the AWS pool spec produced by the Harness runner helm
// chart (account + ami + size + disk + network{private_ip, security_groups, zone_details}). It
// verifies a per-tenant PrivateLink network override deep-merges correctly over the base:
// overridden fields win, list fields are replaced wholesale, and everything the tenant does not
// touch (including a zone_details list) is inherited.
func TestMergeSpec_AmazonChartShape(t *testing.T) {
	base := &Amazon{
		Account: AmazonAccount{Region: "us-east-1", KeyPairName: "kp"},
		AMI:     "ami-base",
		Size:    "t3.large",
		Disk:    disk{Size: 100},
		Network: AmazonNetwork{
			PrivateIP:      false,
			SecurityGroups: []string{"sg-base"},
			ZoneDetails: []ZoneInfo{
				{AvailabilityZone: "us-east-1a", SubnetID: "subnet-base-a"},
				{AvailabilityZone: "us-east-1b", SubnetID: "subnet-base-b"},
			},
		},
	}
	// PrivateLink customer: private networking + its own security groups; everything else
	// (ami, size, disk, account, zone_details) is inherited from the base.
	override := &Amazon{
		Network: AmazonNetwork{
			PrivateIP:      true,
			SecurityGroups: []string{"sg-customer"},
		},
	}

	mergedIface, err := MergeSpec(base, override)
	if err != nil {
		t.Fatalf("MergeSpec error: %v", err)
	}
	merged := mergedIface.(*Amazon)

	// Inherited scalars / nested objects.
	if merged.AMI != "ami-base" || merged.Size != "t3.large" {
		t.Errorf("expected ami/size inherited, got ami=%q size=%q", merged.AMI, merged.Size)
	}
	if merged.Account.Region != "us-east-1" || merged.Account.KeyPairName != "kp" {
		t.Errorf("expected account inherited, got %+v", merged.Account)
	}
	if merged.Disk.Size != 100 {
		t.Errorf("expected disk size inherited 100, got %d", merged.Disk.Size)
	}
	// Overridden.
	if !merged.Network.PrivateIP {
		t.Errorf("expected private_ip overridden to true")
	}
	if len(merged.Network.SecurityGroups) != 1 || merged.Network.SecurityGroups[0] != "sg-customer" {
		t.Errorf("expected security_groups replaced with [sg-customer], got %v", merged.Network.SecurityGroups)
	}
	// zone_details not touched by the override -> inherited wholesale.
	if len(merged.Network.ZoneDetails) != 2 {
		t.Fatalf("expected 2 inherited zone_details, got %d", len(merged.Network.ZoneDetails))
	}
	if merged.Network.ZoneDetails[0].SubnetID != "subnet-base-a" || merged.Network.ZoneDetails[1].SubnetID != "subnet-base-b" {
		t.Errorf("zone_details not inherited correctly: %+v", merged.Network.ZoneDetails)
	}
	// Base must not be mutated by the merge.
	if base.Network.PrivateIP {
		t.Errorf("base spec mutated: private_ip became true")
	}
}

// TestMergeSpec_AmazonZoneDetailsReplace verifies that when a tenant supplies its own
// zone_details, the whole list replaces the base list (list semantics are replace, not merge) --
// so a PrivateLink customer gets exactly its AZ/subnet mappings.
func TestMergeSpec_AmazonZoneDetailsReplace(t *testing.T) {
	base := &Amazon{
		AMI: "ami-base",
		Network: AmazonNetwork{
			ZoneDetails: []ZoneInfo{
				{AvailabilityZone: "us-east-1a", SubnetID: "subnet-base-a"},
				{AvailabilityZone: "us-east-1b", SubnetID: "subnet-base-b"},
			},
		},
	}
	override := &Amazon{
		Network: AmazonNetwork{
			PrivateIP: true,
			ZoneDetails: []ZoneInfo{
				{AvailabilityZone: "us-east-1c", SubnetID: "subnet-customer-c"},
			},
		},
	}

	merged := mustMerge(t, base, override).(*Amazon)
	if len(merged.Network.ZoneDetails) != 1 {
		t.Fatalf("expected zone_details replaced (len 1), got %d: %+v", len(merged.Network.ZoneDetails), merged.Network.ZoneDetails)
	}
	if merged.Network.ZoneDetails[0].SubnetID != "subnet-customer-c" {
		t.Errorf("expected zone_details subnet-customer-c, got %q", merged.Network.ZoneDetails[0].SubnetID)
	}
	if merged.AMI != "ami-base" {
		t.Errorf("expected ami inherited, got %q", merged.AMI)
	}
}

// TestMergeSpec_AmazonPrivateIPDirection documents the (intended) limitation of the JSON
// omitempty-based merge for boolean/zero-value fields: an override can only flip a bool in the
// truthy direction. A tenant CAN turn private_ip false->true (the PrivateLink use case), but an
// override cannot force it back to false because a false bool is elided from the override JSON
// and therefore inherits the base value. Callers that need per-tenant "public" networking should
// keep the base private_ip=false and opt individual tenants into private_ip=true.
func TestMergeSpec_AmazonPrivateIPDirection(t *testing.T) {
	// false -> true works.
	up := mustMerge(t,
		&Amazon{Network: AmazonNetwork{PrivateIP: false}},
		&Amazon{Network: AmazonNetwork{PrivateIP: true}},
	).(*Amazon)
	if !up.Network.PrivateIP {
		t.Errorf("expected private_ip false->true override to apply")
	}

	// true -> false does NOT downgrade (false is omitted from the override); base value wins.
	down := mustMerge(t,
		&Amazon{Network: AmazonNetwork{PrivateIP: true}},
		&Amazon{Network: AmazonNetwork{PrivateIP: false}},
	).(*Amazon)
	if !down.Network.PrivateIP {
		t.Errorf("documented limitation changed: expected private_ip to remain true (cannot override true->false)")
	}
}

func mustMerge(t *testing.T, base, override interface{}) interface{} {
	t.Helper()
	merged, err := MergeSpec(base, override)
	if err != nil {
		t.Fatalf("MergeSpec error: %v", err)
	}
	return merged
}
