package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/drone/runner-go/logger"
	"github.com/hashicorp/golang-lru/v2/expirable"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const testMachineType = "c4d-standard-8"

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
			//nolint:revive // reproduces GCP's exact stockout message verbatim
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
			//nolint:revive // reproduces GCP's exact stockout error code verbatim
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

	got := p.buildCreateCandidates(first, testMachineType)

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

	got := p.buildCreateCandidates(first, testMachineType)

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

	got := p.buildCreateCandidates(first, testMachineType)

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

	got := zonesOf(p.buildCreateCandidates(first, testMachineType))

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

	got := p.buildCreateCandidates(first, testMachineType)

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

	got := p.buildCreateCandidates(first, testMachineType)

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

// --- stockout cache deprioritization ---

func newTestStockoutCache() *expirable.LRU[string, struct{}] {
	return expirable.NewLRU[string, struct{}](16, nil, time.Minute)
}

func TestBuildCreateCandidates_DeprioritizesCachedStockoutFirst(t *testing.T) {
	// nc0 first zone is the initial pick; mark it stocked out so it gets pushed
	// to the back even though it is candidate 0.
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-central", zones: []string{zoneUSCentral1A, zoneUSCentral1B}},
			{network: "vpc-east", zones: []string{zoneUSEast1B}},
		},
		stockoutCache: newTestStockoutCache(),
	}
	p.markStockout(zoneUSCentral1A, testMachineType)

	first := createCandidate{zone: zoneUSCentral1A, network: networkVPCCentral}
	got := p.buildCreateCandidates(first, testMachineType)
	gotZones := zonesOf(got)

	if gotZones[0] == zoneUSCentral1A {
		t.Fatalf("cached-bad zone should not be first, got %v", gotZones)
	}
	// Deprioritized, not removed: it must still appear, at the back.
	if gotZones[len(gotZones)-1] != zoneUSCentral1A {
		t.Fatalf("cached-bad zone should be deprioritized to the back, got %v", gotZones)
	}
}

func TestBuildCreateCandidates_StockoutDeprioritizationIsStable(t *testing.T) {
	// nc1's zone is stocked out; healthy nc0 zones keep their relative order
	// ahead of it.
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-central", zones: []string{zoneUSCentral1A}},
			{network: "vpc-east", zones: []string{zoneUSEast1B}},
		},
		stockoutCache: newTestStockoutCache(),
	}
	p.markStockout(zoneUSEast1B, testMachineType)

	first := createCandidate{zone: zoneUSCentral1A, network: networkVPCCentral}
	gotZones := zonesOf(p.buildCreateCandidates(first, testMachineType))

	want := []string{zoneUSCentral1A, zoneUSEast1B}
	for i, z := range want {
		if gotZones[i] != z {
			t.Fatalf("want %v (healthy before cached-bad), got %v", want, gotZones)
		}
	}
}

func TestBuildCreateCandidates_AllCachedStillTried(t *testing.T) {
	// Every zone is cached as stocked out: the list must still be non-empty so a
	// create attempt is always made.
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-central", zones: []string{zoneUSCentral1A, zoneUSCentral1B}},
		},
		stockoutCache: newTestStockoutCache(),
	}
	p.markStockout(zoneUSCentral1A, testMachineType)
	p.markStockout(zoneUSCentral1B, testMachineType)

	first := createCandidate{zone: zoneUSCentral1A, network: networkVPCCentral}
	got := p.buildCreateCandidates(first, testMachineType)

	if len(got) == 0 {
		t.Fatal("candidate list must not be empty when all zones are cached-bad")
	}
}

func TestBuildCreateCandidates_StockoutKeyedByMachineType(t *testing.T) {
	// A stockout for a different machine type must not deprioritize the zone.
	p := &config{
		projectID: "proj",
		networkConfigs: []networkConfig{
			{network: "vpc-central", zones: []string{zoneUSCentral1A, zoneUSCentral1B}},
		},
		stockoutCache: newTestStockoutCache(),
	}
	p.markStockout(zoneUSCentral1A, "e2-standard-2")

	first := createCandidate{zone: zoneUSCentral1A, network: networkVPCCentral}
	gotZones := zonesOf(p.buildCreateCandidates(first, testMachineType))

	if gotZones[0] != zoneUSCentral1A {
		t.Fatalf("stockout for a different machine type should not deprioritize zone, got %v", gotZones)
	}
}

