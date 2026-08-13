package config

import (
	"strings"
	"testing"
)

// TestEgressProxyConfig verifies the EgressProxy envconfig defaults and that the
// DRONE_EGRESS_* environment variables override them. Enablement comes from pool
// egress_control, not an Enabled flag on this struct.
func TestEgressProxyConfig(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, err := FromEnviron()
		if err != nil {
			t.Fatalf("FromEnviron: %v", err)
		}
		if !strings.Contains(cfg.Egress.Proxy.NoProxy, "169.254.169.254") {
			t.Errorf("NoProxy default missing metadata endpoint: %q", cfg.Egress.Proxy.NoProxy)
		}
		wantCAEnvVars := []string{
			"SSL_CERT_FILE",
			"NODE_EXTRA_CA_CERTS",
			"REQUESTS_CA_BUNDLE",
			"CURL_CA_BUNDLE",
			"GIT_SSL_CAINFO",
		}
		got := cfg.Egress.Proxy.CAEnvVars
		if len(got) != len(wantCAEnvVars) {
			t.Fatalf("CAEnvVars default = %v, want %v", got, wantCAEnvVars)
		}
		for i, v := range wantCAEnvVars {
			if got[i] != v {
				t.Errorf("CAEnvVars[%d] = %q, want %q", i, got[i], v)
			}
		}
	})

	t.Run("overrides from env", func(t *testing.T) {
		t.Setenv("DRONE_EGRESS_NO_PROXY", "localhost,foo.local")
		t.Setenv("DRONE_EGRESS_PROXY_CA_CERT", "MY-CA")
		t.Setenv("DRONE_EGRESS_CA_ENV_VARS", "SSL_CERT_FILE,MY_CUSTOM_CA_VAR")

		cfg, err := FromEnviron()
		if err != nil {
			t.Fatalf("FromEnviron: %v", err)
		}
		if cfg.Egress.Proxy.NoProxy != "localhost,foo.local" {
			t.Errorf("NoProxy = %q", cfg.Egress.Proxy.NoProxy)
		}
		if cfg.Egress.Proxy.CACert != "MY-CA" {
			t.Errorf("CACert = %q", cfg.Egress.Proxy.CACert)
		}
		got := cfg.Egress.Proxy.CAEnvVars
		if len(got) != 2 || got[0] != "SSL_CERT_FILE" || got[1] != "MY_CUSTOM_CA_VAR" {
			t.Errorf("CAEnvVars = %v, want [SSL_CERT_FILE MY_CUSTOM_CA_VAR]", got)
		}
	})
}

func TestGoogleNetworkProxyURL(t *testing.T) {
	n := GoogleNetwork{
		Network:    "vpc-west",
		Subnetwork: "subnet-west",
		ProxyURL:   "http://10.0.1.10:3128",
	}
	if n.ProxyURL != "http://10.0.1.10:3128" {
		t.Errorf("ProxyURL = %q", n.ProxyURL)
	}
}
