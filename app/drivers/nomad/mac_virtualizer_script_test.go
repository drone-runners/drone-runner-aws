package nomad

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	cf "github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/types"
)

// TestGenerateStartupScriptSyntax catches regressions where Go fmt-string
// escaping breaks the generated bash (e.g. unescaped `%` in parameter
// expansions producing `%!:(MISSING)` and an unbalanced `(`).
func TestGenerateStartupScriptSyntax(t *testing.T) {
	script := generateScriptForTest(t, "ghcr.io/example/macos-base:latest", "ghcr.io")

	cmd := exec.CommandContext(context.Background(), "bash", "-n")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash syntax check failed: %v\noutput: %s\n--- script ---\n%s", err, out, script)
	}
}

// TestRegistryHostnameDerivedFromImage guards against the regression where
// TART_REGISTRY_HOSTNAME was set from the connector URL (e.g.
// https://index.docker.io/v1/) instead of the image host (registry-1.docker.io).
// tart only applies credentials when the hostname matches the image host, so a
// mismatch causes anonymous pulls and 401s for private images.
func TestRegistryHostnameDerivedFromImage(t *testing.T) {
	cases := []struct {
		name     string
		vmImage  string
		registry string
	}{
		{
			name:     "docker hub connector url does not leak into hostname",
			vmImage:  "registry-1.docker.io/dhirajharness/byoi-paypal-test:tag",
			registry: "https://index.docker.io/v1/",
		},
		{
			// GAR worked before because its connector host already equals the
			// image host; deriving from the image keeps it identical.
			name:     "gar host stays correct",
			vmImage:  "us-west1-docker.pkg.dev/proj/repo/macos-base:latest",
			registry: "https://us-west1-docker.pkg.dev",
		},
		{
			name:     "ghcr image host",
			vmImage:  "ghcr.io/example/macos-base:latest",
			registry: "https://ghcr.io",
		},
		{
			name:     "registry with port",
			vmImage:  "registry.local:5000/team/macos-base:latest",
			registry: "https://registry.local:5000/v2/",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := generateScriptForTest(t, tc.vmImage, tc.registry)
			wantHost := tc.vmImage[:strings.Index(tc.vmImage, "/")]
			wantExport := `export TART_REGISTRY_HOSTNAME="${VM_IMAGE%%/*}"`
			if !strings.Contains(script, wantExport) {
				t.Fatalf("script does not derive hostname from image; want %q in:\n%s", wantExport, script)
			}
			// The connector URL must not be used as the hostname value.
			badExport := `export TART_REGISTRY_HOSTNAME="$REGISTRY"`
			if strings.Contains(script, badExport) {
				t.Fatalf("script still sets hostname from connector URL %q", tc.registry)
			}
			// Sanity: confirm the derived value resolves to the image host.
			resolved := resolveRegistryHostname(t, tc.vmImage)
			if resolved != wantHost {
				t.Fatalf("derived hostname = %q, want %q", resolved, wantHost)
			}
		})
	}
}

// resolveRegistryHostname runs the bash parameter expansion the script uses so
// the test verifies the actual runtime value, not just the literal source.
func resolveRegistryHostname(t *testing.T, vmImage string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "bash", "-c", `VM_IMAGE="$1"; printf '%s' "${VM_IMAGE%%/*}"`, "bash", vmImage)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash expansion failed: %v", err)
	}
	return string(out)
}

