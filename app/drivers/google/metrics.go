package google

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/drone-runners/drone-runner-aws/metric"

	"google.golang.org/api/googleapi"
)

// classifyOpts tunes error classification for call sites whose semantics
// change the meaning of an HTTP 404 response.
type classifyOpts struct {
	// isDelete marks the call as a delete request, where a 404 means the
	// resource is already gone -- a success, not a failure.
	isDelete bool
	// isZoneScan marks the call as a per-zone existence probe (e.g. scanning
	// every configured zone for a reservation/instance), where a 404 is an
	// expected part of the scan and must not increment any failure-count-based
	// alert.
	isZoneScan bool
}

// quotaMarkers identifies GCP error reasons/messages that indicate a quota
// problem as opposed to a generic permission or rate-limit issue.
var quotaMarkers = []string{
	"quotaexceeded",
	"quota_exceeded",
	"resource_quota_exceeded",
	"quota exceeded",
}

// rateLimitMarkers identifies GCP error reasons that indicate rate limiting
// even when surfaced via an HTTP code other than 429.
var rateLimitMarkers = []string{
	"ratelimitexceeded",
	"userratelimitexceeded",
}

// Coarse HTTP status classes used on the raw-request metric so the exact
// status code (unbounded) never ends up in a label.
const (
	statusClass2xx     = "2xx"
	statusClass4xx     = "4xx"
	statusClass5xx     = "5xx"
	statusClassUnknown = "unknown"
)

// networkErrorMarkers is a best-effort set of substrings found in low-level
// transport errors that are not otherwise classifiable via typed errors.
var networkErrorMarkers = []string{
	"connection refused",
	"connection reset",
	"no such host",
	"broken pipe",
	"eof",
	"network is unreachable",
}

// classifyGCPError maps a GCP SDK error (or nil, for success) into the
// bounded outcome/reason taxonomy shared by the raw-request and
// logical-operation metric layers. It never returns the raw GCP error
// message or GCP's own error reason string as part of the result -- only
// values from the fixed enum below are returned, so labels stay bounded.
//
//nolint:gocyclo
func classifyGCPError(ctx context.Context, err error, opts classifyOpts) (outcome, reason string) {
	if err == nil {
		return metric.GCPOutcomeSuccess, metric.GCPReasonNone
	}

	// Context cancellation takes priority: it is neither a real success nor an
	// actionable service failure.
	if ctx != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return metric.GCPOutcomeCancelled, metric.GCPReasonCancelled
	}
	if errors.Is(err, context.Canceled) {
		return metric.GCPOutcomeCancelled, metric.GCPReasonCancelled
	}

	// Stockout/capacity errors must never be classified as a generic 5xx or
	// backend_error, regardless of the HTTP status GCP wraps them in.
	if isStockoutError(err) {
		return metric.GCPOutcomeError, metric.GCPReasonStockout
	}

	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		if hasReasonOrMessage(gerr, rateLimitMarkers...) {
			return metric.GCPOutcomeError, metric.GCPReasonRateLimited
		}
		if hasReasonOrMessage(gerr, quotaMarkers...) {
			return metric.GCPOutcomeError, metric.GCPReasonQuotaExceeded
		}
		switch gerr.Code {
		case http.StatusNotFound:
			switch {
			case opts.isDelete:
				return metric.GCPOutcomeSuccess, metric.GCPReasonAlreadyAbsent
			case opts.isZoneScan:
				return metric.GCPOutcomeSuccess, metric.GCPReasonExpectedMiss
			default:
				return metric.GCPOutcomeError, metric.GCPReasonNotFound
			}
		case http.StatusTooManyRequests:
			return metric.GCPOutcomeError, metric.GCPReasonRateLimited
		case http.StatusForbidden:
			return metric.GCPOutcomeError, metric.GCPReasonPermissionDenied
		case http.StatusConflict:
			return metric.GCPOutcomeError, metric.GCPReasonConflict
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			return metric.GCPOutcomeError, metric.GCPReasonInvalidRequest
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return metric.GCPOutcomeError, metric.GCPReasonTimeout
		}
		switch {
		case gerr.Code >= 500 && gerr.Code < 600:
			return metric.GCPOutcomeError, metric.GCPReasonBackendError
		case gerr.Code >= 400 && gerr.Code < 500:
			return metric.GCPOutcomeError, metric.GCPReasonInvalidRequest
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return metric.GCPOutcomeError, metric.GCPReasonTimeout
	}

	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return metric.GCPOutcomeError, metric.GCPReasonTimeout
	}

	var netOpErr *net.OpError
	var urlErr *url.Error
	if errors.As(err, &netOpErr) || errors.As(err, &urlErr) {
		return metric.GCPOutcomeError, metric.GCPReasonNetworkError
	}
	if containsAny(strings.ToLower(err.Error()), networkErrorMarkers) {
		return metric.GCPOutcomeError, metric.GCPReasonNetworkError
	}

	return metric.GCPOutcomeError, metric.GCPReasonUnknown
}

