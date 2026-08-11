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

// TestResolveTenants_GroupIDUsedAsTenantID verifies that when a tenant sets group_id, it becomes
// the stable tenant identity (returned as ResolvedTenant.ID) and every member account routes to
// it — decoupling identity from the mutable ids list.
func TestResolveTenants_GroupIDUsedAsTenantID(t *testing.T) {
	inst := &Instance{
		Name: "p",
		Type: "amazon",
		Spec: &Amazon{},
		Tenants: []Tenant{
			{GroupID: "group-egress", IDs: []string{"acctA", "acctB"}, Spec: &Amazon{Network: AmazonNetwork{SubnetID: "subnet-egress"}}},
			{IDs: []string{"acctC"}, Spec: &Amazon{Network: AmazonNetwork{SubnetID: "subnet-c"}}},
		},
	}
	tenants, accountToTenant, err := ResolveTenants(inst)
	if err != nil {
		t.Fatalf("ResolveTenants: %v", err)
	}
	if len(tenants) != 3 {
		t.Fatalf("expected 3 tenants (default + 2), got %d", len(tenants))
	}
	if tenants[1].ID != "group-egress" {
		t.Errorf("expected group_id as tenant id, got %q", tenants[1].ID)
	}
	if tenants[2].ID != "acctC" {
		t.Errorf("expected ids[0] fallback for tenant without group_id, got %q", tenants[2].ID)
	}
	for _, acct := range []string{"acctA", "acctB"} {
		if got := accountToTenant[acct]; got != "group-egress" {
			t.Errorf("accountToTenant[%q]: got %q, want group-egress", acct, got)
		}
	}
	if got := accountToTenant["acctC"]; got != "acctC" {
		t.Errorf("accountToTenant[acctC]: got %q, want acctC", got)
	}
	// The override spec merge must still happen under the group id.
	if got := tenants[1].Spec.(*Amazon).Network.SubnetID; got != "subnet-egress" {
		t.Errorf("group tenant subnet: got %q", got)
	}
}

// TestResolveTenants_GroupIDMayEqualOwnMemberID covers the zero-migration cutover: group_id set
// to the current ids[0] (e.g. CbUXX...) while that account remains a member of its own group.
func TestResolveTenants_GroupIDMayEqualOwnMemberID(t *testing.T) {
	inst := &Instance{
		Name: "p",
		Type: "amazon",
		Spec: &Amazon{},
		Tenants: []Tenant{
			{GroupID: "acctA", IDs: []string{"acctA", "acctB"}, Spec: &Amazon{}},
		},
	}
	tenants, accountToTenant, err := ResolveTenants(inst)
	if err != nil {
		t.Fatalf("ResolveTenants: %v", err)
	}
	if tenants[1].ID != "acctA" {
		t.Errorf("tenant id: got %q, want acctA", tenants[1].ID)
	}
	if got := accountToTenant["acctA"]; got != "acctA" {
		t.Errorf("accountToTenant[acctA]: got %q, want acctA", got)
	}
	if got := accountToTenant["acctB"]; got != "acctA" {
		t.Errorf("accountToTenant[acctB]: got %q, want acctA", got)
	}
}

func TestResolveTenants_GroupIDReservedDefault(t *testing.T) {
	inst := &Instance{
		Name: "p",
		Type: "amazon",
		Spec: &Amazon{},
		Tenants: []Tenant{
			{GroupID: DefaultTenantID, IDs: []string{"acctA"}, Spec: &Amazon{}},
		},
	}
	if _, _, err := ResolveTenants(inst); err == nil {
		t.Fatalf("expected error for group_id colliding with reserved default tenant id")
	}
}

func TestResolveTenants_DuplicateGroupID(t *testing.T) {
	inst := &Instance{
		Name: "p",
		Type: "amazon",
		Spec: &Amazon{},
		Tenants: []Tenant{
			{GroupID: "grp", IDs: []string{"acctA"}, Spec: &Amazon{}},
			{GroupID: "grp", IDs: []string{"acctB"}, Spec: &Amazon{}},
		},
	}
	if _, _, err := ResolveTenants(inst); err == nil {
		t.Fatalf("expected error for duplicate group_id")
	}
}

