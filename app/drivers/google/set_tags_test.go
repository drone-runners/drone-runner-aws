package google

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/drone-runners/drone-runner-aws/types"
)

// fakeMetadataServer serves instances.get for a VM with existingItems and records the
// metadata body sent to instances.setMetadata.
type fakeMetadataServer struct {
	mu            sync.Mutex
	existingItems []*compute.MetadataItems
	sent          *compute.Metadata
}

func (f *fakeMetadataServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/instances/test-vm"):
		writeJSON(w, http.StatusOK, map[string]any{
			"name":     "test-vm",
			"metadata": map[string]any{"fingerprint": "fp-1", "items": f.existingItems},
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/instances/test-vm/setMetadata"):
		var md compute.Metadata
		_ = json.NewDecoder(r.Body).Decode(&md)
		f.sent = &md
		writeJSON(w, http.StatusOK, map[string]any{"name": "op-setmetadata"})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func metadataValues(items []*compute.MetadataItems) map[string]string {
	out := make(map[string]string, len(items))
	for _, it := range items {
		if it.Value != nil {
			out[it.Key] = *it.Value
		}
	}
	return out
}

func TestSetTags_WritesCaseSensitiveMetadataKey(t *testing.T) {
	fake := &fakeMetadataServer{existingItems: []*compute.MetadataItems{
		{Key: "harness-stage-execution-id", Value: strPtr("stage-1")},
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	svc, err := compute.NewService(context.Background(), option.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("compute.NewService: %v", err)
	}
	svc.BasePath = srv.URL + "/"
	p := &config{projectID: "proj", service: svc}
	inst := &types.Instance{ID: "test-vm", Zone: "us-central1-a"}

	if err := p.SetTags(context.Background(), inst, map[string]string{"harnessAccountId": "Acc-AbC123"}); err != nil {
		t.Fatalf("SetTags: %v", err)
	}

	if fake.sent == nil {
		t.Fatal("setMetadata was not called")
	}
	got := metadataValues(fake.sent.Items)
	if got["harnessAccountId"] != "Acc-AbC123" {
		t.Errorf("harnessAccountId: got %q, want %q (key and value must keep their case); items=%v", got["harnessAccountId"], "Acc-AbC123", got)
	}
	if _, ok := got["harnessaccountid"]; ok {
		t.Error("key was lowercased; GCE metadata keys are case sensitive and must be sent as-is")
	}
	if got["harness-stage-execution-id"] != "stage-1" {
		t.Error("existing metadata items must be preserved")
	}
	if fake.sent.Fingerprint != "fp-1" {
		t.Errorf("fingerprint: got %q, want %q", fake.sent.Fingerprint, "fp-1")
	}
}

func strPtr(s string) *string { return &s }
