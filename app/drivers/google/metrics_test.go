package google

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/api/googleapi"

	"github.com/drone-runners/drone-runner-aws/metric"
)

// newTestMetrics builds a fresh, unregistered *metric.Metrics for use in
// tests. It intentionally avoids metric.RegisterMetrics(), which calls
// prometheus.MustRegister against the global registry and would panic if
// invoked more than once across the test suite.
func newTestMetrics() *metric.Metrics {
	return &metric.Metrics{
		GCPAPIRequestsCount:      metric.GCPAPIRequestsCount(),
		GCPAPIRequestDuration:    metric.GCPAPIRequestDuration(),
		GCPOperationsCount:       metric.GCPOperationsCount(),
		GCPOperationDuration:     metric.GCPOperationDuration(),
		GCPOperationRetriesCount: metric.GCPOperationRetriesCount(),
		GCPOperationsInflight:    metric.GCPOperationsInflight(),
	}
}

func TestClassifyGCPError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		opts       classifyOpts
		wantOut    string
		wantReason string
	}{
		{
			name:       "success",
			err:        nil,
			wantOut:    metric.GCPOutcomeSuccess,
			wantReason: metric.GCPReasonNone,
		},
		{
			name:       "404 on delete is success/already_absent",
			err:        &googleapi.Error{Code: 404, Message: "not found"},
			opts:       classifyOpts{isDelete: true},
			wantOut:    metric.GCPOutcomeSuccess,
			wantReason: metric.GCPReasonAlreadyAbsent,
		},
		{
			name:       "404 while scanning zones is success/expected_miss",
			err:        &googleapi.Error{Code: 404, Message: "not found"},
			opts:       classifyOpts{isZoneScan: true},
			wantOut:    metric.GCPOutcomeSuccess,
			wantReason: metric.GCPReasonExpectedMiss,
		},
		{
			name:       "404 without special-casing is a real not_found error",
			err:        &googleapi.Error{Code: 404, Message: "not found"},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonNotFound,
		},
		{
			name: "ZONE_RESOURCE_POOL_EXHAUSTED is stockout, never backend_error",
			err: &googleapi.Error{
				Code: 400,
				Errors: []googleapi.ErrorItem{
					{Reason: "ZONE_RESOURCE_POOL_EXHAUSTED", Message: "no capacity"},
				},
			},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonStockout,
		},
		{
			name:       "plain stockout message string",
			err:        errors.New("the zone does not have enough resources available to fulfill the request"),
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonStockout,
		},
		{
			name: "quota exceeded",
			err: &googleapi.Error{
				Code: 403,
				Errors: []googleapi.ErrorItem{
					{Reason: "quotaExceeded", Message: "Quota exceeded for quota metric"},
				},
			},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonQuotaExceeded,
		},
		{
			name:       "rate limited via 429",
			err:        &googleapi.Error{Code: 429, Message: "too many requests"},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonRateLimited,
		},
		{
			name: "rate limited via reason on non-429 code",
			err: &googleapi.Error{
				Code: 403,
				Errors: []googleapi.ErrorItem{
					{Reason: "rateLimitExceeded", Message: "rate limit exceeded"},
				},
			},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonRateLimited,
		},
		{
			name:       "permission denied",
			err:        &googleapi.Error{Code: 403, Message: "forbidden"},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonPermissionDenied,
		},
		{
			name:       "invalid request via 400",
			err:        &googleapi.Error{Code: 400, Message: "bad request"},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonInvalidRequest,
		},
		{
			name:       "conflict via 409",
			err:        &googleapi.Error{Code: 409, Message: "conflict"},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonConflict,
		},
		{
			name:       "timeout via 504",
			err:        &googleapi.Error{Code: 504, Message: "gateway timeout"},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonTimeout,
		},
		{
			name:       "context deadline exceeded is timeout",
			err:        context.DeadlineExceeded,
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonTimeout,
		},
		{
			name:       "generic 5xx is backend_error",
			err:        &googleapi.Error{Code: 503, Message: "server error"},
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonBackendError,
		},
		{
			name:       "context canceled",
			err:        context.Canceled,
			wantOut:    metric.GCPOutcomeCancelled,
			wantReason: metric.GCPReasonCancelled,
		},
		{
			name:       "unknown error falls back to unknown",
			err:        errors.New("something unexpected happened"),
			wantOut:    metric.GCPOutcomeError,
			wantReason: metric.GCPReasonUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotOut, gotReason := classifyGCPError(context.Background(), tt.err, tt.opts)
			if gotOut != tt.wantOut {
				t.Errorf("outcome = %q, want %q", gotOut, tt.wantOut)
			}
			if gotReason != tt.wantReason {
				t.Errorf("reason = %q, want %q", gotReason, tt.wantReason)
			}
		})
	}
}

func TestClassifyGCPError_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome, reason := classifyGCPError(ctx, ctx.Err(), classifyOpts{})
	if outcome != metric.GCPOutcomeCancelled {
		t.Errorf("outcome = %q, want %q", outcome, metric.GCPOutcomeCancelled)
	}
	if reason != metric.GCPReasonCancelled {
		t.Errorf("reason = %q, want %q", reason, metric.GCPReasonCancelled)
	}
}

