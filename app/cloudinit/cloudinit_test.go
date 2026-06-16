package cloudinit_test

import (
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
