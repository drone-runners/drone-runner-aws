package google

import (
	"sync"
	"testing"
)

// --- selectNetwork ---

func TestSelectNetwork_NoNetworkConfigs_FallsBackToSingleFields(t *testing.T) {
	p := &config{
		network:    "my-vpc",
		subnetwork: "my-subnet",
		tags:       []string{"tag-a"},
		zones:      []string{"us-central1-a"},
	}

	nc := p.selectNetwork("")

	if nc.network != "my-vpc" {
		t.Errorf("network: want my-vpc, got %s", nc.network)
	}
	if nc.subnetwork != "my-subnet" {
		t.Errorf("subnetwork: want my-subnet, got %s", nc.subnetwork)
	}
	if len(nc.tags) != 1 || nc.tags[0] != "tag-a" {
		t.Errorf("tags: want [tag-a], got %v", nc.tags)
	}
	if len(nc.zones) != 1 || nc.zones[0] != "us-central1-a" {
		t.Errorf("zones: want [us-central1-a], got %v", nc.zones)
	}
}

func TestSelectNetwork_WithZone_MatchesEntry(t *testing.T) {
	p := &config{
		networkConfigs: []networkConfig{
			{network: "vpc-east", subnetwork: "sub-east", tags: []string{"east"}, zones: []string{"us-east1-b", "us-east1-c"}},
			{network: "vpc-central", subnetwork: "sub-central", tags: []string{"central"}, zones: []string{"us-central1-a", "us-central1-b"}},
		},
	}

	nc := p.selectNetwork("us-central1-a")

	if nc.network != "vpc-central" {
		t.Errorf("want vpc-central, got %s", nc.network)
	}
}

func TestSelectNetwork_WithZone_NoMatch_FallsBackToFirst(t *testing.T) {
	p := &config{
		networkConfigs: []networkConfig{
			{network: "vpc-east", zones: []string{"us-east1-b"}},
			{network: "vpc-central", zones: []string{"us-central1-a"}},
		},
	}

	nc := p.selectNetwork("europe-west1-b")

	if nc.network != "vpc-east" {
		t.Errorf("want vpc-east (first entry), got %s", nc.network)
	}
}

func TestSelectNetwork_NoZone_RoundRobin(t *testing.T) {
	p := &config{
		networkConfigs: []networkConfig{
			{network: "vpc-0"},
			{network: "vpc-1"},
			{network: "vpc-2"},
		},
	}

	results := make([]string, 6)
	for i := range results {
		results[i] = p.selectNetwork("").network
	}

	expected := []string{"vpc-0", "vpc-1", "vpc-2", "vpc-0", "vpc-1", "vpc-2"}
	for i, want := range expected {
		if results[i] != want {
			t.Errorf("call %d: want %s, got %s", i, want, results[i])
		}
	}
}

