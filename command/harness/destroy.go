package harness

import (
	"context"
	"fmt"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"strconv"
	"time"

	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/internal/drivers"
	"github.com/drone-runners/drone-runner-aws/internal/lehelper"
	"github.com/drone-runners/drone-runner-aws/internal/oshelp"
	ierrors "github.com/drone-runners/drone-runner-aws/internal/types"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
	"github.com/harness/lite-engine/api"

	"github.com/cenkalti/backoff/v4"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	destroyTimeout = 10 * time.Minute
)

type VMCleanupRequest struct {
	PoolID             string              `json:"pool_id"`
	StageRuntimeID     string              `json:"stage_runtime_id"`
	LogKey             string              `json:"log_key,omitempty"`
	Distributed        bool                `json:"distributed,omitempty"`
	Context            Context             `json:"context,omitempty"`
	StorageCleanupType storage.CleanupType `json:"storage_cleanup_type,omitempty"`
}

func HandleDestroy(ctx context.Context, r *VMCleanupRequest, s store.StageOwnerStore, env *config.EnvConfig, poolManager drivers.IManager, metrics *metric.Metrics) error {
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
			_, err := handleDestroy(ctx, r, s, env, poolManager, metrics, cnt, logr)
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

func handleDestroy(ctx context.Context, r *VMCleanupRequest, s store.StageOwnerStore, env *config.EnvConfig,
	poolManager drivers.IManager, metrics *metric.Metrics, retryCount int, logr *logrus.Entry) (*types.Instance, error) {
	logr = logr.WithField("retry_count", retryCount)
	entity, err := s.Find(ctx, r.StageRuntimeID)
	if err != nil || entity == nil {
		return nil, errors.Wrap(err, fmt.Sprintf("failed to find stage owner entity for stage: %s", r.StageRuntimeID))
	}
	poolID := entity.PoolName
	logr = logr.WithField("pool_id", poolID)

	logr = AddContext(logr, &r.Context, map[string]string{})

	logr.Infoln("starting the destroy process")

	inst, err := poolManager.GetInstanceByStageID(ctx, poolID, r.StageRuntimeID)
	if err != nil {
		return nil, fmt.Errorf("cannot get the instance by tag: %w", err)
	}
	if inst == nil {
		return nil, fmt.Errorf("instance with stage runtime ID %s not found", r.StageRuntimeID)
	}

	logr = logr.
		WithField("instance_id", inst.ID).
		WithField("instance_name", inst.Name)

	logr.Traceln("invoking lite engine cleanup")
	client, err := lehelper.GetClient(inst, poolManager.GetTLSServerName(), inst.Port, env.LiteEngine.EnableMock, env.LiteEngine.MockStepTimeoutSecs)
	if err != nil {
		logr.WithError(err).Errorln("could not create lite engine client for invoking cleanup")
	} else {
		// Attempting to call lite engine destroy
		resp, destroyErr := client.Destroy(context.Background(),
			&api.DestroyRequest{LogDrone: false, LogKey: r.LogKey, LiteEnginePath: oshelp.GetLiteEngineLogsPath(inst.OS), StageRuntimeID: r.StageRuntimeID})
		if destroyErr != nil {
			// we can continue even if lite engine destroy does not happen successfully. This is because
			// the VM is anyways destroyed so the process will be killed
			logr.WithError(destroyErr).Errorln("could not invoke lite engine cleanup")
		}
		if resp != nil && resp.OSStats != nil {
			var cpuGe50, cpuGe70, cpuGe90, memGe50, memGe70, memGe90 bool
			if resp.OSStats.MaxCPUUsagePct >= 50.0 { //nolint:gomnd
				cpuGe50 = true
				if resp.OSStats.MaxCPUUsagePct >= 70.0 { //nolint:gomnd
					cpuGe70 = true
					if resp.OSStats.MaxCPUUsagePct >= 90.0 { //nolint:gomnd
						cpuGe90 = true
					}
				}
			}
			if resp.OSStats.MaxMemUsagePct >= 50.0 { //nolint:gomnd
				memGe50 = true
				if resp.OSStats.MaxMemUsagePct >= 70.0 { //nolint:gomnd
					memGe70 = true
					if resp.OSStats.MaxMemUsagePct >= 90.0 { //nolint:gomnd
						memGe90 = true
					}
				}
			}

			metrics.CPUPercentile.WithLabelValues(poolID, inst.OS, inst.Arch, string(inst.Provider), strconv.FormatBool(poolManager.IsDistributed())).Observe(resp.OSStats.MaxCPUUsagePct)
			metrics.MemoryPercentile.WithLabelValues(poolID, inst.OS, inst.Arch, string(inst.Provider), strconv.FormatBool(poolManager.IsDistributed())).Observe(resp.OSStats.MaxMemUsagePct)

			logr.WithField("cpu_ge50", cpuGe50).WithField("cpu_ge70", cpuGe70).WithField("cpu_ge90", cpuGe90).
				WithField("mem_ge50", memGe50).WithField("mem_ge70", memGe70).WithField("mem_ge90", memGe90).
				Infof("execution stats: total_mem_mb: %f, cpu_cores: %d, avg_mem_usage_pct (%%): %.2f, avg_cpu_usage (%%): %.2f, max_mem_usage_pct (%%): %.2f, max_cpu_usage_pct (%%): %.2f",
					resp.OSStats.TotalMemMB, resp.OSStats.CPUCores, resp.OSStats.AvgMemUsagePct, resp.OSStats.AvgCPUUsagePct, resp.OSStats.MaxMemUsagePct, resp.OSStats.MaxCPUUsagePct)
		}
	}

	logr.Infoln("successfully invoked lite engine cleanup, destroying instance")

	if err = poolManager.Destroy(ctx, poolID, inst.ID, &r.StorageCleanupType); err != nil {
		return nil, fmt.Errorf("cannot destroy the instance: %w", err)
	}
	logr.Infoln("destroyed instance")

	envState().Delete(r.StageRuntimeID)

	if err = s.Delete(ctx, r.StageRuntimeID); err != nil {
		logr.WithError(err).Errorln("failed to delete stage owner entity")
	}

	return inst, nil
}

func createBackoff(maxElapsedTime time.Duration) *backoff.ExponentialBackOff {
	exp := backoff.NewExponentialBackOff()
	exp.MaxElapsedTime = maxElapsedTime
	return exp
}
