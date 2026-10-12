package jobs

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/store"
)

const (
	HistoryCleanupJobName = "history-cleanup"
	logFieldCutoffTime    = "cutoff_time"
)

// HistoryCleanupJob periodically removes old utilization history records.
type HistoryCleanupJob struct {
	historyStore    store.UtilizationHistoryStore
	interval        time.Duration
	retentionPeriod time.Duration

	capabilityStore store.CapabilityHistoryStore
}

// NewHistoryCleanupJob creates a new HistoryCleanupJob.
func NewHistoryCleanupJob(
	historyStore store.UtilizationHistoryStore,
	interval time.Duration,
	retentionPeriod time.Duration,
) *HistoryCleanupJob {
	return &HistoryCleanupJob{
		historyStore:    historyStore,
		interval:        interval,
		retentionPeriod: retentionPeriod,
	}
}

// WithCapabilityHistory also applies the retention period to the capability history table.
func (j *HistoryCleanupJob) WithCapabilityHistory(s store.CapabilityHistoryStore) *HistoryCleanupJob {
	j.capabilityStore = s
	return j
}

// Name returns the job name.
func (j *HistoryCleanupJob) Name() string {
	return HistoryCleanupJobName
}

// Interval returns how often the job should run.
func (j *HistoryCleanupJob) Interval() time.Duration {
	return j.interval
}

// Timeout returns 0 to use the interval as the timeout.
func (j *HistoryCleanupJob) Timeout() time.Duration {
	return j.interval
}

// RunOnStart returns false - no need to cleanup immediately on start.
func (j *HistoryCleanupJob) RunOnStart() bool {
	return false
}

// Execute removes utilization records older than the retention period.
func (j *HistoryCleanupJob) Execute(ctx context.Context) error {
	cutoff := time.Now().Add(-j.retentionPeriod).Unix()

	rowsAffected, err := j.historyStore.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		return err
	}

	logrus.WithFields(logrus.Fields{
		"rows_deleted":     rowsAffected,
		logFieldCutoffTime: time.Unix(cutoff, 0),
	}).Infoln("cleaned up old utilization records")

	if j.capabilityStore != nil {
		rows, err := j.capabilityStore.DeleteOlderThan(ctx, cutoff)
		if err != nil {
			return err
		}
		logrus.WithFields(logrus.Fields{
			"rows_deleted":     rows,
			logFieldCutoffTime: time.Unix(cutoff, 0),
		}).Infoln("cleaned up old capability utilization records")
	}

	return nil
}
