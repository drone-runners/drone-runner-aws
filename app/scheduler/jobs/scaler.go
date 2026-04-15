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
	Driver   string
	MinSize  int
	Variants []ScalableVariant
}

// ScalableVariant represents a variant configuration for scaling.
type ScalableVariant struct {
	MinSize int
	Params  types.SetupInstanceParams
}

// InstanceKey identifies a unique combination of dimensions for counting free instances.
// Add new fields here when new scaling dimensions are introduced.
type InstanceKey struct {
	VariantID string
	ImageName string
}

// Scaler handles scaling pools up and down based on predictions.
type Scaler struct {
	manager       *drivers.DistributedManager
	predictor     predictor.Predictor
	instanceStore store.InstanceStore
	historyStore  store.UtilizationHistoryStore
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
	historyStore store.UtilizationHistoryStore,
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
		historyStore:  historyStore,
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

	// Check if pool is in the disabled list
	if s.isPoolDisabled(poolName) {
		logrus.WithField("pool", poolName).Infoln("scaler: pool is disabled for scaling, skipping")
		return nil
	}

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

	// Get current free instance counts for this pool, grouped by (variant, image)
	freeCounts, err := s.getFreeInstanceCountsForPool(ctx, *targetPool)
	if err != nil {
		return fmt.Errorf("scaler: failed to get free instance counts: %w", err)
	}

	s.scalePoolInternal(ctx, *targetPool, windowStart, windowEnd, freeCounts)

	logrus.WithField("pool", poolName).Infoln("scaler: scaling operation completed for pool")
	return nil
}

// scalePoolInternal scales a single pool and its variants.
// It discovers active images per variant and scales each (variant, image) combination independently.
func (s *Scaler) scalePoolInternal(
	ctx context.Context,
	pool ScalablePool,
	windowStart, windowEnd int64,
	freeCounts map[InstanceKey]int,
) {
	logr := logrus.WithField("pool", pool.Name)

	// Scale the default variant (pool itself)
	s.scaleVariantForActiveImages(ctx, pool, "default", pool.MinSize, nil, windowStart, windowEnd, freeCounts)

	// Scale each variant
	for i := range pool.Variants {
		variant := &pool.Variants[i]
		params := variant.Params // Copy to avoid pointer issues
		s.scaleVariantForActiveImages(ctx, pool, params.VariantID, variant.MinSize, &params, windowStart, windowEnd, freeCounts)
	}

	logr.Debugln("scaler: finished scaling all variants and images")
}

// scaleVariantForActiveImages discovers active images for a variant and scales each (variant, image) pair.
func (s *Scaler) scaleVariantForActiveImages(
	ctx context.Context,
	pool ScalablePool,
	variantID string,
	minSize int,
	params *types.SetupInstanceParams,
	windowStart, windowEnd int64,
	freeCounts map[InstanceKey]int,
) {
	logr := logrus.WithFields(logrus.Fields{
		"pool":       pool.Name,
		"variant_id": variantID,
	})

	// Discover active images from utilization history
	since := time.Now().AddDate(0, 0, -s.config.ActiveImageLookbackDays).Unix()
	activeImages, err := s.historyStore.GetActiveImages(ctx, pool.Name, variantID, since)
	if err != nil {
		logr.WithError(err).Errorln("scaler: failed to get active images")
		return
	}

	for _, imageName := range activeImages {
		if imageName == "" {
			logr.Debugln("scaler: no image name, skipping")
			continue
		}
		if err := s.scaleVariant(ctx, pool, variantID, imageName, minSize, params, windowStart, windowEnd, freeCounts); err != nil {
			logr.WithError(err).WithField("image_name", imageName).
				Errorln("scaler: failed to scale variant for image")
		}
	}
}