// --- insertWithStockoutRetry / cleanupFailedInstance (HTTP-level) ---

// stockoutMsg is GCP's resource-exhaustion phrasing; it must contain a marker
// from stockoutMarkers so isStockoutError classifies it as a stockout.
const stockoutMsg = "The zone 'projects/proj/zones/us-central1-a' does not have enough resources available to fulfill the request."

// fakeCompute emulates just enough of the Compute API to drive
// insertWithStockoutRetry: instance insert/delete and zone-operation polling.
// It models GLOBAL_DEFAULT DNS by tracking instance-name existence
// project-wide, so reusing a name across zones returns 409 alreadyExists unless
// the previous instance was deleted first.
type fakeCompute struct {
	mu           sync.Mutex
	exists       bool     // is the (project-wide) instance name currently taken
	events       []string // ordered insert:/delete: calls, for ordering asserts
	insertCount  int32
	deleteCount  int32
	stockoutZone string // inserts here "succeed" then the op fails with stockout
}

func zoneFromPath(path string) string {
	i := strings.Index(path, "/zones/")
	if i < 0 {
		return ""
	}
	rest := path[i+len("/zones/"):]
	if j := strings.Index(rest, "/"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeCompute) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/instances"):
		zone := zoneFromPath(path)
		atomic.AddInt32(&f.insertCount, 1)
		f.mu.Lock()
		f.events = append(f.events, "insert:"+zone)
		if f.exists {
			f.mu.Unlock()
			writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{
				"code":    http.StatusConflict,
				"message": fmt.Sprintf("The resource 'projects/proj/zones/%s/instances/test-vm' already exists", zone),
				"errors":  []map[string]any{{"reason": "alreadyExists", "message": "already exists"}},
			}})
			return
		}
		// Insert is accepted: the name is reserved even though the async op may
		// later fail with a stockout.
		f.exists = true
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"name": "opinsert-" + zone})

	case r.Method == http.MethodDelete && strings.Contains(path, "/instances/"):
		zone := zoneFromPath(path)
		atomic.AddInt32(&f.deleteCount, 1)
		f.mu.Lock()
		f.events = append(f.events, "delete:"+zone)
		f.exists = false
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"name": "opdelete-" + zone})

	case r.Method == http.MethodGet && strings.Contains(path, "/operations/"):
		op := path[strings.Index(path, "/operations/")+len("/operations/"):]
		if strings.HasPrefix(op, "opinsert-") && strings.TrimPrefix(op, "opinsert-") == f.stockoutZone {
			writeJSON(w, http.StatusOK, map[string]any{
				"name":   op,
				"status": "DONE",
				"error":  map[string]any{"errors": []map[string]any{{"message": stockoutMsg}}},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": op, "status": "DONE"})

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newFakeComputeConfig(t *testing.T, f *fakeCompute) (*config, func()) {
	t.Helper()
	srv := httptest.NewServer(f)
	svc, err := compute.NewService(context.Background(), option.WithHTTPClient(srv.Client()))
	if err != nil {
		srv.Close()
		t.Fatalf("compute.NewService: %v", err)
	}
	svc.BasePath = srv.URL + "/"
	return &config{projectID: "proj", service: svc}, srv.Close
}

func twoZoneCandidates() []createCandidate {
	return []createCandidate{
		{zone: "us-central1-a", network: "projects/proj/global/networks/vpc", subnetwork: "projects/proj/regions/us-central1/subnetworks/sub", tags: []string{"t"}},
		{zone: "us-central1-b", network: "projects/proj/global/networks/vpc", subnetwork: "projects/proj/regions/us-central1/subnetworks/sub", tags: []string{"t"}},
	}
}

func newTestInstance() *compute.Instance {
	return &compute.Instance{
		Name:              "test-vm",
		Disks:             []*compute.AttachedDisk{{InitializeParams: &compute.AttachedDiskInitializeParams{}}},
		NetworkInterfaces: []*compute.NetworkInterface{{}},
	}
}

// TestInsertWithStockoutRetry_DeletesOrphanBeforeRetry is the regression guard
// for the GLOBAL_DEFAULT-DNS 409 bug: a stocked-out insert leaves the instance
// name reserved, so the retry in another zone must delete it first or the
// re-insert 409s. Asserts the retry succeeds in the alternate zone and that a
// delete for the stocked-out zone happened before the second insert.
func TestInsertWithStockoutRetry_DeletesOrphanBeforeRetry(t *testing.T) {
	f := &fakeCompute{stockoutZone: "us-central1-a"}
	p, cleanup := newFakeComputeConfig(t, f)
	defer cleanup()

	op, succeeded, err := p.insertWithStockoutRetry(
		context.Background(), newTestInstance(), twoZoneCandidates(),
		&types.InstanceCreateOpts{}, "c4d-standard-4", "pd-balanced",
		true /*stockoutRetryEnabled*/, false /*usesReservation*/, logger.Discard(),
	)
	if err != nil {
		t.Fatalf("expected retry to succeed in alternate zone, got error: %v", err)
	}
	if op == nil {
		t.Fatal("expected a non-nil operation on success")
	}
	if succeeded.zone != "us-central1-b" {
		t.Fatalf("expected success in us-central1-b, got %s", succeeded.zone)
	}
	if got := atomic.LoadInt32(&f.insertCount); got != 2 {
		t.Errorf("expected 2 inserts (a then b), got %d", got)
	}
	if got := atomic.LoadInt32(&f.deleteCount); got != 1 {
		t.Errorf("expected 1 cleanup delete of the stocked-out instance, got %d", got)
	}

	// The delete of the stocked-out zone must precede the second insert,
	// otherwise the re-insert would collide with the reserved name.
	want := []string{"insert:us-central1-a", "delete:us-central1-a", "insert:us-central1-b"}
	f.mu.Lock()
	got := append([]string(nil), f.events...)
	f.mu.Unlock()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("call order: want %v, got %v", want, got)
	}
}

// TestInsertWithStockoutRetry_NoRetryWhenDisabled verifies that with retry
// disabled (e.g. persistent disk / reservation) a stockout fails fast on the
// first candidate without attempting cleanup or a second zone.
func TestInsertWithStockoutRetry_NoRetryWhenDisabled(t *testing.T) {
	f := &fakeCompute{stockoutZone: "us-central1-a"}
	p, cleanup := newFakeComputeConfig(t, f)
	defer cleanup()

	_, _, err := p.insertWithStockoutRetry(
		context.Background(), newTestInstance(), twoZoneCandidates(),
		&types.InstanceCreateOpts{}, "c4d-standard-4", "pd-balanced",
		false /*stockoutRetryEnabled*/, false /*usesReservation*/, logger.Discard(),
	)
	if err == nil {
		t.Fatal("expected stockout error when retry is disabled")
	}
	if !isStockoutError(err) {
		t.Fatalf("expected a stockout error, got: %v", err)
	}
	if got := atomic.LoadInt32(&f.insertCount); got != 1 {
		t.Errorf("expected exactly 1 insert when retry disabled, got %d", got)
	}
	if got := atomic.LoadInt32(&f.deleteCount); got != 0 {
		t.Errorf("expected no cleanup delete when retry disabled, got %d", got)
	}
}

// TestCleanupFailedInstance_Handles404 verifies the best-effort cleanup treats a
// missing instance (e.g. ZonalOnly projects, or GCP already rolled it back) as a
// no-op rather than failing.
func TestCleanupFailedInstance_Handles404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always report the instance as gone.
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{
			"code": http.StatusNotFound, "message": "not found",
		}})
	}))
	defer srv.Close()
	svc, err := compute.NewService(context.Background(), option.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("compute.NewService: %v", err)
	}
	svc.BasePath = srv.URL + "/"
	p := &config{projectID: "proj", service: svc}

	// Must return without panicking or blocking.
	p.cleanupFailedInstance(context.Background(), "us-central1-a", "test-vm", logger.Discard())
}

