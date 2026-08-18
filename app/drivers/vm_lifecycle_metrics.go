package drivers

import (
	"context"
	"errors"
)

// VM hibernate/resume outcomes. Bounded set for runner_vm_hibernate_attempts_total /
// runner_vm_hibernate_duration_seconds / runner_vm_resume_attempts_total /
// runner_vm_resume_duration_seconds / runner_vm_resume_to_ready_duration_seconds. Lives in
// package drivers (not package metric) for the same import-cycle reason documented on
// MetricsRecorder in purger_metrics.go.
const (
	VMLifecycleOutcomeSuccess   = "success"
	VMLifecycleOutcomeError     = "error"
	VMLifecycleOutcomeCancelled = "cancelled" //nolint:misspell // mandated label spelling per the metrics spec, matches GCPOutcomeCancelled/VMCreationOutcomeCancelled
)

// VM hibernate/resume failure reasons.
//
// VMLifecycleReasonHealthFailed and VMLifecycleReasonSetupFailed are part of the bounded
// taxonomy but are not currently emitted as a label value anywhere: they can only occur after a
// successful cloud-level resume (during the health-check/setup phase in
// command/harness/setup.go's handleSetup), and the only metric covering that phase -
// runner_vm_resume_to_ready_duration_seconds - has no `reason` label (outcome only, per the
// metrics spec), so there's currently no series that would carry these values. Revisit if
// resume-to-ready ever grows a reason label.
const (
	VMLifecycleReasonNone            = "none"
	VMLifecycleReasonCloudCallFailed = "cloud_call_failed"
	VMLifecycleReasonStateFailed     = "state_failed"
	VMLifecycleReasonHealthFailed    = "health_failed" //nolint:unused // reserved, not yet emitted (see comment above)
	VMLifecycleReasonSetupFailed     = "setup_failed"  //nolint:unused // reserved, not yet emitted (see comment above)
	VMLifecycleReasonTimeout         = "timeout"
	VMLifecycleReasonUnknown         = "unknown"
)

// lifecycleStage distinguishes which phase of a hibernate/resume operation produced an error, so
// classifyLifecycleError can map it to a bounded metric reason without string-matching internal
// error text.
type lifecycleStage string

const (
	lifecycleStageCloud lifecycleStage = "cloud"
	lifecycleStageState lifecycleStage = "state"
)

// lifecycleStageError tags an internal hibernate/resume error with which stage produced it
// (the cloud-level driver call, or a DB state read/write around it).
type lifecycleStageError struct {
	stage lifecycleStage
	err   error
}

func (e *lifecycleStageError) Error() string { return e.err.Error() }
func (e *lifecycleStageError) Unwrap() error { return e.err }

// classifyLifecycleError maps a hibernate/resume error into the bounded outcome/reason set for
// runner_vm_hibernate_attempts_total / runner_vm_resume_attempts_total. Never puts the raw error
// text into a label - only used here to pick a value from the bounded set above.
func classifyLifecycleError(ctx context.Context, err error) (outcome, reason string) {
	if err == nil {
		return VMLifecycleOutcomeSuccess, VMLifecycleReasonNone
	}
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		return VMLifecycleOutcomeCancelled, VMLifecycleReasonNone
	}
	if (ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) || errors.Is(err, context.DeadlineExceeded) {
		return VMLifecycleOutcomeError, VMLifecycleReasonTimeout
	}
	var stageErr *lifecycleStageError
	if errors.As(err, &stageErr) {
		if stageErr.stage == lifecycleStageCloud {
			return VMLifecycleOutcomeError, VMLifecycleReasonCloudCallFailed
		}
		return VMLifecycleOutcomeError, VMLifecycleReasonStateFailed
	}
	return VMLifecycleOutcomeError, VMLifecycleReasonUnknown
}