// hasReasonOrMessage reports whether any of the googleapi.Error's nested
// error reasons/messages (or its top-level message) contain one of the given
// markers, case-insensitively. Used only to select a bounded taxonomy value;
// the raw text itself is never placed in a metric label.
func hasReasonOrMessage(gerr *googleapi.Error, markers ...string) bool {
	candidates := []string{strings.ToLower(gerr.Message)}
	for _, e := range gerr.Errors {
		candidates = append(candidates, strings.ToLower(e.Reason), strings.ToLower(e.Message))
	}
	for _, c := range candidates {
		if containsAny(c, markers) {
			return true
		}
	}
	return false
}

func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// statusClass buckets an error into a coarse HTTP status class for the
// raw-request metric, so the exact status code (which is not bounded) never
// ends up in a label.
func statusClass(err error) string {
	if err == nil {
		return statusClass2xx
	}
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		switch {
		case gerr.Code >= 200 && gerr.Code < 300:
			return statusClass2xx
		case gerr.Code >= 400 && gerr.Code < 500:
			return statusClass4xx
		case gerr.Code >= 500 && gerr.Code < 600:
			return statusClass5xx
		}
	}
	return statusClassUnknown
}

// apiCall wraps a single raw GCP Compute API HTTP attempt (a `.Do()` call),
// recording per-attempt outcome/latency. Every retry and long-running
// operation poll request goes through this so that SDK-level retries/polling
// don't distort the logical per-operation success rate.
func apiCall[T any](ctx context.Context, m *metric.Metrics, resource, operation, zone string, opts classifyOpts, call func() (T, error)) (T, error) {
	start := time.Now()
	result, err := call()
	if m != nil {
		outcome, reason := classifyGCPError(ctx, err, opts)
		m.GCPAPIRequestsCount.WithLabelValues(resource, operation, outcome, reason, statusClass(err), zone).Inc()
		m.GCPAPIRequestDuration.WithLabelValues(resource, operation, outcome, zone).Observe(time.Since(start).Seconds())
	}
	return result, err
}

// trackOperation instruments a logical GCP operation: an action Runner
// starts that may retry and/or poll a GCP long-running operation until a
// terminal result. The inflight gauge is incremented before f runs and
// decremented via defer so it reflects reality even when f panics or errors,
// making stuck operations visible instead of leaking a stale count.
func trackOperation(ctx context.Context, m *metric.Metrics, resource, operation, zone, vmType string, opts classifyOpts, f func() error) error {
	if m == nil {
		return f()
	}
	m.GCPOperationsInflight.WithLabelValues(resource, operation, zone).Inc()
	start := time.Now()
	var err error
	defer func() {
		m.GCPOperationsInflight.WithLabelValues(resource, operation, zone).Dec()
		outcome, reason := classifyGCPError(ctx, err, opts)
		m.GCPOperationsCount.WithLabelValues(resource, operation, outcome, reason, zone, vmType).Inc()
		m.GCPOperationDuration.WithLabelValues(resource, operation, outcome, zone, vmType).Observe(time.Since(start).Seconds())
	}()
	err = f()
	return err
}

// recordRetry increments the retry counter for a logical operation. reason
// should reflect the error that triggered the retry.
func recordRetry(m *metric.Metrics, resource, operation, zone string, err error) {
	if m == nil {
		return
	}
	_, reason := classifyGCPError(context.Background(), err, classifyOpts{})
	m.GCPOperationRetriesCount.WithLabelValues(resource, operation, reason, zone).Inc()
}
