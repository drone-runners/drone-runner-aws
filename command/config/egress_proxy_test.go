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
	})

	t.Run("overrides from env", func(t *testing.T) {
		t.Setenv("DRONE_EGRESS_PROXY_URL", "http://proxy.example.com:8080")
		t.Setenv("DRONE_EGRESS_NO_PROXY", "localhost,foo.local")
		t.Setenv("DRONE_EGRESS_PROXY_CA_CERT", "MY-CA")

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
