package drivers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/harness/lite-engine/engine/spec"
	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// OutboxProcessor processes outbox jobs
type OutboxProcessor struct {
	manager       *DistributedManager
	outboxStore   store.OutboxStore
	pollInterval  time.Duration
	retryInterval time.Duration
	maxRetries    int
	ctx           context.Context
	cancel        context.CancelFunc
	batchSize     int
}

// NewOutboxProcessor creates a new OutboxProcessor
func NewOutboxProcessor(
	ctx context.Context,
	manager *DistributedManager,
	outboxStore store.OutboxStore,
	pollInterval time.Duration,
	retryInterval time.Duration,
	maxRetries int,
	batchSize int,
) *OutboxProcessor {
	ctx, cancel := context.WithCancel(ctx)
	return &OutboxProcessor{
		manager:       manager,
		outboxStore:   outboxStore,
		pollInterval:  pollInterval,
		retryInterval: retryInterval,
		maxRetries:    maxRetries,
		batchSize:     batchSize,
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Start starts the outbox processor and cleanup process
func (p *OutboxProcessor) Start() {
	go p.processLoop()
	go p.cleanupLoop()
}

// Stop stops the outbox processor
func (p *OutboxProcessor) Stop() {
	p.cancel()
}

// processLoop continuously processes outbox jobs
func (p *OutboxProcessor) processLoop() {
	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			if err := p.processPendingJobs(); err != nil {
				logrus.WithError(err).WithField("runner_name", p.manager.runnerName).Errorln("failed to process pending outbox jobs")
			}
		}
	}
}

// processPendingJobs processes pending outbox jobs in batches
func (p *OutboxProcessor) processPendingJobs() error {
	// Find and claim pending jobs
	jobs, err := p.outboxStore.FindAndClaimPending(p.ctx, p.manager.runnerName, []types.OutboxJobType{types.OutboxJobTypeSetupInstance}, p.batchSize, p.retryInterval)
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
			if err := p.outboxStore.Delete(p.ctx, job.ID); err != nil {
				logrus.WithError(err).WithFields(logrus.Fields{
					"job_id":      job.ID,
					"pool_name":   job.PoolName,
					"runner_name": p.manager.runnerName,
				}).Errorln("failed to delete job after max retries")
			} else {
				logrus.WithFields(logrus.Fields{
					"job_id":      job.ID,
					"pool_name":   job.PoolName,
					"runner_name": p.manager.runnerName,
				}).Infoln("deleted job after max retries")
			}
			continue
		}

		if err := p.processJob(job); err != nil {
			logrus.WithError(err).WithFields(logrus.Fields{
				"job_id":      job.ID,
				"pool_name":   job.PoolName,
				"runner_name": p.manager.runnerName,
			}).Errorln("failed to process job")
			// Update the job to pending with the error message
			if updateErr := p.outboxStore.UpdateStatus(p.ctx, job.ID, types.OutboxJobStatusPending, err.Error()); updateErr != nil {
				logrus.WithError(updateErr).WithFields(logrus.Fields{
					"job_id":      job.ID,
					"pool_name":   job.PoolName,
					"runner_name": p.manager.runnerName,
				}).Errorln("failed to update job error message")
			}
			continue
		}
	}

	return nil
}

// processJob processes a single outbox job
func (p *OutboxProcessor) processJob(job *types.OutboxJob) error {
	if job.JobType != types.OutboxJobTypeSetupInstance {
		return fmt.Errorf("unknown job type: %s", job.JobType)
	}

	if err := p.processSetupInstanceJob(job); err != nil {
		return err
	}

	// Delete the completed job
	if err := p.outboxStore.Delete(p.ctx, job.ID); err != nil {
		return fmt.Errorf("failed to delete completed job: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"job_id":      job.ID,
		"pool_name":   job.PoolName,
		"runner_name": p.manager.runnerName,
	}).Infoln("completed job to setup instance")

	return nil
}

// cleanupLoop continuously cleans up old outbox jobs
func (p *OutboxProcessor) cleanupLoop() {
	// Run cleanup every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			if err := p.cleanupOldJobs(); err != nil {
				logrus.WithError(err).WithField("runner_name", p.manager.runnerName).Errorln("failed to cleanup old outbox jobs")
			}
		}
	}
}

// cleanupOldJobs deletes jobs older than 48 hours
// this means that no replenishment will happen for the instance after 48 hours
func (p *OutboxProcessor) cleanupOldJobs() error {
	cutoff := time.Now().Add(-48 * time.Hour).Unix()
	rowsAffected, err := p.outboxStore.DeleteOlderThan(p.ctx, cutoff)
	if err != nil {
		return fmt.Errorf("failed to delete old jobs: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"runner_name":   p.manager.runnerName,
		"rows_affected": rowsAffected,
		"cutoff_time":   time.Unix(cutoff, 0),
	}).Infoln("cleaned up old outbox jobs")

	return nil
}

// processSetupInstanceJob processes a setup instance job
func (p *OutboxProcessor) processSetupInstanceJob(job *types.OutboxJob) error {
	// Get pool
	pool := p.manager.poolMap[job.PoolName]
	if pool == nil {
		return fmt.Errorf("pool not found: %s", job.PoolName)
	}

	// Parse job params
	var params *types.SetupInstanceParams
	var machineConfig *types.MachineConfig
	if job.JobParams != nil {
		params = &types.SetupInstanceParams{}
		if err := json.Unmarshal(*job.JobParams, params); err != nil {
			return fmt.Errorf("failed to unmarshal job params: %w", err)
		}
		machineConfig = &types.MachineConfig{
			Zone:                 params.Zone,
			MachineType:          params.MachineType,
			NestedVirtualization: params.NestedVirtualization,
			Hibernate:            params.Hibernate,
			VariantID:            params.VariantID,
		}

		// Create MachineConfig if ImageName is provided
		if params.ImageName != "" {
			machineConfig.VMImageConfig = &spec.VMImageConfig{
				ImageName: params.ImageName,
			}
		}
	}

	// Setup instance with values from job params
	_, err := p.manager.setupInstanceWithHibernate(
		p.ctx,
		pool,
		p.manager.GetTLSServerName(),
		"",
		"",
		machineConfig,
		nil,
		nil,
		-1,
		nil,
	)

	if err != nil {
		return fmt.Errorf("failed to setup instance: %w", err)
	}

	return nil
}
