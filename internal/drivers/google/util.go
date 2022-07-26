package google

import "math/rand"

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

// substrStrPrefix removes additional characters from prefix
// if input string size is greater than input max length
func substrStrPrefix(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[len(s)-maxLen:]
}