// TestResolveTenants_GroupIDConflictsWithOtherMemberID: a group_id that is another tenant's
// member account id is rejected — otherwise account routing and group identity would disagree.
func TestResolveTenants_GroupIDConflictsWithOtherMemberID(t *testing.T) {
	inst := &Instance{
		Name: "p",
		Type: "amazon",
		Spec: &Amazon{},
		Tenants: []Tenant{
			{GroupID: "acctB", IDs: []string{"acctA"}, Spec: &Amazon{}},
			{IDs: []string{"acctC", "acctB"}, Spec: &Amazon{}},
		},
	}
	if _, _, err := ResolveTenants(inst); err == nil {
		t.Fatalf("expected error for group_id equal to another tenant's member id")
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

// TestMergeSpec_GoogleEgressControlAndNetworks verifies a tenant can opt into egress_control
// (false->true) and replace networks[] (including per-network proxy_url) wholesale.
func TestMergeSpec_GoogleEgressControlAndNetworks(t *testing.T) {
	base := &Google{
		EgressControl: false,
		Networks: []GoogleNetwork{
			{Network: "net-base", Subnetwork: "subnet-base", ProxyURL: "http://base:3128"},
		},
	}
	override := &Google{
		EgressControl: true,
		Networks: []GoogleNetwork{
			{Network: "net-tenant", Subnetwork: "subnet-tenant", ProxyURL: "http://tenant:3128"},
		},
	}

	merged := mustMerge(t, base, override).(*Google)
	if !merged.EgressControl {
		t.Errorf("expected egress_control false->true")
	}
	if len(merged.Networks) != 1 {
		t.Fatalf("expected networks replaced (len 1), got %d", len(merged.Networks))
	}
	if merged.Networks[0].Network != "net-tenant" || merged.Networks[0].ProxyURL != "http://tenant:3128" {
		t.Errorf("unexpected network: %+v", merged.Networks[0])
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

func TestMergeVariants_EmptyOverrideInheritsBase(t *testing.T) {
	base := []types.PoolVariant{{
		Pool: 1,
		SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_large", MachineType: "n2-standard-8", ResourceClass: "large",
		},
	}}
	got := mergeVariants(base, nil)
	if len(got) != 1 || got[0].MachineType != "n2-standard-8" {
		t.Fatalf("expected base inherited, got %+v", got)
	}
	got = mergeVariants(base, []types.PoolVariant{})
	if len(got) != 1 || got[0].MachineType != "n2-standard-8" {
		t.Fatalf("empty slice override should inherit base, got %+v", got)
	}
}

func TestMergeVariants_EmptyBaseReturnsOverride(t *testing.T) {
	override := []types.PoolVariant{{
		SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_flex", MachineType: "n1-standard-1", ResourceClass: "flex"},
	}}
	got := mergeVariants(nil, override)
	if len(got) != 1 || got[0].VariantID != "variant_flex" {
		t.Fatalf("expected override as-is, got %+v", got)
	}
}

func TestMergeVariants_AppendOnlyNewVariantID(t *testing.T) {
	base := []types.PoolVariant{{
		SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large", MachineType: "n2-standard-8", ResourceClass: "large"},
	}}
	override := []types.PoolVariant{{
		SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_flex", MachineType: "n1-standard-1", ResourceClass: "flex", NestedVirtualization: true},
	}}
	got := mergeVariants(base, override)
	if len(got) != 2 {
		t.Fatalf("expected base + appended, got %d", len(got))
	}
	if got[0].VariantID != "variant_large" || got[1].VariantID != "variant_flex" {
		t.Errorf("order: got %q then %q", got[0].VariantID, got[1].VariantID)
	}
}

func TestMergeVariants_DuplicateOverrideIDsLastWins(t *testing.T) {
	base := []types.PoolVariant{{
		SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large", MachineType: "n2-standard-8", ResourceClass: "large"},
	}}
	override := []types.PoolVariant{
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large", MachineType: "c4d-standard-4", ResourceClass: "large"}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large", MachineType: "c4d-standard-8", ResourceClass: "large"}},
	}
	got := mergeVariants(base, override)
	if len(got) != 1 {
		t.Fatalf("expected 1 variant, got %d", len(got))
	}
	if got[0].MachineType != "c4d-standard-8" {
		t.Errorf("last duplicate should win: got %q", got[0].MachineType)
	}
}

func TestMergeVariants_ReplaceByVariantID(t *testing.T) {
	base := []types.PoolVariant{
		{
			Pool: 1, Limit: 5,
			SetupInstanceParams: types.SetupInstanceParams{
				VariantID: "variant_large", MachineType: "c4d-standard-8-lssd",
				DiskType: "hyperdisk-balanced", DiskSize: 100, ResourceClass: "large",
			},
		},
		{
			Pool: 1, Limit: 5,
			SetupInstanceParams: types.SetupInstanceParams{
				VariantID: "variant_xlarge", MachineType: "c4d-standard-8-lssd",
				DiskType: "hyperdisk-balanced", DiskSize: 100, ResourceClass: "xlarge",
			},
		},
		{
			Pool: 0, Limit: 0,
			SetupInstanceParams: types.SetupInstanceParams{
				VariantID: "variant_xxxlarge", MachineType: "g2-standard-16",
				GPU: true, ResourceClass: "xxxlarge",
			},
		},
	}
	// Matching variant_id fully replaces the base entry (not a field merge).
	override := []types.PoolVariant{{
		Pool: 2, Limit: 10,
		SetupInstanceParams: types.SetupInstanceParams{
			VariantID: "variant_large", MachineType: "c4d-standard-8",
			DiskType: "hyperdisk-balanced", DiskSize: 100, ResourceClass: "large",
		},
	}}

	got := mergeVariants(base, override)
	if len(got) != 3 {
		t.Fatalf("expected 3 variants (base length preserved), got %d", len(got))
	}

	large := got[0]
	if large.VariantID != "variant_large" {
		t.Fatalf("order: expected variant_large first, got %q", large.VariantID)
	}
	if large.MachineType != "c4d-standard-8" || large.Pool != 2 || large.Limit != 10 {
		t.Errorf("variant_large should be fully replaced: %+v", large)
	}
	if got[1].MachineType != "c4d-standard-8-lssd" || got[1].VariantID != "variant_xlarge" {
		t.Errorf("unmentioned variant_xlarge must stay unchanged: %+v", got[1])
	}
	if !got[2].GPU || got[2].MachineType != "g2-standard-16" {
		t.Errorf("unmentioned variant_xxxlarge must stay unchanged: %+v", got[2])
	}
}

func TestMergeVariants_FullOverrideAndAppend(t *testing.T) {
	base := []types.PoolVariant{
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large", MachineType: "n2-standard-8", ResourceClass: "large"}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_xlarge", MachineType: "n2-standard-16", ResourceClass: "xlarge"}},
	}
	override := []types.PoolVariant{
		{Pool: 2, SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_large", MachineType: "c2-standard-30", ResourceClass: "large", DiskSize: 200}},
		{SetupInstanceParams: types.SetupInstanceParams{VariantID: "variant_flex", MachineType: "n1-standard-1", ResourceClass: "flex", NestedVirtualization: true}},
	}

	got := mergeVariants(base, override)
	if len(got) != 3 {
		t.Fatalf("expected 3 (2 base + 1 new), got %d %+v", len(got), got)
	}
	if got[0].MachineType != "c2-standard-30" || got[0].Pool != 2 || got[0].ResourceClass != "large" {
		t.Errorf("variant_large replace: %+v", got[0])
	}
	if got[0].DiskSize != 200 {
		t.Errorf("disk_size: got %d", got[0].DiskSize)
	}
	if got[1].VariantID != "variant_xlarge" || got[1].MachineType != "n2-standard-16" {
		t.Errorf("variant_xlarge should remain: %+v", got[1])
	}
	if got[2].VariantID != "variant_flex" || !got[2].NestedVirtualization {
		t.Errorf("new variant_flex should append: %+v", got[2])
	}
}

