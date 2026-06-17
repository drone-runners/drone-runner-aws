package cloudinit_test

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/drone-runners/drone-runner-aws/app/cloudinit"
	"github.com/drone-runners/drone-runner-aws/types"

	"gopkg.in/yaml.v2"
)

const (
	liteEnginePath = "/lite/engine/goes/here"
	caCertFile     = "qwerty123"
	certFile       = "abcdef456"
	keyFile        = "xyzuvw789"
)

var (
	platform = types.Platform{
		OS:   "spectrum",
		Arch: "z80",
	}
)

func TestLinux(t *testing.T) {
	params := &cloudinit.Params{
		LiteEnginePath: liteEnginePath,
		CACert:         caCertFile + "\n",
		TLSCert:        certFile + "\n",
		TLSKey:         keyFile + "\n",
		Platform:       platform,
	}

	s, _ := cloudinit.Linux(params)
	lePath := fmt.Sprintf(`"%s/lite-engine-%s-%s"`, params.LiteEnginePath, params.Platform.OS, params.Platform.Arch)
	if !strings.Contains(s, lePath) {
		t.Error("linux init script does not contain LE path")
	}
}

// TestLinuxRendersValidYAML guards against malformed cloud-config YAML in the
// Linux userdata template. cloud-init rejects the entire document if any part
// is invalid, which silently prevents lite-engine from starting and makes the
// VM appear to "fail to start". Render every combination of the conditional
// flags that gate template blocks and assert the output parses as YAML. It also
// verifies that the lite-engine diagnostics (tcpdump) block only renders when
// both IsHosted and EnableLEDiagnostics are set.
func TestLinuxRendersValidYAML(t *testing.T) {
	for _, isHosted := range []bool{false, true} {
		for _, enableLEDiagnostics := range []bool{false, true} {
			isHosted := isHosted
			enableLEDiagnostics := enableLEDiagnostics
			name := fmt.Sprintf("IsHosted=%t/EnableLEDiagnostics=%t", isHosted, enableLEDiagnostics)
			t.Run(name, func(t *testing.T) {
				params := &cloudinit.Params{
					LiteEnginePath:      liteEnginePath,
					CACert:              caCertFile + "\n",
					TLSCert:             certFile + "\n",
					TLSKey:              keyFile + "\n",
					Platform:            types.Platform{OS: "linux", Arch: "amd64"},
					IsHosted:            isHosted,
					EnableLEDiagnostics: enableLEDiagnostics,
				}

				s, err := cloudinit.Linux(params)
				if err != nil {
					t.Fatalf("failed to render linux userdata: %v", err)
				}

				var out map[string]interface{}
				if err := yaml.Unmarshal([]byte(s), &out); err != nil {
					t.Fatalf("rendered cloud-config is not valid YAML: %v\n---\n%s", err, s)
				}

				wantPcap := isHosted && enableLEDiagnostics
				gotPcap := strings.Contains(s, "le-pcap.service")
				if gotPcap != wantPcap {
					t.Errorf("le-pcap diagnostics block presence = %t, want %t", gotPcap, wantPcap)
				}
			})
		}
	}
}

