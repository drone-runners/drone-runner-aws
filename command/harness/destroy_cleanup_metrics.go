package harness

import (
	"context"
	"errors"
)

// CleanupResource is the bounded resource-type set for runner_cleanup_attempts_total/
// runner_cleanup_duration_seconds. firewall_rule is part of the taxonomy but intentionally never
// emitted: the egress/firewall cleanup code this was meant to cover (CleanupEgressPolicy +
// firewallStore.DeleteByStageID) was removed upstream in CI-23855 ("Deprecate old egress flow"),
// along with the firewall_rules table itself - there is no remaining call site to instrument.
const (
	CleanupResourceVM                  = "vm"
	CleanupResourceCapacityReservation = "capacity_reservation"
	CleanupResourceFirewallRule        = "firewall_rule" //nolint:unused // reserved, unemitted - see doc comment above
	CleanupResourceStageOwner          = "stage_owner"
)

// CleanupOutcome is the bounded outcome set shared by both cleanup metrics.
const (
	CleanupOutcomeSuccess = "success"
	CleanupOutcomeError   = "error"
)

// CleanupReason is the bounded failure-reason set shared across all cleanup resource types.
//
// CleanupReasonNotFound is part of the taxonomy but not currently distinguishable from
// CleanupReasonCloudCallFailed: none of poolManager.Destroy/DestroyCapacity expose a sentinel
// "already gone" error today, so an already-deleted cloud resource surfaces as a generic
// cloud_call_failed. Revisit if a driver adds a distinguishable not-found error.
const (
	CleanupReasonCloudCallFailed = "cloud_call_failed"
	CleanupReasonStoreError      = "store_error"
	CleanupReasonNotFound        = "not_found" //nolint:unused // reserved, not yet distinguishable - see doc comment above
	CleanupReasonTimeout         = "timeout"
	CleanupReasonCancelled       = "cancelled" //nolint:misspell // mandated label spelling per the metrics spec
	CleanupReasonUnknown         = "unknown"
)

// classifyCleanupErr classifies the outcome/reason of a single on-demand cleanup attempt.
// ctx cancellation/deadline-exceeded takes priority over fallbackReason because it's a more
// reliable root cause than whichever specific call happened to return the error, mirroring
// classifyPhaseOutcome's handling of the equivalent handleSetup case.
func classifyCleanupErr(ctx context.Context, err error, fallbackReason string) (outcome, reason string) {
	if err == nil {
		return CleanupOutcomeSuccess, ""
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		switch {
		case errors.Is(ctxErr, context.Canceled):
			return CleanupOutcomeError, CleanupReasonCancelled
		case errors.Is(ctxErr, context.DeadlineExceeded):
			return CleanupOutcomeError, CleanupReasonTimeout
		}
	}
	return CleanupOutcomeError, fallbackReason
}
