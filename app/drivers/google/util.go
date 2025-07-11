package google

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

const (
	letters       = "0123456789abcdefghijklmnopqrstuvwxyz"
	maxSplitParts = 2
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
func buildImagePathFromTag(imageTag, projectID string) string {
	imagePath := fmt.Sprintf("projects/%s/global/images/", projectID)
	imageName := extractImageNameFromTag(imageTag)
	return imagePath + imageName
}

// isFullImagePath returns true if the image name contains full path ie. it is of the format:
// projects/<project-name>/global/images/<image-tag>
func isFullImagePath(imageName string) bool {
	imageList := strings.Split(imageName, "/")
	if len(imageList) < 5 {
		return false
	}

	if imageList[0] != "projects" && imageList[2] != "global" && imageList[3] != "images" {
		return false
	}

	if imageList[1] == "" || imageList[4] == "" {
		return false
	}

	return true
}

func extractImageNameFromTag(tag string) string {
	index := strings.Index(tag, ":")
	if index == -1 {
		return tag
	}
	parts := strings.SplitN(tag, ":", maxSplitParts)
	return parts[1]
}
