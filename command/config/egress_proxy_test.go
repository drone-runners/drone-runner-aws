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
		if cfg.Egress.Proxy.URL != "http://127.0.0.1:3128" {
			t.Errorf("URL default = %q, want http://127.0.0.1:3128", cfg.Egress.Proxy.URL)
		}
		if !strings.Contains(cfg.Egress.Proxy.NoProxy, "169.254.169.254") {
			t.Errorf("NoProxy default missing metadata endpoint: %q", cfg.Egress.Proxy.NoProxy)
		}
		wantBundleEnvVars := []string{
			"SSL_CERT_FILE",
			"REQUESTS_CA_BUNDLE",
			"CURL_CA_BUNDLE",
			"GIT_SSL_CAINFO",
		}
		got := cfg.Egress.Proxy.CABundleEnvVars
		if len(got) != len(wantBundleEnvVars) {
			t.Fatalf("CABundleEnvVars default = %v, want %v", got, wantBundleEnvVars)
		}
		for i, v := range wantBundleEnvVars {
			if got[i] != v {
				t.Errorf("CABundleEnvVars[%d] = %q, want %q", i, got[i], v)
			}
		}
		gotExtra := cfg.Egress.Proxy.CAExtraEnvVars
		if len(gotExtra) != 1 || gotExtra[0] != "NODE_EXTRA_CA_CERTS" {
			t.Errorf("CAExtraEnvVars default = %v, want [NODE_EXTRA_CA_CERTS]", gotExtra)
		}
	})

	t.Run("overrides from env", func(t *testing.T) {
		t.Setenv("DRONE_EGRESS_PROXY_URL", "http://proxy.example.com:8080")
		t.Setenv("DRONE_EGRESS_NO_PROXY", "localhost,foo.local")
		t.Setenv("DRONE_EGRESS_PROXY_CA_CERT", "MY-CA")
		t.Setenv("DRONE_EGRESS_CA_BUNDLE_ENV_VARS", "SSL_CERT_FILE,MY_CUSTOM_CA_VAR")
		t.Setenv("DRONE_EGRESS_CA_EXTRA_ENV_VARS", "NODE_EXTRA_CA_CERTS,MY_EXTRA_VAR")

		cfg, err := FromEnviron()
		if err != nil {
			t.Fatalf("FromEnviron: %v", err)
		}
		if cfg.Egress.Proxy.URL != "http://proxy.example.com:8080" {
			t.Errorf("URL = %q", cfg.Egress.Proxy.URL)
		}
		if cfg.Egress.Proxy.NoProxy != "localhost,foo.local" {
			t.Errorf("NoProxy = %q", cfg.Egress.Proxy.NoProxy)
		}
		if cfg.Egress.Proxy.CACert != "MY-CA" {
			t.Errorf("CACert = %q", cfg.Egress.Proxy.CACert)
		}
		got := cfg.Egress.Proxy.CABundleEnvVars
		if len(got) != 2 || got[0] != "SSL_CERT_FILE" || got[1] != "MY_CUSTOM_CA_VAR" {
			t.Errorf("CABundleEnvVars = %v, want [SSL_CERT_FILE MY_CUSTOM_CA_VAR]", got)
		}
		gotExtra := cfg.Egress.Proxy.CAExtraEnvVars
		if len(gotExtra) != 2 || gotExtra[0] != "NODE_EXTRA_CA_CERTS" || gotExtra[1] != "MY_EXTRA_VAR" {
			t.Errorf("CAExtraEnvVars = %v, want [NODE_EXTRA_CA_CERTS MY_EXTRA_VAR]", gotExtra)
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
