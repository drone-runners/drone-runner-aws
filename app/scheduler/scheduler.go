package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Job defines the interface for a scheduled job.
type Job interface {
	// Name returns a unique identifier for the job.
	Name() string
	// Interval returns how often the job should run.
	Interval() time.Duration
	// Execute runs the job. Returns an error if the job fails.
	Execute(ctx context.Context) error
	// RunOnStart returns true if the job should run immediately when scheduled.
	RunOnStart() bool
}

// Scheduler manages and executes multiple scheduled jobs.
type Scheduler struct {
	jobs       map[string]Job
	jobCancels map[string]context.CancelFunc
	mu         sync.RWMutex
	ctx        context.Context
	cancelFunc context.CancelFunc
	started    bool
}

// New creates a new Scheduler.
func New(ctx context.Context) *Scheduler {
	ctx, cancel := context.WithCancel(ctx)
	return &Scheduler{
		jobs:       make(map[string]Job),
		jobCancels: make(map[string]context.CancelFunc),
		ctx:        ctx,
		cancelFunc: cancel,
	}
}

// Register adds a job to the scheduler. If the scheduler is already running,
// the job will start immediately.
func (s *Scheduler) Register(job Job) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := job.Name()

	// If job already exists, stop the old one first
	if cancelFn, exists := s.jobCancels[name]; exists {
		cancelFn()
	}

	s.jobs[name] = job

	// If scheduler is already started, start the job immediately
	if s.started {
		s.startJob(job)
	}

	logrus.WithFields(logrus.Fields{
		"job":      name,
		"interval": job.Interval(),
	}).Infoln("registered scheduled job")
}

// Start begins executing all registered jobs.
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return
	}

	s.started = true

	for _, job := range s.jobs {
		s.startJob(job)
	}

	logrus.Infoln("scheduler started")
}

// Unregister removes a job from the scheduler.
func (s *Scheduler) Unregister(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cancelFn, exists := s.jobCancels[name]; exists {
		cancelFn()
		delete(s.jobCancels, name)
		delete(s.jobs, name)
		logrus.WithField("job", name).Infoln("unregistered scheduled job")
	}
}

// Stop gracefully stops the scheduler and all jobs.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Cancel the main context which will stop all jobs
	s.cancelFunc()

	logrus.Infoln("scheduler stopped")
}

// GetJob returns a registered job by name.
func (s *Scheduler) GetJob(name string) (Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, exists := s.jobs[name]
	return job, exists
}

// ListJobs returns the names of all registered jobs.
func (s *Scheduler) ListJobs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.jobs))
	for name := range s.jobs {
		names = append(names, name)
	}
	return names
}

// startJob starts a goroutine that runs the job at its specified interval.
func (s *Scheduler) startJob(job Job) {
	jobCtx, jobCancel := context.WithCancel(s.ctx)
	s.jobCancels[job.Name()] = jobCancel

	go func() {
		ticker := time.NewTicker(job.Interval())
		defer ticker.Stop()

		// Run immediately if configured
		if job.RunOnStart() {
			s.executeJob(jobCtx, job)
		}

		for {
			select {
			case <-jobCtx.Done():
				return
			case <-ticker.C:
				s.executeJob(jobCtx, job)
			}
		}
	}()
}

// executeJob runs a job and logs any errors.
func (s *Scheduler) executeJob(ctx context.Context, job Job) {
	if err := job.Execute(ctx); err != nil {
		logrus.WithError(err).WithField("job", job.Name()).
			Errorln("scheduled job failed")
	}
}