func TestResolveTenants_ReplaceVariantByID(t *testing.T) {
	inst := &Instance{
		Name: "linux-amd64-gcp",
		Type: "google",
		Spec: &Google{MachineType: "e2-standard-4", Image: "img-base"},
		Variants: []types.PoolVariant{
			{Pool: 1, SetupInstanceParams: types.SetupInstanceParams{
				VariantID: "variant_large", ResourceClass: "large", MachineType: "c4d-standard-8-lssd", DiskType: "hyperdisk-balanced",
			}},
			{Pool: 1, SetupInstanceParams: types.SetupInstanceParams{
				VariantID: "variant_xlarge", ResourceClass: "xlarge", MachineType: "c4d-standard-16-lssd",
			}},
		},
		Tenants: []Tenant{{
			IDs:  []string{"acctA"},
			Spec: &Google{MachineType: "c4d-standard-8"},
			Variants: []types.PoolVariant{{
				Pool: 1,
				SetupInstanceParams: types.SetupInstanceParams{
					VariantID: "variant_large", ResourceClass: "large", MachineType: "c4d-standard-8", DiskType: "hyperdisk-balanced",
				},
			}},
		}},
	}

	tenants, _, err := ResolveTenants(inst)
	if err != nil {
		t.Fatalf("ResolveTenants: %v", err)
	}
	byID := map[string]ResolvedTenant{}
	for _, tn := range tenants {
		byID[tn.ID] = tn
	}

	ta := byID["acctA"]
	if len(ta.Variants) != 2 {
		t.Fatalf("acctA should keep both variants after replace-by-id, got %d", len(ta.Variants))
	}
	if ta.Variants[0].MachineType != "c4d-standard-8" || ta.Variants[0].ResourceClass != "large" {
		t.Errorf("variant_large should be replaced: %+v", ta.Variants[0])
	}
	if ta.Variants[1].MachineType != "c4d-standard-16-lssd" {
		t.Errorf("variant_xlarge should be untouched: %+v", ta.Variants[1])
	}
	if ta.Spec.(*Google).MachineType != "c4d-standard-8" {
		t.Errorf("spec machine_type: got %q", ta.Spec.(*Google).MachineType)
	}
}
