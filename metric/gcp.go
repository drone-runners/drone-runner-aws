package metric

import "github.com/prometheus/client_golang/prometheus"

// GCP API/operation outcome values. Bounded, shared across the raw API
// request layer and the logical operation layer.
const (
	GCPOutcomeSuccess   = "success"
	GCPOutcomeError     = "error"
	GCPOutcomeCancelled = "cancelled" //nolint:misspell // mandated label spelling per the metrics spec
)

// GCP API/operation reason values. Bounded taxonomy that GCP SDK errors are
// mapped into. Never populate these labels with a raw GCP error message or
// GCP's own error reason string.
const (
	GCPReasonNone             = "none"
	GCPReasonStockout         = "stockout"
	GCPReasonQuotaExceeded    = "quota_exceeded"
	GCPReasonRateLimited      = "rate_limited"
	GCPReasonPermissionDenied = "permission_denied"
	GCPReasonInvalidRequest   = "invalid_request"
	GCPReasonConflict         = "conflict"
	GCPReasonNotFound         = "not_found"
	GCPReasonAlreadyAbsent    = "already_absent"
	GCPReasonExpectedMiss     = "expected_miss"
	GCPReasonBackendError     = "backend_error"
	GCPReasonNetworkError     = "network_error"
	GCPReasonTimeout          = "timeout"
	GCPReasonCancelled        = "cancelled" //nolint:misspell // mandated label spelling per the metrics spec
	GCPReasonUnknown          = "unknown"
)

// GCP resource label values.
const (
	GCPResourceInstance        = "instance"
	GCPResourceReservation     = "reservation"
	GCPResourceDisk            = "disk"
	GCPResourceFirewall        = "firewall"
	GCPResourceZoneOperation   = "zone_operation"
	GCPResourceGlobalOperation = "global_operation"
	GCPResourceRegion          = "region"
)

// GCP operation label values.
const (
	GCPOperationInsert           = "insert"
	GCPOperationGet              = "get"
	GCPOperationDelete           = "delete"
	GCPOperationSuspend          = "suspend"
	GCPOperationResume           = "resume"
	GCPOperationSetLabels        = "set_labels"
	GCPOperationSetMetadata      = "set_metadata"
	GCPOperationSerialPortOutput = "serial_port_output"
	GCPOperationList             = "list"
)

// GCPAPIRequestsCount provides metrics for raw GCP Compute API HTTP requests,
// including retries and long-running-operation poll requests. This layer
// exists so SDK-level retries/polling don't distort the logical
// per-operation success rate reported by GCPOperationsCount.
func GCPAPIRequestsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_gcp_api_requests_total",
			Help: "Total number of raw GCP Compute API HTTP requests, including retries and operation-poll requests",
		},
		[]string{"resource", "operation", "outcome", "reason", "status_class", "zone"},
	)
}

// GCPAPIRequestDuration provides the latency of a single raw GCP Compute API
// HTTP request/attempt.
func GCPAPIRequestDuration() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_gcp_api_request_duration_seconds",
			Help:    "Latency of a single raw GCP Compute API HTTP request",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60},
		},
		[]string{"resource", "operation", "outcome", "zone"},
	)
}

// GCPOperationsCount provides metrics for logical GCP operations: from the
// moment Runner starts an action until retries and any GCP long-running
// operation reach a terminal result.
func GCPOperationsCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_gcp_operations_total",
			Help: "Total number of completed logical GCP operations (Runner-initiated actions, including their retries and long-running-operation polling)",
		},
		[]string{"resource", "operation", "outcome", "reason", "zone", "vm_type"},
	)
}

// GCPOperationDuration provides the end-to-end duration of a logical GCP
// operation, including retries and long-running-operation polling.
func GCPOperationDuration() *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "runner_gcp_operation_duration_seconds",
			Help:    "End-to-end duration of a logical GCP operation, including retries and long-running-operation polling",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 30, 60, 90, 120, 180, 300, 600},
		},
		[]string{"resource", "operation", "outcome", "zone", "vm_type"},
	)
}

// GCPOperationRetriesCount counts retry attempts made within a single
// logical GCP operation (e.g. transient 5xx/429 retries by the SDK-call
// wrapper).
func GCPOperationRetriesCount() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "runner_gcp_operation_retries_total",
			Help: "Total number of retry attempts made within logical GCP operations",
		},
		[]string{"resource", "operation", "reason", "zone"},
	)
}

// GCPOperationsInflight tracks the number of logical GCP operations
// currently in progress, so operations stuck beyond their expected duration
// remain visible as a non-decaying gauge rather than only showing up after
// they eventually terminate.
func GCPOperationsInflight() *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "runner_gcp_operations_inflight",
			Help: "Number of logical GCP operations currently in progress",
		},
		[]string{"resource", "operation", "zone"},
	)
}
