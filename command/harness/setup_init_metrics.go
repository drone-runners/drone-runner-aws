package harness

import (
	"context"
	"errors"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/types"
)

// InitSource distinguishes how the instance handed back to handleSetup was obtained, for the
// overall health/init metrics (runner_vm_health_check_*, runner_vm_setup_*, runner_vm_init_*).
// This is intentionally a different (smaller) taxonomy than app/drivers' VMCreationOutcome/
// VMSource-style constants used by runner_vm_creation_attempts_total: those describe how a VM
// was *created*, this describes how *this setup attempt* obtained an instance to work with.
const (
	InitSourceHotpool    = "hotpool"
	InitSourceHibernated = "hibernated"
	InitSourceReserved   = "reserved"
	InitSourceOnDemand   = "ondemand"
)

// InitOutcome is the bounded outcome set shared by all six overall health/init metrics.
const (
	InitOutcomeSuccess   = "success"
	InitOutcomeError     = "error"
	InitOutcomeCancelled = "cancelled" //nolint:misspell // mandated label spelling per the metrics spec
)

// InitReason is the bounded failure-reason set shared by all six overall health/init metrics.
//
// InitReasonCapacityUnavailable is part of the taxonomy but intentionally left unemitted: within
// handleSetup's scope, a reservedCapacity that can't be resolved to an instance is a silent
// fallback to on-demand creation (see DistributedManager.provisionFromReservedCapacity), not an
// error, so there is no failure signal to attach this reason to today. isCapacityTask (the flag
// that would route through driver.ReserveCapacity, where a real "capacity unavailable" failure
// can occur) is always false for Setup requests - that flow lives in command/harness/capacity.go
// and is out of scope here.
//
// InitReasonCancelled is intentionally not defined as a distinct value: cancellation is carried
// on the `outcome` label (InitOutcomeCancelled) with reason=none, mirroring the
// VMLifecycleOutcome/Reason convention used by the hibernate/resume metrics.
const (
	InitReasonNone                = "none"
	InitReasonCapacityUnavailable = "capacity_unavailable" //nolint:unused // reserved, not yet emitted - see doc comment above
	InitReasonPoolExhausted       = "pool_exhausted"
	InitReasonCloudCreateFailed   = "cloud_create_failed"
	InitReasonResumeFailed        = "resume_failed"
	InitReasonHealthFailed        = "health_failed"
	InitReasonSetupFailed         = "setup_failed"
	InitReasonTimeout             = "timeout"
	InitReasonStateFailed         = "state_failed"
	InitReasonUnknown             = "unknown"
)

// computeInitSource classifies how handleSetup obtained the instance it's working with, for the
// `source` label shared by the overall health/init metrics. hibernated takes priority over
// reserved when both apply (a capacity-reservation-claimed instance that also happens to be
// hibernated behaves very differently init-wise, and hibernate/resume already has its own
// dedicated metrics), and reserved takes priority over a plain hotpool claim since it's a more
// specific signal about how the instance was obtained.
func computeInitSource(reservedCapacity *types.CapacityReservation, warmed, hibernated bool) string {
	switch {
	case hibernated:
		return InitSourceHibernated
	case reservedCapacity != nil:
		return InitSourceReserved
	case warmed:
		return InitSourceHotpool
	default:
		return InitSourceOnDemand
	}
}

// classifyCtxOutcome checks ctx for cancellation/deadline-exceeded, independent of which
// handleSetup phase produced err. This is checked first by every classifier below because a
// canceled/timed-out context is a more reliable root cause than whichever specific call
// happened to return the error.
func classifyCtxOutcome(ctx context.Context) (outcome, reason string, matched bool) {
	if ctx.Err() == nil {
		return "", "", false
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return InitOutcomeCancelled, InitReasonNone, true
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return InitOutcomeError, InitReasonTimeout, true
	}
	return "", "", false
}

// classifyPhaseOutcome classifies the outcome/reason of a single handleSetup phase (health
// check, setup, or the aggregate init attempt), using fallbackReason when err is non-nil and the
// failure isn't attributable to ctx cancellation/timeout.
func classifyPhaseOutcome(ctx context.Context, err error, fallbackReason string) (outcome, reason string) {
	if err == nil {
		return InitOutcomeSuccess, InitReasonNone
	}
	if o, r, ok := classifyCtxOutcome(ctx); ok {
		return o, r
	}
	return InitOutcomeError, fallbackReason
}

// classifyProvisionReason maps a poolManager.Provision() failure to a bounded reason.
// ErrorNoInstanceAvailable is the only distinguishable sentinel Provision returns today (raised
// only by the non-distributed Manager when the pool is at max size with no free instance -
// DistributedManager has no equivalent pool-size-cap concept and always falls through to
// on-demand creation instead, so pool_exhausted is only ever observed for non-distributed
// pools). Everything else - cert generation, store list/update/create errors, driver.Create
// failures - is bucketed as cloud_create_failed, since Provision is treated as a single opaque
// call from handleSetup's perspective.
func classifyProvisionReason(err error) string {
	if errors.Is(err, drivers.ErrorNoInstanceAvailable) {
		return InitReasonPoolExhausted
	}
	return InitReasonCloudCreateFailed
}
