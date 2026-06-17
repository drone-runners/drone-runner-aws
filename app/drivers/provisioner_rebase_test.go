package drivers

import "testing"

func TestRebaseToHarnessDownload(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
		{
			name: "already canonical is unchanged",
			in:   "https://app.harness.io/storage/harness-download/harness-ti/harness-lite-engine/v0.5.179/",
			want: "https://app.harness.io/storage/harness-download/harness-ti/harness-lite-engine/v0.5.179/",
		},
		{
			name: "different harness domain is rebased preserving suffix",
			in:   "https://qa.harness.io/storage/harness-download/harness-ti/hcli/v0.15/",
			want: "https://app.harness.io/storage/harness-download/harness-ti/hcli/v0.15/",
		},
		{
			name: "custom cdn with marker is rebased preserving suffix",
			in:   "https://cdn.example.com/mirror/storage/harness-download/harness-ti/harness-plugin/v3.9.7",
			want: "https://app.harness.io/storage/harness-download/harness-ti/harness-plugin/v3.9.7",
		},
		{
			name: "http scheme is normalized to https",
			in:   "http://qa.harness.io/storage/harness-download/harness-ti/harness-envman/v2.5.6/",
			want: "https://app.harness.io/storage/harness-download/harness-ti/harness-envman/v2.5.6/",
		},
		{
			name: "gcs bucket host is re-rooted under the download base",
			in:   "https://storage.googleapis.com/harness-ti/harness-lite-engine/v0.5.179",
			want: "https://app.harness.io/storage/harness-download/harness-ti/harness-lite-engine/v0.5.179",
		},
		{
			name: "non-canonical host path is re-rooted under the download base",
			in:   "https://mirror.example.com/harness-ti/auto-injection/1.0.19",
			want: "https://app.harness.io/storage/harness-download/harness-ti/auto-injection/1.0.19",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := rebaseToHarnessDownload(tc.in)
			if got != tc.want {
				t.Errorf("rebaseToHarnessDownload(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
