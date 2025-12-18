package jobs

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

const (
	UtilizationTrackerJobName = "utilization-tracker"
)

// UtilizationTrackerJob tracks instance utilization history at regular intervals.
type UtilizationTrackerJob struct {
	manager      drivers.IManager
	historyStore store.UtilizationHistoryStore
	interval     time.Duration
}

// NewUtilizationTrackerJob creates a new UtilizationTrackerJob.
func NewUtilizationTrackerJob(
	manager drivers.IManager,
	historyStore store.UtilizationHistoryStore,
	interval time.Duration,
) *UtilizationTrackerJob {
	return &UtilizationTrackerJob{
		manager:      manager,
		historyStore: historyStore,
		interval:     interval,
	}
}

// Name returns the job name.
func (j *UtilizationTrackerJob) Name() string {
	return UtilizationTrackerJobName
}

// Interval returns how often the job should run.
func (j *UtilizationTrackerJob) Interval() time.Duration {
	return j.interval
}

// RunOnStart returns true - we want to record immediately on start.
func (j *UtilizationTrackerJob) RunOnStart() bool {
	return true
}

// Execute records the current utilization for all pools.
func (j *UtilizationTrackerJob) Execute(ctx context.Context) error {
	poolNames := j.manager.GetPoolNames()
	now := time.Now().Unix()

	for _, poolName := range poolNames {
		if err := j.recordPoolUtilization(ctx, poolName, now); err != nil {
			logrus.WithError(err).WithField("pool", poolName).
				Errorln("failed to record pool utilization")
		}
	}

	return nil
}

// recordPoolUtilization records utilization for a specific pool.
func (j *UtilizationTrackerJob) recordPoolUtilization(ctx context.Context, poolName string, timestamp int64) error {
	// Get counts grouped by variant ID directly from the database
	params := &types.QueryParams{
		PoolName: poolName,
		Status:   types.StateInUse,
	}
	variantCounts, err := j.manager.GetInstanceStore().CountGroupBy(ctx, params, "variant_id")
	if err != nil {
		return err
	}

	// If no variants found, record a zero count for the pool with empty variant
	if len(variantCounts) == 0 {
		variantCounts[""] = 0
	}

	// Record utilization for each variant
	for variantID, count := range variantCounts {
		record := &types.UtilizationRecord{
			Pool:           poolName,
			VariantID:      variantID,
			InUseInstances: count,
			RecordedAt:     timestamp,
		}

		if err := j.historyStore.Create(ctx, record); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"pool":       poolName,
				"variant_id": variantID,
				"count":      count,
			}).Errorln("failed to create utilization record")
			continue
		}

		logrus.WithFields(logrus.Fields{
			"pool":       poolName,
			"variant_id": variantID,
			"count":      count,
		}).Debugln("recorded utilization")
	}

	return nil
}
