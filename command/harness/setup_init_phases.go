package harness

import (
	"context"
	"fmt"
	"time"

	"github.com/harness/lite-engine/api"
	lehttp "github.com/harness/lite-engine/cli/client"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/types"
)

// tagAndConnectInstance stamps the instance with the current stage, pushes tags to the driver,
// and constructs the lite-engine client - the local/state-preparation work in handleSetup that
// sits between a successful Provision/resume and the health-check phase. All three failures are
// bucketed under InitReasonStateFailed (see setup_init_metrics.go's InitReason doc comment):
// none of them are cloud-VM-creation, health-check, or lite-engine-setup failures.
func tagAndConnectInstance(
	ctx context.Context,
	poolManager drivers.IManager,
	instance *types.Instance,
	pool, stageRuntimeID string,
	tags map[string]string,
	enableMock bool,
	mockTimeout int,
) (lehttp.Client, error) {
	instance.Stage = stageRuntimeID
	instance.Updated = time.Now().Unix()
	if err := poolManager.Update(ctx, instance); err != nil {
		return nil, fmt.Errorf("failed to tag: %w", err)
	}
	if err := poolManager.SetInstanceTags(ctx, pool, instance, tags); err != nil {
		return nil, fmt.Errorf("failed to add tags to the instance: %w", err)
	}
	client, err := lehelper.GetClient(instance, poolManager.GetTLSServerName(), instance.Port, enableMock, mockTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to create LE client: %w", err)
	}
	return client, nil
}

// runHealthCheckPhase runs the lite-engine health check and instruments
// runner_vm_health_check_attempts_total/_duration_seconds around it, isolated from the
// surrounding provision/tag/setup work in handleSetup.
func runHealthCheckPhase(
	ctx context.Context,
	client lehttp.Client,
	req *api.HealthRequest,
	metrics *metric.Metrics,
	pool, zone, vmType, source string,
) error {
	start := time.Now()
	_, err := client.RetryHealth(ctx, req)
	outcome, reason := classifyPhaseOutcome(ctx, err, InitReasonHealthFailed)
	metrics.RecordVMHealthCheckAttempt(pool, zone, vmType, source, outcome, reason)
	metrics.RecordVMHealthCheckDuration(pool, zone, vmType, source, outcome, time.Since(start))
	return err
}

// runSetupPhase runs the lite-engine setup call and instruments
// runner_vm_setup_attempts_total/_duration_seconds around it, isolated from the health-check
// phase that precedes it in handleSetup.
func runSetupPhase(
	ctx context.Context,
	client lehttp.Client,
	req *api.SetupRequest,
	timeout time.Duration,
	metrics *metric.Metrics,
	pool, zone, vmType, source string,
) error {
	return runInstrumentedSetupPhase(ctx, func() error {
		_, err := client.RetrySetup(ctx, req, timeout)
		return err
	}, metrics, pool, zone, vmType, source)
}

func runInstrumentedSetupPhase(
	ctx context.Context,
	setup func() error,
	metrics *metric.Metrics,
	pool, zone, vmType, source string,
) error {
	start := time.Now()
	err := setup()
	outcome, reason := classifyPhaseOutcome(ctx, err, InitReasonSetupFailed)
	metrics.RecordVMSetupAttempt(pool, zone, vmType, source, outcome, reason)
	metrics.RecordVMSetupDuration(pool, zone, vmType, source, outcome, time.Since(start))
	return err
}
