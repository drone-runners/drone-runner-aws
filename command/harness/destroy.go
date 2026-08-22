package harness

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/harness/lite-engine/api"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/lehelper"
	"github.com/drone-runners/drone-runner-aws/app/oshelp"
	ierrors "github.com/drone-runners/drone-runner-aws/app/types"
	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"

	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	destroyTimeout           = 10 * time.Minute
	liteEngineDestroyTimeout = 30 * time.Second
)

type VMCleanupRequest struct {
	PoolID             string              `json:"pool_id"`
	StageRuntimeID     string              `json:"stage_runtime_id"`
	LogKey             string              `json:"log_key,omitempty"`
	Distributed        bool                `json:"distributed,omitempty"`
	Context            Context             `json:"context,omitempty"`
	StorageCleanupType storage.CleanupType `json:"storage_cleanup_type,omitempty"`
	InstanceInfo       common.InstanceInfo `json:"instance_info,omitempty"`
}

func HandleDestroy(
	ctx context.Context,
	r *VMCleanupRequest,
	s store.StageOwnerStore,
	crs store.CapacityReservationStore,
	enableMock bool, // only used for scale testing
	mockTimeout int, // only used for scale testing
	poolManager drivers.IManager,
	metrics *metric.Metrics,
) error {
	if r.StageRuntimeID == "" {
		return ierrors.NewBadRequestError("mandatory field 'stage_runtime_id' in the request body is empty")
	}
	logr := logrus.
		WithField("stage_runtime_id", r.StageRuntimeID).
		WithField("api", "dlite:destroy").
		WithField("task_id", r.Context.TaskID)
	// We do retries on destroy in case a destroy call comes while an initialize call is still happening.
	cnt := 0
	var lastErr error
	b := createBackoff(destroyTimeout)
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		duration := b.NextBackOff()
		// drain the timer
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(duration) // Reset the timer with the new duration

		select {
		case <-ctx.Done():
			// drain the timer
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
			_, err := handleDestroy(ctx, r, s, crs, enableMock, mockTimeout, poolManager, metrics, cnt, logr)
			if err != nil {
				if lastErr == nil || (lastErr.Error() != err.Error()) {
					logr.WithError(err).Errorln("could not destroy VM")
					lastErr = err
				}
				if duration == backoff.Stop {
					return err
				}
				cnt++
				continue
			}
			return nil
		}
	}
}