// TestImageCleanup exercises the tart image cleanup + pull logic with a mock
// `tart` binary, verifying which images get deleted and pulled in each case.
func TestImageCleanup(t *testing.T) {
	cases := []struct {
		name            string
		vmImage         string
		registry        string
		tartList        []string
		expectedDeletes []string
		expectedPulls   []string
	}{
		{
			name:     "different fully qualified tag deletes both old refs and pulls new",
			vmImage:  "registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.6",
			registry: "registry-1.docker.io",
			tartList: []string{
				"sequoia_local_a",
				"sonoma_local_b",
				"registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.5",
				"registry-1.docker.io/harness/macos-vm-images@sha256:abc",
			},
			expectedDeletes: []string{
				"registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.5",
				"registry-1.docker.io/harness/macos-vm-images@sha256:abc",
			},
			expectedPulls: []string{
				"registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.6",
			},
		},
		{
			name:     "same tag already present preserves both refs and skips pull",
			vmImage:  "registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.6",
			registry: "registry-1.docker.io",
			tartList: []string{
				"sequoia_local_a",
				"registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.6",
				"registry-1.docker.io/harness/macos-vm-images@sha256:abc",
			},
			expectedDeletes: nil,
			expectedPulls:   nil,
		},
		{
			name:     "non fully qualified image request skips cleanup and pull entirely",
			vmImage:  "sequoia_local_a",
			registry: "",
			tartList: []string{
				"sequoia_local_a",
				"registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.5",
			},
			expectedDeletes: nil,
			expectedPulls:   nil,
		},
		{
			name:     "non fully qualified images in cache are never deleted",
			vmImage:  "registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.6",
			registry: "registry-1.docker.io",
			tartList: []string{
				"sequoia_local_a",
				"sonoma_local_b",
			},
			expectedDeletes: nil,
			expectedPulls: []string{
				"registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.6",
			},
		},
		{
			name:            "empty cache just pulls the requested image",
			vmImage:         "registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.6",
			registry:        "registry-1.docker.io",
			tartList:        nil,
			expectedDeletes: nil,
			expectedPulls: []string{
				"registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.6",
			},
		},
		{
			name:     "image without tag is normalized to :latest and matches cached :latest",
			vmImage:  "ghcr.io/example/macos-base",
			registry: "ghcr.io",
			tartList: []string{
				"ghcr.io/example/macos-base:latest",
				"ghcr.io/example/macos-base@sha256:abc",
			},
			expectedDeletes: nil,
			expectedPulls:   nil,
		},
		{
			name:     "image with explicit :latest matches cached :latest",
			vmImage:  "ghcr.io/example/macos-base:latest",
			registry: "ghcr.io",
			tartList: []string{
				"ghcr.io/example/macos-base:latest",
				"ghcr.io/example/macos-base@sha256:abc",
			},
			expectedDeletes: nil,
			expectedPulls:   nil,
		},
		{
			name:            "image without tag against empty cache pulls normalized :latest",
			vmImage:         "ghcr.io/example/macos-base",
			registry:        "ghcr.io",
			tartList:        nil,
			expectedDeletes: nil,
			expectedPulls: []string{
				"ghcr.io/example/macos-base:latest",
			},
		},
		{
			name:     "registry with port and no tag is normalized correctly",
			vmImage:  "registry.local:5000/team/macos-base",
			registry: "registry.local:5000",
			tartList: []string{
				"registry.local:5000/team/macos-base:latest",
			},
			expectedDeletes: nil,
			expectedPulls:   nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mockPath := installMockTart(t, dir, tc.tartList)
			script := extractImageCleanupScript(t, tc.vmImage, tc.registry)
			// Redirect the hard-coded /opt/homebrew/bin/tart path to our mock,
			// invoked via `bash <mockPath>` so we don't need the executable bit.
			script = strings.ReplaceAll(script, "/opt/homebrew/bin/tart", "bash "+mockPath)

			cmd := exec.CommandContext(context.Background(), "bash")
			cmd.Stdin = strings.NewReader(script)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("script failed: %v\noutput:\n%s", err, out)
			}

			deletes, pulls := readMockCalls(t, dir)
			if !sortedEqual(deletes, tc.expectedDeletes) {
				t.Errorf("deletes mismatch\n  got:  %v\n  want: %v\nscript output:\n%s",
					deletes, tc.expectedDeletes, out)
			}
			if !sortedEqual(pulls, tc.expectedPulls) {
				t.Errorf("pulls mismatch\n  got:  %v\n  want: %v\nscript output:\n%s",
					pulls, tc.expectedPulls, out)
			}
		})
	}
}

// generateScriptForTest invokes generateStartupScript with stable test values.
func generateScriptForTest(t *testing.T, vmImage, registry string) string {
	t.Helper()
	mv := NewMacVirtualizer(&types.NomadConfig{})
	imgCfg := types.VMImageConfig{
		ImageName: vmImage,
		Username:  "admin",
		Password:  "pw",
		VMImageAuth: types.VMImageAuth{
			Registry: registry,
			Username: "u",
			Password: "p",
		},
	}
	resource := cf.NomadResource{Cpus: "4", MemoryGB: "8", DiskSize: "100"}
	return mv.generateStartupScript("vm-id", "machine-pw", imgCfg, resource, 8080)
}

// extractImageCleanupScript returns the portion of the generated startup
// script up to (but not including) the `tart clone` step. This lets tests
// exercise just the image presence-check, cleanup and pull logic without
// trying to actually start a VM.
func extractImageCleanupScript(t *testing.T, vmImage, registry string) string {
	t.Helper()
	full := generateScriptForTest(t, vmImage, registry)
	marker := `echo "Cloning tart VM`
	cutAt := strings.Index(full, marker)
	if cutAt < 0 {
		t.Fatalf("could not find %q in generated script", marker)
	}
	return full[:cutAt]
}

// installMockTart writes a fake `tart` shell script into dir that responds
// to `tart list` with the given image names (in the same column layout as
// real tart output) and logs every invocation's args to calls.log.
func installMockTart(t *testing.T, dir string, tartList []string) string {
	t.Helper()
	listFile := filepath.Join(dir, "list.txt")
	rows := []string{"Source Name Disk Size SizeOnDisk State"}
	for _, img := range tartList {
		rows = append(rows, "OCI "+img+" 0 0 0 stopped")
	}
	if err := os.WriteFile(listFile, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write list file: %v", err)
	}
	callsFile := filepath.Join(dir, "calls.log")

	mock := "#!/bin/bash\n" +
		"echo \"$@\" >> '" + callsFile + "'\n" +
		"case \"$1\" in\n" +
		"  list) cat '" + listFile + "' ;;\n" +
		"esac\n" +
		"exit 0\n"
	mockPath := filepath.Join(dir, "tart")
	if err := os.WriteFile(mockPath, []byte(mock), 0o600); err != nil {
		t.Fatalf("write mock tart: %v", err)
	}
	// The mock is invoked as `bash <mockPath> ...` from the test script, so we
	// don't need the executable bit (and gosec G302/G306 forbid setting it).
	return mockPath
}

// readMockCalls parses the mock tart call log and returns the image names
// passed to `tart delete` and `tart pull`.
func readMockCalls(t *testing.T, dir string) (deletes, pulls []string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		t.Fatalf("read calls log: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "delete":
			deletes = append(deletes, fields[1])
		case "pull":
			pulls = append(pulls, fields[1])
		}
	}
	return deletes, pulls
}

func sortedEqual(a, b []string) bool {
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	return reflect.DeepEqual(x, y)
}
