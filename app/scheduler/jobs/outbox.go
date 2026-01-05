package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/harness/lite-engine/engine/spec"
	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

const (
	OutboxProcessorJobName    = "outbox-processor"
	OutboxCleanupJobName      = "outbox-cleanup"
	OutboxProcessorJobTimeout = 10 * time.Minute
)

// OutboxProcessor processes outbox jobs.
type OutboxProcessor struct {
	manager       *drivers.DistributedManager
	outboxStore   store.OutboxStore
	retryInterval time.Duration
	maxRetries    int
	batchSize     int
	scaler        *Scaler
}

// NewOutboxProcessor creates a new OutboxProcessor
func NewOutboxProcessor(
	manager *drivers.DistributedManager,
	outboxStore store.OutboxStore,
	retryInterval time.Duration,
	maxRetries int,
	batchSize int,
) *OutboxProcessor {
	return &OutboxProcessor{
		manager:       manager,
		outboxStore:   outboxStore,
		retryInterval: retryInterval,
		maxRetries:    maxRetries,
		batchSize:     batchSize,
	}
}

// SetScaler sets the scaler for processing scale jobs.
// This is set separately to avoid circular dependencies during initialization.
func (p *OutboxProcessor) SetScaler(scaler *Scaler) {
	p.scaler = scaler
}

// ProcessPendingJobs processes pending outbox jobs in batches.
func (p *OutboxProcessor) ProcessPendingJobs(ctx context.Context) error {
	jobTypes := []types.OutboxJobType{types.OutboxJobTypeSetupInstance, types.OutboxJobTypeScale}

	// 1. Find and claim runner-specific jobs (runner_name matches this runner)
	runnerJobs, err := p.outboxStore.FindAndClaimPending(ctx, p.manager.GetRunnerName(), jobTypes, p.batchSize, p.retryInterval)
	if err != nil {
		return fmt.Errorf("failed to find and claim runner-specific jobs: %w", err)
	}

	// 2. Find and claim global jobs (empty runner_name - created by scaler)
	globalJobs, err := p.outboxStore.FindAndClaimPending(ctx, "", jobTypes, p.batchSize, p.retryInterval)
	if err != nil {
		return fmt.Errorf("failed to find and claim global jobs: %w", err)
	}

	// Combine both job lists
	jobs := append(runnerJobs, globalJobs...)
	if len(jobs) == 0 {
		return nil // No pending jobs found
	}

	// Process jobs in parallel
	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go func(job *types.OutboxJob) {
			defer wg.Done()
			p.processJobWithRetry(ctx, job)
		}(job)
	}

	// Wait for all jobs to complete
	wg.Wait()

	return nil
}

// processJobWithRetry handles a single job including retry logic
func (p *OutboxProcessor) processJobWithRetry(ctx context.Context, job *types.OutboxJob) {
	if job.RetryCount > p.maxRetries {
		// Delete the job after max retries
		if err := p.outboxStore.Delete(ctx, job.ID); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"job_id":      job.ID,
				"job_type":    job.JobType,
				"pool_name":   job.PoolName,
				"runner_name": p.manager.GetRunnerName(),
			}).Errorln("failed to delete job after max retries")
		} else {
			logrus.WithFields(logrus.Fields{
				"job_id":      job.ID,
				"job_type":    job.JobType,
				"pool_name":   job.PoolName,
				"runner_name": p.manager.GetRunnerName(),
			}).Infoln("deleted job after max retries")
		}
		return
	}

	if err := p.processJob(ctx, job); err != nil {
		logrus.WithError(err).WithFields(logrus.Fields{
			"job_id":      job.ID,
			"job_type":    job.JobType,
			"pool_name":   job.PoolName,
			"runner_name": p.manager.GetRunnerName(),
		}).Errorln("failed to process job")
		// Update the job to pending with the error message
		if updateErr := p.outboxStore.UpdateStatus(ctx, job.ID, types.OutboxJobStatusPending, err.Error()); updateErr != nil {
			logrus.WithError(updateErr).WithFields(logrus.Fields{
				"job_id":      job.ID,
				"job_type":    job.JobType,
				"pool_name":   job.PoolName,
				"runner_name": p.manager.GetRunnerName(),
			}).Errorln("failed to update job error message")
		}
	}
}

// processJob processes a single outbox job
func (p *OutboxProcessor) processJob(ctx context.Context, job *types.OutboxJob) error {
	switch job.JobType {
	case types.OutboxJobTypeSetupInstance:
		if err := p.processSetupInstanceJob(ctx, job); err != nil {
			return err
		}
		logrus.WithFields(logrus.Fields{
			"job_id":      job.ID,
			"pool_name":   job.PoolName,
			"runner_name": p.manager.GetRunnerName(),
		}).Infoln("completed job to setup instance")

	case types.OutboxJobTypeScale:
		if err := p.processScaleJob(ctx, job); err != nil {
			return err
		}
		logrus.WithFields(logrus.Fields{
			"job_id":      job.ID,
			"pool_name":   job.PoolName,
			"runner_name": p.manager.GetRunnerName(),
		}).Infoln("completed scale job")
		// Don't delete scale jobs - keep them so FindScaleJobForWindow can prevent
		// duplicate jobs from being created during the lead time window.
		// The cleanup job will delete them after 48 hours.
		return nil

	default:
		return fmt.Errorf("unknown job type: %s", job.JobType)
	}

	// Delete the completed job (only for non-scale jobs like setup_instance)
	if err := p.outboxStore.Delete(ctx, job.ID); err != nil {
		return fmt.Errorf("failed to delete completed job: %w", err)
	}

	return nil
}