func handleDestroy(ctx context.Context, r *VMCleanupRequest, s store.StageOwnerStore, crs store.CapacityReservationStore, enableMock bool, mockTimeout int,
	poolManager drivers.IManager, metrics *metric.Metrics, retryCount int, logr *logrus.Entry) (*types.Instance, error) {
	logr = logr.WithField("retry_count", retryCount)

	// poolID is declared here (rather than at its first assignment below) so the deferred
	// capacity-reservation cleanup closure below can read its final value for metric labeling -
	// defer captures by reference, but the variable must already be in scope at the point the
	// closure literal is written.
	var poolID string

	// Declare capacity variable early and defer its destruction to ensure cleanup happens regardless of errors
	defer func() {
		var capacity *types.CapacityReservation
		var err error
		if crs != nil {
			capacity, err = crs.Find(ctx, r.StageRuntimeID)
			if err == nil {
				logr.WithField("destroy_caller", "destroy_handler:deferred_capacity_cleanup").
					WithField("reservation_id", capacity.ReservationID).
					Infoln("destroy_capacity: deferred capacity reservation cleanup")
				capDestroyStart := time.Now()
				destroyCapErr := poolManager.DestroyCapacity(ctx, capacity)
				if destroyCapErr != nil {
					logr.WithError(destroyCapErr).Errorln("failed to destroy capacity reservation")
				}
				capOutcome, capReason := classifyCleanupErr(ctx, destroyCapErr, CleanupReasonCloudCallFailed)
				metrics.RecordCleanupAttempt(CleanupResourceCapacityReservation, poolID, capacity.GetZone(), capOutcome, capReason)
				metrics.RecordCleanupDuration(CleanupResourceCapacityReservation, poolID, capacity.GetZone(), capOutcome, time.Since(capDestroyStart))
			}
			// crs.Find returning an error (e.g. no reservation exists for this stage) means there
			// is nothing to clean up - intentionally not recorded as a cleanup attempt.
		}
	}()

	var stageOwner *types.StageOwner
	if r.InstanceInfo.PoolName == "" {
		entity, err := s.Find(ctx, r.StageRuntimeID)
		if err != nil || entity == nil {
			return nil, errors.Wrap(err, fmt.Sprintf("failed to find stage owner entity for stage: %s", r.StageRuntimeID))
		}
		stageOwner = entity
		poolID = entity.PoolName
	} else {
		poolID = r.InstanceInfo.PoolName
	}

	logr = logr.WithField("pool_id", poolID)

	logr = AddContext(logr, &r.Context, map[string]string{})

	logr.WithField("destroy_caller", "destroy_handler:api_request").
		Infoln("starting the destroy process")

	inst, alreadyRemoved, err := resolveDestroyInstance(ctx, r, stageOwner, poolID, poolManager, logr)
	if err != nil {
		return nil, err
	}
	if alreadyRemoved {
		logr.Infoln("instance was already removed; completing stage owner cleanup")
		envState().Delete(r.StageRuntimeID)
		if deleteErr := s.Delete(ctx, r.StageRuntimeID); deleteErr != nil {
			return nil, fmt.Errorf("failed to delete stage owner entity: %w", deleteErr)
		}
		return nil, nil
	}

	logr = logr.
		WithField("instance_id", inst.ID).
		WithField("instance_name", inst.Name)

	// usageStartUnix is a best-effort proxy for "became inuse at": there's no dedicated column
	// for that, so this reads inst.Updated before it gets overwritten below. It's only reliable
	// when nothing else wrote to this row between the inuse claim and now; if inst came from the
	// caller's request payload rather than a fresh DB fetch (see the ValidateStructForKeys branch
	// above), Updated will be zero and usage duration is simply not recorded for that call.
	// Known limitation: for instances claimed from the non-distributed Manager's warm pool,
	// inst.Updated is not refreshed at claim time (see provisionFromPool in
	// app/drivers/provisioner.go), so this proxy reflects "last touched at" rather than "became
	// inuse at" and can understate/overstate usage duration by however long the instance sat
	// idle in the pool before being claimed.
	usageStartUnix := inst.Updated

	// Update instance state to terminating and update timestamp
	inst.State = types.StateTerminating
	inst.Updated = time.Now().Unix()
	if updateErr := poolManager.Update(ctx, inst); updateErr != nil {
		logr.WithError(updateErr).Warnln("failed to update instance state to terminating")
	}

	logr.Traceln("invoking lite engine cleanup")
	client, err := lehelper.GetClient(inst, poolManager.GetTLSServerName(), inst.Port, enableMock, mockTimeout)
	if err != nil {
		logr.WithError(err).Errorln("could not create lite engine client for invoking cleanup")
	} else {
		// Attempting to call lite engine destroy with timeout controlled at caller level
		leCtx, leCancel := context.WithTimeout(ctx, liteEngineDestroyTimeout)
		defer leCancel()
		resp, destroyErr := client.Destroy(leCtx,
			&api.DestroyRequest{LogDrone: false, LogKey: r.LogKey, LiteEnginePath: oshelp.GetLiteEngineLogsPath(inst.OS), StageRuntimeID: r.StageRuntimeID})
		if destroyErr != nil {
			// we can continue even if lite engine destroy does not happen successfully. This is because
			// the VM is anyways destroyed so the process will be killed
			logr.WithError(destroyErr).Errorln("could not invoke lite engine cleanup")
		}
		if resp != nil && resp.OSStats != nil {
			var cpuGe50, cpuGe70, cpuGe90, memGe50, memGe70, memGe90 bool
			if resp.OSStats.MaxCPUUsagePct >= 50.0 { //nolint:mnd
				cpuGe50 = true
				if resp.OSStats.MaxCPUUsagePct >= 70.0 { //nolint:mnd
					cpuGe70 = true
					if resp.OSStats.MaxCPUUsagePct >= 90.0 { //nolint:mnd
						cpuGe90 = true
					}
				}
			}
			if resp.OSStats.MaxMemUsagePct >= 50.0 { //nolint:mnd
				memGe50 = true
				if resp.OSStats.MaxMemUsagePct >= 70.0 { //nolint:mnd
					memGe70 = true
					if resp.OSStats.MaxMemUsagePct >= 90.0 { //nolint:mnd
						memGe90 = true
					}
				}
			}

			metrics.CPUPercentile.WithLabelValues(poolID, inst.OS, inst.Arch, string(inst.Provider), strconv.FormatBool(poolManager.IsDistributed()), inst.VariantID).Observe(resp.OSStats.MaxCPUUsagePct)
			metrics.MemoryPercentile.WithLabelValues(poolID, inst.OS, inst.Arch, string(inst.Provider), strconv.FormatBool(poolManager.IsDistributed()), inst.VariantID).Observe(resp.OSStats.MaxMemUsagePct)

			logr.WithField("cpu_ge50", cpuGe50).WithField("cpu_ge70", cpuGe70).WithField("cpu_ge90", cpuGe90).
				WithField("mem_ge50", memGe50).WithField("mem_ge70", memGe70).WithField("mem_ge90", memGe90).
				Infof("execution stats: total_mem_mb: %f, cpu_cores: %d, avg_mem_usage_pct (%%): %.2f, avg_cpu_usage (%%): %.2f, max_mem_usage_pct (%%): %.2f, max_cpu_usage_pct (%%): %.2f",
					resp.OSStats.TotalMemMB, resp.OSStats.CPUCores, resp.OSStats.AvgMemUsagePct, resp.OSStats.AvgCPUUsagePct, resp.OSStats.MaxMemUsagePct, resp.OSStats.MaxCPUUsagePct)
		}
	}

	logr.WithField("destroy_caller", "destroy_handler:api_request").
		Infoln("successfully invoked lite engine cleanup, destroying instance")

	vmDestroyStart := time.Now()
	destroyErr := poolManager.Destroy(ctx, poolID, inst.ID, inst, &r.StorageCleanupType)
	vmOutcome, vmReason := classifyCleanupErr(ctx, destroyErr, CleanupReasonCloudCallFailed)
	metrics.RecordCleanupAttempt(CleanupResourceVM, poolID, inst.Zone, vmOutcome, vmReason)
	metrics.RecordCleanupDuration(CleanupResourceVM, poolID, inst.Zone, vmOutcome, time.Since(vmDestroyStart))
	if destroyErr != nil {
		return nil, fmt.Errorf("cannot destroy the instance: %w", destroyErr)
	}
	logr.Infoln("destroy_handler: instance destroyed successfully")

	recordNormalCleanupUsageDuration(metrics, poolID, inst, usageStartUnix)

	envState().Delete(r.StageRuntimeID)

	stageOwnerDeleteStart := time.Now()
	stageOwnerErr := s.Delete(ctx, r.StageRuntimeID)
	if stageOwnerErr != nil {
		logr.WithError(stageOwnerErr).Errorln("failed to delete stage owner entity")
	}
	stageOwnerOutcome, stageOwnerReason := classifyCleanupErr(ctx, stageOwnerErr, CleanupReasonStoreError)
	metrics.RecordCleanupAttempt(CleanupResourceStageOwner, poolID, inst.Zone, stageOwnerOutcome, stageOwnerReason)
	metrics.RecordCleanupDuration(CleanupResourceStageOwner, poolID, inst.Zone, stageOwnerOutcome, time.Since(stageOwnerDeleteStart))

	return inst, nil
}

