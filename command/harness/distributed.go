package harness

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/scheduler"
	"github.com/drone-runners/drone-runner-aws/app/scheduler/jobs"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/store/database"
	"github.com/drone-runners/drone-runner-aws/types"
)

// DistributedSetupResult contains all the components initialized during distributed setup
type DistributedSetupResult struct {
	PoolManager              drivers.IManager
	InstanceStore            store.InstanceStore
	StageOwnerStore          store.StageOwnerStore
	CapacityReservationStore store.CapacityReservationStore
	Scheduler                *scheduler.Scheduler
	PoolConfig               *config.PoolFile
}

// DistributedSetupConfig contains configuration needed for distributed setup
type DistributedSetupConfig struct {
	Ctx      context.Context
	Env      *config.EnvConfig
	PoolFile string
}

// SetupDistributedMode initializes the distributed pool manager, scheduler, and all related components.
// This is shared between delegate and dlite modes.
func SetupDistributedMode(cfg DistributedSetupConfig) (*DistributedSetupResult, error) {
	logrus.Infoln("Starting postgres database for distributed mode")

	instanceStore, stageOwnerStore, outboxStore, capacityReservationStore, utilizationHistoryStore, err := database.ProvideStore(
		cfg.Env.DistributedMode.Driver,
		cfg.Env.DistributedMode.Datasource,
	)
	if err != nil {
		logrus.WithError(err).Fatalln("Unable to start the database")
		return nil, err
	}

	// Create a distributed manager
	poolManager := drivers.NewDistributedManager(
		drivers.NewManager(
			cfg.Ctx,
			instanceStore,
			stageOwnerStore,
			capacityReservationStore,
			types.Tmate(cfg.Env.Tmate),
			cfg.Env.Runner.Name,
			cfg.Env.LiteEngine.Path,
			cfg.Env.Settings.HarnessTestBinaryURI,
			cfg.Env.Settings.PluginBinaryURI,
			cfg.Env.Settings.AutoInjectionBinaryURI,
			cfg.Env.LiteEngine.FallbackPath,
			cfg.Env.Settings.PluginBinaryFallbackURI,
			types.RunnerConfig(cfg.Env.RunnerConfig),
			cfg.Env.Settings.AnnotationsBinaryURI,
			cfg.Env.Settings.AnnotationsBinaryFallbackURI,
		),
		outboxStore,
	)

	// Initialize scheduler and register jobs
	sched := scheduler.New(cfg.Ctx)

	// Register outbox processor jobs
	outboxProcessor := jobs.NewOutboxProcessor(
		poolManager,
		outboxStore,
		time.Duration(cfg.Env.OutboxProcessor.RetryIntervalSecs)*time.Second,
		cfg.Env.OutboxProcessor.MaxRetries,
		cfg.Env.OutboxProcessor.BatchSize,
	)

	outboxProcessorJob := jobs.NewOutboxProcessorJob(
		outboxProcessor,
		time.Duration(cfg.Env.OutboxProcessor.PollIntervalSecs)*time.Second,
	)
	sched.Register(outboxProcessorJob)

	outboxCleanupJob := jobs.NewOutboxCleanupJob(
		outboxProcessor,
		1*time.Hour, //nolint:mnd
	)
	sched.Register(outboxCleanupJob)

	// Register utilization tracking jobs if stores are available
	if instanceStore != nil && utilizationHistoryStore != nil {
		utilizationTrackerJob := jobs.NewUtilizationTrackerJob(
			instanceStore,
			utilizationHistoryStore,
			time.Duration(cfg.Env.Scheduler.UtilizationTracker.IntervalSecs)*time.Second,
		)
		sched.Register(utilizationTrackerJob)

		historyCleanupJob := jobs.NewHistoryCleanupJob(
			utilizationHistoryStore,
			time.Duration(cfg.Env.Scheduler.HistoryCleanup.IntervalHours)*time.Hour,
			time.Duration(cfg.Env.Scheduler.HistoryCleanup.RetentionDays)*24*time.Hour,
		)
		sched.Register(historyCleanupJob)
	}

	// Setup the pool
	poolConfig, err := SetupPoolWithEnv(cfg.Ctx, cfg.Env, poolManager, cfg.PoolFile)
	if err != nil {
		logrus.WithError(err).Error("could not setup distributed pool")
		return nil, err
	}

	return &DistributedSetupResult{
		PoolManager:              poolManager,
		InstanceStore:            instanceStore,
		StageOwnerStore:          stageOwnerStore,
		CapacityReservationStore: capacityReservationStore,
		Scheduler:                sched,
		PoolConfig:               poolConfig,
	}, nil
}

// RegisterDistributedMetrics adds metrics for distributed mode
func RegisterDistributedMetrics(ctx context.Context, metrics *metric.Metrics, result *DistributedSetupResult, runnerName string) {
	metrics.AddMetricStore(&metric.Store{
		Store: result.InstanceStore,
		Query: &types.QueryParams{
			RunnerName: runnerName,
		},
		Manager:     result.PoolManager,
		PoolConfig:  result.PoolConfig,
		Distributed: true,
	})
	metrics.UpdateRunningCount(ctx)
	metrics.UpdateWarmPoolCount(ctx)
}
