package database

import (
	"errors"
	"io"
	"net"
	"testing"

	"github.com/lib/pq"
)

func TestIsTransientDBError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "pq connection exception (class 08)",
			err:      &pq.Error{Code: "08006"},
			expected: true,
		},
		{
			name:     "pq operator intervention (class 57)",
			err:      &pq.Error{Code: "57P01"},
			expected: true,
		},
		{
			name:     "pq syntax error (not transient)",
			err:      &pq.Error{Code: "42601"},
			expected: false,
		},
		{
			name:     "EOF",
			err:      io.EOF,
			expected: true,
		},
		{
			name:     "unexpected EOF",
			err:      io.ErrUnexpectedEOF,
			expected: true,
		},
		{
			name:     "connection reset",
			err:      errors.New("read tcp: connection reset by peer"),
			expected: true,
		},
		{
			name:     "connection refused",
			err:      errors.New("dial tcp: connection refused"),
			expected: true,
		},
		{
			name:     "bad connection",
			err:      errors.New("driver: bad connection"),
			expected: true,
		},
		{
			name:     "broken pipe",
			err:      errors.New("write: broken pipe"),
			expected: true,
		},
		{
			name:     "regular query error (not transient)",
			err:      errors.New("pq: relation \"foo\" does not exist"),
			expected: false,
		},
		{
			name:     "no rows (not transient)",
			err:      errors.New("sql: no rows in result set"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsTransientDBError(tt.err)
			if result != tt.expected {
				t.Errorf("IsTransientDBError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// mockNetError implements net.Error for testing.
type mockNetError struct{}

func (e *mockNetError) Error() string   { return "mock network error" }
func (e *mockNetError) Timeout() bool   { return false }
func (e *mockNetError) Temporary() bool { return true } //nolint:staticcheck

var _ net.Error = (*mockNetError)(nil)

func TestIsTransientDBError_NetError(t *testing.T) {
	err := &mockNetError{}
	if !IsTransientDBError(err) {
		t.Error("expected net.Error to be transient")
	}
}

func TestRetry_SucceedsImmediately(t *testing.T) {
	calls := 0
	result, err := Retry(func() (string, error) {
		calls++
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected 'ok', got %q", result)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestRetry_RetriesTransientThenSucceeds(t *testing.T) {
	calls := 0
	result, err := Retry(func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("driver: bad connection")
		}
		return "recovered", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "recovered" {
		t.Fatalf("expected 'recovered', got %q", result)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestRetry_DoesNotRetryPermanent(t *testing.T) {
	calls := 0
	_, err := Retry(func() (string, error) {
		calls++
		return "", errors.New("pq: relation \"foo\" does not exist")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (no retry for permanent error), got %d", calls)
	}
}

func TestRetryVoid_SucceedsImmediately(t *testing.T) {
	calls := 0
	err := RetryVoid(func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}
