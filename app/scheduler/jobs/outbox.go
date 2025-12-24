package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/harness/lite-engine/engine/spec"
	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

const (
	OutboxProcessorJobName = "outbox-processor"
	OutboxCleanupJobName   = "outbox-cleanup"
)

// OutboxProcessor processes outbox jobs.
type OutboxProcessor struct {
	manager       *drivers.DistributedManager
	outboxStore   store.OutboxStore
	retryInterval time.Duration
	maxRetries    int
	batchSize     int
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

// ProcessPendingJobs processes pending outbox jobs in batches.
func (p *OutboxProcessor) ProcessPendingJobs(ctx context.Context) error {
	// Find and claim pending jobs
	jobs, err := p.outboxStore.FindAndClaimPending(ctx, p.manager.GetRunnerName(), []types.OutboxJobType{types.OutboxJobTypeSetupInstance}, p.batchSize, p.retryInterval)
	if err != nil {
		return fmt.Errorf("failed to find and claim pending jobs: %w", err)
	}

	if len(jobs) == 0 {
		return nil // No pending jobs found
	}

	// Process each job
	for _, job := range jobs {
		if job.RetryCount > p.maxRetries {
			// Delete the job after max retries
			if err := p.outboxStore.Delete(ctx, job.ID); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"job_id":      job.ID,
					"pool_name":   job.PoolName,
					"runner_name": p.manager.GetRunnerName(),
				}).Errorln("failed to delete job after max retries")
			} else {
				logrus.WithFields(logrus.Fields{
					"job_id":      job.ID,
					"pool_name":   job.PoolName,
					"runner_name": p.manager.GetRunnerName(),
				}).Infoln("deleted job after max retries")
			}
			continue
		}

		if err := p.processJob(ctx, job); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"job_id":      job.ID,
				"pool_name":   job.PoolName,
				"runner_name": p.manager.GetRunnerName(),
			}).Errorln("failed to process job")
			// Update the job to pending with the error message
			if updateErr := p.outboxStore.UpdateStatus(ctx, job.ID, types.OutboxJobStatusPending, err.Error()); updateErr != nil {
				logrus.WithError(updateErr).WithFields(logrus.Fields{
					"job_id":      job.ID,
					"pool_name":   job.PoolName,
					"runner_name": p.manager.GetRunnerName(),
				}).Errorln("failed to update job error message")
			}
			continue
		}
	}

	return nil
}

// processJob processes a single outbox job
func (p *OutboxProcessor) processJob(ctx context.Context, job *types.OutboxJob) error {
	if job.JobType != types.OutboxJobTypeSetupInstance {
		return fmt.Errorf("unknown job type: %s", job.JobType)
	}

	if err := p.processSetupInstanceJob(ctx, job); err != nil {
		return err
	}

	// Delete the completed job
	if err := p.outboxStore.Delete(ctx, job.ID); err != nil {
		return fmt.Errorf("failed to delete completed job: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"job_id":      job.ID,
		"pool_name":   job.PoolName,
		"runner_name": p.manager.GetRunnerName(),
	}).Infoln("completed job to setup instance")

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
