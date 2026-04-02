package database

import (
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

// IsTransientDBError returns true if the error is a transient database error
// that may resolve on retry (e.g., connection drops during RDS failover).
func IsTransientDBError(err error) bool {
	if err == nil {
		return false
	}

	// Check for pq-specific errors
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		// Class 08 = Connection Exception
		// Class 57 = Operator Intervention (includes failover restart)
		// 25006   = READ_ONLY_SQL_TRANSACTION (standby receives write during failover)
		code := string(pqErr.Code)
		if strings.HasPrefix(code, "08") || strings.HasPrefix(code, "57") || code == "25006" {
			return true
		}
	}

	// Check for network errors
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	// Check for EOF (connection dropped)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// Check for common transient error strings
	errMsg := strings.ToLower(err.Error())
	transientPatterns := []string{
		"connection reset",
		"connection refused",
		"broken pipe",
		"bad connection",
		"driver: bad connection",
		"no connection",
		"unexpected eof",
		"recovery",
		"read-write",
		"read-only",
	}
	for _, pattern := range transientPatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	return false
}

// Retry executes fn with exponential backoff, retrying only on transient DB errors.
// Suitable for wrapping database operations that may fail during RDS failover.
func Retry[T any](fn func() (T, error)) (T, error) {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 500 * time.Millisecond
	bo.MaxInterval = 5 * time.Second
	bo.MaxElapsedTime = 50 * time.Second
	bo.Multiplier = 2

	var result T
	attempt := 0
	err := backoff.Retry(func() error {
		var err error
		result, err = fn()
		if err == nil {
			return nil
		}
		if IsTransientDBError(err) {
			attempt++
			logrus.WithError(err).WithField("attempt", attempt).Warn("transient database error, retrying")
			return err
		}
		return backoff.Permanent(err)
	}, bo)
	return result, err
}

// RetryVoid executes fn with exponential backoff for void operations.
func RetryVoid(fn func() error) error {
	_, err := Retry(func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}