// TestLinuxEgressDispatch verifies EgressControl picks the egress template and substitutes the TPA endpoint.
func TestLinuxEgressDispatch(t *testing.T) {
	const (
		egressMarker = "systemctl enable --now envoy-proxy"
		tpaAddr      = "10.20.30.40"
		tpaPort      = "5442"
	)

	t.Run("EgressControl=false uses default ubuntu_linux template", func(t *testing.T) {
		params := &cloudinit.Params{
			LiteEnginePath: liteEnginePath,
			CACert:         caCertFile + "\n",
			TLSCert:        certFile + "\n",
			TLSKey:         keyFile + "\n",
			Platform:       types.Platform{OS: "linux", Arch: "amd64"},
		}
		s, err := cloudinit.Linux(params)
		if err != nil {
			t.Fatalf("render failed: %v", err)
		}
		if strings.Contains(s, egressMarker) {
			t.Errorf("default template unexpectedly contains egress marker %q", egressMarker)
		}
	})

	t.Run("EgressControl=true uses hosted_ubuntu_linux_egress and substitutes TPA endpoint", func(t *testing.T) {
		for _, arch := range []string{"amd64", "arm64"} {
			arch := arch
			t.Run("arch="+arch, func(t *testing.T) {
				params := &cloudinit.Params{
					LiteEnginePath: liteEnginePath,
					CACert:         caCertFile + "\n",
					TLSCert:        certFile + "\n",
					TLSKey:         keyFile + "\n",
					Platform:       types.Platform{OS: "linux", Arch: arch},
					EgressControl:  true,
					TPAAddress:     tpaAddr,
					TPAPort:        tpaPort,
				}
				s, err := cloudinit.Linux(params)
				if err != nil {
					t.Fatalf("render failed: %v", err)
				}
				if !strings.Contains(s, egressMarker) {
					t.Errorf("egress template missing marker %q", egressMarker)
				}
				if !strings.Contains(s, "TPA_ADDRESS="+tpaAddr) {
					t.Errorf("rendered output missing TPA_ADDRESS=%s", tpaAddr)
				}
				if !strings.Contains(s, "TPA_PORT="+tpaPort) {
					t.Errorf("rendered output missing TPA_PORT=%s", tpaPort)
				}
				archURL := fmt.Sprintf("lite-engine-linux-%s", arch)
				if !strings.Contains(s, archURL) {
					t.Errorf("rendered output missing arch-specific lite-engine URL fragment %q", archURL)
				}
			})
		}
	})
}

// TestUbuntuBinariesPartialShared verifies the shared "ubuntu_binaries" partial
// renders correctly in all three ubuntu templates (default, egress, egress+proxy):
// the output must contain every binary-download command and parse as valid YAML.
func TestUbuntuBinariesPartialShared(t *testing.T) {
	base := func() *cloudinit.Params {
		return &cloudinit.Params{
			LiteEnginePath:               liteEnginePath,
			LiteEngineFallbackPath:       liteEnginePath,
			CACert:                       caCertFile + "\n",
			TLSCert:                      certFile + "\n",
			TLSKey:                       keyFile + "\n",
			Platform:                     types.Platform{OS: "linux", Arch: "amd64"},
			HarnessTestBinaryURI:         "https://example.com/ti",
			PluginBinaryURI:              "https://example.com/plugin",
			PluginBinaryFallbackURI:      "https://example.com/plugin-fb",
			AutoInjectionBinaryURI:       "https://example.com/ai",
			AnnotationsBinaryURI:         "https://example.com/hcli/",
			AnnotationsBinaryFallbackURI: "https://example.com/hcli-fb/",
			EnvmanBinaryURI:              "https://example.com/envman/",
			EnvmanBinaryFallbackURI:      "https://example.com/envman-fb/",
			TmateBinaryURI:               "https://example.com/tmate/",
			TmateBinaryFallbackURI:       "https://example.com/tmate-fb/",
			Tmate:                        types.Tmate{Enabled: true},
			EgressCACert:                 caCertFile + "\n",
		}
	}

	markers := []string{
		"/usr/bin/lite-engine",
		"/usr/bin/split_tests",
		"/usr/bin/plugin",
		"/usr/bin/auto-injection",
		"/usr/bin/envman",
		"/usr/bin/hcli",
		"/addon/tmate.xz",
	}

	cases := []struct {
		name     string
		mutate   func(p *cloudinit.Params)
		wantPcap bool
	}{
		{"default", func(p *cloudinit.Params) {}, false},
		{"egress", func(p *cloudinit.Params) { p.EgressControl = true }, false},
		{"egress_proxy", func(p *cloudinit.Params) { p.EgressControl = true; p.EgressProxyEnabled = true }, false},
		{"egress_proxy_diagnostics", func(p *cloudinit.Params) {
			p.EgressControl = true
			p.EgressProxyEnabled = true
			p.EnableLEDiagnostics = true
		}, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			params := base()
			tc.mutate(params)

			s, err := cloudinit.Linux(params)
			if err != nil {
				t.Fatalf("render failed: %v", err)
			}

			var out map[string]interface{}
			if err := yaml.Unmarshal([]byte(s), &out); err != nil {
				t.Fatalf("rendered cloud-config is not valid YAML: %v\n---\n%s", err, s)
			}

			for _, m := range markers {
				if !strings.Contains(s, m) {
					t.Errorf("rendered output missing binary marker %q", m)
				}
			}

			gotPcap := strings.Contains(s, "le-pcap.service")
			if gotPcap != tc.wantPcap {
				t.Errorf("le-pcap diagnostics block presence = %t, want %t", gotPcap, tc.wantPcap)
			}
		})
	}
}

