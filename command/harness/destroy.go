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
			logr.WithError(ctx.Err()).
				WithField("retry_count", cnt).
				Warnln("destroy operation cancelled due to context timeout or cancellation")
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
					logr.WithError(err).
						WithField("retry_attempt", cnt).
						WithField("next_retry_in", duration).
						Errorln("could not destroy VM, retrying")
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
	startTime := time.Now()
	logr = logr.WithField("retry_count", retryCount)

	defer func() {
		logr.WithField("total_duration_ms", time.Since(startTime).Milliseconds()).
			Traceln("handleDestroy completed")
	}()

	// Declare capacity variable early and defer its destruction to ensure cleanup happens regardless of errors
	defer func() {
		var capacity *types.CapacityReservation
		var err error
		if crs != nil {
			capacity, err = crs.Find(ctx, r.StageRuntimeID)
			if err != nil {
				logr.WithError(err).Warnln("deferred_capacity_cleanup: capacity reservation not found, skipping cleanup")
			} else if capacity != nil {
				logr.WithField("destroy_caller", "destroy_handler:deferred_capacity_cleanup").
					WithField("reservation_id", capacity.ReservationID).
					Infoln("destroy_capacity: deferred capacity reservation cleanup")
				if destroyCapErr := poolManager.DestroyCapacity(ctx, capacity); destroyCapErr != nil {
					logr.WithError(destroyCapErr).
						WithField("reservation_id", capacity.ReservationID).
						Errorln("failed to destroy capacity reservation")
				} else {
					logr.WithField("reservation_id", capacity.ReservationID).
						Infoln("capacity reservation destroyed successfully")
				}
			} else {
				logr.Debugln("deferred_capacity_cleanup: no capacity reservation found for stage")
			}
		} else {
			logr.Traceln("deferred_capacity_cleanup: capacity reservation store not initialized")
		}
	}()

	var poolID string
	if r.InstanceInfo.PoolName == "" {
		entity, err := s.Find(ctx, r.StageRuntimeID)
		if err != nil {
			logr.WithError(err).
				Errorln("failed to find stage owner entity - possible race condition with initialize/destroy calls")
			return nil, errors.Wrap(err, fmt.Sprintf("failed to find stage owner entity for stage: %s", r.StageRuntimeID))
		}
		if entity == nil {
			logr.Errorln("stage owner entity is nil - possible race condition or entity already cleaned up")
			return nil, errors.Wrap(err, fmt.Sprintf("failed to find stage owner entity for stage: %s", r.StageRuntimeID))
		}
		poolID = entity.PoolName
		logr.WithField("pool_id", poolID).Debugln("pool ID retrieved from stage owner entity")
	} else {
		poolID = r.InstanceInfo.PoolName
		logr.WithField("pool_id", poolID).Debugln("pool ID retrieved from instance info")
	}

	logr = logr.WithField("pool_id", poolID)

	logr = AddContext(logr, &r.Context, map[string]string{})

	logr.WithField("destroy_caller", "destroy_handler:api_request").
		Infoln("starting the destroy process")

	var inst *types.Instance
	err := common.ValidateStructForKeys(r.InstanceInfo, []string{"ID", "Zone", "PoolName", "StorageIdentifier"})
	if err != nil {
		logr.Debugln("instance info not provided in request, fetching from database")
		inst, err = poolManager.GetInstanceByStageID(ctx, poolID, r.StageRuntimeID)
		if err != nil {
			return nil, fmt.Errorf("cannot get the instance by tag: %w", err)
		}
		if inst == nil {
			return nil, fmt.Errorf("instance with stage runtime ID %s not found", r.StageRuntimeID)
		}
		logr.WithField("instance_id", inst.ID).
			WithField("instance_name", inst.Name).
			Debugln("instance fetched from database")
	} else {
		inst = common.BuildInstanceFromRequest(r.InstanceInfo)
		logr.WithField("instance_id", inst.ID).
			WithField("instance_name", inst.Name).
			Debugln("using instance information from request")
	}

	logr = logr.
		WithField("instance_id", inst.ID).
		WithField("instance_name", inst.Name)

	// Update instance state to terminating and update timestamp
	inst.State = types.StateTerminating
	inst.Updated = time.Now().Unix()
	if updateErr := poolManager.Update(ctx, inst); updateErr != nil {
		logr.WithError(updateErr).Warnln("failed to update instance state to terminating")
	} else {
		logr.WithField("new_state", types.StateTerminating).
			Debugln("instance state updated to terminating")
	}

	logr.Traceln("invoking lite engine cleanup")
	leStart := time.Now()
	client, err := lehelper.GetClient(inst, poolManager.GetTLSServerName(), inst.Port, enableMock, mockTimeout)
	if err != nil {
		logr.WithError(err).
			WithField("elapsed_ms", time.Since(leStart).Milliseconds()).
			Errorln("could not create lite engine client for invoking cleanup")
	} else {
		// Attempting to call lite engine destroy with timeout controlled at caller level
		leCtx, leCancel := context.WithTimeout(ctx, liteEngineDestroyTimeout)
		defer leCancel()
		resp, destroyErr := client.Destroy(leCtx,
			&api.DestroyRequest{LogDrone: false, LogKey: r.LogKey, LiteEnginePath: oshelp.GetLiteEngineLogsPath(inst.OS), StageRuntimeID: r.StageRuntimeID})

		logr = logr.WithField("le_cleanup_duration_ms", time.Since(leStart).Milliseconds())

		if destroyErr != nil {
			// we can continue even if lite engine destroy does not happen successfully. This is because
			// the VM is anyways destroyed so the process will be killed
			logr.WithError(destroyErr).Errorln("could not invoke lite engine cleanup")
		} else {
			logr.Infoln("lite engine cleanup completed successfully")
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

	if err = poolManager.Destroy(ctx, poolID, inst.ID, inst, &r.StorageCleanupType); err != nil {
		return nil, fmt.Errorf("cannot destroy the instance: %w", err)
	}
	logr.Infoln("destroy_handler: instance destroyed successfully")

	envState().Delete(r.StageRuntimeID)

	if err = s.Delete(ctx, r.StageRuntimeID); err != nil {
		logr.WithError(err).Errorln("failed to delete stage owner entity")
	} else {
		logr.Debugln("stage owner entity deleted successfully")
	}

	logr.WithFields(logrus.Fields{
		"total_duration_ms": time.Since(startTime).Milliseconds(),
		"retry_count":       retryCount,
		"instance_id":       inst.ID,
		"pool_id":           poolID,
	}).Infoln("destroy operation completed successfully")

	return inst, nil
}

func createBackoff(maxElapsedTime time.Duration) *backoff.ExponentialBackOff {
	exp := backoff.NewExponentialBackOff()
	exp.MaxElapsedTime = maxElapsedTime
	return exp
}