func resolveDestroyInstance(
	ctx context.Context,
	r *VMCleanupRequest,
	stageOwner *types.StageOwner,
	poolID string,
	poolManager drivers.IManager,
	logr *logrus.Entry,
) (*types.Instance, bool, error) {
	validationErr := common.ValidateStructForKeys(
		r.InstanceInfo, []string{"ID", "Zone", "PoolName", "StorageIdentifier"})
	if validationErr == nil {
		logr.Infoln("Using the instance information from the VM Cleanup Request")
		return common.BuildInstanceFromRequest(r.InstanceInfo), false, nil
	}

	logr.Debugf("Instance information is not passed to the VM Cleanup Request, fetching it from the DB: %v",
		validationErr)
	if stageOwner != nil && stageOwner.InstanceID != "" {
		instance, err := poolManager.Find(ctx, stageOwner.InstanceID)
		if store.IsNotFound(err) || (err == nil && instance == nil) {
			return nil, true, nil
		}
		if err != nil {
			return nil, false, fmt.Errorf("cannot get the instance by ID: %w", err)
		}
		return instance, false, nil
	}

	instance, err := poolManager.GetInstanceByStageID(ctx, poolID, r.StageRuntimeID)
	if err != nil {
		return nil, false, fmt.Errorf("cannot get the instance by tag: %w", err)
	}
	if instance == nil {
		return nil, false, fmt.Errorf("instance with stage runtime ID %s not found", r.StageRuntimeID)
	}
	return instance, false, nil
}

// recordNormalCleanupUsageDuration records runner_vm_usage_duration_seconds with
// termination_reason=normal_cleanup after a successful handleDestroy. usageStartUnix is a
// best-effort proxy for "became inuse at" (inst.Updated, read before it was overwritten to
// terminating); it is 0/unset when inst was built from the caller's request payload rather than
// fetched fresh from the DB (see the ValidateStructForKeys branch in handleDestroy), in which
// case usage duration is simply not recorded for that call.
func recordNormalCleanupUsageDuration(metrics *metric.Metrics, poolID string, inst *types.Instance, usageStartUnix int64) {
	if metrics == nil || usageStartUnix <= 0 {
		return
	}
	metrics.RecordVMUsageDuration(
		poolID, inst.Zone, inst.Size, string(inst.Source),
		drivers.VMTerminationReasonNormalCleanup, time.Since(time.Unix(usageStartUnix, 0)),
	)
}

func createBackoff(maxElapsedTime time.Duration) *backoff.ExponentialBackOff {
	exp := backoff.NewExponentialBackOff()
	exp.MaxElapsedTime = maxElapsedTime
	return exp
}
