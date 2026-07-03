package google

import (
	"errors"
	"math/rand"
	"net/http"
	"sync/atomic"
	"time"

	"google.golang.org/api/googleapi"
)

const (
	gcpDestroyBatchPace = 200 * time.Millisecond
)

var (
	gcpPauseInitialDur = 5 * time.Second
	gcpPauseMaxDur     = 60 * time.Second

	gcpPausedUntil atomic.Int64
	gcpPauseLevel  atomic.Int32
)

func isRateLimited(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	if gerr.Code == http.StatusTooManyRequests {
		return true
	}
	for _, item := range gerr.Errors {
		switch item.Reason {
		case "rateLimitExceeded", "quotaExceeded", "userRateLimitExceeded":
			return true
		}
	}
	return false
}

func isGCPPaused() bool {
	until := gcpPausedUntil.Load()
	return until > 0 && until > time.Now().UnixNano()
}

// pauseGCPOn429 extends a process-wide pause so concurrent GCP callers back off together.
func pauseGCPOn429() {
	level := gcpPauseLevel.Add(1)
	if level > 4 {
		gcpPauseLevel.Store(4)
		level = 4
	}

	pause := gcpPauseInitialDur << (level - 1)
	if pause > gcpPauseMaxDur {
		pause = gcpPauseMaxDur
	}
	pause = addJitter(pause)

	until := time.Now().Add(pause).UnixNano()
	for {
		old := gcpPausedUntil.Load()
		if until <= old {
			return
		}
		if gcpPausedUntil.CompareAndSwap(old, until) {
			return
		}
	}
}

func waitIfGCPPaused() {
	for {
		until := gcpPausedUntil.Load()
		if until == 0 {
			return
		}
		remaining := time.Until(time.Unix(0, until))
		if remaining <= 0 {
			gcpPausedUntil.Store(0)
			gcpPauseLevel.Store(0)
			return
		}
		sleep := remaining
		if sleep > 500*time.Millisecond {
			sleep = 500 * time.Millisecond
		}
		time.Sleep(sleep)
	}
}

func addJitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	// ±25% jitter to avoid synchronized retries.
	jitter := time.Duration(rand.Int63n(int64(d/2))) - d/4 //nolint:mnd
	return d + jitter
}

func retrySleepForError(err error, defaultSecs int) time.Duration {
	if isRateLimited(err) {
		level := gcpPauseLevel.Load()
		if level <= 0 {
			level = 1
		}
		sleep := gcpPauseInitialDur << (level - 1)
		if sleep > gcpPauseMaxDur {
			sleep = gcpPauseMaxDur
		}
		return addJitter(sleep)
	}
	return time.Duration(defaultSecs) * time.Second
}

func resetGCPRateLimitStateForTest() {
	gcpPausedUntil.Store(0)
	gcpPauseLevel.Store(0)
}
