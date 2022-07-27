package google

import (
	"crypto/rand"
	"math/big"
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

// substrStrPrefix removes additional characters from prefix
// if input string size is greater than input max length
func substrStrPrefix(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[len(s)-maxLen:]
}
