package jobs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

const (
	// ScalerTriggerJobName is the name of the scaler trigger job
	ScalerTriggerJobName = "scaler-trigger"
)

// ScalerTriggerJob creates scale outbox jobs at aligned window boundaries.
// It creates one scale job per pool to allow independent processing.
type ScalerTriggerJob struct {
	outboxStore   store.OutboxStore
	config        types.ScalerConfig
	runnerName    string
	pools         []ScalablePool
	lastWindowKey int64 // Track last window to avoid duplicates
}

// NewScalerTriggerJob creates a new ScalerTriggerJob.
func NewScalerTriggerJob(
	outboxStore store.OutboxStore,
	config types.ScalerConfig,
	runnerName string,
	pools []ScalablePool,
) *ScalerTriggerJob {
	if config.WindowDuration == 0 {
		config.WindowDuration = DefaultWindowDuration
	}
	if config.LeadTime == 0 {
		config.LeadTime = DefaultLeadTime
	}

	return &ScalerTriggerJob{
		outboxStore: outboxStore,
		config:      config,
		runnerName:  runnerName,
		pools:       pools,
	}
}

// Name returns the job name.
func (j *ScalerTriggerJob) Name() string {
	return ScalerTriggerJobName
}

// Interval returns how often the job should run.
// Run every minute to check if we need to create a scale job.
func (j *ScalerTriggerJob) Interval() time.Duration {
	return 1 * time.Minute
}

// Timeout returns 0 to use the interval as the timeout.
func (j *ScalerTriggerJob) Timeout() time.Duration {
	return j.Interval()
}

// RunOnStart returns true - check immediately on start.
func (j *ScalerTriggerJob) RunOnStart() bool {
	return false
}

// Execute checks if it's time to create scale jobs for the upcoming window.
// It creates one scale job per pool to allow independent processing.
func (j *ScalerTriggerJob) Execute(ctx context.Context) error {
	if !j.config.Enabled {
		return nil
	}

	now := time.Now().UTC() // Use UTC for consistent window calculations across all pods

	// Calculate the next window boundary (the upcoming window we need to prepare for)
	windowStart, windowEnd := j.getNextWindowBoundary(now)

	// Check if we're within the lead time before the window starts
	timeUntilWindow := time.Unix(windowStart, 0).Sub(now)
	if timeUntilWindow > j.config.LeadTime || timeUntilWindow < 0 {
		// Not yet time to scale for this window, or window already started
		return nil
	}

	// Check if we already processed this window in this instance
	if windowStart == j.lastWindowKey {
		return nil // Already processed this window
	}

	// Create scale jobs for each pool
	jobsCreated := 0
	for _, pool := range j.pools {
		// Check if a scale job already exists for this pool and window
		existingJob, err := j.outboxStore.FindScaleJobForWindow(ctx, pool.Name, windowStart)
		if err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"pool":         pool.Name,
				"window_start": time.Unix(windowStart, 0).Format(time.RFC3339),
			}).Errorln("scaler trigger: failed to check for existing scale job")
			continue
		}
		if existingJob != nil {
			logrus.WithFields(logrus.Fields{
				"pool":         pool.Name,
				"window_start": time.Unix(windowStart, 0).Format(time.RFC3339),
				"job_id":       existingJob.ID,
			}).Debugln("scaler trigger: scale job already exists for pool and window")
			continue
		}

		// Create the scale job params (PoolName is stored in OutboxJob.PoolName)
		scaleParams := &types.ScaleJobParams{
			WindowStart: windowStart,
			WindowEnd:   windowEnd,
		}
		paramsJSON, err := json.Marshal(scaleParams)
		if err != nil {
			logrus.WithError(err).WithField("pool", pool.Name).
				Errorln("scaler trigger: failed to marshal scale params")
			continue
		}
		rawMsg := json.RawMessage(paramsJSON)

		// Create the scale outbox job for this pool
		// Leave RunnerName empty so any runner can process this job
		job := &types.OutboxJob{
			PoolName:  pool.Name,
			JobType:   types.OutboxJobTypeScale,
			JobParams: &rawMsg,
			Status:    types.OutboxJobStatusPending,
		}

		if err := j.outboxStore.Create(ctx, job); err != nil {
			logrus.WithError(err).WithField("pool", pool.Name).
				Errorln("scaler trigger: failed to create scale job")
			continue
		}

		jobsCreated++
		logrus.WithFields(logrus.Fields{
			"pool":              pool.Name,
			"window_start":      time.Unix(windowStart, 0).Format(time.RFC3339),
			"window_end":        time.Unix(windowEnd, 0).Format(time.RFC3339),
			"job_id":            job.ID,
			"time_until_window": timeUntilWindow,
		}).Infoln("scaler trigger: created scale job for pool")
	}

	j.lastWindowKey = windowStart

	logrus.WithFields(logrus.Fields{
		"window_start": time.Unix(windowStart, 0).Format(time.RFC3339),
		"jobs_created": jobsCreated,
		"total_pools":  len(j.pools),
	}).Infoln("scaler trigger: finished creating scale jobs for window")

	return nil
}

// getNextWindowBoundary calculates the next aligned window boundary.
// For a 60-minute window, boundaries are at :00 of each hour.
// For a 30-minute window, boundaries are at :00 and :30 of each hour.
func (j *ScalerTriggerJob) getNextWindowBoundary(now time.Time) (windowStart, windowEnd int64) {
	windowMinutes := int(j.config.WindowDuration.Minutes())

	// Calculate minutes since midnight in UTC for consistent window boundaries
	nowUTC := now.UTC()
	midnight := time.Date(nowUTC.Year(), nowUTC.Month(), nowUTC.Day(), 0, 0, 0, 0, time.UTC)
	minutesSinceMidnight := int(nowUTC.Sub(midnight).Minutes())

	// Find the current window index and calculate next window
	currentWindowIndex := minutesSinceMidnight / windowMinutes
	nextWindowIndex := currentWindowIndex + 1

	// Calculate the next window start time
	nextWindowMinutes := nextWindowIndex * windowMinutes
	nextWindowStart := midnight.Add(time.Duration(nextWindowMinutes) * time.Minute)
	nextWindowEnd := nextWindowStart.Add(j.config.WindowDuration)

	return nextWindowStart.Unix(), nextWindowEnd.Unix()
}

// GetWindowDuration returns the configured window duration.
func (j *ScalerTriggerJob) GetWindowDuration() time.Duration {
	return j.config.WindowDuration
}
