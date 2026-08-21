package harness

import (
	"testing"

	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	"github.com/drone-runners/drone-runner-aws/command/config"
)

const (
	egressTestProxyURL = "http://proxy.internal:3128"
	egressTestNoProxy  = "localhost,10.0.0.0/8"
)

// defaultEgressCfg mirrors the config.EgressProxy defaults.
func defaultEgressCfg() *config.EgressProxy {
	return &config.EgressProxy{
		CABundleEnvVars: []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"},
		CAExtraEnvVars:  []string{"NODE_EXTRA_CA_CERTS"},
	}
}

// TestConfigureEgressStep verifies the egress-control wiring applied to a step:
// proxy env vars come from the persisted/instance proxy URL, NO_PROXY is global,
// the egress CA is always exposed via HARNESS_CA_PATH and the merged bundle via
// HARNESS_CA_BUNDLE, and the bind-mount target differs per OS (files on linux,
// the parent directory on windows).
func TestConfigureEgressStep(t *testing.T) {
	t.Run("linux with proxy URL sets proxy envs, CA path and file volume", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSLinux, egressTestProxyURL, egressTestNoProxy, &config.EgressProxy{})

		wantEnvs := map[string]string{
			"HTTPS_PROXY":       egressTestProxyURL,
			"HTTP_PROXY":        egressTestProxyURL,
			"https_proxy":       egressTestProxyURL,
			"http_proxy":        egressTestProxyURL,
			"NO_PROXY":          egressTestNoProxy,
			"no_proxy":          egressTestNoProxy,
			"HARNESS_CA_PATH":   egressCAHostPath,
			"HARNESS_CA_BUNDLE": egressCABundleHostPath,
		}
		for k, v := range wantEnvs {
			if r.Envs[k] != v {
				t.Errorf("env %q = %q, want %q", k, r.Envs[k], v)
			}
		}

		if len(r.Volumes) != 2 {
			t.Fatalf("got %d volumes, want 2", len(r.Volumes))
		}
		if r.Volumes[0].Path != egressCAHostPath {
			t.Errorf("volume path = %q, want %q", r.Volumes[0].Path, egressCAHostPath)
		}
		if r.Volumes[0].Name != fileID("ca.crt") {
			t.Errorf("volume name = %q, want %q", r.Volumes[0].Name, fileID("ca.crt"))
		}
		if r.Volumes[1].Path != egressCABundleHostPath {
			t.Errorf("bundle volume path = %q, want %q", r.Volumes[1].Path, egressCABundleHostPath)
		}
		if r.Volumes[1].Name != fileID("ca-bundle.crt") {
			t.Errorf("bundle volume name = %q, want %q", r.Volumes[1].Name, fileID("ca-bundle.crt"))
		}
	})

	t.Run("empty proxy URL omits proxy envs but still mounts CA", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSLinux, "", egressTestNoProxy, &config.EgressProxy{})

		for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "NO_PROXY", "no_proxy"} {
			if _, ok := r.Envs[k]; ok {
				t.Errorf("env %q should not be set when proxy URL empty", k)
			}
		}
		if r.Envs["HARNESS_CA_PATH"] != egressCAHostPath {
			t.Errorf("HARNESS_CA_PATH = %q, want %q", r.Envs["HARNESS_CA_PATH"], egressCAHostPath)
		}
		if r.Envs["HARNESS_CA_BUNDLE"] != egressCABundleHostPath {
			t.Errorf("HARNESS_CA_BUNDLE = %q, want %q", r.Envs["HARNESS_CA_BUNDLE"], egressCABundleHostPath)
		}
		if len(r.Volumes) != 2 {
			t.Fatalf("got %d volumes, want 2", len(r.Volumes))
		}
	})

	t.Run("windows mounts parent cert directory", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSWindows, egressTestProxyURL, egressTestNoProxy, &config.EgressProxy{})

		if r.Envs["HARNESS_CA_PATH"] != egressCAWindowsHostPath {
			t.Errorf("HARNESS_CA_PATH = %q, want %q", r.Envs["HARNESS_CA_PATH"], egressCAWindowsHostPath)
		}
		if r.Envs["HARNESS_CA_BUNDLE"] != egressCABundleWindowsHostPath {
			t.Errorf("HARNESS_CA_BUNDLE = %q, want %q", r.Envs["HARNESS_CA_BUNDLE"], egressCABundleWindowsHostPath)
		}
		if len(r.Volumes) != 1 {
			t.Fatalf("got %d volumes, want 1", len(r.Volumes))
		}
		if r.Volumes[0].Path != `C:\harness-certs` {
			t.Errorf("volume path = %q, want %q", r.Volumes[0].Path, `C:\harness-certs`)
		}
	})
}