func TestSelectNetwork_RoundRobin_Concurrent(t *testing.T) {
	p := &config{
		networkConfigs: []networkConfig{
			{network: "vpc-0"},
			{network: "vpc-1"},
		},
	}

	counts := map[string]int{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	n := 100
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			nc := p.selectNetwork("")
			mu.Lock()
			counts[nc.network]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	if counts["vpc-0"] == 0 || counts["vpc-1"] == 0 {
		t.Errorf("expected both configs selected, got %v", counts)
	}
}

// --- allZones ---

func TestAllZones_NoNetworkConfigs(t *testing.T) {
	p := &config{zones: []string{"us-central1-a", "us-central1-b"}}

	zones := p.allZones()

	if len(zones) != 2 || zones[0] != "us-central1-a" || zones[1] != "us-central1-b" {
		t.Errorf("want pool zones, got %v", zones)
	}
}

func TestAllZones_FromNetworkConfigs_Deduplicated(t *testing.T) {
	p := &config{
		zones: []string{"fallback-zone"},
		networkConfigs: []networkConfig{
			{zones: []string{"us-east1-b", "us-central1-a"}},
			{zones: []string{"us-central1-a", "us-west1-a"}}, // us-central1-a is a duplicate
		},
	}

	zones := p.allZones()

	expected := map[string]bool{"us-east1-b": true, "us-central1-a": true, "us-west1-a": true}
	if len(zones) != len(expected) {
		t.Fatalf("want %d zones, got %d: %v", len(expected), len(zones), zones)
	}
	for _, z := range zones {
		if !expected[z] {
			t.Errorf("unexpected zone %s", z)
		}
	}
}

func TestAllZones_NetworkConfigsWithNoZones_FallsBackToPoolZones(t *testing.T) {
	p := &config{
		zones: []string{"fallback-zone"},
		networkConfigs: []networkConfig{
			{network: "vpc-1"}, // no zones
		},
	}

	zones := p.allZones()

	if len(zones) != 1 || zones[0] != "fallback-zone" {
		t.Errorf("want [fallback-zone], got %v", zones)
	}
}

// --- resolve ---

func TestResolve_SimpleNames_FullyQualified(t *testing.T) {
	nc := &networkConfig{
		network:    "my-vpc",
		subnetwork: "my-subnet",
		tags:       []string{"tag-1", "tag-2"},
	}

	network, subnetwork, zone, tags := nc.resolve("my-project", "us-central1-a", getRegion)

	if network != "projects/my-project/global/networks/my-vpc" {
		t.Errorf("network: got %s", network)
	}
	if subnetwork != "projects/my-project/regions/us-central1/subnetworks/my-subnet" {
		t.Errorf("subnetwork: got %s", subnetwork)
	}
	if zone != "us-central1-a" {
		t.Errorf("zone: want us-central1-a, got %s", zone)
	}
	if len(tags) != 2 || tags[0] != "tag-1" {
		t.Errorf("tags: got %v", tags)
	}
}

func TestResolve_FullyQualifiedPaths_PassedThrough(t *testing.T) {
	nc := &networkConfig{
		network:    "projects/other-project/global/networks/custom",
		subnetwork: "projects/other-project/regions/us-east1/subnetworks/custom-sub",
		tags:       []string{"custom-tag"},
	}

	network, subnetwork, _, _ := nc.resolve("my-project", "us-central1-a", getRegion)

	if network != "projects/other-project/global/networks/custom" {
		t.Errorf("network should pass through, got %s", network)
	}
	if subnetwork != "projects/other-project/regions/us-east1/subnetworks/custom-sub" {
		t.Errorf("subnetwork should pass through, got %s", subnetwork)
	}
}

func TestResolve_EmptyFields(t *testing.T) {
	nc := &networkConfig{
		tags: []string{"tag"},
	}

	network, subnetwork, zone, _ := nc.resolve("proj", "us-central1-a", getRegion)

	if network != "" {
		t.Errorf("network: want empty, got %s", network)
	}
	if subnetwork != "" {
		t.Errorf("subnetwork: want empty, got %s", subnetwork)
	}
	if zone != "us-central1-a" {
		t.Errorf("zone: want us-central1-a, got %s", zone)
	}
}

func TestResolve_NoZoneFallback_PicksFromEntry(t *testing.T) {
	nc := &networkConfig{
		network: "vpc",
		zones:   []string{"us-west1-a"},
	}

	_, _, zone, _ := nc.resolve("proj", "", getRegion)

	if zone != "us-west1-a" {
		t.Errorf("zone: want us-west1-a, got %s", zone)
	}
}

func TestResolve_RegionZone_OverridesEntryZones(t *testing.T) {
	nc := &networkConfig{
		network:    "vpc",
		subnetwork: "sub",
		zones:      []string{"us-east1-b", "us-east1-c"},
	}

	_, subnetwork, zone, _ := nc.resolve("proj", "us-central1-a", getRegion)

	if zone != "us-central1-a" {
		t.Errorf("zone: want us-central1-a (passed in), got %s", zone)
	}
	// subnetwork region should use the passed-in zone, not entry zones
	if subnetwork != "projects/proj/regions/us-central1/subnetworks/sub" {
		t.Errorf("subnetwork region wrong: got %s", subnetwork)
	}
}

func TestResolve_MultipleEntryZones_PicksOne(t *testing.T) {
	nc := &networkConfig{
		network: "vpc",
		zones:   []string{"z-a", "z-b", "z-c"},
	}

	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		_, _, zone, _ := nc.resolve("proj", "", getRegion)
		seen[zone] = true
	}

	for _, z := range nc.zones {
		if !seen[z] {
			t.Errorf("zone %s was never picked", z)
		}
	}
}

