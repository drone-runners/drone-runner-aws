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

// MetricsRecorder records observability metrics for background/lifecycle concerns in the
// Manager/DistributedManager (the instance/capacity purger, and hot-pool claim/dwell metrics -
// see hotpool_metrics.go). This interface is defined here (rather than the
// Manager/DistributedManager simply holding a *metric.Metrics) because package metric already
// imports package drivers (for drivers.IManager); having drivers depend on metric directly would
// create an import cycle. *metric.Metrics implements this interface via the wrapper methods in
// metric/purger.go and metric/hotpool.go.
type MetricsRecorder interface {
	// RecordPurgerLastRun sets the last-run-timestamp gauge for a pool to the current time.
	RecordPurgerLastRun(poolID string)
	// RecordInstanceDestroyAttempt records the outcome of one instance destroy attempt.
	RecordInstanceDestroyAttempt(poolID, zone, reason, outcome string)
	// RecordInstancesForceDeleted records count leaked instance rows force-deleted for a pool.
	RecordInstancesForceDeleted(poolID, cleanupType string, count int)
	// RecordCapacityDestroyAttempt records the outcome of one capacity reservation destroy attempt.
	RecordCapacityDestroyAttempt(poolID, reason, outcome string)
	// RecordHotpoolClaimAttempt records the outcome of one attempt to claim a warm instance from
	// a hot pool.
	RecordHotpoolClaimAttempt(poolID, zone, vmType, outcome, reason string)
	// RecordHotpoolStateDuration observes how long an instance dwelled in a hot-pool lifecycle
	// state before transitioning out of it.
	RecordHotpoolStateDuration(poolID, zone, vmType, state string, dwell time.Duration)
}
