package lehelper

import (
	"encoding/base64"
	"strings"
	"testing"

	"gopkg.in/yaml.v2"

	"github.com/drone-runners/drone-runner-aws/types"
)

func ephemeralOpts() *types.InstanceCreateOpts {
	return &types.InstanceCreateOpts{
		EphemeralDelegate:       true,
		Platform:                types.Platform{OS: "linux", Arch: "amd64"},
		AccountID:               "acct1",
		DelegatePath:            "https://dl.example.com/delegates/3.9.0",
		DelegateToken:           "tok",
		DelegateManagerEndpoint: "https://app.harness.io",
		DelegateName:            "vm-delegate",
		DelegateTransactionID:   "txn1",
		DelegateTags:            "linux-amd64,ephemeral-try",
	}
}

func egressOpts() *types.InstanceCreateOpts {
	opts := ephemeralOpts()
	opts.EgressControl = true
	opts.EgressProxyURL = "http://proxy.example.com:3128"
	opts.EgressProxyUser = "user"
	opts.EgressProxyPassword = "pass"
	opts.EgressCACert = "CA-PEM"
	opts.EgressNoProxy = "metadata.google.internal"
	return opts
}

func TestRenderDelegateConfigEnv(t *testing.T) {
	t.Run("no egress renders sorted delegate keys and skips empty values", func(t *testing.T) {
		got := renderDelegateConfigEnv(ephemeralOpts(), "", "", "")
		want := "HARNESS_ACCOUNT_ID=acct1\n" +
			"HARNESS_DELEGATE_NAME=vm-delegate\n" +
			"HARNESS_DELEGATE_TOKEN=tok\n" +
			"HARNESS_MANAGER_HOST_AND_PORT=https://app.harness.io\n" +
			"HARNESS_PARALLEL_WORKERS=2\n" +
			"HARNESS_TAGS=linux-amd64,ephemeral-try\n" +
			"HARNESS_TRANSACTION_ID=txn1\n"
		if got != want {
			t.Errorf("config.env mismatch\n got: %q\nwant: %q", got, want)
		}
		if strings.Contains(got, "PROXY") {
			t.Errorf("proxy keys must not be rendered without egress: %q", got)
		}
	})

	t.Run("explicit parallel workers wins over the default", func(t *testing.T) {
		opts := ephemeralOpts()
		opts.DelegateParallelWorkers = "7"
		got := renderDelegateConfigEnv(opts, "", "", "")
		if !strings.Contains(got, "HARNESS_PARALLEL_WORKERS=7\n") {
			t.Errorf("want HARNESS_PARALLEL_WORKERS=7 in %q", got)
		}
	})

	t.Run("empty tags are omitted", func(t *testing.T) {
		opts := ephemeralOpts()
		opts.DelegateTags = ""
		got := renderDelegateConfigEnv(opts, "", "", "")
		if strings.Contains(got, "HARNESS_TAGS") {
			t.Errorf("empty tags must be omitted: %q", got)
		}
	})

	t.Run("egress adds proxy keys", func(t *testing.T) {
		got := renderDelegateConfigEnv(egressOpts(), "http", "proxy.example.com", "3128")
		for _, line := range []string{
			"PROXY_SCHEME=http\n",
			"PROXY_HOST=proxy.example.com\n",
			"PROXY_PORT=3128\n",
			"PROXY_USER=user\n",
			"PROXY_PASSWORD=pass\n",
			"NO_PROXY=metadata.google.internal\n",
		} {
			if !strings.Contains(got, line) {
				t.Errorf("missing %q in %q", line, got)
			}
		}
	})
}

func TestApplyEphemeralUserdataErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(o *types.InstanceCreateOpts)
		base    func() *types.InstanceCreateOpts
		wantErr string
	}{
		{
			name:    "missing delegate path",
			base:    ephemeralOpts,
			mutate:  func(o *types.InstanceCreateOpts) { o.DelegatePath = "" },
			wantErr: "delegate download path is required",
		},
		{
			name:    "egress without proxy url",
			base:    egressOpts,
			mutate:  func(o *types.InstanceCreateOpts) { o.EgressProxyURL = "" },
			wantErr: "egress proxy URL is required",
		},
		{
			name:    "egress without ca certificate",
			base:    egressOpts,
			mutate:  func(o *types.InstanceCreateOpts) { o.EgressCACert = "" },
			wantErr: "egress CA certificate is required",
		},
		{
			name:    "egress proxy url without port",
			base:    egressOpts,
			mutate:  func(o *types.InstanceCreateOpts) { o.EgressProxyURL = "http://proxy.example.com" },
			wantErr: "must include a port",
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.base()
			tc.mutate(opts)
			_, err := GenerateUserdata("", opts)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("GenerateUserdata error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestGenerateUserdataEphemeral(t *testing.T) {
	t.Run("installs delegate and none of the lite-engine setup", func(t *testing.T) {
		opts := ephemeralOpts()
		out, err := GenerateUserdata("", opts)
		if err != nil {
			t.Fatalf("GenerateUserdata: %v", err)
		}
		mustContain(t, out,
			"https://dl.example.com/delegates/3.9.0/delegate-linux-amd64",
			"systemctl enable --now harness-delegate",
			"ExecStart=/usr/bin/delegate server",
			"/opt/harness-delegate/config.env",
			base64.StdEncoding.EncodeToString([]byte(renderDelegateConfigEnv(opts, "", "", ""))),
			"docker-ce",
		)
		mustNotContain(t, out, "lite-engine", "9079", "envoy-proxy", "healthz", "update-ca-certificates")
		assertValidYAML(t, out)
	})

	t.Run("ignores custom pool userdata", func(t *testing.T) {
		out, err := GenerateUserdata("#cloud-config\nruncmd:\n  - echo custom\n", ephemeralOpts())
		if err != nil {
			t.Fatalf("GenerateUserdata: %v", err)
		}
		mustContain(t, out, "harness-delegate")
		mustNotContain(t, out, "echo custom")
	})

	t.Run("no fallback path exits when the primary download fails", func(t *testing.T) {
		out, err := GenerateUserdata("", ephemeralOpts())
		if err != nil {
			t.Fatalf("GenerateUserdata: %v", err)
		}
		mustContain(t, out, "exit 1")
	})

	t.Run("fallback path is used when set", func(t *testing.T) {
		opts := ephemeralOpts()
		opts.DelegateFallbackPath = "https://fallback.example.com/delegates/3.9.0"
		out, err := GenerateUserdata("", opts)
		if err != nil {
			t.Fatalf("GenerateUserdata: %v", err)
		}
		mustContain(t, out, "https://fallback.example.com/delegates/3.9.0/delegate-linux-amd64")
		mustNotContain(t, out, "exit 1")
	})

	t.Run("egress uses the fleet proxy template", func(t *testing.T) {
		opts := egressOpts()
		out, err := GenerateUserdata("", opts)
		if err != nil {
			t.Fatalf("GenerateUserdata: %v", err)
		}
		mustContain(t, out,
			"http://proxy.example.com:3128/healthz",
			"http://user:pass@proxy.example.com:3128",
			"update-ca-certificates",
			"/etc/harness-certs/ca-bundle.crt",
			"http-proxy.conf",
			base64.StdEncoding.EncodeToString([]byte("CA-PEM")),
			base64.StdEncoding.EncodeToString([]byte(renderDelegateConfigEnv(opts, "http", "proxy.example.com", "3128"))),
		)
		mustNotContain(t, out, "lite-engine", "9079", "envoy-proxy")
		assertValidYAML(t, out)
	})
}

func TestProxyURLHelpers(t *testing.T) {
	t.Run("splitProxyURL defaults a missing scheme to http", func(t *testing.T) {
		scheme, host, port, err := splitProxyURL("proxy.example.com:3128")
		if err != nil {
			t.Fatalf("splitProxyURL: %v", err)
		}
		if scheme != "http" || host != "proxy.example.com" || port != "3128" {
			t.Errorf("got %q %q %q", scheme, host, port)
		}
	})

	t.Run("credentialedProxyURL escapes special characters", func(t *testing.T) {
		got, err := credentialedProxyURL("http://proxy.example.com:3128", "user", "p@ss")
		if err != nil {
			t.Fatalf("credentialedProxyURL: %v", err)
		}
		if want := "http://user:p%40ss@proxy.example.com:3128"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("credentialedProxyURL leaves url alone without credentials", func(t *testing.T) {
		got, err := credentialedProxyURL("http://proxy.example.com:3128", "", "")
		if err != nil {
			t.Fatalf("credentialedProxyURL: %v", err)
		}
		if want := "http://proxy.example.com:3128"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("empty url is rejected", func(t *testing.T) {
		if _, err := credentialedProxyURL("", "u", "p"); err == nil {
			t.Fatal("want error for empty proxy URL")
		}
	})
}

func mustContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("userdata missing %q", sub)
		}
	}
}

func mustNotContain(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("userdata must not contain %q", sub)
		}
	}
}

func assertValidYAML(t *testing.T, s string) {
	t.Helper()
	var out map[string]interface{}
	if err := yaml.Unmarshal([]byte(s), &out); err != nil {
		t.Errorf("userdata is not valid YAML: %v", err)
	}
}
