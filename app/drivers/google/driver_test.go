package google

import (
	"errors"
	"testing"

	"google.golang.org/api/googleapi"
)

// --- isStockoutError ---

func TestIsStockoutError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{
			name: "operation message resource exhausted",
			err:  errors.New("The zone 'projects/p/zones/us-west1-a' does not have enough resources available to fulfill the request."),
			want: true,
		},
		{
			name: "plain string ZONE_RESOURCE_POOL_EXHAUSTED",
			err:  errors.New("ZONE_RESOURCE_POOL_EXHAUSTED"),
			want: true,
		},
		{
			name: "plain string with details variant",
			err:  errors.New("ZONE_RESOURCE_POOL_EXHAUSTED_WITH_DETAILS: ..."),
			want: true,
		},
		{
			name: "plain string POOL_CAPACITY_INSUFFICIENT",
			err:  errors.New("POOL_CAPACITY_INSUFFICIENT"),
			want: true,
		},
		{
			name: "plain string STOCKOUT",
			err:  errors.New("got STOCKOUT from provider"),
			want: true,
		},
		{
			name: "googleapi error reason",
			err:  &googleapi.Error{Code: 503, Errors: []googleapi.ErrorItem{{Reason: "ZONE_RESOURCE_POOL_EXHAUSTED", Message: "boom"}}},
			want: true,
		},
		{
			name: "googleapi error message",
			err:  &googleapi.Error{Code: 503, Errors: []googleapi.ErrorItem{{Reason: "RESOURCE_OPERATION_RATE_EXCEEDED", Message: "does not have enough resources available"}}},
			want: true,
		},
		{
			name: "generic 429 not stockout",
			err:  &googleapi.Error{Code: 429, Message: "rateLimitExceeded"},
			want: false,
		},
		{
			name: "generic 503 not stockout",
			err:  &googleapi.Error{Code: 503, Message: "backendError"},
			want: false,
		},
		{
			name: "unrelated error",
			err:  errors.New("failed to generate user data"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStockoutError(tt.err); got != tt.want {
				t.Errorf("isStockoutError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsStockoutError_WrappedGoogleAPIError(t *testing.T) {
	base := &googleapi.Error{Code: 503, Errors: []googleapi.ErrorItem{{Reason: "ZONE_RESOURCE_POOL_EXHAUSTED"}}}
	wrapped := errors.Join(errors.New("provision failed"), base)
	if !isStockoutError(wrapped) {
		t.Errorf("expected wrapped googleapi stockout error to be detected")
	}
}

// --- buildCreateCandidates ---

func zonesOf(c []createCandidate) []string {
	out := make([]string, len(c))
	for i := range c {
		out[i] = c[i].zone
	}
	return out
}

func TestBuildCreateCandidates_PreservesFirst(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-central", subnetwork: "sub-central", zones: []string{zoneUSCentral1A, zoneUSCentral1B}},
		},
	}
	first := createCandidate{zone: zoneUSCentral1A, network: networkVPCCentral, subnetwork: subnetworkCentral}

	got := p.buildCreateCandidates(first)

	if len(got) == 0 || got[0].zone != zoneUSCentral1A || got[0].network != networkVPCCentral {
		t.Fatalf("first candidate not preserved, got %+v", got)
	}
}

func TestBuildCreateCandidates_NoNetworkConfigs_UsesPoolZones(t *testing.T) {
	p := &config{
		projectID:  "proj",
		network:    "vpc-1",
		subnetwork: "sub-1",
		zones:      []string{zoneUSCentral1A, zoneUSCentral1B, zoneUSCentral1C},
	}
	first := createCandidate{zone: zoneUSCentral1A, network: networkVPC1}

	got := p.buildCreateCandidates(first)

	gotZones := zonesOf(got)
	if len(gotZones) != 3 {
		t.Fatalf("zones: want 3 candidates, got %v", gotZones)
	}
	// First candidate is preserved; alternates are the remaining pool zones in
	// any order (zones are shuffled within a network config).
	if gotZones[0] != zoneUSCentral1A {
		t.Errorf("first zone: want %s, got %s", zoneUSCentral1A, gotZones[0])
	}
	alternates := map[string]bool{gotZones[1]: true, gotZones[2]: true}
	for _, z := range []string{zoneUSCentral1B, zoneUSCentral1C} {
		if !alternates[z] {
			t.Errorf("expected alternate zone %s in %v", z, gotZones)
		}
	}
	// Alternate candidates should resolve a fully-qualified network.
	if got[1].network != networkVPC1 {
		t.Errorf("alternate network: want %s, got %s", networkVPC1, got[1].network)
	}
}

func TestBuildCreateCandidates_NetworkConfigsStayInOrder(t *testing.T) {
	// nc0 has one remaining zone after the first attempt, nc1 has one zone, so
	// the ordering across configs is deterministic and must be nc0 then nc1.
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-central", zones: []string{zoneUSCentral1A, zoneUSCentral1B}},
			{network: "vpc-east", zones: []string{zoneUSEast1B}},
		},
	}
	first := createCandidate{zone: zoneUSCentral1A, network: networkVPCCentral}

	got := p.buildCreateCandidates(first)

	want := []string{zoneUSCentral1A, zoneUSCentral1B, zoneUSEast1B}
	gotZones := zonesOf(got)
	for i, z := range want {
		if i >= len(gotZones) || gotZones[i] != z {
			t.Fatalf("zones: want %v (nc0 before nc1), got %v", want, gotZones)
		}
	}
}

func TestBuildCreateCandidates_ExcludesDuplicateFirstZone(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-central", zones: []string{zoneUSCentral1A, zoneUSCentral1B}},
		},
	}
	first := createCandidate{zone: zoneUSCentral1A, network: networkVPCCentral}

	got := zonesOf(p.buildCreateCandidates(first))

	// us-central1-a is the first attempt and must not be repeated.
	count := 0
	for _, z := range got {
		if z == zoneUSCentral1A {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected us-central1-a exactly once, got %d in %v", count, got)
	}
}

func TestBuildCreateCandidates_CapsAtMaxAttempts(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-a", zones: []string{"z-a", "z-b", "z-c"}},
			{network: "vpc-b", zones: []string{"z-d", "z-e"}},
		},
	}
	first := createCandidate{zone: "z-a", network: "projects/proj/global/networks/vpc-a"}

	got := p.buildCreateCandidates(first)

	if len(got) != maxStockoutAttempts {
		t.Errorf("expected %d candidates, got %d (%v)", maxStockoutAttempts, len(got), zonesOf(got))
	}
}

func TestBuildCreateCandidates_EnumeratesAcrossNetworkConfigs(t *testing.T) {
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-central", zones: []string{zoneUSCentral1A}},
			{network: "vpc-east", zones: []string{zoneUSEast1B}},
		},
	}
	first := createCandidate{zone: zoneUSCentral1A, network: networkVPCCentral}

	got := p.buildCreateCandidates(first)

	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %d (%v)", len(got), zonesOf(got))
	}
	if got[1].zone != zoneUSEast1B {
		t.Errorf("second candidate zone: want %s, got %s", zoneUSEast1B, got[1].zone)
	}
	if got[1].network != networkVPCEast {
		t.Errorf("second candidate network: want %s, got %s", networkVPCEast, got[1].network)
	}
}
