package harness

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
)

// TestResumeToReadyOutcome_Success covers the resume-to-ready path when StartInstance, the
// health check, and setup all succeed.
func TestResumeToReadyOutcome_Success(t *testing.T) {
	outcome := resumeToReadyOutcome(context.Background(), nil)
	assert.Equal(t, drivers.VMLifecycleOutcomeSuccess, outcome)
}

// TestResumeToReadyOutcome_CloudStartFailed covers a cloud-level resume failure - resume never
// got underway, so this is a resume-to-ready failure too.
func TestResumeToReadyOutcome_CloudStartFailed(t *testing.T) {
	err := errors.New("failed to start the instance up: cloud provider rejected resume")
	outcome := resumeToReadyOutcome(context.Background(), err)
	assert.Equal(t, drivers.VMLifecycleOutcomeError, outcome)
}

// TestResumeToReadyOutcome_HealthCheckTimeoutAfterSuccessfulResume covers the scenario called
// out explicitly in the metrics prompt: the cloud-level resume call succeeds, but the subsequent
// lite-engine health check times out. Since runner_vm_resume_to_ready_duration_seconds has no
// `reason` label, this is only distinguishable from any other post-resume failure by outcome, not
// by a dedicated "health_failed"/"timeout" value.
func TestResumeToReadyOutcome_HealthCheckTimeoutAfterSuccessfulResume(t *testing.T) {
	healthCheckErr := errors.New("failed to call lite-engine retry health: context deadline exceeded")
	outcome := resumeToReadyOutcome(context.Background(), healthCheckErr)
	assert.Equal(t, drivers.VMLifecycleOutcomeError, outcome)
}

// TestResumeToReadyOutcome_SetupFailedAfterSuccessfulResume covers a setup (RetrySetup) failure
// following a successful resume and health check.
func TestResumeToReadyOutcome_SetupFailedAfterSuccessfulResume(t *testing.T) {
	setupErr := errors.New("failed to call setup lite-engine: connection refused")
	outcome := resumeToReadyOutcome(context.Background(), setupErr)
	assert.Equal(t, drivers.VMLifecycleOutcomeError, outcome)
}

// TestResumeToReadyOutcome_ContextCancelled covers a caller-canceled request, which should be
// reported distinctly from a genuine failure.
func TestResumeToReadyOutcome_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome := resumeToReadyOutcome(ctx, errors.New("context canceled"))
	assert.Equal(t, drivers.VMLifecycleOutcomeCancelled, outcome)
}
