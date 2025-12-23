package jobs

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/store"
)

const (
	HistoryCleanupJobName = "history-cleanup"
)

// HistoryCleanupJob periodically removes old utilization history records.
type HistoryCleanupJob struct {
	historyStore    store.UtilizationHistoryStore
	interval        time.Duration
	retentionPeriod time.Duration
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

// Name returns the job name.
func (j *HistoryCleanupJob) Name() string {
	return HistoryCleanupJobName
}

// Interval returns how often the job should run.
func (j *HistoryCleanupJob) Interval() time.Duration {
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
		"rows_deleted": rowsAffected,
		"cutoff_time":  time.Unix(cutoff, 0),
	}).Infoln("cleaned up old utilization records")

	return nil
}
