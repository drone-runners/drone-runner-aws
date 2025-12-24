package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/predictor"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

const (
	// DefaultWindowDuration is the default scaling window duration
	DefaultWindowDuration = 30 * time.Minute
	// DefaultLeadTime is how long before a window to start scaling
	DefaultLeadTime = 5 * time.Minute
)

// ScalablePool represents a pool and its variants that can be scaled.
type ScalablePool struct {
	Name     string
	MinSize  int
	Variants []ScalableVariant
}

// ScalableVariant represents a variant configuration for scaling.
type ScalableVariant struct {
	MinSize int
	Params  types.SetupInstanceParams
}

// Scaler handles scaling pools up and down based on predictions.
type Scaler struct {
	manager       *drivers.DistributedManager
	predictor     predictor.Predictor
	instanceStore store.InstanceStore
	outboxStore   store.OutboxStore
	config        types.ScalerConfig
	poolsToScale  []ScalablePool
	metrics       *metric.Metrics
}

// NewScaler creates a new Scaler.
func NewScaler(
	manager *drivers.DistributedManager,
	pred predictor.Predictor,
	instanceStore store.InstanceStore,
	outboxStore store.OutboxStore,
	config types.ScalerConfig,
	pools []ScalablePool,
	metrics *metric.Metrics,
) *Scaler {
	if config.WindowDuration == 0 {
		config.WindowDuration = DefaultWindowDuration
	}
	if config.LeadTime == 0 {
		config.LeadTime = DefaultLeadTime
	}

	return &Scaler{
		manager:       manager,
		predictor:     pred,
		instanceStore: instanceStore,
		outboxStore:   outboxStore,
		config:        config,
		poolsToScale:  pools,
		metrics:       metrics,
	}
}

// ScalePool scales a specific pool and its variants based on predictions for the given window.
// This is called by the outbox processor for each pool-specific scale job.
func (s *Scaler) ScalePool(ctx context.Context, poolName string, windowStart, windowEnd int64) error {
	logrus.WithFields(logrus.Fields{
		"pool":         poolName,
		"window_start": time.Unix(windowStart, 0).Format(time.RFC3339),
		"window_end":   time.Unix(windowEnd, 0).Format(time.RFC3339),
	}).Infoln("scaler: starting scaling operation for pool")

	// Find the pool to scale
	var targetPool *ScalablePool
	for i := range s.poolsToScale {
		if s.poolsToScale[i].Name == poolName {
			targetPool = &s.poolsToScale[i]
			break
		}
	}

	if targetPool == nil {
		logrus.WithField("pool", poolName).Errorln("scaler: pool not found in scalable pools")
		return nil
	}

	// Get current free instance counts for this pool
	freeCountsByPoolVariant, err := s.getFreeInstanceCountsForPool(ctx, *targetPool)
	if err != nil {
		return fmt.Errorf("scaler: failed to get free instance counts: %w", err)
	}

	if err := s.scalePoolInternal(ctx, *targetPool, windowStart, windowEnd, freeCountsByPoolVariant); err != nil {
		return fmt.Errorf("scaler: failed to scale pool %s: %w", poolName, err)
	}

	logrus.WithField("pool", poolName).Infoln("scaler: scaling operation completed for pool")
	return nil
}

// scalePoolInternal scales a single pool and its variants.
func (s *Scaler) scalePoolInternal(
	ctx context.Context,
	pool ScalablePool,
	windowStart, windowEnd int64,
	freeCountsByVariant map[string]int,
) error {
	logr := logrus.WithField("pool", pool.Name)

	// Scale the default variant (pool itself)
	if err := s.scaleVariant(ctx, pool, "default", pool.MinSize, nil, windowStart, windowEnd, freeCountsByVariant); err != nil {
		logr.WithError(err).Errorln("scaler: failed to scale default variant")
	}

	// Scale each variant
	for _, variant := range pool.Variants {
		params := variant.Params // Copy to avoid pointer issues
		if err := s.scaleVariant(ctx, pool, params.VariantID, variant.MinSize, &params, windowStart, windowEnd, freeCountsByVariant); err != nil {
			logr.WithError(err).WithField("variant_id", params.VariantID).
				Errorln("scaler: failed to scale variant")
		}
	}

	return nil
}

