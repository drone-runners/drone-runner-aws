package nomad

import (
	"math/rand"
	"time"
)

// stringToPtr returns a pointer to a string
func stringToPtr(s string) *string {
	return &s
}

// intToPtr returns a pointer to a int
func intToPtr(i int) *int {
	return &i
}

// boolToPtr returns a pointer to a bool
func boolToPtr(b bool) *bool {
	return &b
}

// durationToPtr returns a pointer to a duration
func durationToPtr(d time.Duration) *time.Duration {
	return &d
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

// random generates a random string of length n
func random(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

// seed the random number generator
func init() {
	rand.Seed(time.Now().UnixNano())
}
