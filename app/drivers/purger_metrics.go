package drivers

import "time"

// Purger instance destroy reasons. Bounded set - do not add per-instance identifiers here.
// These live in package drivers (rather than package metric) because metric already imports
// drivers; drivers importing metric back would create an import cycle. metric.Metrics's
// Record* methods take these as plain strings, so this is the single source of truth.
const (
	PurgerReasonBusyMaxAge        = "busy_maxage"
	PurgerReasonFreeMaxAge        = "free_maxage"
	PurgerReasonStuckProvisioning = "stuck_provisioning"
	// PurgerReasonBusyMaxAgeTTLExtended and PurgerReasonStuckTerminating are part of the bounded
	// taxonomy but are not currently emitted by DistributedManager: its claim query ORs the
	// ttl-extended and stuck-terminating sub-conditions together with the plain busy_maxage
	// condition in a single SQL statement, so the specific sub-condition that matched a given
	// row isn't available at the point the metric is recorded. Revisit if finer-grained busy
	// reasons are needed - either by splitting the claim query per sub-condition or by returning
	// enough columns (instance_state, instance_updated, instance_labels) to reclassify in Go.
	PurgerReasonBusyMaxAgeTTLExtended = "busy_maxage_ttl_extended"
	PurgerReasonStuckTerminating      = "stuck_terminating"
)

// Purger capacity reservation destroy reasons.
const (
	PurgerCapacityReasonStuckTerminating = "stuck_terminating"
	PurgerCapacityReasonStuckCreated     = "stuck_created"
	PurgerCapacityReasonOrphanedInUse    = "orphaned_inuse"
)

// Purger destroy outcomes, shared by instance and capacity destroy attempts.
const (
	PurgerOutcomeDestroyed          = "destroyed"
	PurgerOutcomeFailedLeftForRetry = "failed_left_for_retry"
)

// Purger force-delete cleanup types.
const (
	PurgerCleanupTypeBusy = "busy"
	PurgerCleanupTypeFree = "free"
)

// MetricsRecorder records observability metrics for the background instance/capacity purger in
// the Manager/DistributedManager. This interface is defined here (rather than the
// Manager/DistributedManager simply holding a *metric.Metrics) because package metric already
// imports package drivers (for drivers.IManager); having drivers depend on metric directly would
// create an import cycle. *metric.Metrics implements this interface via the wrapper methods in
// metric/purger.go.
type MetricsRecorder interface {
	// RecordPurgerLastRun sets the last-run-timestamp gauge for a pool to the current time.
	RecordPurgerLastRun(poolID string)
	// RecordInstanceDestroyAttempt records the outcome of one instance destroy attempt.
	RecordInstanceDestroyAttempt(poolID, zone, reason, outcome string)
	// RecordInstancesForceDeleted records count leaked instance rows force-deleted for a pool.
	RecordInstancesForceDeleted(poolID, cleanupType string, count int)
	// RecordCapacityDestroyAttempt records the outcome of one capacity reservation destroy attempt.
	RecordCapacityDestroyAttempt(poolID, reason, outcome string)
	// RecordVMCreationAttempt records the outcome of one driver.Create() call (see vm_metrics.go
	// for the bounded outcome/reason values).
	RecordVMCreationAttempt(poolID, zone, vmType, source, outcome, reason string)
	// RecordVMCreationDuration observes how long one driver.Create() call took, from immediately
	// before invocation to immediately after it returns (success or error), before any
	// subsequent health/setup work.
	RecordVMCreationDuration(poolID, zone, vmType, source, outcome string, duration time.Duration)
	// RecordVMUsageDuration observes how long a VM was in the inuse state before being
	// terminated (see vm_metrics.go for the bounded termination_reason values).
	RecordVMUsageDuration(poolID, zone, vmType, source, terminationReason string, dwell time.Duration)
	// RecordVMHibernateAttempt records the outcome of one logical hibernate operation (the whole
	// hibernateOrStopWithRetries call, including any internal retries - see
	// vm_lifecycle_metrics.go for the bounded outcome/reason values).
	RecordVMHibernateAttempt(poolID, zone, vmType, outcome, reason string)
	// RecordVMHibernateDuration observes how long one logical hibernate operation took, including
	// any internal retry backoff sleeps.
	RecordVMHibernateDuration(poolID, zone, vmType, outcome string, duration time.Duration)
	// RecordVMResumeAttempt records the outcome of one logical resume operation (the cloud-level
	// start/resume call plus the subsequent state write that clears IsHibernated).
	RecordVMResumeAttempt(poolID, zone, vmType, outcome, reason string)
	// RecordVMResumeDuration observes how long just the cloud-level start/resume call took,
	// narrower than RecordVMResumeAttempt's scope - see vm_lifecycle_metrics.go.
	RecordVMResumeDuration(poolID, zone, vmType, outcome string, duration time.Duration)
	// RecordVMResumeToReadyDuration observes the customer-relevant latency from resume start to
	// the resumed VM being confirmed healthy and set up for a stage (includes the health-check
	// and setup phases, unlike RecordVMResumeDuration).
	RecordVMResumeToReadyDuration(poolID, zone, vmType, outcome string, duration time.Duration)
}