// TestLinuxEgressProxyDispatch verifies that when both EgressControl and
// EgressProxyEnabled are set, the ubuntu_linux_egress_v2 template is selected and
// the proxy URL, no-proxy list and mitm CA are substituted from params. It also
// guards that EgressControl alone (proxy disabled) keeps using the v1 egress
// template and does not leak proxy wiring.
func TestLinuxEgressProxyDispatch(t *testing.T) {
	const (
		proxyURL = "http://egress-proxy.internal:3128"
		noProxy  = "localhost,127.0.0.1,10.0.0.0/8"
		caCert   = "EGRESS-CA-PEM-CONTENT"
	)

	t.Run("EgressControl+EgressProxyEnabled uses v2 proxy template", func(t *testing.T) {
		params := &cloudinit.Params{
			LiteEnginePath:         liteEnginePath,
			LiteEngineFallbackPath: liteEnginePath,
			CACert:                 caCertFile + "\n",
			TLSCert:                certFile + "\n",
			TLSKey:                 keyFile + "\n",
			Platform:               types.Platform{OS: "linux", Arch: "amd64"},
			EgressControl:          true,
			EgressProxyEnabled:     true,
			EgressProxyURL:         proxyURL,
			EgressNoProxy:          noProxy,
			EgressCACert:           caCert,
		}

		s, err := cloudinit.Linux(params)
		if err != nil {
			t.Fatalf("render failed: %v", err)
		}

		var out map[string]interface{}
		if err := yaml.Unmarshal([]byte(s), &out); err != nil {
			t.Fatalf("rendered cloud-config is not valid YAML: %v\n---\n%s", err, s)
		}

		for _, want := range []string{
			"HTTPS_PROXY=" + proxyURL,
			"HTTP_PROXY=" + proxyURL,
			"NO_PROXY=" + noProxy,
			proxyURL + "/healthz",
			base64.StdEncoding.EncodeToString([]byte(caCert)),
		} {
			if !strings.Contains(s, want) {
				t.Errorf("v2 egress-proxy output missing %q", want)
			}
		}
	})

	t.Run("EgressControl without proxy keeps v1 egress template", func(t *testing.T) {
		params := &cloudinit.Params{
			LiteEnginePath:         liteEnginePath,
			LiteEngineFallbackPath: liteEnginePath,
			CACert:                 caCertFile + "\n",
			TLSCert:                certFile + "\n",
			TLSKey:                 keyFile + "\n",
			Platform:               types.Platform{OS: "linux", Arch: "amd64"},
			EgressControl:          true,
			EgressProxyEnabled:     false,
			EgressProxyURL:         proxyURL,
		}

		s, err := cloudinit.Linux(params)
		if err != nil {
			t.Fatalf("render failed: %v", err)
		}
		if strings.Contains(s, proxyURL) {
			t.Errorf("v1 egress template unexpectedly contains proxy URL %q", proxyURL)
		}
	})
}