// scaleVariant scales a single variant within a pool.
func (s *Scaler) scaleVariant(
	ctx context.Context,
	pool ScalablePool,
	variantID string,
	minSize int,
	params *types.SetupInstanceParams,
	windowStart, windowEnd int64,
	freeCountsByVariant map[string]int,
) error {
	logr := logrus.WithFields(logrus.Fields{
		"pool":       pool.Name,
		"variant_id": variantID,
	})

	// Get prediction for this pool/variant
	prediction, err := s.predictor.Predict(ctx, &predictor.PredictionInput{
		PoolName:       pool.Name,
		VariantID:      variantID,
		StartTimestamp: windowStart,
		EndTimestamp:   windowEnd,
	})
	if err != nil {
		return fmt.Errorf("failed to get prediction: %w", err)
	}

	// Get current free count
	currentFree := 0
	if count, ok := freeCountsByVariant[variantID]; ok {
		currentFree = count
	}

	// Ensure we never go below min size
	targetCount := prediction.RecommendedInstances
	if targetCount < minSize {
		targetCount = minSize
	}

	delta := targetCount - currentFree

	logr.WithFields(logrus.Fields{
		"current_free": currentFree,
		"predicted":    prediction.RecommendedInstances,
		"target":       targetCount,
		"min_size":     minSize,
		"delta":        delta,
	}).Infoln("scaler: calculated scaling delta")

	// Record metrics
	s.metrics.ScalerPredictedInstances.WithLabelValues(pool.Name, variantID).Set(float64(prediction.RecommendedInstances))

	if delta > 0 {
		// Scale up: create instances
		if err := s.scaleUp(ctx, pool, variantID, params, delta); err != nil {
			return fmt.Errorf("failed to scale up: %w", err)
		}
	} else if delta < 0 {
		// Scale down: destroy excess instances
		if err := s.scaleDown(ctx, pool.Name, variantID, -delta); err != nil {
			return fmt.Errorf("failed to scale down: %w", err)
		}
	}

	return nil
}

// scaleUp creates new instances by adding outbox jobs.
func (s *Scaler) scaleUp(
	ctx context.Context,
	pool ScalablePool,
	variantID string,
	params *types.SetupInstanceParams,
	count int,
) error {
	logr := logrus.WithFields(logrus.Fields{
		"pool":       pool.Name,
		"variant_id": variantID,
		"count":      count,
	})

	logr.Infoln("scaler: scaling up")

	successCount := 0
	for i := 0; i < count; i++ {
		// Build setup params - create a copy to avoid modifying the original
		setupParams := &types.SetupInstanceParams{
			VariantID: variantID,
		}
		if params != nil {
			paramsCopy := *params
			paramsCopy.VariantID = variantID
			setupParams = &paramsCopy
		}

		// Marshal params to JSON
		paramsJSON, err := json.Marshal(setupParams)
		if err != nil {
			logr.WithError(err).Errorln("scaler: failed to marshal setup params")
			continue
		}
		rawMsg := json.RawMessage(paramsJSON)

		// Create outbox job for instance setup
		// Leave RunnerName empty so any runner can process this job
		job := &types.OutboxJob{
			PoolName:  pool.Name,
			JobType:   types.OutboxJobTypeSetupInstance,
			JobParams: &rawMsg,
			Status:    types.OutboxJobStatusPending,
		}

		if err := s.outboxStore.Create(ctx, job); err != nil {
			logr.WithError(err).Errorln("scaler: failed to create setup instance job")
			continue
		}

		successCount++
		logr.WithField("job_id", job.ID).Debugln("scaler: created setup instance job")
	}

	return nil
}

// scaleDown destroys excess free instances.
func (s *Scaler) scaleDown(
	ctx context.Context,
	poolName, variantID string,
	count int,
) error {
	logr := logrus.WithFields(logrus.Fields{
		"pool":       poolName,
		"variant_id": variantID,
		"count":      count,
	})

	logr.Infoln("scaler: scaling down")

	// Use FindAndClaim to atomically claim instances for termination
	// This avoids race conditions with other processes
	queryParams := &types.QueryParams{
		PoolName:  poolName,
		VariantID: variantID,
	}

	allowedStates := []types.InstanceState{types.StateCreated, types.StateHibernating}

	destroyedCount := 0
	for i := 0; i < count; i++ {
		// Atomically find and claim a free instance, setting it to terminating state
		inst, err := s.instanceStore.FindAndClaim(ctx, queryParams, types.StateTerminating, allowedStates, false)
		if err != nil {
			logr.WithError(err).Warnln("scaler: failed to find and claim instance for scale down")
			break
		}
		if inst == nil {
			logr.Debugln("scaler: no more free instances to remove")
			break
		}

		// Destroy the claimed instance
		if err := s.manager.Destroy(ctx, poolName, inst.ID, inst, nil); err != nil {
			logr.WithError(err).WithField("instance_id", inst.ID).
				Errorln("scaler: failed to destroy instance")
			continue
		}
		destroyedCount++
		logr.WithField("instance_id", inst.ID).Infoln("scaler: destroyed excess instance")
	}

	logr.WithField("destroyed_count", destroyedCount).Infoln("scaler: scale down complete")
	return nil
}

// getFreeInstanceCountsForPool returns a map of variant -> free count for a specific pool.
func (s *Scaler) getFreeInstanceCountsForPool(ctx context.Context, pool ScalablePool) (map[string]int, error) {
	instances, err := s.instanceStore.List(ctx, pool.Name, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list instances for pool %s: %w", pool.Name, err)
	}

	variantCounts := make(map[string]int)
	for _, inst := range instances {
		if inst.State == types.StateCreated || inst.State == types.StateHibernating {
			variantCounts[inst.VariantID]++
		}
	}

	return variantCounts, nil
}