// --- WithNetworkConfigs option ---

func TestWithNetworkConfigs_SetsConfigs(t *testing.T) {
	p := &config{}

	opt := WithNetworkConfigs([]NetworkConfigInput{
		{Network: "vpc-1", Subnetwork: "sub-1", Tags: []string{"t1"}, Zones: []string{"z1"}},
		{Network: "vpc-2", Subnetwork: "sub-2", Zones: []string{"z2"}},
	})
	opt(p)

	if len(p.networkConfigs) != 2 {
		t.Fatalf("want 2 configs, got %d", len(p.networkConfigs))
	}
	if p.networkConfigs[0].network != "vpc-1" {
		t.Errorf("config 0 network: want vpc-1, got %s", p.networkConfigs[0].network)
	}
	if p.networkConfigs[0].tags[0] != "t1" {
		t.Errorf("config 0 tags: want [t1], got %v", p.networkConfigs[0].tags)
	}
	// Config without tags should get defaults
	if len(p.networkConfigs[1].tags) != 1 || p.networkConfigs[1].tags[0] != "allow-docker" {
		t.Errorf("config 1 tags: want default [allow-docker], got %v", p.networkConfigs[1].tags)
	}
	if len(p.networkConfigs[1].zones) != 1 || p.networkConfigs[1].zones[0] != "z2" {
		t.Errorf("config 1 zones: want [z2], got %v", p.networkConfigs[1].zones)
	}
}

// --- nextNetworkConfig round-robin ---

func TestNextNetworkConfig_CyclesThroughAll(t *testing.T) {
	p := &config{
		networkConfigs: []networkConfig{
			{network: "a"},
			{network: "b"},
			{network: "c"},
		},
	}

	for cycle := 0; cycle < 3; cycle++ {
		for i, want := range []string{"a", "b", "c"} {
			nc := p.nextNetworkConfig()
			if nc.network != want {
				t.Errorf("cycle %d, index %d: want %s, got %s", cycle, i, want, nc.network)
			}
		}
	}
}

// --- Integration: selectNetwork + resolve end-to-end ---

func TestSelectAndResolve_NoConfigs_SingleFields(t *testing.T) {
	p := &config{
		projectID:  "proj",
		network:    "default-vpc",
		subnetwork: "default-sub",
		tags:       []string{"allow-dlite"},
		zones:      []string{"us-central1-a", "us-central1-b"},
	}

	nc := p.selectNetwork("")
	network, subnetwork, zone, tags := nc.resolve(p.projectID, "", p.GetRegion)

	if network != "projects/proj/global/networks/default-vpc" {
		t.Errorf("network: got %s", network)
	}
	// Zone should be picked from entry zones
	if zone != "us-central1-a" && zone != "us-central1-b" {
		t.Errorf("zone: want one of pool zones, got %s", zone)
	}
	if subnetwork == "" {
		t.Error("subnetwork should not be empty")
	}
	if len(tags) != 1 || tags[0] != "allow-dlite" {
		t.Errorf("tags: got %v", tags)
	}
}

