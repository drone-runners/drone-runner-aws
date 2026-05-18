package nomad

import (
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

	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash syntax check failed: %v\noutput: %s\n--- script ---\n%s", err, out, script)
	}
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
			name:     "empty cache just pulls the requested image",
			vmImage:  "registry-1.docker.io/harness/macos-vm-images:vanilla_sequoia_15.6",
			registry: "registry-1.docker.io",
			tartList: nil,
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
			name:     "image without tag against empty cache pulls normalized :latest",
			vmImage:  "ghcr.io/example/macos-base",
			registry: "ghcr.io",
			tartList: nil,
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
			// Redirect the hard-coded /opt/homebrew/bin/tart path to our mock.
			script = strings.ReplaceAll(script, "/opt/homebrew/bin/tart", mockPath)

			cmd := exec.Command("bash")
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
	if err := os.WriteFile(listFile, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
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
	if err := os.WriteFile(mockPath, []byte(mock), 0o755); err != nil {
		t.Fatalf("write mock tart: %v", err)
	}
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
