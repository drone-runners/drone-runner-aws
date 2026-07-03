package google

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drone-runners/drone-runner-aws/types"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

func TestGetZone_TrustsStoredZone(t *testing.T) {
	p := &config{projectID: "test-project"}
	zone, err := p.getZone(context.Background(), &types.Instance{
		ID:   "123",
		Zone: "us-central1-a",
	})
	if err != nil {
		t.Fatalf("getZone returned error: %v", err)
	}
	if zone != "us-central1-a" {
		t.Fatalf("getZone = %q, want us-central1-a", zone)
	}
}

func TestFindInstanceZone_StopsOnRateLimit(t *testing.T) {
	resetGCPRateLimitStateForTest()
	oldInitial := gcpPauseInitialDur
	gcpPauseInitialDur = 10 * time.Millisecond
	defer func() {
		gcpPauseInitialDur = oldInitial
		resetGCPRateLimitStateForTest()
	}()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Rate limit exceeded","errors":[{"reason":"rateLimitExceeded"}]}}`))
	}))
	defer srv.Close()

	service, err := compute.NewService(
		context.Background(),
		option.WithoutAuthentication(),
		option.WithEndpoint(srv.URL),
	)
	if err != nil {
		t.Fatalf("compute.NewService: %v", err)
	}

	p := &config{
		projectID: "test-project",
		zones:     []string{"us-central1-a", "us-central1-b", "us-central1-c"},
		service:   service,
	}

	_, err = p.findInstanceZone(context.Background(), "instance-1")
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	// getInstance retries 429 up to getRetries times in the first zone, then zone walk stops.
	if calls.Load() != int32(getRetries) {
		t.Fatalf("expected zone walk to stop after first zone (%d API calls), got %d", getRetries, calls.Load())
	}
}

func TestDestroyInstanceAndStorage_PacesWhenPaused(t *testing.T) {
	resetGCPRateLimitStateForTest()
	gcpPausedUntil.Store(time.Now().Add(250 * time.Millisecond).UnixNano())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	service, err := compute.NewService(
		context.Background(),
		option.WithoutAuthentication(),
		option.WithEndpoint(srv.URL),
	)
	if err != nil {
		t.Fatalf("compute.NewService: %v", err)
	}

	p := &config{projectID: "test-project", service: service}
	instances := []*types.Instance{
		{ID: "vm-1", Zone: "us-central1-a"},
		{ID: "vm-2", Zone: "us-central1-a"},
	}

	start := time.Now()
	_, err = p.DestroyInstanceAndStorage(context.Background(), instances, nil)
	elapsed := time.Since(start)

	// Not found is treated as success for destroy; pacing should add delay between instances.
	if elapsed < 150*time.Millisecond {
		t.Fatalf("expected pacing between batched deletes while paused, only took %v", elapsed)
	}
	if err != nil {
		t.Fatalf("DestroyInstanceAndStorage returned error: %v", err)
	}
}
