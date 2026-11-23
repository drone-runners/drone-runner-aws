package harness

import (
	"testing"

	leapi "github.com/harness/lite-engine/api"
)

// The sidecar flag is the stage's choice, so the runner must carry it from the
// request through the merge. Dropping it would silently turn the redirect off.
func TestMergeEgressPolicyKeepsTheSidecarFlag(t *testing.T) {
	tests := []struct {
		name     string
		existing *leapi.EgressPolicy
		want     bool
	}{
		{
			name:     "flag on in the request",
			existing: &leapi.EgressPolicy{Username: "acct", Password: "token", SidecarEnabled: true},
			want:     true,
		},
		{
			name:     "flag off in the request",
			existing: &leapi.EgressPolicy{Username: "acct", Password: "token"},
			want:     false,
		},
		{
			name:     "no request policy",
			existing: nil,
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeEgressPolicy(tt.existing, "http://172.16.0.11:3128", "localhost")
			if got == nil {
				t.Fatal("merge returned nil for a stage with a proxy URL")
			}
			if got.SidecarEnabled != tt.want {
				t.Fatalf("SidecarEnabled = %v, want %v", got.SidecarEnabled, tt.want)
			}
			if got.ProxyURL != "http://172.16.0.11:3128" {
				t.Fatalf("ProxyURL = %q, want the runner-known proxy", got.ProxyURL)
			}
		})
	}
}
