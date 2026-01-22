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
	shortImagePath     = 4
	imagesSegment      = "images"
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
//   - Extracts the part after ":" as the image name
//
// 2. Plain name: "hosted-vm-164-arm"
//   - Uses the entire string as the image name
//
// Returns the 4-segment format: {projectID}/global/images/{imageName}
// This format is compatible with the SourceImage construction which adds:
//
//	https://www.googleapis.com/compute/v1/projects/
//
// Final URL: https://www.googleapis.com/compute/v1/projects/{projectID}/global/images/{imageName}
func buildImagePathFromTag(imageTag, projectID string) string {
	imagePath := fmt.Sprintf("%s/global/images/", projectID)
	imageName := extractImageNameFromTag(imageTag)
	return imagePath + imageName
}

// isFullImagePath returns true if the image name contains a full GCP image path.
// Supports 5-segment (projects/<project>/global/images/<name>) and 4-segment (<project>/global/images/<name>) formats.
func isFullImagePath(imageName string) bool {
	imageList := strings.Split(imageName, "/")

	// 5-segment: projects/{project}/global/images/{name}
	if len(imageList) == minImagePathSplits {
		if imageList[0] == "projects" && imageList[2] == "global" && imageList[3] == imagesSegment {
			if imageList[1] != "" && imageList[4] != "" {
				return true
			}
		}
	}

	// 4-segment: {project}/global/images/{name}
	if len(imageList) == shortImagePath {
		if imageList[1] == "global" && imageList[2] == imagesSegment {
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

// normalizeImagePath converts 5-segment paths to 4-segment by stripping "projects/" prefix.
// Prevents double "projects/" in SourceImage URL.
func normalizeImagePath(imagePath string) string {
	parts := strings.Split(imagePath, "/")
	if len(parts) == minImagePathSplits && parts[0] == "projects" {
		return strings.Join(parts[1:], "/")
	}
	return imagePath
}

// isByoiImage returns true if the image path contains a BYOI custom image (starts with "byoi-").
func isByoiImage(imagePath string) bool {
	parts := strings.Split(imagePath, "/")
	if len(parts) >= shortImagePath && parts[len(parts)-2] == imagesSegment {
		imageName := parts[len(parts)-1]
		return strings.HasPrefix(strings.ToLower(imageName), "byoi-")
	}
	return strings.HasPrefix(strings.ToLower(imagePath), "byoi-")
}
