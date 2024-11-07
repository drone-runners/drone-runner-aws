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