func TestSelectAndResolve_WithConfigs_ZoneMatch(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-east", subnetwork: "sub-east", tags: []string{"east-tag"}, zones: []string{"us-east1-b"}},
			{network: "vpc-central", subnetwork: "sub-central", tags: []string{"central-tag"}, zones: []string{"us-central1-a"}},
		},
	}

	nc := p.selectNetwork("us-central1-a")
	network, subnetwork, zone, tags := nc.resolve(p.projectID, "us-central1-a", p.GetRegion)

	if network != "projects/proj/global/networks/vpc-central" {
		t.Errorf("network: got %s", network)
	}
	if subnetwork != "projects/proj/regions/us-central1/subnetworks/sub-central" {
		t.Errorf("subnetwork: got %s", subnetwork)
	}
	if zone != "us-central1-a" {
		t.Errorf("zone: got %s", zone)
	}
	if len(tags) != 1 || tags[0] != "central-tag" {
		t.Errorf("tags: got %v", tags)
	}
}

func TestSelectAndResolve_WithConfigs_RoundRobin_PicksZone(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-a", subnetwork: "sub-a", tags: []string{"a"}, zones: []string{"us-east1-b"}},
			{network: "vpc-b", subnetwork: "sub-b", tags: []string{"b"}, zones: []string{"us-west1-a"}},
		},
	}

	// First call: round-robin picks vpc-a
	nc1 := p.selectNetwork("")
	net1, sub1, zone1, tags1 := nc1.resolve(p.projectID, "", p.GetRegion)

	if net1 != "projects/proj/global/networks/vpc-a" {
		t.Errorf("call 1 network: got %s", net1)
	}
	if sub1 != "projects/proj/regions/us-east1/subnetworks/sub-a" {
		t.Errorf("call 1 subnetwork: got %s", sub1)
	}
	if zone1 != "us-east1-b" {
		t.Errorf("call 1 zone: got %s", zone1)
	}
	if tags1[0] != "a" {
		t.Errorf("call 1 tags: got %v", tags1)
	}

	// Second call: round-robin picks vpc-b
	nc2 := p.selectNetwork("")
	net2, _, zone2, _ := nc2.resolve(p.projectID, "", p.GetRegion)

	if net2 != "projects/proj/global/networks/vpc-b" {
		t.Errorf("call 2 network: got %s", net2)
	}
	if zone2 != "us-west1-a" {
		t.Errorf("call 2 zone: got %s", zone2)
	}
}

func TestSelectAndResolve_CapacityReservationZone_OverridesRoundRobin(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-east", subnetwork: "sub-east", tags: []string{"east"}, zones: []string{"us-east1-b"}},
			{network: "vpc-central", subnetwork: "sub-central", tags: []string{"central"}, zones: []string{"us-central1-c"}},
		},
	}

	// Simulate: capacity reservation returned zone us-central1-c
	// selectNetwork should pick vpc-central (matching entry), NOT round-robin
	nc := p.selectNetwork("us-central1-c")
	network, _, zone, _ := nc.resolve(p.projectID, "us-central1-c", p.GetRegion)

	if network != "projects/proj/global/networks/vpc-central" {
		t.Errorf("want vpc-central for reservation zone, got %s", network)
	}
	if zone != "us-central1-c" {
		t.Errorf("zone: got %s", zone)
	}
}

// --- resolveNetworkAndZone (full create flow logic) ---

// Case 1: No networkConfigs — uses single fields

func TestResolveNetworkAndZone_NoConfigs_NoRequestZones(t *testing.T) {
	p := &config{
		projectID:  "proj",
		network:    "default-vpc",
		subnetwork: "default-sub",
		tags:       []string{"tag-1"},
		zones:      []string{"us-central1-a", "us-central1-b"},
	}

	zone, network, _, tags := p.resolveNetworkAndZone("", nil)

	// No networkConfigs fallback creates a networkConfig with p.zones,
	// so resolve picks a random zone from the entry
	if zone != "us-central1-a" && zone != "us-central1-b" {
		t.Errorf("zone: want one of pool zones, got %s", zone)
	}
	if network != "projects/proj/global/networks/default-vpc" {
		t.Errorf("network: got %s", network)
	}
	if len(tags) != 1 || tags[0] != "tag-1" {
		t.Errorf("tags: got %v", tags)
	}
}

