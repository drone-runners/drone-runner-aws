package nomad

import (
	"testing"
	"time"

	"github.com/drone-runners/drone-runner-aws/types"
)

// TestIsSequoiaImage verifies that image name detection is case-insensitive
// and correctly distinguishes Sequoia images from others (including empty).
func TestIsSequoiaImage(t *testing.T) {
	cases := []struct {
		name      string
		imageName string
		want      bool
	}{
		{
			name:      "exact sequoia image name",
			imageName: "sequoia_15.6.1_xcodes_16.4_default_16.3",
			want:      true,
		},
		{
			name:      "sequoia with uppercase",
			imageName: "Sequoia_15.6.1",
			want:      true,
		},
		{
			name:      "sequoia uppercase",
			imageName: "SEQUOIA",
			want:      true,
		},
		{
			name:      "sonoma image",
			imageName: "sonoma_14.6_xcodes_15.2_default_16.3_16.1_15.1",
			want:      false,
		},
		{
			name:      "tahoe image",
			imageName: "tahoe_26.5_xcodes_26.4.1_default",
			want:      false,
		},
		{
			name:      "empty image name (pool default, treated as non-sequoia)",
			imageName: "",
			want:      false,
		},
		{
			name:      "fully qualified sequoia OCI image",
			imageName: "us-docker.pkg.dev/ci-play/cie-hosted-images/sequoia_base:latest",
			want:      true,
		},
		{
			name:      "fully qualified sonoma OCI image",
			imageName: "us-docker.pkg.dev/ci-play/cie-hosted-images/sonoma_base:latest",
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isSequoiaImage(tc.imageName)
			if got != tc.want {
				t.Errorf("isSequoiaImage(%q) = %v, want %v", tc.imageName, got, tc.want)
			}
		})
	}
}

// TestSequoiaExclusionConstraint verifies the constraint targets the correct
// Nomad meta attribute with the != operand.
func TestSequoiaExclusionConstraint(t *testing.T) {
	c := sequoiaExclusionConstraint()
	if c == nil {
		t.Fatal("sequoiaExclusionConstraint() returned nil")
	}
	if c.LTarget != "${meta.sequoia_only}" {
		t.Errorf("LTarget = %q, want %q", c.LTarget, "${meta.sequoia_only}")
	}
	if c.RTarget != "true" {
		t.Errorf("RTarget = %q, want %q", c.RTarget, "true")
	}
	if c.Operand != "!=" {
		t.Errorf("Operand = %q, want %q", c.Operand, "!=")
	}
}

// TestResourceJobSequoiaExclusionConstraint verifies that resourceJob() injects
// the sequoia exclusion constraint for non-Sequoia images and omits it for
// Sequoia images. This is the core scheduling invariant for M4 machine routing.
func TestResourceJobSequoiaExclusionConstraint(t *testing.T) {
	p := &config{
		nomadConfig: &types.NomadConfig{
			ClientDisconnectTimeout: time.Minute,
			ResourceJobTimeout:      5 * time.Minute,
		},
		virtualizer: NewMacVirtualizer(&types.NomadConfig{
			ClientDisconnectTimeout: time.Minute,
			InitTimeout:             5 * time.Minute,
			MinNomadCPUMhz:          100,
			MinNomadMemoryMb:        128,
			MacMachineFrequencyMhz:  3200,
		}),
	}

	noopHealthCheck := func(_ time.Duration, _, _ string) string { return "sleep 1" }

	cases := []struct {
		name                 string
		imageName            string
		accountID            string
		wantExclusionPresent bool
	}{
		{
			name:                 "sonoma image gets exclusion constraint",
			imageName:            "sonoma_14.6_xcodes_15.2_default_16.3_16.1_15.1",
			accountID:            "GLOBAL_ACCOUNT_ID_MAC",
			wantExclusionPresent: true,
		},
		{
			name:                 "tahoe image gets exclusion constraint",
			imageName:            "tahoe_26.5_xcodes_26.4.1_default",
			accountID:            "GLOBAL_ACCOUNT_ID_MAC",
			wantExclusionPresent: true,
		},
		{
			name:                 "empty image name gets exclusion constraint (pool default is non-sequoia)",
			imageName:            "",
			accountID:            "GLOBAL_ACCOUNT_ID_MAC",
			wantExclusionPresent: true,
		},
		{
			name:                 "sequoia image does NOT get exclusion constraint",
			imageName:            "sequoia_15.6.1_xcodes_16.4_default_16.3",
			accountID:            "GLOBAL_ACCOUNT_ID_MAC",
			wantExclusionPresent: false,
		},
		{
			name:                 "sequoia image with no accountID does NOT get exclusion constraint",
			imageName:            "sequoia_15.6.1_xcodes_16.4_default_16.3",
			accountID:            "",
			wantExclusionPresent: false,
		},
		{
			name:                 "sonoma with no accountID still gets exclusion constraint",
			imageName:            "sonoma_14.6_xcodes_15.2_default_16.3_16.1_15.1",
			accountID:            "",
			wantExclusionPresent: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vmImageConfig := types.VMImageConfig{ImageName: tc.imageName}
			job, _ := p.resourceJob(6, 12, 3200, 0, "test-vm-id", tc.accountID, vmImageConfig, noopHealthCheck)

			found := false
			for _, c := range job.Constraints {
				if c.LTarget == "${meta.sequoia_only}" && c.Operand == "!=" && c.RTarget == "true" {
					found = true
					break
				}
			}

			if found != tc.wantExclusionPresent {
				t.Errorf("image=%q: sequoia exclusion constraint present=%v, want %v",
					tc.imageName, found, tc.wantExclusionPresent)
			}
		})
	}
}

// TestResourceJobNodeClassConstraintPreserved verifies that the existing
// node.class (account pinning) constraint is not affected by the new
// sequoia exclusion logic.
func TestResourceJobNodeClassConstraintPreserved(t *testing.T) {
	p := &config{
		nomadConfig: &types.NomadConfig{
			ClientDisconnectTimeout: time.Minute,
			ResourceJobTimeout:      5 * time.Minute,
		},
		virtualizer: NewMacVirtualizer(&types.NomadConfig{
			ClientDisconnectTimeout: time.Minute,
			InitTimeout:             5 * time.Minute,
			MinNomadCPUMhz:          100,
			MinNomadMemoryMb:        128,
			MacMachineFrequencyMhz:  3200,
		}),
	}

	noopHealthCheck := func(_ time.Duration, _, _ string) string { return "sleep 1" }
	vmImageConfig := types.VMImageConfig{ImageName: "sonoma_14.6_xcodes_15.2"}
	job, _ := p.resourceJob(6, 12, 3200, 0, "test-vm-id", "GLOBAL_ACCOUNT_ID_MAC", vmImageConfig, noopHealthCheck)

	var nodeClassFound, sequoiaExclusionFound bool
	for _, c := range job.Constraints {
		if c.LTarget == "${node.class}" && c.Operand == "=" && c.RTarget == "GLOBAL_ACCOUNT_ID_MAC" {
			nodeClassFound = true
		}
		if c.LTarget == "${meta.sequoia_only}" && c.Operand == "!=" {
			sequoiaExclusionFound = true
		}
	}

	if !nodeClassFound {
		t.Error("node.class constraint missing — account pinning was broken")
	}
	if !sequoiaExclusionFound {
		t.Error("sequoia exclusion constraint missing for sonoma image")
	}
}
