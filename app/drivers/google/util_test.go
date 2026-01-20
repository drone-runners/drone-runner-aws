package google

import (
	"testing"
)

func Test_substrSuffix(t *testing.T) {
	tests := []struct {
		s        string
		maxLen   int
		expected string
	}{
		{s: "hello", maxLen: 63, expected: "hello"},
		{s: "hello", maxLen: 2, expected: "lo"},
		{s: "hello", maxLen: 5, expected: "hello"},
	}

	for _, test := range tests {
		if got, want := substrSuffix(test.s, test.maxLen), test.expected; got != want {
			t.Errorf("Want substring %s, got %s", want, got)
		}
	}
}

func Test_isFullImagePath(t *testing.T) {
	tests := []struct {
		name      string
		imageName string
		expected  bool
	}{
		// 4-segment format (standard Harness images and BYOI)
		{
			name:      "4-segment Harness image",
			imageName: "ubuntu-os-cloud/global/images/ubuntu-2004-focal-v20231213",
			expected:  true,
		},
		{
			name:      "4-segment BYOI custom image",
			imageName: "harness-byoi-prod/global/images/abc123-my-ubuntu-v1.0.0",
			expected:  true,
		},
		// 5-segment format (with projects/ prefix)
		{
			name:      "5-segment with projects prefix",
			imageName: "projects/debian-cloud/global/images/debian-11-bullseye-v20250705",
			expected:  true,
		},
		{
			name:      "5-segment with projects prefix - ubuntu",
			imageName: "projects/ubuntu-os-cloud/global/images/ubuntu-2404-lts-amd64",
			expected:  true,
		},
		// Invalid formats
		{
			name:      "simple image tag",
			imageName: "hosted-vm-ubuntu-2204-jammy-v20250508",
			expected:  false,
		},
		{
			name:      "docker image format",
			imageName: "harness/vmimage:hosted-vm-ubuntu-2204-jammy-v20250508",
			expected:  false,
		},
		{
			name:      "empty string",
			imageName: "",
			expected:  false,
		},
		{
			name:      "3-segment path",
			imageName: "project/global/images",
			expected:  false,
		},
		{
			name:      "4-segment wrong structure",
			imageName: "project/wrong/images/name",
			expected:  false,
		},
		{
			name:      "5-segment wrong prefix",
			imageName: "wrong/project/global/images/name",
			expected:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := isFullImagePath(test.imageName)
			if got != test.expected {
				t.Errorf("isFullImagePath(%q) = %v, want %v", test.imageName, got, test.expected)
			}
		})
	}
}

func Test_buildImagePathFromTag(t *testing.T) {
	tests := []struct {
		name      string
		imageTag  string
		projectID string
		expected  string
	}{
		{
			name:      "docker image format with colon",
			imageTag:  "harness/vmimage:hosted-vm-ubuntu-2204-jammy-v20250508",
			projectID: "harness-ci-images",
			expected:  "harness-ci-images/global/images/hosted-vm-ubuntu-2204-jammy-v20250508",
		},
		{
			name:      "plain image name",
			imageTag:  "hosted-vm-164-arm",
			projectID: "harness-ci-images",
			expected:  "harness-ci-images/global/images/hosted-vm-164-arm",
		},
		{
			name:      "image with version tag",
			imageTag:  "custom-image:v1.0.0",
			projectID: "my-project",
			expected:  "my-project/global/images/v1.0.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := buildImagePathFromTag(test.imageTag, test.projectID)
			if got != test.expected {
				t.Errorf("buildImagePathFromTag(%q, %q) = %q, want %q",
					test.imageTag, test.projectID, got, test.expected)
			}
		})
	}
}