// --- pool YAML topology coverage (resolveNetworkAndZone -> buildCreateCandidates) ---

// These cases mirror the distinct google driver topologies found in the real
// pool YAMLs (harness0 prod variants + the local runner pool) so we can verify
// the stockout-aware candidate path (resolveNetworkAndZone -> buildCreateCandidates,
// the exact flow create() uses) holds up for every shape: multi-network configs,
// single-network configs, single network/subnetwork/tags fields, bare network,
// and single-zone pools.

const (
	prodProject  = "cie-hosted-vm-paid-prod"
	playProject  = "ci-play"
	prodVPC      = "projects/sharedvpc-prod-362718/global/networks/cie-hosted-vm-prod-vpc"
	subWest1     = "projects/sharedvpc-prod-362718/regions/us-west1/subnetworks/s1-west1-cie-hosted-vm-paid-prod"
	subCentral1  = "projects/sharedvpc-prod-362718/regions/us-central1/subnetworks/s1-central1-cie-hosted-vm-paid-prod"
	zoneUSWest1B = "us-west1-b"
	zoneUSCentF  = "us-central1-f"
	poolMachine  = "c4d-standard-4"
)

func westNetworkConfig() networkConfig {
	return networkConfig{
		network:    prodVPC,
		subnetwork: subWest1,
		tags:       []string{"allow-dlite", "self-managed-nat-us-west1"},
		zones:      []string{zoneUSWest1A, zoneUSWest1B},
	}
}

