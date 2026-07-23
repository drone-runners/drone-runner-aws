package drivers

// Hot-pool claim outcomes. Bounded set - see MetricsRecorder.RecordHotpoolClaimAttempt in
// purger_metrics.go. These live in package drivers (not package metric) for the same
// import-cycle reason documented on MetricsRecorder.
const (
	// HotpoolClaimOutcomeClaimed means a free/hibernated instance was successfully claimed from
	// the hot pool.
	HotpoolClaimOutcomeClaimed = "claimed"
	// HotpoolClaimOutcomeNoReadyCapacity means the pool (across every variant tried) had no
	// free/hibernated instance available to claim.
	HotpoolClaimOutcomeNoReadyCapacity = "no_ready_capacity"
	// HotpoolClaimOutcomeClaimFailed means an instance was found but the claim attempt itself
	// failed (a store error either finding/claiming or tagging the instance).
	HotpoolClaimOutcomeClaimFailed = "claim_failed"
)

// Hot-pool claim failure reasons. race_lost is intentionally not part of this taxonomy:
// InstanceStore.FindAndClaim is a single atomic DB transaction, so there is no way to observe
// "another caller claimed this specific row first" as distinct from "no rows were available"
// (both surface as sql.ErrNoRows -> HotpoolClaimOutcomeNoReadyCapacity). The one place a real
// race could occur - the post-claim InstanceStore.Update() call that tags the new owner racing
// against a concurrent purger/hibernator mutation - can't be distinguished either, because
// Update() is an unconditional `UPDATE ... WHERE instance_id = :id` with no rows-affected check,
// so it returns a nil error even when 0 rows were touched. Detecting that would require changing
// Update()'s signature/semantics, which has ~10 call sites across this package and is out of
// scope for hot-pool metrics. Revisit if Update() is ever changed to surface a rows-affected
// result.
const (
	HotpoolClaimReasonNone       = "none"
	HotpoolClaimReasonStoreError = "store_error"
)

// Hot-pool instance lifecycle states, shared by runner_hotpool_instances_current (gauge) and
// runner_hotpool_state_duration_seconds (histogram).
const (
	HotpoolStateReady        = "ready"
	HotpoolStateBusy         = "busy"
	HotpoolStateProvisioning = "provisioning"
	HotpoolStateHibernated   = "hibernated"
	HotpoolStateHibernating  = "hibernating"
)
