package drivers

// VM creation outcomes. Bounded set for runner_vm_creation_attempts_total /
// runner_vm_creation_duration_seconds. Lives in package drivers (not package metric) for the
// same import-cycle reason documented on MetricsRecorder in purger_metrics.go.
const (
	VMCreationOutcomeSuccess   = "success"
	VMCreationOutcomeError     = "error"
	VMCreationOutcomeCancelled = "cancelled" //nolint:misspell // mandated label spelling per the metrics spec, matches GCPOutcomeCancelled
)

// VM creation failure reasons. driver.Create() is shared across every driver (GCP, AWS, Azure,
// Nomad, Anka, ...), so this is intentionally a small, driver-agnostic set rather than the
// richer GCP-specific taxonomy used by runner_gcp_operations_total - most non-GCP drivers will
// only ever produce driver_error/timeout/unknown today, and stockout/quota_exceeded are
// detected on a best-effort basis from the error text (see classifyVMCreationError).
const (
	VMCreationReasonNone          = "none"
	VMCreationReasonStockout      = "stockout"
	VMCreationReasonQuotaExceeded = "quota_exceeded"
	VMCreationReasonTimeout       = "timeout"
	VMCreationReasonDriverError   = "driver_error"
	VMCreationReasonUnknown       = "unknown"
)

// VM usage termination reasons. Bounded set for runner_vm_usage_duration_seconds.
//
// Only VMTerminationReasonNormalCleanup and VMTerminationReasonPurgerStale are currently
// emitted:
//   - normal_cleanup: command/harness/destroy.go's handleDestroy, on every successful destroy.
//   - purger_stale: Manager.purgeStaleInstancesForPool (non-distributed), for busy instances
//     destroyed for exceeding maxAgeBusy. NOT emitted by DistributedManager.executeInstanceCleanup:
//     its claim query both overwrites instance_updated and reclaims the row in one
//     UPDATE...RETURNING statement, so the pre-claim "became inuse at" timestamp this metric
//     needs is neither preserved nor returned to Go. Revisit if a CTE-based
//     UPDATE...FROM (SELECT ... old value) ... RETURNING is ever added to preserve it.
//
// VMTerminationReasonSetupFailed and VMTerminationReasonExplicitDestroy are part of the bounded
// taxonomy but intentionally left unemitted for now (deferred pending clearer ownership of their
// destroy call sites - see the VM creation/usage metrics prompt).
const (
	VMTerminationReasonNormalCleanup   = "normal_cleanup"
	VMTerminationReasonPurgerStale     = "purger_stale"
	VMTerminationReasonSetupFailed     = "setup_failed"     //nolint:unused // reserved, not yet emitted
	VMTerminationReasonExplicitDestroy = "explicit_destroy" //nolint:unused // reserved, not yet emitted
)
