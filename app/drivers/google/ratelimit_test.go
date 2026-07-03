package google

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

func TestIsRateLimited(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "429", err: &googleapi.Error{Code: http.StatusTooManyRequests}, want: true},
		{
			name: "quota reason",
			err:  &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "quotaExceeded"}}},
			want: true,
		},
		{name: "500", err: &googleapi.Error{Code: 500}, want: false},
		{name: "404", err: &googleapi.Error{Code: 404}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRateLimited(tt.err); got != tt.want {
				t.Fatalf("isRateLimited() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPauseGCPOn429_ExtendsPauseWindow(t *testing.T) {
	resetGCPRateLimitStateForTest()
	oldInitial := gcpPauseInitialDur
	gcpPauseInitialDur = 50 * time.Millisecond
	defer func() {
		gcpPauseInitialDur = oldInitial
		resetGCPRateLimitStateForTest()
	}()

	pauseGCPOn429()
	firstUntil := gcpPausedUntil.Load()
	if firstUntil <= time.Now().UnixNano() {
		t.Fatal("expected active pause after first 429")
	}

	pauseGCPOn429()
	secondUntil := gcpPausedUntil.Load()
	if secondUntil <= firstUntil {
		t.Fatal("expected pause window to extend on repeated 429")
	}
}

func TestWaitIfGCPPaused_ClearsAfterExpiry(t *testing.T) {
	resetGCPRateLimitStateForTest()
	gcpPausedUntil.Store(time.Now().Add(30 * time.Millisecond).UnixNano())

	start := time.Now()
	waitIfGCPPaused()
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("expected to wait for pause expiry, only waited %v", elapsed)
	}
	if isGCPPaused() {
		t.Fatal("expected pause to clear after expiry")
	}
}

func TestRetry_UsesLongerBackoffOn429(t *testing.T) {
	resetGCPRateLimitStateForTest()
	oldInitial := gcpPauseInitialDur
	gcpPauseInitialDur = 80 * time.Millisecond
	defer func() {
		gcpPauseInitialDur = oldInitial
		resetGCPRateLimitStateForTest()
	}()

	var calls atomic.Int32
	start := time.Now()
	_, err := retry(context.Background(), 2, 1, func() (struct{}, error) {
		if calls.Add(1) == 1 {
			return struct{}{}, &googleapi.Error{Code: http.StatusTooManyRequests}
		}
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("retry returned error: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", calls.Load())
	}
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Fatalf("expected longer backoff on 429, only waited %v", elapsed)
	}
}

func TestRetry_StillUsesDefaultSleepOn5xx(t *testing.T) {
	resetGCPRateLimitStateForTest()
	oldInitial := gcpPauseInitialDur
	gcpPauseInitialDur = 500 * time.Millisecond
	defer func() {
		gcpPauseInitialDur = oldInitial
		resetGCPRateLimitStateForTest()
	}()

	var calls atomic.Int32
	start := time.Now()
	_, err := retry(context.Background(), 2, 1, func() (struct{}, error) {
		if calls.Add(1) == 1 {
			return struct{}{}, &googleapi.Error{Code: 503}
		}
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("retry returned error: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Fatalf("expected ~1s sleep for 5xx retry, got %v", elapsed)
	}
}

func TestRetry_DoesNotRetryNonRetryableError(t *testing.T) {
	resetGCPRateLimitStateForTest()
	var calls atomic.Int32
	_, err := retry(context.Background(), 3, 1, func() (struct{}, error) {
		calls.Add(1)
		return struct{}{}, errors.New("permanent failure")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected single attempt, got %d", calls.Load())
	}
}