func TestResolveNetworkAndZone_NoConfigs_WithRequestZones(t *testing.T) {
	p := &config{
		projectID:  "proj",
		network:    "default-vpc",
		subnetwork: "default-sub",
		tags:       []string{"tag-1"},
		zones:      []string{"us-central1-a"},
	}

	zone, network, subnetwork, tags := p.resolveNetworkAndZone("", []string{"us-east1-b"})

	if zone != "us-east1-b" {
		t.Errorf("zone: want us-east1-b (from request), got %s", zone)
	}
	if network != "projects/proj/global/networks/default-vpc" {
		t.Errorf("network: got %s", network)
	}
	// Subnetwork region should be derived from the request zone
	if subnetwork != "projects/proj/regions/us-east1/subnetworks/default-sub" {
		t.Errorf("subnetwork: got %s", subnetwork)
	}
	if len(tags) != 1 || tags[0] != "tag-1" {
		t.Errorf("tags: got %v", tags)
	}
}

func TestResolveNetworkAndZone_NoConfigs_ReservationZone_OverridesRequestZone(t *testing.T) {
	p := &config{
		projectID:  "proj",
		network:    "my-vpc",
		subnetwork: "my-sub",
		tags:       []string{"t"},
		zones:      []string{"us-central1-a"},
	}

	zone, _, subnetwork, _ := p.resolveNetworkAndZone("us-west1-c", []string{"us-east1-b"})

	// Reservation zone has highest priority
	if zone != "us-west1-c" {
		t.Errorf("zone: want us-west1-c (reservation), got %s", zone)
	}
	// Subnetwork region should use reservation zone
	if subnetwork != "projects/proj/regions/us-west1/subnetworks/my-sub" {
		t.Errorf("subnetwork: got %s", subnetwork)
	}
}

func TestResolveNetworkAndZone_NoConfigs_EmptyNetwork(t *testing.T) {
	p := &config{
		projectID: "proj",
		zones:     []string{"us-central1-a"},
	}

	zone, network, subnetwork, _ := p.resolveNetworkAndZone("", nil)

	if zone != "us-central1-a" {
		t.Errorf("zone: got %s", zone)
	}
	if network != "" {
		t.Errorf("network: want empty, got %s", network)
	}
	if subnetwork != "" {
		t.Errorf("subnetwork: want empty, got %s", subnetwork)
	}
}

// Case 2: With networkConfigs

func TestResolveNetworkAndZone_WithConfigs_NoRequestZones_RoundRobin(t *testing.T) {
	p := &config{
		projectID: "proj",
		zones:     []string{"fallback-zone"},
		networkConfigs: []networkConfig{
			{network: "vpc-east", subnetwork: "sub-east", tags: []string{"east"}, zones: []string{"us-east1-b"}},
			{network: "vpc-west", subnetwork: "sub-west", tags: []string{"west"}, zones: []string{"us-west1-a"}},
		},
	}

	// First call: round-robin picks vpc-east
	zone1, net1, sub1, tags1 := p.resolveNetworkAndZone("", nil)

	if zone1 != "us-east1-b" {
		t.Errorf("call 1 zone: want us-east1-b, got %s", zone1)
	}
	if net1 != "projects/proj/global/networks/vpc-east" {
		t.Errorf("call 1 network: got %s", net1)
	}
	if sub1 != "projects/proj/regions/us-east1/subnetworks/sub-east" {
		t.Errorf("call 1 subnetwork: got %s", sub1)
	}
	if tags1[0] != "east" {
		t.Errorf("call 1 tags: got %v", tags1)
	}

	// Second call: round-robin picks vpc-west
	zone2, net2, _, tags2 := p.resolveNetworkAndZone("", nil)

	if zone2 != "us-west1-a" {
		t.Errorf("call 2 zone: want us-west1-a, got %s", zone2)
	}
	if net2 != "projects/proj/global/networks/vpc-west" {
		t.Errorf("call 2 network: got %s", net2)
	}
	if tags2[0] != "west" {
		t.Errorf("call 2 tags: got %v", tags2)
	}
}

