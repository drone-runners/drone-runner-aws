package google

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

const letters = "0123456789abcdefghijklmnopqrstuvwxyz"

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
func (p *config) buildImagePathFromTag(imageTag string) string {
	imagePath := fmt.Sprintf("projects/%s/global/images/", p.projectID)
	imageName := extractImageNameFromTag(imageTag)
	return imagePath + imageName
}

func extractImageNameFromTag(tag string) string {
	index := strings.Index(tag, ":")
	if index == -1 {
		return tag
	}
	parts := strings.SplitN(tag, ":", 2)
	return parts[1]
}