func centralNetworkConfig() networkConfig {
	return networkConfig{
		network:    prodVPC,
		subnetwork: subCentral1,
		tags:       []string{"allow-dlite", "self-managed-nat-us-central1"},
		zones:      []string{zoneUSCentral1A, zoneUSCentral1B, zoneUSCentF},
	}
}

// validateCandidates asserts the invariants the create()/stockout path relies on.
func validateCandidates(t *testing.T, name string, got []createCandidate, allowed map[string]bool, wantCount int, requireSubnet bool) {
	t.Helper()
	if len(got) != wantCount {
		t.Fatalf("%s: want %d candidates, got %d (%v)", name, wantCount, len(got), zonesOf(got))
	}
	seen := map[string]bool{}
	for _, c := range got {
		if c.zone == "" {
			t.Errorf("%s: empty zone in %v", name, zonesOf(got))
		}
		if !allowed[c.zone] {
			t.Errorf("%s: zone %q not in configured set %v", name, c.zone, zonesOf(got))
		}
		if seen[c.zone] {
			t.Errorf("%s: duplicate zone %q in %v", name, c.zone, zonesOf(got))
		}
		seen[c.zone] = true
		if !strings.HasPrefix(c.network, "projects/") || !strings.Contains(c.network, "/networks/") {
			t.Errorf("%s: network not fully qualified: %q", name, c.network)
		}
		if requireSubnet && (!strings.HasPrefix(c.subnetwork, "projects/") || !strings.Contains(c.subnetwork, "/subnetworks/")) {
			t.Errorf("%s: subnetwork not fully qualified: %q", name, c.subnetwork)
		}
	}
}