func TestResolveNetworkAndZone_WithConfigs_RequestZone_MatchesEntry(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-east", subnetwork: "sub-east", tags: []string{"east"}, zones: []string{"us-east1-b", "us-east1-c"}},
			{network: "vpc-central", subnetwork: "sub-central", tags: []string{"central"}, zones: []string{"us-central1-a", "us-central1-c"}},
		},
	}

	zone, net, sub, tags := p.resolveNetworkAndZone("", []string{"us-central1-a"})

	// Request zone us-central1-a matches vpc-central
	if zone != "us-central1-a" {
		t.Errorf("zone: want us-central1-a, got %s", zone)
	}
	if net != "projects/proj/global/networks/vpc-central" {
		t.Errorf("network: want vpc-central, got %s", net)
	}
	if sub != "projects/proj/regions/us-central1/subnetworks/sub-central" {
		t.Errorf("subnetwork: got %s", sub)
	}
	if tags[0] != "central" {
		t.Errorf("tags: got %v", tags)
	}
}

func TestResolveNetworkAndZone_WithConfigs_RequestZone_NoMatch_FallsBackToFirst(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-east", subnetwork: "sub-east", tags: []string{"east"}, zones: []string{"us-east1-b"}},
			{network: "vpc-west", subnetwork: "sub-west", tags: []string{"west"}, zones: []string{"us-west1-a"}},
		},
	}

	zone, net, _, _ := p.resolveNetworkAndZone("", []string{"europe-west1-b"})

	// No entry matches europe-west1-b → falls back to first entry
	if zone != "europe-west1-b" {
		t.Errorf("zone: want europe-west1-b (from request), got %s", zone)
	}
	if net != "projects/proj/global/networks/vpc-east" {
		t.Errorf("network: want vpc-east (first entry fallback), got %s", net)
	}
}

func TestResolveNetworkAndZone_WithConfigs_ReservationZone_MatchesEntry(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-east", subnetwork: "sub-east", tags: []string{"east"}, zones: []string{"us-east1-b"}},
			{network: "vpc-central", subnetwork: "sub-central", tags: []string{"central"}, zones: []string{"us-central1-c"}},
		},
	}

	zone, net, sub, tags := p.resolveNetworkAndZone("us-central1-c", nil)

	// Reservation zone matches vpc-central
	if zone != "us-central1-c" {
		t.Errorf("zone: want us-central1-c, got %s", zone)
	}
	if net != "projects/proj/global/networks/vpc-central" {
		t.Errorf("network: want vpc-central, got %s", net)
	}
	if sub != "projects/proj/regions/us-central1/subnetworks/sub-central" {
		t.Errorf("subnetwork: got %s", sub)
	}
	if tags[0] != "central" {
		t.Errorf("tags: got %v", tags)
	}
}

func TestResolveNetworkAndZone_WithConfigs_ReservationZone_OverridesRequestZone(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-east", subnetwork: "sub-east", tags: []string{"east"}, zones: []string{"us-east1-b"}},
			{network: "vpc-central", subnetwork: "sub-central", tags: []string{"central"}, zones: []string{"us-central1-a"}},
		},
	}

	zone, net, _, _ := p.resolveNetworkAndZone("us-central1-a", []string{"us-east1-b"})

	// Reservation zone takes priority over request zone
	if zone != "us-central1-a" {
		t.Errorf("zone: want us-central1-a (reservation), got %s", zone)
	}
	if net != "projects/proj/global/networks/vpc-central" {
		t.Errorf("network: want vpc-central (matched by reservation zone), got %s", net)
	}
}

