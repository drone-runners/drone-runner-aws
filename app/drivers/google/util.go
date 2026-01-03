package google

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

const (
	letters            = "0123456789abcdefghijklmnopqrstuvwxyz"
	maxSplitParts      = 2
	minImagePathSplits = 5
)

func randStringRunes(n int) (string, error) {
	ret := make([]byte, n)
	for i := 0; i < n; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			return "", err
		}
		ret[i] = letters[num.Int64()]
	}

	return string(ret), nil
}

// substrSuffix removes additional characters from prefix
// if input string size is greater than input max length
func substrSuffix(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[len(s)-maxLen:]
}
// buildImagePathFromTag constructs a GCP image path from an image tag.
//
// The image tag can be in two formats:
// 1. With prefix: "harness/vmimage:hosted-vm-ubuntu-2404-noble-amd64-v20250530"
//    - Extracts the part after ":" as the image name
// 2. Plain name: "hosted-vm-164-arm"
//    - Uses the entire string as the image name
//
// Returns the 4-segment format: {projectID}/global/images/{imageName}
// This format is compatible with the SourceImage construction which adds:
//   https://www.googleapis.com/compute/v1/projects/
//
// Final URL: https://www.googleapis.com/compute/v1/projects/{projectID}/global/images/{imageName}
func buildImagePathFromTag(imageTag, projectID string) string {
	imagePath := fmt.Sprintf("%s/global/images/", projectID)
	imageName := extractImageNameFromTag(imageTag)
	return imagePath + imageName
}

// isFullImagePath returns true if the image name contains a full GCP image path.
//
// Supported formats:
// 1. 5-segment with "projects/" prefix: projects/<project>/global/images/<image>
//    Example: projects/debian-cloud/global/images/debian-11-bullseye-v20250705
//
// 2. 4-segment without "projects/" prefix: <project>/global/images/<image>
//    Example: ubuntu-os-cloud/global/images/ubuntu-2004-focal-v20231213
//    Example: harness-byoi-prod/global/images/abc123-my-ubuntu-v1.0.0 (BYOI custom images)
//
// The 4-segment format matches the default p.image format and is used by:
// - Harness-owned default images
// - BYOI custom images
func isFullImagePath(imageName string) bool {
	imageList := strings.Split(imageName, "/")

	// Check for 5-segment format: projects/{project}/global/images/{name}
	if len(imageList) == minImagePathSplits {
		if imageList[0] == "projects" && imageList[2] == "global" && imageList[3] == "images" {
			if imageList[1] != "" && imageList[4] != "" {
				return true
			}
		}
	}

	// Check for 4-segment format: {project}/global/images/{name}
	// This matches the default p.image format used by Harness-owned images and BYOI
	if len(imageList) == 4 {
		if imageList[1] == "global" && imageList[2] == "images" {
			if imageList[0] != "" && imageList[3] != "" {
				return true
			}
		}
	}

	return false
}

func extractImageNameFromTag(tag string) string {
	index := strings.Index(tag, ":")
	if index == -1 {
		return tag
	}
	parts := strings.SplitN(tag, ":", maxSplitParts)
	return parts[1]
}