// scaleVariant scales a single (variant, image) combination within a pool.
func (s *Scaler) scaleVariant(
	ctx context.Context,
	pool ScalablePool,
	variantID, imageName string,
	minSize int,
	params *types.SetupInstanceParams,
	windowStart, windowEnd int64,
	freeCounts map[InstanceKey]int,
) error {
	logr := logrus.WithFields(logrus.Fields{
		"pool":       pool.Name,
		"variant_id": variantID,
		"image_name": imageName,
	})

	// Get prediction for this pool/variant/image
	prediction, err := s.predictor.Predict(ctx, &predictor.PredictionInput{
		PoolName:       pool.Name,
		VariantID:      variantID,
		ImageName:      imageName,
		StartTimestamp: windowStart,
		EndTimestamp:   windowEnd,
	})
	if err != nil {
		return fmt.Errorf("failed to get prediction: %w", err)
	}

	// Get current free count for this instance key combination
	key := InstanceKey{VariantID: variantID, ImageName: imageName}
	currentFree := freeCounts[key]

	// Ensure we never go below min size
	targetCount := prediction.RecommendedInstances
	if targetCount < minSize {
		targetCount = minSize
	}

	// If prediction is 0 but there was recent usage, apply a minimum floor
	if prediction.RecommendedInstances == 0 && s.config.RecentUsageMinInstances > 0 {
		hasRecentUsage, checkErr := s.hasRecentUsage(ctx, pool.Name, variantID, imageName)
		if checkErr != nil {
			logr.WithError(checkErr).Warnln("scaler: failed to check recent usage, skipping recent usage minimum")
		} else if hasRecentUsage && targetCount < s.config.RecentUsageMinInstances {
			targetCount = s.config.RecentUsageMinInstances
		}
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
	if s.metrics != nil {
		s.metrics.ScalerPredictedInstances.WithLabelValues(pool.Name, variantID, imageName).Set(float64(prediction.RecommendedInstances))
	}

	// If dry run mode is enabled, only record metrics and skip actual scaling
	if s.config.DryRun {
		logr.WithField("dry_run", true).Infoln("scaler: dry run mode enabled, skipping scale operation")
		return nil
	}

	if delta > 0 {
		// Scale up: create instances
		s.scaleUp(ctx, pool, variantID, imageName, params, delta)
	} else if delta < 0 {
		// Scale down: destroy excess instances
		s.scaleDown(ctx, pool.Name, variantID, imageName, -delta)
	}

	return nil
}

// scaleUp creates new instances by adding outbox jobs.
func (s *Scaler) scaleUp(
	ctx context.Context,
	pool ScalablePool,
	variantID, imageName string,
	params *types.SetupInstanceParams,
	count int,
) {
	logr := logrus.WithFields(logrus.Fields{
		"pool":       pool.Name,
		"variant_id": variantID,
		"image_name": imageName,
		"count":      count,
	})

	logr.Infoln("scaler: scaling up")

	successCount := 0
	for i := 0; i < count; i++ {
		// Build setup params - create a copy to avoid modifying the original
		setupParams := &types.SetupInstanceParams{
			VariantID: variantID,
			ImageName: imageName,
			Source:    types.InstanceSourcePredictor,
		}
		if params != nil {
			paramsCopy := *params
			paramsCopy.VariantID = variantID
			paramsCopy.Source = types.InstanceSourcePredictor
			if imageName != "" {
				paramsCopy.ImageName = imageName
			}
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
}

// scaleDown destroys excess free instances.
func (s *Scaler) scaleDown(
	ctx context.Context,
	poolName, variantID, imageName string,
	count int,
) {
	logr := logrus.WithFields(logrus.Fields{
		"pool":       poolName,
		"variant_id": variantID,
		"image_name": imageName,
		"count":      count,
	})

	logr.Infoln("scaler: scaling down")

	// Use FindAndClaim to atomically claim instances for termination
	// This avoids race conditions with other processes
	queryParams := &types.QueryParams{
		PoolName:     poolName,
		VariantID:    variantID,
		ImageName:    imageName,
		FilterSource: types.InstanceSourcePredictor,
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
		logr.WithFields(logrus.Fields{
			"instance_id":    inst.ID,
			"instance_state": string(inst.State),
			"destroy_caller": "scaler:scale_down",
		}).Infoln("scaler: destroying excess instance")
		if err := s.manager.Destroy(ctx, poolName, inst.ID, inst, nil); err != nil {
			logr.WithError(err).WithField("instance_id", inst.ID).
				Errorln("scaler: failed to destroy instance")
			continue
		}
		destroyedCount++
		logr.WithField("instance_id", inst.ID).Infoln("scaler: destroyed excess instance")
	}

	logr.WithField("destroyed_count", destroyedCount).Infoln("scaler: scale down complete")
}

// getFreeInstanceCountsForPool returns free instance counts keyed by InstanceKey for a specific pool.
func (s *Scaler) getFreeInstanceCountsForPool(ctx context.Context, pool ScalablePool) (map[InstanceKey]int, error) {
	instances, err := s.instanceStore.List(ctx, pool.Name, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list instances for pool %s: %w", pool.Name, err)
	}

	counts := make(map[InstanceKey]int)
	for _, inst := range instances {
		if inst.State == types.StateCreated || inst.State == types.StateHibernating || inst.State == types.StateProvisioning {
			key := InstanceKey{VariantID: inst.VariantID, ImageName: inst.Image}
			counts[key]++
		}
	}

	return counts, nil
}

// isPoolDisabled checks if the given pool name is in the disabled pools list.
// hasRecentUsage checks if a variant/image combination had any non-zero utilization
// within the configured lookback window.
func (s *Scaler) hasRecentUsage(ctx context.Context, poolName, variantID, imageName string) (bool, error) {
	since := time.Now().AddDate(0, 0, -s.config.RecentUsageLookbackDays).Unix()
	return s.historyStore.HasRecentUsage(ctx, poolName, variantID, imageName, since)
}

func (s *Scaler) isPoolDisabled(poolName string) bool {
	for _, disabledPool := range s.config.DisabledPools {
		if disabledPool == poolName {
			return true
		}
	}
	return false
}