// TestConfigureEgressStepCAEnvVars verifies bundle env vars point at the merged
// bundle and extra env vars at the Harness-only CA, without overriding values
// the step already defines.
func TestConfigureEgressStepCAEnvVars(t *testing.T) {
	t.Run("linux injects CA env vars: bundle for replacement-style, CA for additive", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSLinux, egressTestProxyURL, egressTestNoProxy, defaultEgressCfg())

		for _, k := range []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
			if r.Envs[k] != egressCABundleHostPath {
				t.Errorf("env %q = %q, want merged bundle %q", k, r.Envs[k], egressCABundleHostPath)
			}
		}
		if r.Envs["NODE_EXTRA_CA_CERTS"] != egressCAHostPath {
			t.Errorf("NODE_EXTRA_CA_CERTS = %q, want Harness-only CA %q", r.Envs["NODE_EXTRA_CA_CERTS"], egressCAHostPath)
		}
	})

	t.Run("windows CA env vars point at the windows CA paths", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSWindows, egressTestProxyURL, egressTestNoProxy, defaultEgressCfg())

		for _, k := range []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
			if r.Envs[k] != egressCABundleWindowsHostPath {
				t.Errorf("env %q = %q, want merged bundle %q", k, r.Envs[k], egressCABundleWindowsHostPath)
			}
		}
		if r.Envs["NODE_EXTRA_CA_CERTS"] != egressCAWindowsHostPath {
			t.Errorf("NODE_EXTRA_CA_CERTS = %q, want Harness-only CA %q", r.Envs["NODE_EXTRA_CA_CERTS"], egressCAWindowsHostPath)
		}
	})

	t.Run("step-provided CA env vars are not overridden", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		r.Envs = map[string]string{"SSL_CERT_FILE": "/user-provided/ca.pem"}
		configureEgressStep(r, oshelp.OSLinux, egressTestProxyURL, egressTestNoProxy, defaultEgressCfg())

		if r.Envs["SSL_CERT_FILE"] != "/user-provided/ca.pem" {
			t.Errorf("SSL_CERT_FILE = %q, want step-provided value preserved", r.Envs["SSL_CERT_FILE"])
		}
		for _, k := range []string{"REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO"} {
			if r.Envs[k] != egressCABundleHostPath {
				t.Errorf("env %q = %q, want merged bundle %q", k, r.Envs[k], egressCABundleHostPath)
			}
		}
		if r.Envs["NODE_EXTRA_CA_CERTS"] != egressCAHostPath {
			t.Errorf("NODE_EXTRA_CA_CERTS = %q, want %q", r.Envs["NODE_EXTRA_CA_CERTS"], egressCAHostPath)
		}
	})

	t.Run("custom list injects only listed vars and skips empty entries", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSLinux, "", egressTestNoProxy, &config.EgressProxy{CABundleEnvVars: []string{"REQUESTS_CA_BUNDLE", ""}})

		if r.Envs["REQUESTS_CA_BUNDLE"] != egressCABundleHostPath {
			t.Errorf("REQUESTS_CA_BUNDLE = %q, want %q", r.Envs["REQUESTS_CA_BUNDLE"], egressCABundleHostPath)
		}
		for _, k := range []string{"SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS", "CURL_CA_BUNDLE", "GIT_SSL_CAINFO", ""} {
			if _, ok := r.Envs[k]; ok {
				t.Errorf("env %q should not be set", k)
			}
		}
	})

	t.Run("nil CA env list injects no CA env vars", func(t *testing.T) {
		r := &ExecuteVMRequest{}
		configureEgressStep(r, oshelp.OSLinux, egressTestProxyURL, egressTestNoProxy, &config.EgressProxy{})

		cfg := defaultEgressCfg()
		for _, k := range append(append([]string{}, cfg.CABundleEnvVars...), cfg.CAExtraEnvVars...) {
			if _, ok := r.Envs[k]; ok {
				t.Errorf("env %q should not be set when CA env list is nil", k)
			}
		}
	})
}
