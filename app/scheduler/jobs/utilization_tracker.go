package jobs

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

const (
	UtilizationTrackerJobName = "utilization-tracker"
)

// UtilizationTrackerJob tracks instance utilization history at regular intervals.
type UtilizationTrackerJob struct {
	instanceStore store.InstanceStore
	historyStore  store.UtilizationHistoryStore
	interval      time.Duration
}

// NewUtilizationTrackerJob creates a new UtilizationTrackerJob.
func NewUtilizationTrackerJob(
	instanceStore store.InstanceStore,
	historyStore store.UtilizationHistoryStore,
	interval time.Duration,
) *UtilizationTrackerJob {
	return &UtilizationTrackerJob{
		instanceStore: instanceStore,
		historyStore:  historyStore,
		interval:      interval,
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

// Timeout returns 0 to use the interval as the timeout.
func (j *UtilizationTrackerJob) Timeout() time.Duration {
	return j.interval
}

// RunOnStart returns true - we want to record immediately on start.
func (j *UtilizationTrackerJob) RunOnStart() bool {
	return false
}

// Execute records the current utilization for all pools.
func (j *UtilizationTrackerJob) Execute(ctx context.Context) error {
	now := time.Now().Unix()

	// Single DB call for all pools, variants, and images
	counts, err := j.instanceStore.CountByPoolVariantAndImage(ctx, types.StateInUse)
	if err != nil {
		return err
	}

	for poolName, variantCounts := range counts {
		for variantID, imageCounts := range variantCounts {
			for imageName, count := range imageCounts {
				record := &types.UtilizationRecord{
					Pool:           poolName,
					VariantID:      variantID,
					ImageName:      imageName,
					InUseInstances: count,
					RecordedAt:     now,
				}

				if err := j.historyStore.Create(ctx, record); err != nil {
					logrus.WithError(err).WithFields(logrus.Fields{
						"pool":       poolName,
						"variant_id": variantID,
						"image_name": imageName,
						"count":      count,
					}).Errorln("failed to create utilization record")
					continue
				}

				logrus.WithFields(logrus.Fields{
					"pool":       poolName,
					"variant_id": variantID,
					"image_name": imageName,
					"count":      count,
				}).Debugln("recorded utilization")
			}
		}
	}

	return nil
}