func TestStatusClass(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "success", err: nil, want: "2xx"},
		{name: "4xx", err: &googleapi.Error{Code: 404}, want: "4xx"},
		{name: "5xx", err: &googleapi.Error{Code: 500}, want: "5xx"},
		{name: "non-googleapi error", err: errors.New("boom"), want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := statusClass(tt.err); got != tt.want {
				t.Errorf("statusClass() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPICall_RecordsRawRequestMetrics(t *testing.T) {
	m := newTestMetrics()

	_, err := apiCall(context.Background(), m, metric.GCPResourceInstance, metric.GCPOperationGet, "us-central1-a", classifyOpts{}, func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := testutil.ToFloat64(m.GCPAPIRequestsCount.WithLabelValues(
		metric.GCPResourceInstance, metric.GCPOperationGet, metric.GCPOutcomeSuccess, metric.GCPReasonNone, "2xx", "us-central1-a"))
	if got != 1 {
		t.Errorf("GCPAPIRequestsCount = %v, want 1", got)
	}
}

func TestAPICall_NilMetricsIsNoOp(t *testing.T) {
	result, err := apiCall[string](context.Background(), nil, metric.GCPResourceInstance, metric.GCPOperationGet, "zone", classifyOpts{}, func() (string, error) {
		return "value", nil
	})
	if err != nil || result != "value" {
		t.Fatalf("apiCall with nil metrics should still invoke call(): result=%q err=%v", result, err)
	}
}

// TestTrackOperation_InflightDecrementedOnError asserts that the inflight
// gauge is decremented (via defer) even when the wrapped operation errors,
// so a failing operation never leaves the gauge permanently elevated.
func TestTrackOperation_InflightDecrementedOnError(t *testing.T) {
	m := newTestMetrics()

	err := trackOperation(context.Background(), m, metric.GCPResourceInstance, metric.GCPOperationInsert, "us-central1-a", testMachineType, classifyOpts{}, func() error {
		if got := testutil.ToFloat64(m.GCPOperationsInflight.WithLabelValues(metric.GCPResourceInstance, metric.GCPOperationInsert, "us-central1-a")); got != 1 {
			t.Errorf("inflight while running = %v, want 1", got)
		}
		return errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error to be propagated")
	}

	got := testutil.ToFloat64(m.GCPOperationsInflight.WithLabelValues(metric.GCPResourceInstance, metric.GCPOperationInsert, "us-central1-a"))
	if got != 0 {
		t.Errorf("inflight after error = %v, want 0", got)
	}

	opCount := testutil.ToFloat64(m.GCPOperationsCount.WithLabelValues(
		metric.GCPResourceInstance, metric.GCPOperationInsert, metric.GCPOutcomeError, metric.GCPReasonUnknown, "us-central1-a", testMachineType))
	if opCount != 1 {
		t.Errorf("GCPOperationsCount(error) = %v, want 1", opCount)
	}
}

func TestTrackOperation_InflightDecrementedOnSuccess(t *testing.T) {
	m := newTestMetrics()

	err := trackOperation(context.Background(), m, metric.GCPResourceInstance, metric.GCPOperationDelete, "us-central1-a", "", classifyOpts{isDelete: true}, func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := testutil.ToFloat64(m.GCPOperationsInflight.WithLabelValues(metric.GCPResourceInstance, metric.GCPOperationDelete, "us-central1-a"))
	if got != 0 {
		t.Errorf("inflight after success = %v, want 0", got)
	}

	opCount := testutil.ToFloat64(m.GCPOperationsCount.WithLabelValues(
		metric.GCPResourceInstance, metric.GCPOperationDelete, metric.GCPOutcomeSuccess, metric.GCPReasonNone, "us-central1-a", ""))
	if opCount != 1 {
		t.Errorf("GCPOperationsCount(success) = %v, want 1", opCount)
	}
}

func TestRetry_RecordsRetriesAndSucceedsAfterTransientError(t *testing.T) {
	m := newTestMetrics()

	attempts := 0
	result, err := retry(context.Background(), m, metric.GCPResourceInstance, metric.GCPOperationGet, "us-central1-a", classifyOpts{}, 3, 0, func() (string, error) {
		attempts++
		if attempts < 2 {
			return "", &googleapi.Error{Code: 503, Message: "backend hiccup"}
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("result = %q, want ok", result)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}

	retries := testutil.ToFloat64(m.GCPOperationRetriesCount.WithLabelValues(
		metric.GCPResourceInstance, metric.GCPOperationGet, metric.GCPReasonBackendError, "us-central1-a"))
	if retries != 1 {
		t.Errorf("GCPOperationRetriesCount = %v, want 1", retries)
	}

	// Every attempt (including the failed one) should be recorded at the raw
	// request layer.
	successCount := testutil.ToFloat64(m.GCPAPIRequestsCount.WithLabelValues(
		metric.GCPResourceInstance, metric.GCPOperationGet, metric.GCPOutcomeSuccess, metric.GCPReasonNone, "2xx", "us-central1-a"))
	failCount := testutil.ToFloat64(m.GCPAPIRequestsCount.WithLabelValues(
		metric.GCPResourceInstance, metric.GCPOperationGet, metric.GCPOutcomeError, metric.GCPReasonBackendError, "5xx", "us-central1-a"))
	if successCount != 1 || failCount != 1 {
		t.Errorf("GCPAPIRequestsCount success=%v fail=%v, want 1 and 1", successCount, failCount)
	}
}

func TestRetry_DoesNotRetryNonRetryableError(t *testing.T) {
	m := newTestMetrics()

	attempts := 0
	_, err := retry(context.Background(), m, metric.GCPResourceInstance, metric.GCPOperationDelete, "us-central1-a", classifyOpts{isDelete: true}, 3, 0, func() (string, error) {
		attempts++
		return "", &googleapi.Error{Code: 404, Message: "not found"}
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (404 should not be retried)", attempts)
	}
}