func TestResolveNetworkAndZone_WithConfigs_NoZonesOnEntries_ReturnsEmptyZone(t *testing.T) {
	p := &config{
		projectID: "proj",
		zones:     []string{"us-central1-a"},
		networkConfigs: []networkConfig{
			{network: "vpc-1", subnetwork: "sub-1", tags: []string{"t1"}},
			{network: "vpc-2", subnetwork: "sub-2", tags: []string{"t2"}},
		},
	}

	zone, net, _, _ := p.resolveNetworkAndZone("", nil)

	// No zones on entries, no request zones → zone is empty (caller handles fallback)
	if zone != "" {
		t.Errorf("zone: want empty, got %s", zone)
	}
	// Network should still be selected via round-robin
	if net != "projects/proj/global/networks/vpc-1" {
		t.Errorf("network: want vpc-1 (round-robin), got %s", net)
	}
}

func TestResolveNetworkAndZone_WithConfigs_MultipleZonesOnEntry_PicksOne(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc", subnetwork: "sub", tags: []string{"t"}, zones: []string{"z-a", "z-b", "z-c"}},
		},
	}

	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		zone, _, _, _ := p.resolveNetworkAndZone("", nil)
		seen[zone] = true
	}

	for _, z := range []string{"z-a", "z-b", "z-c"} {
		if !seen[z] {
			t.Errorf("zone %s was never picked", z)
		}
	}
}

func TestResolveNetworkAndZone_WithConfigs_RoundRobinDoesNotAdvance_WhenZoneMatches(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-0", zones: []string{"z-a"}},
			{network: "vpc-1", zones: []string{"z-b"}},
			{network: "vpc-2", zones: []string{"z-c"}},
		},
	}

	// Zone-matched calls should NOT advance the round-robin counter
	_, net1, _, _ := p.resolveNetworkAndZone("", []string{"z-b"})
	if net1 != "projects/proj/global/networks/vpc-1" {
		t.Errorf("call 1: want vpc-1, got %s", net1)
	}

	// Next call without zone should use round-robin starting from 0 (counter not touched)
	_, net2, _, _ := p.resolveNetworkAndZone("", nil)
	if net2 != "projects/proj/global/networks/vpc-0" {
		t.Errorf("call 2: want vpc-0 (round-robin start), got %s", net2)
	}

	_, net3, _, _ := p.resolveNetworkAndZone("", nil)
	if net3 != "projects/proj/global/networks/vpc-1" {
		t.Errorf("call 3: want vpc-1 (round-robin next), got %s", net3)
	}
}

func TestResolveNetworkAndZone_WithConfigs_FullyQualifiedPaths_PassedThrough(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{
				network:    "projects/other/global/networks/custom",
				subnetwork: "projects/other/regions/us-east1/subnetworks/custom-sub",
				tags:       []string{"custom"},
				zones:      []string{"us-east1-b"},
			},
		},
	}

	zone, net, sub, _ := p.resolveNetworkAndZone("", nil)

	if zone != "us-east1-b" {
		t.Errorf("zone: got %s", zone)
	}
	// Fully qualified paths should pass through unchanged
	if net != "projects/other/global/networks/custom" {
		t.Errorf("network should pass through, got %s", net)
	}
	if sub != "projects/other/regions/us-east1/subnetworks/custom-sub" {
		t.Errorf("subnetwork should pass through, got %s", sub)
	}
}

// helper that mirrors config.GetRegion
func getRegion(zone string) string {
	// e.g. "us-central1-a" -> "us-central1"
	for i := len(zone) - 1; i >= 0; i-- {
		if zone[i] == '-' {
			return zone[:i]
		}
	}
	return zone
}
