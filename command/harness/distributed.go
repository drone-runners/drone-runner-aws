package harness

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/predictor"
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
	Metrics  *metric.Metrics
	Hosted   bool
}

// SetupDistributedMode initializes the distributed pool manager, scheduler, and all related components.
// This is shared between delegate and dlite modes.
func SetupDistributedMode(cfg DistributedSetupConfig) (*DistributedSetupResult, error) {
	logrus.Infoln("Starting postgres database for distributed mode")

	instanceStore, stageOwnerStore, outboxStore, capacityReservationStore, utilizationHistoryStore, capabilityHistoryStore, err := database.ProvideStore(
		cfg.Ctx,
		cfg.Env.DistributedMode.Driver,
		cfg.Env.DistributedMode.Datasource,
		cfg.Env.DistributedMode.IAMAuth,
		cfg.Env.DistributedMode.Region,
	)
	if err != nil {
		logrus.WithError(err).Fatalln("Unable to start the database")
		return nil, err
	}

	// Create a distributed manager
	managerCfg := drivers.NewManagerConfigFromEnv(cfg.Ctx, instanceStore, cfg.Env)
	managerCfg.StageOwnerStore = stageOwnerStore
	managerCfg.CapacityReservationStore = capacityReservationStore
	managerCfg.Hosted = cfg.Hosted
	poolManager := drivers.NewDistributedManager(
		drivers.NewManagerFromConfig(&managerCfg),
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

	algorithm, err := types.ParseSchedulerAlgorithm(cfg.Env.Scheduler.Algorithm)
	if err != nil {
		return nil, err
	}

	// Setup the pool
	poolConfig, err := SetupPoolWithEnv(cfg.Ctx, cfg.Env, poolManager, cfg.PoolFile, cfg.Metrics)
	if err != nil {
		logrus.WithError(err).Error("could not setup distributed pool")
		return nil, err
	}

	catalog, err := capability.NewCatalog(poolConfig, cfg.Env.Scheduler.PredictionDrivers, func(pool string) types.Platform {
		platform, _, _ := poolManager.Inspect(pool)
		return platform
	})
	if err != nil {
		logrus.WithError(err).Error("could not build capability catalog")
		return nil, err
	}
	logrus.WithFields(logrus.Fields{
		"algorithm":          algorithm,
		"prediction_drivers": cfg.Env.Scheduler.PredictionDrivers,
		"pools":              len(catalog.Pools()),
		"capabilities":       len(catalog.Groups()),
	}).Infoln("capability catalog built")

	// Register utilization tracking jobs if stores are available
	if instanceStore != nil && utilizationHistoryStore != nil {
		utilizationTrackerJob := jobs.NewUtilizationTrackerJob(
			instanceStore,
			utilizationHistoryStore,
			time.Duration(cfg.Env.Scheduler.UtilizationTracker.IntervalSecs)*time.Second,
		)
		historyCleanupJob := jobs.NewHistoryCleanupJob(
			utilizationHistoryStore,
			time.Duration(cfg.Env.Scheduler.HistoryCleanup.IntervalHours)*time.Hour,
			time.Duration(cfg.Env.Scheduler.HistoryCleanup.RetentionDays)*24*time.Hour,
		)
		if capabilityHistoryStore != nil {
			utilizationTrackerJob.WithCapabilityHistory(capabilityHistoryStore, catalog)
			historyCleanupJob.WithCapabilityHistory(capabilityHistoryStore)
		}
		sched.Register(utilizationTrackerJob)
		sched.Register(historyCleanupJob)

		if capabilityHistoryStore != nil && cfg.Env.Scheduler.CapabilityBackfill {
			go func() {
				if _, err := jobs.BackfillCapabilityHistory(cfg.Ctx, capabilityHistoryStore, catalog); err != nil {
					logrus.WithError(err).Errorln("capability backfill failed")
				}
			}()
		}
	}

	// Register scaler if enabled and we have the necessary stores
	if instanceStore != nil && utilizationHistoryStore != nil {
		if algorithm == types.SchedulerAlgorithmCapability && capabilityHistoryStore == nil {
			return nil, fmt.Errorf("scheduler algorithm %q requires the capability history store (postgres)", algorithm)
		}
		scalerConfig := types.ScalerConfig{
			Enabled:                 cfg.Env.Scheduler.Scaler.Enabled,
			WindowDuration:          time.Duration(cfg.Env.Scheduler.Scaler.WindowDurationMins) * time.Minute,
			LeadTime:                time.Duration(cfg.Env.Scheduler.Scaler.LeadTimeMins) * time.Minute,
			DryRun:                  cfg.Env.Scheduler.Scaler.DryRun,
			DisabledPools:           cfg.Env.Scheduler.Scaler.DisabledPools,
			ActiveImageLookbackDays: cfg.Env.Scheduler.Scaler.ActiveImageLookbackDays,
			RecentUsageLookbackDays: cfg.Env.Scheduler.Scaler.RecentUsageLookbackDays,
			RecentUsageMinInstances: cfg.Env.Scheduler.Scaler.RecentUsageMinInstances,
			ScalePercent:            cfg.Env.Scheduler.Scaler.ScalePercent,
			PredictionDrivers:       cfg.Env.Scheduler.PredictionDrivers,
			Algorithm:               algorithm,
		}

		// Build scalable pools from pool config
		scalablePools := buildScalablePools(poolConfig)

		// Create predictor with config from environment
		predictorConfig := predictor.PredictorConfig{
			EMAPeriod:        cfg.Env.Scheduler.Predictor.EMAPeriod,
			EMAWeight:        cfg.Env.Scheduler.Predictor.EMAWeight,
			WeekDecayFactors: cfg.Env.PredictorConfig(),
			MinInstances:     cfg.Env.Scheduler.Predictor.MinInstances,
			MaxLookbackDays:  cfg.Env.Scheduler.Predictor.MaxLookbackDays,
			TargetWeekdays:   cfg.Env.Scheduler.Predictor.TargetWeekdays,
		}
		var (
			scaleTargets   jobs.ScaleTargetSource
			scaleJobRunner jobs.ScaleJobRunner
		)
		switch algorithm {
		case types.SchedulerAlgorithmCapability:
			capabilityScaler := jobs.NewCapabilityScaler(
				poolManager,
				predictor.NewEMAWeekendDecayPredictorFromHistory(predictor.NewCapabilityHistoryReader(capabilityHistoryStore), predictorConfig),
				instanceStore,
				capabilityHistoryStore,
				outboxStore,
				catalog,
				scalerConfig,
				cfg.Metrics,
			)
			scaleTargets = jobs.NewCapabilityScaleTargets(catalog, scalerConfig.DisabledPools)
			scaleJobRunner = capabilityScaler
			outboxProcessor.SetSetupFailureHandler(capabilityScaler)
		case types.SchedulerAlgorithmPool:
			scaleTargets = jobs.NewPoolScaleTargets(scalablePools, scalerConfig.PredictionDrivers)
			scaleJobRunner = jobs.NewScaler(
				poolManager,
				predictor.NewEMAWeekendDecayPredictor(utilizationHistoryStore, predictorConfig),
				instanceStore,
				utilizationHistoryStore,
				outboxStore,
				scalerConfig,
				scalablePools,
				cfg.Metrics,
			)
		default:
			return nil, fmt.Errorf("unknown scheduler algorithm %q", algorithm)
		}
		outboxProcessor.SetScaleJobRunner(scaleJobRunner)

		// Create and register scaler trigger job
		scalerTriggerJob := jobs.NewScalerTriggerJob(
			outboxStore,
			scalerConfig,
			cfg.Env.Runner.Name,
			scaleTargets,
		)
		sched.Register(scalerTriggerJob)

		logrus.WithFields(logrus.Fields{
			"enabled":         scalerConfig.Enabled,
			"algorithm":       scalerConfig.Algorithm,
			"window_duration": scalerConfig.WindowDuration,
			"lead_time":       scalerConfig.LeadTime,
			"pools":           len(scalablePools),
		}).Infoln("scaler registered")
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

// buildScalablePools converts pool configuration to scalable pool definitions.
func buildScalablePools(poolConfig *config.PoolFile) []jobs.ScalablePool {
	var scalablePools []jobs.ScalablePool

	for i := range poolConfig.Instances {
		instance := &poolConfig.Instances[i]
		pool := jobs.ScalablePool{
			Name:    instance.Name,
			MinSize: instance.Pool, // Pool field represents min size
			Driver:  instance.Type,
		}

		if len(instance.Tenants) > 0 {
			resolved, _, err := config.ResolveTenants(instance)
			if err != nil {
				logrus.WithError(err).WithField("pool", instance.Name).
					Errorln("scaler: failed to resolve tenants, scaling default only")
				pool.Variants = buildScalableVariants(instance.Variants)
			} else {
				for ri := range resolved {
					rt := &resolved[ri]
					pool.Tenants = append(pool.Tenants, jobs.ScalableTenant{
						ID:       rt.ID,
						MinSize:  rt.Pool,
						Variants: buildScalableVariants(rt.Variants),
					})
				}
			}
		} else {
			// Add variants if any
			pool.Variants = buildScalableVariants(instance.Variants)
		}

		scalablePools = append(scalablePools, pool)
	}

	return scalablePools
}

// buildScalableVariants converts pool variants to scalable variant definitions.
func buildScalableVariants(variants []types.PoolVariant) []jobs.ScalableVariant {
	var out []jobs.ScalableVariant
	for j := range variants {
		variant := &variants[j]
		out = append(out, jobs.ScalableVariant{
			MinSize: variant.Pool, // Pool field represents min size for variant
			Params: types.SetupInstanceParams{
				VariantID:            variant.VariantID,
				ImageName:            variant.ImageName,
				MachineType:          variant.MachineType,
				MachineTypeFallbacks: append([]types.MachineTypeFallback(nil), variant.MachineTypeFallbacks...),
				NestedVirtualization: variant.NestedVirtualization,
				Hibernate:            variant.Hibernate,
				Zones:                variant.Zones,
				DiskSize:             variant.DiskSize,
				DiskType:             variant.DiskType,
				GPU:                  variant.GPU,
			},
		})
	}
	return out
}
