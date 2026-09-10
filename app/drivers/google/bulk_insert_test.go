package google

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drone/runner-go/logger"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/types"
)

func TestZoneFromBulkInsertMetadata(t *testing.T) {
	op := &compute.Operation{
		InstancesBulkInsertOperationMetadata: &compute.InstancesBulkInsertOperationMetadata{
			PerLocationStatus: map[string]compute.BulkInsertOperationStatus{
				"zones/us-west1-a": {CreatedVmCount: 0, FailedToCreateVmCount: 1},
				"zones/us-west1-b": {CreatedVmCount: 1, Status: "DONE"},
			},
		},
	}
	got, err := zoneFromBulkInsertMetadata(op)
	if err != nil {
		t.Fatal(err)
	}
	if got != "us-west1-b" {
		t.Fatalf("zone = %q, want us-west1-b", got)
	}
}

func TestZoneFromBulkInsertMetadata_NoneCreated(t *testing.T) {
	op := &compute.Operation{
		InstancesBulkInsertOperationMetadata: &compute.InstancesBulkInsertOperationMetadata{
			PerLocationStatus: map[string]compute.BulkInsertOperationStatus{
				"zones/us-west1-a": {CreatedVmCount: 0},
			},
		},
	}
	if _, err := zoneFromBulkInsertMetadata(op); err == nil {
		t.Fatal("expected error when no VM was created")
	}
}

func TestZoneFromBulkInsertMetadata_RolledBack(t *testing.T) {
	op := &compute.Operation{
		InstancesBulkInsertOperationMetadata: &compute.InstancesBulkInsertOperationMetadata{
			PerLocationStatus: map[string]compute.BulkInsertOperationStatus{
				"zones/us-west1-a": {
					CreatedVmCount: 1,
					DeletedVmCount: 1,
					Status:         "DONE",
				},
			},
		},
	}
	if _, err := zoneFromBulkInsertMetadata(op); err == nil {
		t.Fatal("expected error when the created VM was rolled back")
	}
}

func TestBulkInsertCandidatesUseFirstRegionalNetworkGroup(t *testing.T) {
	candidates := []createCandidate{
		{zone: "us-central1-a", network: "vpc", subnetwork: "central", tags: []string{"runner"}, proxyURL: "http://central"},
		{zone: "us-central1-b", network: "vpc", subnetwork: "central", tags: []string{"runner"}, proxyURL: "http://central"},
		{zone: "us-west1-a", network: "vpc", subnetwork: "west", tags: []string{"runner"}, proxyURL: "http://west"},
	}

	got := bulkInsertCandidates(candidates)

	if len(got) != 2 {
		t.Fatalf("bulk candidates=%v, want two central candidates", got)
	}
	if got[0].zone != "us-central1-a" || got[1].zone != "us-central1-b" {
		t.Fatalf("bulk zones=%v, want [us-central1-a us-central1-b]", zonesOf(got))
	}
}

