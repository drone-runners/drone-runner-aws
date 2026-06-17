package harness

import (
	"testing"

	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/command/config"
)

// TestConfigureEgressStep verifies the egress-control wiring applied to a step:
// proxy env vars are only injected when the proxy is enabled, the mitm CA is
// always exposed via HARNESS_CA_PATH, and the bind-mount target differs per OS
// (a file on linux, the parent directory on windows).
func TestConfigureEgressStep(t *testing.T) {
	proxyEnabled := config.EgressProxy{
		Enabled: true,
		URL:     "http://proxy.internal:3128",
		NoProxy: "localhost,10.0.0.0/8",
	}

	t.Run("linux with proxy enabled sets proxy envs, CA path and file volume", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSLinux, proxyEnabled)

		wantEnvs := map[string]string{
			"HTTPS_PROXY":     proxyEnabled.URL,
			"HTTP_PROXY":      proxyEnabled.URL,
			"https_proxy":     proxyEnabled.URL,
			"http_proxy":      proxyEnabled.URL,
			"NO_PROXY":        proxyEnabled.NoProxy,
			"no_proxy":        proxyEnabled.NoProxy,
			"HARNESS_CA_PATH": egressCAHostPath,
		}
		for k, v := range wantEnvs {
			if r.Envs[k] != v {
				t.Errorf("env %q = %q, want %q", k, r.Envs[k], v)
			}
		}

		if len(r.Volumes) != 1 {
			t.Fatalf("got %d volumes, want 1", len(r.Volumes))
		}
		if r.Volumes[0].Path != egressCAHostPath {
			t.Errorf("volume path = %q, want %q", r.Volumes[0].Path, egressCAHostPath)
		}
		if r.Volumes[0].Name != fileID("ca.crt") {
			t.Errorf("volume name = %q, want %q", r.Volumes[0].Name, fileID("ca.crt"))
		}
	})

	t.Run("proxy disabled omits proxy envs but still mounts CA", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSLinux, config.EgressProxy{Enabled: false, URL: "http://unused:3128"})

		for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "NO_PROXY", "no_proxy"} {
			if _, ok := r.Envs[k]; ok {
				t.Errorf("env %q should not be set when proxy disabled", k)
			}
		}
		if r.Envs["HARNESS_CA_PATH"] != egressCAHostPath {
			t.Errorf("HARNESS_CA_PATH = %q, want %q", r.Envs["HARNESS_CA_PATH"], egressCAHostPath)
		}
		if len(r.Volumes) != 1 {
			t.Fatalf("got %d volumes, want 1", len(r.Volumes))
		}
	})

	t.Run("windows mounts parent cert directory", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSWindows, proxyEnabled)

		if r.Envs["HARNESS_CA_PATH"] != egressCAWindowsHostPath {
			t.Errorf("HARNESS_CA_PATH = %q, want %q", r.Envs["HARNESS_CA_PATH"], egressCAWindowsHostPath)
		}
		if len(r.Volumes) != 1 {
			t.Fatalf("got %d volumes, want 1", len(r.Volumes))
		}
		if r.Volumes[0].Path != `C:\harness-certs` {
			t.Errorf("volume path = %q, want %q", r.Volumes[0].Path, `C:\harness-certs`)
		}
	})
}