func TestPoolYAMLTopologies_CandidatePath(t *testing.T) {
	min3 := func(n int) int {
		if n < maxStockoutAttempts {
			return n
		}
		return maxStockoutAttempts
	}

	cases := []struct {
		name          string
		cfg           *config
		allowed       []string
		requireSubnet bool
	}{
		{
			name: "harness0 linux-amd64 (2 network configs, west1+central1)",
			cfg: &config{
				projectID:      prodProject,
				network:        "default", // WithNetwork default; unused when networkConfigs set
				tags:           defaultTags,
				zones:          []string{zoneUSWest1A, zoneUSWest1B},
				networkConfigs: []networkConfig{westNetworkConfig(), centralNetworkConfig()},
			},
			allowed:       []string{zoneUSWest1A, zoneUSWest1B, zoneUSCentral1A, zoneUSCentral1B, zoneUSCentF},
			requireSubnet: true,
		},
		{
			name: "harness0.1 linux-amd64 (single network config, west1)",
			cfg: &config{
				projectID:      prodProject,
				zones:          []string{zoneUSWest1A, zoneUSWest1B},
				networkConfigs: []networkConfig{westNetworkConfig()},
			},
			allowed:       []string{zoneUSWest1A, zoneUSWest1B},
			requireSubnet: true,
		},
		{
			name: "harness0 linux-arm64 (single network/subnetwork/tags fields)",
			cfg: &config{
				projectID:  prodProject,
				network:    prodVPC,
				subnetwork: subCentral1,
				tags:       []string{"allow-dlite", "self-managed-nat-us-central1"},
				zones:      []string{zoneUSCentral1B, zoneUSCentF},
			},
			allowed:       []string{zoneUSCentral1B, zoneUSCentF},
			requireSubnet: true,
		},
		{
			name: "harness0.2 linux-amd64 (envSpecific placeholder single fields)",
			cfg: &config{
				projectID:  prodProject,
				network:    "envSpecific",
				subnetwork: "envSpecific",
				tags:       []string{"allow-dlite"},
				zones:      []string{zoneUSWest1A, zoneUSWest1B},
			},
			allowed:       []string{zoneUSWest1A, zoneUSWest1B},
			requireSubnet: true,
		},
		{
			name: "harness0 windows-amd64-fallback (central1 then west1)",
			cfg: &config{
				projectID:      prodProject,
				zones:          []string{zoneUSCentral1A, zoneUSCentral1B, zoneUSCentF},
				networkConfigs: []networkConfig{centralNetworkConfig(), westNetworkConfig()},
			},
			allowed:       []string{zoneUSCentral1A, zoneUSCentral1B, zoneUSCentF, zoneUSWest1A, zoneUSWest1B},
			requireSubnet: true,
		},
		{
			name: "runner linux-amd64-bare-metal (bare network=default, no subnetwork)",
			cfg: &config{
				projectID: playProject,
				network:   "default", // WithNetwork("") -> "default"
				tags:      defaultTags,
				zones:     []string{zoneUSWest1A, zoneUSWest1B},
			},
			allowed:       []string{zoneUSWest1A, zoneUSWest1B},
			requireSubnet: false,
		},
		{
			name: "runner linux-amd64-west4 (single zone)",
			cfg: &config{
				projectID: playProject,
				network:   "default",
				tags:      defaultTags,
				zones:     []string{zoneUSWest1A},
			},
			allowed:       []string{zoneUSWest1A},
			requireSubnet: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allowed := map[string]bool{}
			for _, z := range tc.allowed {
				allowed[z] = true
			}
			wantCount := min3(len(tc.allowed))

			// Run many iterations to exercise zone shuffling and network-config
			// round-robin; every iteration must satisfy the invariants.
			for i := 0; i < 100; i++ {
				zone, network, subnetwork, tags := tc.cfg.resolveNetworkAndZone("", nil)
				first := createCandidate{zone: zone, network: network, subnetwork: subnetwork, tags: tags}
				got := tc.cfg.buildCreateCandidates(first, poolMachine)

				// First candidate must be the initially resolved selection.
				if got[0].zone != first.zone || got[0].network != first.network {
					t.Fatalf("%s: first candidate not preserved: first=%+v got0=%+v", tc.name, first, got[0])
				}
				validateCandidates(t, tc.name, got, allowed, wantCount, tc.requireSubnet)
			}
		})
	}
}

// TestPoolYAMLTopologies_Deprioritization confirms that, for the busiest real
// topology (2 network configs), a stocked-out zone is pushed to the back while
// the candidate set stays valid and capped.
func TestPoolYAMLTopologies_Deprioritization(t *testing.T) {
	cfg := &config{
		projectID:      prodProject,
		zones:          []string{zoneUSWest1A, zoneUSWest1B},
		networkConfigs: []networkConfig{westNetworkConfig(), centralNetworkConfig()},
		stockoutCache:  newTestStockoutCache(),
	}
	cfg.markStockout(zoneUSWest1A, poolMachine)

	for i := 0; i < 100; i++ {
		zone, network, subnetwork, tags := cfg.resolveNetworkAndZone("", nil)
		first := createCandidate{zone: zone, network: network, subnetwork: subnetwork, tags: tags}
		got := zonesOf(cfg.buildCreateCandidates(first, poolMachine))

		// If the stocked-out zone is present, it must not be first (unless it is
		// the only candidate, which cannot happen here given 5 distinct zones).
		if got[0] == zoneUSWest1A {
			t.Fatalf("stocked-out zone should be deprioritized off the front, got %v", got)
		}
	}
}