// CleanupOldJobs deletes jobs older than 48 hours.
// This means that no replenishment will happen for the instance after 48 hours.
func (p *OutboxProcessor) CleanupOldJobs(ctx context.Context) error {
	cutoff := time.Now().Add(-48 * time.Hour).Unix()
	rowsAffected, err := p.outboxStore.DeleteOlderThan(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete old jobs: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"runner_name":   p.manager.GetRunnerName(),
		"rows_affected": rowsAffected,
		"cutoff_time":   time.Unix(cutoff, 0),
	}).Infoln("cleaned up old outbox jobs")

	return nil
}

// processScaleJob processes a pool-specific scale job
func (p *OutboxProcessor) processScaleJob(ctx context.Context, job *types.OutboxJob) error {
	if p.scaler == nil {
		return fmt.Errorf("scaler not configured")
	}

	if job.JobParams == nil {
		return fmt.Errorf("scale job has no params")
	}

	var params types.ScaleJobParams
	if err := json.Unmarshal(*job.JobParams, &params); err != nil {
		return fmt.Errorf("failed to unmarshal scale job params: %w", err)
	}

	// Use pool name from the job directly
	poolName := job.PoolName

	logrus.WithFields(logrus.Fields{
		"job_id":       job.ID,
		"pool_name":    poolName,
		"window_start": time.Unix(params.WindowStart, 0).Format(time.RFC3339),
		"window_end":   time.Unix(params.WindowEnd, 0).Format(time.RFC3339),
	}).Infoln("processing scale job for pool")

	return p.scaler.ScalePool(ctx, poolName, params.WindowStart, params.WindowEnd)
}

// processSetupInstanceJob processes a setup instance job
func (p *OutboxProcessor) processSetupInstanceJob(ctx context.Context, job *types.OutboxJob) error {
	// Parse job params
	var machineConfig *types.MachineConfig
	if job.JobParams != nil {
		var params types.SetupInstanceParams
		if err := json.Unmarshal(*job.JobParams, &params); err != nil {
			return fmt.Errorf("failed to unmarshal job params: %w", err)
		}
		machineConfig = &types.MachineConfig{
			MachineType:          params.MachineType,
			NestedVirtualization: params.NestedVirtualization,
			Hibernate:            params.Hibernate,
			VariantID:            params.VariantID,
			Zones:                params.Zones,
		}

		// Create VMImageConfig if ImageName is provided
		if params.ImageName != "" {
			machineConfig.VMImageConfig = &spec.VMImageConfig{
				ImageName: params.ImageName,
			}
		}
	}

	// Setup instance with values from job params
	_, err := p.manager.SetupInstanceForPool(ctx, job.PoolName, machineConfig)
	if err != nil {
		return fmt.Errorf("failed to setup instance: %w", err)
	}

	return nil
}

// OutboxProcessorJob processes pending outbox jobs at regular intervals.
type OutboxProcessorJob struct {
	processor *OutboxProcessor
	interval  time.Duration
}

// NewOutboxProcessorJob creates a new OutboxProcessorJob.
func NewOutboxProcessorJob(
	processor *OutboxProcessor,
	interval time.Duration,
) *OutboxProcessorJob {
	return &OutboxProcessorJob{
		processor: processor,
		interval:  interval,
	}
}

// Name returns the job name.
func (j *OutboxProcessorJob) Name() string {
	return OutboxProcessorJobName
}

// Interval returns how often the job should run.
func (j *OutboxProcessorJob) Interval() time.Duration {
	return j.interval
}

// Timeout returns the maximum duration for job execution.
// This allows the job to run longer than its interval without being canceled.
func (j *OutboxProcessorJob) Timeout() time.Duration {
	return OutboxProcessorJobTimeout
}

// RunOnStart returns true - we want to process pending jobs immediately.
func (j *OutboxProcessorJob) RunOnStart() bool {
	return true
}

// Execute processes pending outbox jobs.
func (j *OutboxProcessorJob) Execute(ctx context.Context) error {
	if err := j.processor.ProcessPendingJobs(ctx); err != nil {
		logrus.WithError(err).Errorln("failed to process pending outbox jobs")
		return err
	}
	return nil
}

// OutboxCleanupJob removes old outbox jobs at regular intervals.
type OutboxCleanupJob struct {
	processor *OutboxProcessor
	interval  time.Duration
}

// NewOutboxCleanupJob creates a new OutboxCleanupJob.
func NewOutboxCleanupJob(
	processor *OutboxProcessor,
	interval time.Duration,
) *OutboxCleanupJob {
	return &OutboxCleanupJob{
		processor: processor,
		interval:  interval,
	}
}

// Name returns the job name.
func (j *OutboxCleanupJob) Name() string {
	return OutboxCleanupJobName
}

// Interval returns how often the job should run.
func (j *OutboxCleanupJob) Interval() time.Duration {
	return j.interval
}

// Timeout returns 0 to use the interval as the timeout.
func (j *OutboxCleanupJob) Timeout() time.Duration {
	return j.interval
}

// RunOnStart returns false - no need to cleanup immediately on start.
func (j *OutboxCleanupJob) RunOnStart() bool {
	return false
}

// Execute removes old outbox jobs.
func (j *OutboxCleanupJob) Execute(ctx context.Context) error {
	if err := j.processor.CleanupOldJobs(ctx); err != nil {
		logrus.WithError(err).Errorln("failed to cleanup old outbox jobs")
		return err
	}
	return nil
}