func TestWindows(t *testing.T) {
	params := &cloudinit.Params{
		LiteEnginePath: liteEnginePath,
		CACert:         caCertFile + "\n",
		TLSCert:        certFile + "\n",
		TLSKey:         keyFile + "\n",
	}

	s := cloudinit.Windows(params)
	lePath := fmt.Sprintf(`"%s/lite-engine-%s-%s.exe"`, params.LiteEnginePath, params.Platform.OS, params.Platform.Arch)
	if !strings.Contains(s, lePath) {
		t.Error("windows init script does not contain LE path")
	}
}

// TestWindowsBinariesPartialShared verifies the shared "windows_binaries" partial
// renders correctly in both windows templates (default and egress+proxy): the
// output must contain every binary-download command.
func TestWindowsBinariesPartialShared(t *testing.T) {
	base := func() *cloudinit.Params {
		return &cloudinit.Params{
			LiteEnginePath:               liteEnginePath,
			LiteEngineFallbackPath:       liteEnginePath,
			CACert:                       caCertFile + "\n",
			TLSCert:                      certFile + "\n",
			TLSKey:                       keyFile + "\n",
			Platform:                     types.Platform{OS: "windows", Arch: "amd64"},
			PluginBinaryURI:              "https://example.com/plugin",
			PluginBinaryFallbackURI:      "https://example.com/plugin-fb",
			AutoInjectionBinaryURI:       "https://example.com/ai",
			AnnotationsBinaryURI:         "https://example.com/hcli/",
			AnnotationsBinaryFallbackURI: "https://example.com/hcli-fb/",
			EgressCACert:                 caCertFile + "\n",
		}
	}

	markers := []string{
		"lite-engine.exe",
		"plugin.exe",
		"auto-injection.exe",
		"hcli.exe",
	}

	cases := []struct {
		name   string
		mutate func(p *cloudinit.Params)
	}{
		{"default", func(p *cloudinit.Params) {}},
		{"egress_proxy", func(p *cloudinit.Params) { p.EgressControl = true; p.EgressProxyEnabled = true }},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			params := base()
			tc.mutate(params)

			s := cloudinit.Windows(params)
			for _, m := range markers {
				if !strings.Contains(s, m) {
					t.Errorf("rendered output missing binary marker %q", m)
				}
			}
		})
	}
}

// TestWindowsEgressProxyDispatch verifies that when both EgressControl and
// EgressProxyEnabled are set, the windows_egress template is selected and the
// proxy URL, no-proxy list and mitm CA are substituted from params. The default
// windows template (no egress) must not contain any of that proxy wiring.
func TestWindowsEgressProxyDispatch(t *testing.T) {
	const (
		proxyURL = "http://egress-proxy.internal:3128"
		noProxy  = "localhost,127.0.0.1,10.0.0.0/8"
		caCert   = "EGRESS-CA-PEM-CONTENT"
	)

	base := func() *cloudinit.Params {
		return &cloudinit.Params{
			LiteEnginePath:         liteEnginePath,
			LiteEngineFallbackPath: liteEnginePath,
			CACert:                 caCertFile + "\n",
			TLSCert:                certFile + "\n",
			TLSKey:                 keyFile + "\n",
			Platform:               types.Platform{OS: "windows", Arch: "amd64"},
			EgressProxyURL:         proxyURL,
			EgressNoProxy:          noProxy,
			EgressCACert:           caCert,
		}
	}

	t.Run("EgressControl+EgressProxyEnabled uses windows_egress template", func(t *testing.T) {
		params := base()
		params.EgressControl = true
		params.EgressProxyEnabled = true

		s := cloudinit.Windows(params)
		for _, want := range []string{
			`$EgressProxy   = "` + proxyURL + `"`,
			`$EgressNoProxy = "` + noProxy + `"`,
			base64.StdEncoding.EncodeToString([]byte(caCert)),
		} {
			if !strings.Contains(s, want) {
				t.Errorf("windows_egress output missing %q", want)
			}
		}
	})

	t.Run("default windows template has no proxy wiring", func(t *testing.T) {
		s := cloudinit.Windows(base())
		if strings.Contains(s, "$EgressProxy") {
			t.Errorf("default windows template unexpectedly contains egress proxy wiring")
		}
	})
}