func TestBuildRegionalBulkInsertRequestBuildsConfiguredRanks(t *testing.T) {
	userdata := "cloud-config"
	in := &compute.Instance{
		Name:                    "runner-pool-abc",
		MinCpuPlatform:          "Automatic",
		CanIpForward:            false,
		AdvancedMachineFeatures: &compute.AdvancedMachineFeatures{EnableNestedVirtualization: true},
		Metadata:                &compute.Metadata{Items: []*compute.MetadataItems{{Key: "user-data", Value: &userdata}}},
		Labels:                  map[string]string{"pool": "paid"},
		Tags:                    &compute.Tags{Items: []string{"runner", "runner-pool-abc"}},
		ServiceAccounts:         []*compute.ServiceAccount{{Email: "default", Scopes: []string{"scope"}}},
		Scheduling:              &compute.Scheduling{OnHostMaintenance: "MIGRATE"},
		Disks: []*compute.AttachedDisk{{
			Boot: true,
			InitializeParams: &compute.AttachedDiskInitializeParams{
				SourceImage: "https://www.googleapis.com/compute/v1/projects/images/global/images/runner",
				DiskSizeGb:  200,
			},
		}},
		NetworkInterfaces: []*compute.NetworkInterface{{
			Network:    "projects/proj/global/networks/vpc",
			Subnetwork: "projects/proj/regions/us-central1/subnetworks/central",
		}},
	}

	req, err := buildRegionalBulkInsertRequest(
		in,
		"c4d-standard-8",
		"hyperdisk-balanced",
		[]types.MachineTypeFallback{{MachineType: "c4d-standard-8-lssd", DiskType: "hyperdisk-extreme"}},
		[]string{"us-central1-a", "us-central1-b"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if req.Count != 1 || req.MinCount != 1 {
		t.Fatalf("count=%d minCount=%d, want 1/1", req.Count, req.MinCount)
	}
	if _, ok := req.PerInstanceProperties[in.Name]; !ok {
		t.Fatalf("missing exact VM name %q", in.Name)
	}
	if req.InstanceProperties.MachineType != "" {
		t.Fatalf("machine type=%q, want empty with instance flexibility", req.InstanceProperties.MachineType)
	}
	if req.InstanceFlexibilityPolicy == nil {
		t.Fatal("missing machine type selections")
	}
	selections := req.InstanceFlexibilityPolicy.InstanceSelections
	if got := selections["rank-1"]; got.Rank != 1 || !slices.Equal(got.MachineTypes, []string{"c4d-standard-8"}) {
		t.Fatalf("primary selection=%+v", got)
	}
	if got := selections["rank-2"]; got.Rank != 2 || !slices.Equal(got.MachineTypes, []string{"c4d-standard-8-lssd"}) {
		t.Fatalf("fallback selection=%+v", got)
	}
	if req.InstanceProperties.Metadata != in.Metadata ||
		req.InstanceProperties.AdvancedMachineFeatures != in.AdvancedMachineFeatures ||
		req.InstanceProperties.ServiceAccounts[0].Email != "default" {
		t.Fatal("instance properties were not preserved")
	}
	if req.InstanceProperties.Disks != nil {
		t.Fatal("selection disks must override instance properties disks")
	}
	if got := selections["rank-1"].Disks[0].InitializeParams.DiskType; got != "hyperdisk-balanced" {
		t.Fatalf("primary disk type=%q", got)
	}
	if got := selections["rank-2"].Disks[0].InitializeParams.DiskType; got != "hyperdisk-extreme" {
		t.Fatalf("fallback disk type=%q", got)
	}
	if got := selections["rank-2"].Disks[0].InitializeParams.SourceImage; got != in.Disks[0].InitializeParams.SourceImage {
		t.Fatalf("fallback disk source image=%q", got)
	}
	if len(req.LocationPolicy.Zones) != 2 {
		t.Fatalf("locationPolicy zones=%d, want 2", len(req.LocationPolicy.Zones))
	}
}

func TestBuildRegionalBulkInsertRequestRejectsIncompleteFallback(t *testing.T) {
	_, err := buildRegionalBulkInsertRequest(
		newTestInstance(),
		"e2-medium",
		"pd-balanced",
		[]types.MachineTypeFallback{{MachineType: "n2-standard-2"}},
		[]string{"us-central1-a"},
	)
	if err == nil || !strings.Contains(err.Error(), "requires machine_type and disk_type") {
		t.Fatalf("error=%v, want required fallback fields error", err)
	}
}

func TestInsertWithBulkFallbackUsesExistingInsertAfterDefinitiveRejection(t *testing.T) {
	var bulkCalls, insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/regions/us-central1/instances/bulkInsert"):
			atomic.AddInt32(&bulkCalls, 1)
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{
					"code":    http.StatusBadRequest,
					"message": "bulkInsert request is not supported for this instance configuration",
					"errors":  []map[string]any{{"reason": "invalid"}},
				},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/us-central1-a/instances"):
			atomic.AddInt32(&insertCalls, 1)
			var instance compute.Instance
			if err := json.NewDecoder(r.Body).Decode(&instance); err != nil {
				t.Errorf("decode zonal insert: %v", err)
			}
			if instance.Name != "test-vm" {
				t.Errorf("fallback name=%q, want test-vm", instance.Name)
			}
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "id": "2"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/zones/us-central1-a/operations/zonal-op"):
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "status": "DONE", "id": "2"})
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()

	in := newTestInstance()
	in.Metadata = &compute.Metadata{}
	in.NetworkInterfaces = []*compute.NetworkInterface{{}}
	candidates := twoZoneCandidates()

	_, succeeded, err := insertWithBulkFallbackForTest(p, in, candidates, true, false)
	if err != nil {
		t.Fatalf("insertWithBulkFallback: %v", err)
	}
	if succeeded.zone != "us-central1-a" {
		t.Fatalf("succeeded zone=%q, want us-central1-a", succeeded.zone)
	}
	if got := atomic.LoadInt32(&bulkCalls); got != 1 {
		t.Fatalf("bulk calls=%d, want 1", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 1 {
		t.Fatalf("zonal insert calls=%d, want 1", got)
	}
}

func TestInsertWithBulkFallbackDoesNotRetryZonallyAfterAmbiguousRejection(t *testing.T) {
	var bulkCalls, insertCalls, reconcileCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/regions/us-central1/instances/bulkInsert"):
			atomic.AddInt32(&bulkCalls, 1)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]any{
					"code":    http.StatusServiceUnavailable,
					"message": "backend unavailable after request submission",
					"errors":  []map[string]any{{"reason": "backendError"}},
				},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/"):
			atomic.AddInt32(&insertCalls, 1)
			http.Error(w, "unexpected zonal insert", http.StatusInternalServerError)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/zones/us-central1-b/instances/test-vm"):
			if atomic.AddInt32(&reconcileCalls, 1) == 1 {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"name": "test-vm"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/instances/test-vm"):
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()
	p.bulkInsertReconcileTimeout = 25 * time.Millisecond
	p.bulkInsertReconcilePollInterval = 5 * time.Millisecond

	_, failedCandidate, err := insertWithBulkFallbackForTest(
		p, newTestInstance(), twoZoneCandidates(), true, false,
	)
	if err == nil {
		t.Fatal("expected ambiguous bulkInsert error")
	}
	if failedCandidate.zone != "us-central1-b" {
		t.Fatalf("failed candidate zone=%q, want reconciled us-central1-b", failedCandidate.zone)
	}
	if got := atomic.LoadInt32(&bulkCalls); got < 2 {
		t.Fatalf("bulk calls=%d, want at least 2 idempotent attempts", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 0 {
		t.Fatalf("zonal insert calls=%d, want 0", got)
	}
	if got := atomic.LoadInt32(&reconcileCalls); got < 2 {
		t.Fatalf("reconcile calls=%d, want at least 2 to catch delayed visibility", got)
	}
}

func TestInsertWithBulkFallbackDoesNotRetryZonallyWhenAmbiguityCannotBeReconciled(t *testing.T) {
	var bulkCalls, insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/regions/us-central1/instances/bulkInsert"):
			atomic.AddInt32(&bulkCalls, 1)
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]any{"code": http.StatusServiceUnavailable, "message": "ambiguous submission"},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/"):
			atomic.AddInt32(&insertCalls, 1)
			http.Error(w, "unexpected zonal insert", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()
	p.bulkInsertReconcileTimeout = 25 * time.Millisecond
	p.bulkInsertReconcilePollInterval = time.Millisecond

	_, candidate, err := insertWithBulkFallbackForTest(
		p, newTestInstance(), twoZoneCandidates(), true, false,
	)
	if err == nil {
		t.Fatal("expected ambiguous bulkInsert error")
	}
	if candidate.zone != "" || candidate.network != "" || candidate.subnetwork != "" {
		t.Fatalf("candidate=%+v, want no reconciled candidate", candidate)
	}
	if got := atomic.LoadInt32(&bulkCalls); got < 2 {
		t.Fatalf("bulk calls=%d, want idempotent retry", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 0 {
		t.Fatalf("zonal insert calls=%d, want 0", got)
	}
}

func TestInsertWithBulkFallbackReturnsRegionalSuccess(t *testing.T) {
	var bulkCalls, insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/regions/us-central1/instances/bulkInsert"):
			atomic.AddInt32(&bulkCalls, 1)
			var request compute.BulkInsertInstanceResource
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode bulkInsert request: %v", err)
			}
			selections := request.InstanceFlexibilityPolicy.InstanceSelections
			if got := selections["rank-1"]; got.Rank != 1 || !slices.Equal(got.MachineTypes, []string{"c4d-standard-8"}) {
				t.Errorf("primary selection=%+v", got)
			}
			if got := selections["rank-1"].Disks[0].InitializeParams.DiskType; got != "hyperdisk-balanced" {
				t.Errorf("primary disk type=%q, want hyperdisk-balanced", got)
			}
			if got := selections["rank-2"]; got.Rank != 2 || !slices.Equal(got.MachineTypes, []string{"c4d-standard-8-lssd"}) {
				t.Errorf("fallback selection=%+v", got)
			}
			if got := selections["rank-2"].Disks[0].InitializeParams.DiskType; got != "hyperdisk-balanced" {
				t.Errorf("fallback disk type=%q, want hyperdisk-balanced", got)
			}
			writeJSON(w, http.StatusOK, map[string]any{"name": "regional-op", "id": "1"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/regions/us-central1/operations/regional-op"):
			writeJSON(w, http.StatusOK, map[string]any{
				"name":   "regional-op",
				"status": "DONE",
				"id":     "1",
				"instancesBulkInsertOperationMetadata": map[string]any{
					"perLocationStatus": map[string]any{
						"zones/us-central1-b": map[string]any{"createdVmCount": 1, "status": "DONE"},
					},
				},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/"):
			atomic.AddInt32(&insertCalls, 1)
			http.Error(w, "unexpected zonal insert", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()
	in := newTestInstance()
	in.Disks[0].Boot = true

	operation, succeeded, err := insertWithBulkFallbackForTest(
		p, in, twoZoneCandidates(), true, false,
	)
	if err != nil {
		t.Fatalf("insertWithBulkFallback: %v", err)
	}
	if operation == nil || operation.Name != "regional-op" {
		t.Fatalf("operation=%v, want regional-op", operation)
	}
	if succeeded.zone != "us-central1-b" {
		t.Fatalf("succeeded zone=%q, want us-central1-b", succeeded.zone)
	}
	if got := atomic.LoadInt32(&bulkCalls); got != 1 {
		t.Fatalf("bulk calls=%d, want 1", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 0 {
		t.Fatalf("zonal insert calls=%d, want 0", got)
	}
}

func TestInsertWithBulkFallbackTreatsMissingPlacementMetadataAsAmbiguous(t *testing.T) {
	var insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/regions/us-central1/instances/bulkInsert"):
			writeJSON(w, http.StatusOK, map[string]any{"name": "regional-op", "id": "1"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/regions/us-central1/operations/regional-op"):
			writeJSON(w, http.StatusOK, map[string]any{
				"name":   "regional-op",
				"status": "DONE",
				"id":     "1",
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/zones/us-central1-b/instances/test-vm"):
			writeJSON(w, http.StatusOK, map[string]any{"name": "test-vm"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/instances/test-vm"):
			http.NotFound(w, r)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/"):
			atomic.AddInt32(&insertCalls, 1)
			http.Error(w, "unexpected zonal insert", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()
	p.bulkInsertReconcileTimeout = 25 * time.Millisecond
	p.bulkInsertReconcilePollInterval = 5 * time.Millisecond

	_, failedCandidate, err := insertWithBulkFallbackForTest(
		p, newTestInstance(), twoZoneCandidates(), true, false,
	)
	if err == nil {
		t.Fatal("expected ambiguous metadata error")
	}
	if failedCandidate.zone != "us-central1-b" {
		t.Fatalf("failed candidate zone=%q, want reconciled us-central1-b", failedCandidate.zone)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 0 {
		t.Fatalf("zonal insert calls=%d, want 0", got)
	}
}

func TestInsertWithBulkFallbackResubmitsAmbiguousRequestWithSameRequestID(t *testing.T) {
	var bulkCalls, insertCalls int32
	var requestIDs []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/regions/us-central1/instances/bulkInsert"):
			call := atomic.AddInt32(&bulkCalls, 1)
			requestIDs = append(requestIDs, r.URL.Query().Get("requestId"))
			if call == 1 {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"error": map[string]any{
						"code":    http.StatusServiceUnavailable,
						"message": "backend unavailable after request submission",
						"errors":  []map[string]any{{"reason": "backendError"}},
					},
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"name": "regional-op", "id": "1"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/regions/us-central1/operations/regional-op"):
			writeJSON(w, http.StatusOK, map[string]any{
				"name":   "regional-op",
				"status": "DONE",
				"id":     "1",
				"instancesBulkInsertOperationMetadata": map[string]any{
					"perLocationStatus": map[string]any{
						"zones/us-central1-b": map[string]any{"createdVmCount": 1, "status": "DONE"},
					},
				},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/"):
			atomic.AddInt32(&insertCalls, 1)
			http.Error(w, "unexpected zonal insert", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()
	p.bulkInsertReconcileTimeout = 100 * time.Millisecond
	p.bulkInsertReconcilePollInterval = time.Millisecond

	operation, succeeded, err := insertWithBulkFallbackForTest(
		p, newTestInstance(), twoZoneCandidates(), true, false,
	)
	if err != nil {
		t.Fatalf("insertWithBulkFallback: %v", err)
	}
	if operation == nil || operation.Name != "regional-op" {
		t.Fatalf("operation=%v, want regional-op", operation)
	}
	if succeeded.zone != "us-central1-b" {
		t.Fatalf("succeeded zone=%q, want us-central1-b", succeeded.zone)
	}
	if got := atomic.LoadInt32(&bulkCalls); got != 2 {
		t.Fatalf("bulk calls=%d, want 2", got)
	}
	if len(requestIDs) != 2 || requestIDs[0] == "" || requestIDs[0] != requestIDs[1] {
		t.Fatalf("request IDs=%v, want the same non-empty ID", requestIDs)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 0 {
		t.Fatalf("zonal insert calls=%d, want 0", got)
	}
}

func TestInsertWithBulkFallbackDoesNotTreatPollingRejectionAsSubmissionRejection(t *testing.T) {
	var bulkCalls, pollCalls, insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/regions/us-central1/instances/bulkInsert"):
			atomic.AddInt32(&bulkCalls, 1)
			writeJSON(w, http.StatusOK, map[string]any{"name": "regional-op", "id": "1"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/regions/us-central1/operations/regional-op"):
			if atomic.AddInt32(&pollCalls, 1) == 1 {
				writeJSON(w, http.StatusForbidden, map[string]any{
					"error": map[string]any{
						"code":    http.StatusForbidden,
						"message": "temporary polling permission failure",
						"errors":  []map[string]any{{"reason": "forbidden"}},
					},
				})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"name":   "regional-op",
				"status": "DONE",
				"id":     "1",
				"instancesBulkInsertOperationMetadata": map[string]any{
					"perLocationStatus": map[string]any{
						"zones/us-central1-b": map[string]any{"createdVmCount": 1, "status": "DONE"},
					},
				},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/"):
			atomic.AddInt32(&insertCalls, 1)
			http.Error(w, "unexpected zonal insert", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()
	p.bulkInsertReconcileTimeout = 100 * time.Millisecond
	p.bulkInsertReconcilePollInterval = time.Millisecond

	operation, succeeded, err := insertWithBulkFallbackForTest(
		p, newTestInstance(), twoZoneCandidates(), true, false,
	)
	if err != nil {
		t.Fatalf("insertWithBulkFallback: %v", err)
	}
	if operation == nil || operation.Name != "regional-op" {
		t.Fatalf("operation=%v, want regional-op", operation)
	}
	if succeeded.zone != "us-central1-b" {
		t.Fatalf("succeeded zone=%q, want us-central1-b", succeeded.zone)
	}
	if got := atomic.LoadInt32(&bulkCalls); got != 2 {
		t.Fatalf("bulk calls=%d, want 2", got)
	}
	if got := atomic.LoadInt32(&pollCalls); got != 2 {
		t.Fatalf("poll calls=%d, want 2", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 0 {
		t.Fatalf("zonal insert calls=%d, want 0", got)
	}
}

func TestInsertWithBulkFallbackDoesNotTreatResolutionRejectionAsInitialRejection(t *testing.T) {
	var bulkCalls, insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/regions/us-central1/instances/bulkInsert"):
			if atomic.AddInt32(&bulkCalls, 1) == 1 {
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"error": map[string]any{
						"code":    http.StatusServiceUnavailable,
						"message": "ambiguous submission",
						"errors":  []map[string]any{{"reason": "backendError"}},
					},
				})
				return
			}
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error": map[string]any{
					"code":    http.StatusForbidden,
					"message": "resolution request rejected",
					"errors":  []map[string]any{{"reason": "forbidden"}},
				},
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/zones/us-central1-b/instances/test-vm"):
			writeJSON(w, http.StatusOK, map[string]any{"name": "test-vm"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/instances/test-vm"):
			http.NotFound(w, r)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/"):
			atomic.AddInt32(&insertCalls, 1)
			http.Error(w, "unexpected zonal insert", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()
	p.bulkInsertReconcileTimeout = 25 * time.Millisecond
	p.bulkInsertReconcilePollInterval = 5 * time.Millisecond

	_, failedCandidate, err := insertWithBulkFallbackForTest(
		p, newTestInstance(), twoZoneCandidates(), true, false,
	)
	if err == nil {
		t.Fatal("expected ambiguous bulkInsert error")
	}
	if failedCandidate.zone != "us-central1-b" {
		t.Fatalf("failed candidate zone=%q, want reconciled us-central1-b", failedCandidate.zone)
	}
	if got := atomic.LoadInt32(&bulkCalls); got < 2 {
		t.Fatalf("bulk calls=%d, want at least 2", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 0 {
		t.Fatalf("zonal insert calls=%d, want 0", got)
	}
}

func TestInsertWithBulkFallbackUsesZonalCreateAfterRegionalRollback(t *testing.T) {
	var bulkCalls, insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/regions/us-central1/instances/bulkInsert"):
			atomic.AddInt32(&bulkCalls, 1)
			writeJSON(w, http.StatusOK, map[string]any{"name": "regional-op", "id": "1"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/regions/us-central1/operations/regional-op"):
			writeJSON(w, http.StatusOK, map[string]any{
				"name": "regional-op", "status": "DONE", "id": "1",
				"instancesBulkInsertOperationMetadata": map[string]any{
					"perLocationStatus": map[string]any{
						"zones/us-central1-a": map[string]any{
							"createdVmCount": 1, "deletedVmCount": 1, "status": "DONE",
						},
					},
				},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/us-central1-a/instances"):
			atomic.AddInt32(&insertCalls, 1)
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "id": "2"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/zones/us-central1-a/operations/zonal-op"):
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "status": "DONE", "id": "2"})
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()

	_, succeeded, err := insertWithBulkFallbackForTest(
		p, newTestInstance(), twoZoneCandidates(), true, false,
	)
	if err != nil {
		t.Fatalf("insertWithBulkFallback: %v", err)
	}
	if succeeded.zone != "us-central1-a" {
		t.Fatalf("succeeded zone=%q, want us-central1-a", succeeded.zone)
	}
	if got := atomic.LoadInt32(&bulkCalls); got != 1 {
		t.Fatalf("bulk calls=%d, want 1", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 1 {
		t.Fatalf("zonal insert calls=%d, want 1", got)
	}
}

func TestInsertWithBulkFallbackBypassesBulkForReservation(t *testing.T) {
	var bulkCalls, insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/bulkInsert"):
			atomic.AddInt32(&bulkCalls, 1)
			http.Error(w, "unexpected bulk insert", http.StatusInternalServerError)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/us-central1-a/instances"):
			atomic.AddInt32(&insertCalls, 1)
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "id": "2"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/zones/us-central1-a/operations/zonal-op"):
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "status": "DONE", "id": "2"})
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()

	_, _, err := insertWithBulkFallbackForTest(
		p, newTestInstance(), twoZoneCandidates(), false, true,
	)
	if err != nil {
		t.Fatalf("insertWithBulkFallback: %v", err)
	}
	if got := atomic.LoadInt32(&bulkCalls); got != 0 {
		t.Fatalf("bulk calls=%d, want 0", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 1 {
		t.Fatalf("zonal insert calls=%d, want 1", got)
	}
}

func TestInsertWithBulkFallbackUsesZonalCreateWithoutConfiguredFallbacks(t *testing.T) {
	var bulkCalls, insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/bulkInsert"):
			atomic.AddInt32(&bulkCalls, 1)
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": map[string]any{"code": http.StatusBadRequest, "message": "unexpected bulkInsert"},
			})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/us-central1-a/instances"):
			atomic.AddInt32(&insertCalls, 1)
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "id": "2"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/zones/us-central1-a/operations/zonal-op"):
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "status": "DONE", "id": "2"})
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()
	p.machineTypeFallbacks = nil

	_, _, err := insertWithBulkFallbackForTest(
		p, newTestInstance(), twoZoneCandidates(), true, false,
	)
	if err != nil {
		t.Fatalf("insertWithBulkFallback: %v", err)
	}
	if got := atomic.LoadInt32(&bulkCalls); got != 0 {
		t.Fatalf("bulk calls=%d, want 0", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 1 {
		t.Fatalf("zonal insert calls=%d, want 1", got)
	}
}

func TestInsertWithBulkFallbackBypassesBulkForHotPool(t *testing.T) {
	var bulkCalls, insertCalls int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/bulkInsert"):
			atomic.AddInt32(&bulkCalls, 1)
			http.Error(w, "unexpected bulk insert", http.StatusInternalServerError)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/zones/us-central1-a/instances"):
			atomic.AddInt32(&insertCalls, 1)
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "id": "2"})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/zones/us-central1-a/operations/zonal-op"):
			writeJSON(w, http.StatusOK, map[string]any{"name": "zonal-op", "status": "DONE", "id": "2"})
		default:
			http.NotFound(w, r)
		}
	})
	p, cleanup := newBulkTestConfig(t, handler)
	defer cleanup()

	_, _, err := p.insertWithBulkFallback(
		context.Background(), newTestInstance(), twoZoneCandidates(),
		&types.InstanceCreateOpts{DisableMachineTypeFallbacks: true},
		"c4d-standard-8", "hyperdisk-balanced", true, false, logger.Discard(),
	)
	if err != nil {
		t.Fatalf("insertWithBulkFallback: %v", err)
	}
	if got := atomic.LoadInt32(&bulkCalls); got != 0 {
		t.Fatalf("bulk calls=%d, want 0", got)
	}
	if got := atomic.LoadInt32(&insertCalls); got != 1 {
		t.Fatalf("zonal insert calls=%d, want 1", got)
	}
}

func TestWaitRegionOperationCancelsDuringPollDelay(t *testing.T) {
	p, cleanup := newBulkTestConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"name": "regional-op", "status": "PENDING"})
	}))
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	start := time.Now()
	_, err := waitRegionOperation(ctx, p.service, "proj", "us-central1", "regional-op")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("cancellation took %s, want under 250ms", elapsed)
	}
}

func TestWaitRegionOperationRecordsRetryMetric(t *testing.T) {
	var calls int32
	p, cleanup := newBulkTestConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": map[string]any{
					"code":    http.StatusServiceUnavailable,
					"message": "backend hiccup",
					"errors":  []map[string]any{{"reason": "backendError"}},
				},
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": "regional-op", "status": "DONE"})
	}))
	defer cleanup()
	p.metrics = newTestMetrics()

	_, err := waitRegionOperationWithMetrics(
		context.Background(), p.service, p.metrics, "proj", "us-central1", "regional-op",
	)
	if err != nil {
		t.Fatalf("waitRegionOperationWithMetrics: %v", err)
	}
	retries := testutil.ToFloat64(p.metrics.GCPOperationRetriesCount.WithLabelValues(
		metric.GCPResourceRegion,
		metric.GCPOperationGet,
		metric.GCPReasonBackendError,
		"us-central1",
	))
	if retries != 1 {
		t.Fatalf("retry metric=%v, want 1", retries)
	}
}

func TestIsDefinitiveBulkInsertRejection(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{status: http.StatusBadRequest, want: true},
		{status: http.StatusForbidden, want: true},
		{status: http.StatusConflict, want: false},
		{status: http.StatusTooManyRequests, want: false},
		{status: 499, want: false},
		{status: http.StatusServiceUnavailable, want: false},
	}

	for _, test := range tests {
		err := &bulkInsertSubmissionError{err: &googleapi.Error{Code: test.status}}
		if got := isDefinitiveBulkInsertRejection(err); got != test.want {
			t.Errorf("status %d: got %v, want %v", test.status, got, test.want)
		}
	}

	if isDefinitiveBulkInsertRejection(&googleapi.Error{Code: http.StatusForbidden}) {
		t.Fatal("polling error must not be classified as a submission rejection")
	}
}

func TestGetCreatedInstanceUsesBoundedContextAfterCallerCancellation(t *testing.T) {
	var calls int32
	p, cleanup := newBulkTestConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/instances/test-vm") {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&calls, 1)
		writeJSON(w, http.StatusOK, map[string]any{"name": "test-vm"})
	}))
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	vm, err := p.getCreatedInstance(ctx, "proj", "us-central1-a", "test-vm")
	if err != nil {
		t.Fatalf("getCreatedInstance: %v", err)
	}
	if vm.Name != "test-vm" {
		t.Fatalf("name=%q, want test-vm", vm.Name)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("get calls=%d, want 1 recovery request", got)
	}
}

func insertWithBulkFallbackForTest(
	p *config,
	in *compute.Instance,
	candidates []createCandidate,
	stockoutRetryEnabled, usesReservation bool,
) (*compute.Operation, createCandidate, error) {
	return p.insertWithBulkFallback(
		context.Background(),
		in,
		candidates,
		&types.InstanceCreateOpts{},
		"c4d-standard-8",
		"hyperdisk-balanced",
		stockoutRetryEnabled,
		usesReservation,
		logger.Discard(),
	)
}

func newBulkTestConfig(t *testing.T, handler http.Handler) (*config, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	service, err := compute.NewService(context.Background(), option.WithHTTPClient(server.Client()))
	if err != nil {
		server.Close()
		t.Fatalf("compute service: %v", err)
	}
	service.BasePath = server.URL + "/"
	return &config{
		projectID:   "proj",
		service:     service,
		userDataKey: "user-data",
		machineTypeFallbacks: []types.MachineTypeFallback{
			{MachineType: "c4d-standard-8-lssd", DiskType: "hyperdisk-balanced"},
		},
	}, server.Close
}