func Test_normalizeImagePath(t *testing.T) {
	tests := []struct {
		name      string
		imagePath string
		expected  string
	}{
		// 5-segment format with "projects/" prefix should be normalized
		{
			name:      "5-segment to 4-segment",
			imagePath: "projects/debian-cloud/global/images/debian-11-bullseye-v20250705",
			expected:  "debian-cloud/global/images/debian-11-bullseye-v20250705",
		},
		{
			name:      "5-segment ubuntu to 4-segment",
			imagePath: "projects/ubuntu-os-cloud/global/images/ubuntu-2004-focal-v20231213",
			expected:  "ubuntu-os-cloud/global/images/ubuntu-2004-focal-v20231213",
		},
		// 4-segment format should remain unchanged
		{
			name:      "4-segment unchanged",
			imagePath: "harness-ci-images/global/images/hosted-vm-ubuntu-2204-jammy-v20250508",
			expected:  "harness-ci-images/global/images/hosted-vm-ubuntu-2204-jammy-v20250508",
		},
		{
			name:      "4-segment BYOI unchanged",
			imagePath: "ci-play/global/images/byoi-accountid-my-ubuntu-v1",
			expected:  "ci-play/global/images/byoi-accountid-my-ubuntu-v1",
		},
		// Other formats should remain unchanged
		{
			name:      "simple image name unchanged",
			imagePath: "hosted-vm-ubuntu-2204",
			expected:  "hosted-vm-ubuntu-2204",
		},
		{
			name:      "empty string unchanged",
			imagePath: "",
			expected:  "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeImagePath(test.imagePath)
			if got != test.expected {
				t.Errorf("normalizeImagePath(%q) = %q, want %q", test.imagePath, got, test.expected)
			}
		})
	}
}

func Test_isByoiImage(t *testing.T) {
	tests := []struct {
		name      string
		imagePath string
		expected  bool
	}{
		// BYOI images - should return true
		{
			name:      "4-segment BYOI image path",
			imagePath: "ci-play/global/images/byoi-accountid-my-ubuntu-v1",
			expected:  true,
		},
		{
			name:      "4-segment BYOI image path uppercase",
			imagePath: "ci-play/global/images/BYOI-accountid-my-ubuntu-v1",
			expected:  true,
		},
		{
			name:      "5-segment BYOI image path with projects prefix",
			imagePath: "projects/harness-byoi-prod/global/images/byoi-abc123-custom-v2",
			expected:  true,
		},
		{
			name:      "plain BYOI image name",
			imagePath: "byoi-accountid-imagename-v1",
			expected:  true,
		},
		// Non-BYOI images - should return false
		{
			name:      "standard Harness image",
			imagePath: "harness-ci-images/global/images/hosted-vm-ubuntu-2204-jammy-v20250508",
			expected:  false,
		},
		{
			name:      "debian cloud image",
			imagePath: "projects/debian-cloud/global/images/debian-11-bullseye-v20250705",
			expected:  false,
		},
		{
			name:      "plain non-BYOI image name",
			imagePath: "hosted-vm-ubuntu-2204",
			expected:  false,
		},
		{
			name:      "empty string",
			imagePath: "",
			expected:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := isByoiImage(test.imagePath)
			if got != test.expected {
				t.Errorf("isByoiImage(%q) = %v, want %v", test.imagePath, got, test.expected)
			}
		})
	}
}

func Test_extractImageNameFromTag(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		expected string
	}{
		{
			name:     "docker image format",
			tag:      "harness/vmimage:hosted-vm-ubuntu-2204-jammy-v20250508",
			expected: "hosted-vm-ubuntu-2204-jammy-v20250508",
		},
		{
			name:     "plain name without colon",
			tag:      "hosted-vm-164-arm",
			expected: "hosted-vm-164-arm",
		},
		{
			name:     "empty string",
			tag:      "",
			expected: "",
		},
		{
			name:     "just a colon",
			tag:      ":",
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := extractImageNameFromTag(test.tag)
			if got != test.expected {
				t.Errorf("extractImageNameFromTag(%q) = %q, want %q", test.tag, got, test.expected)
			}
		})
	}
}
