package predictor

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// EMAWeekendDecayPredictor implements the Predictor interface using a combination of:
// - Exponential Moving Average (EMA) for recent trend analysis
// - Weekend Offset adjustment for day-of-week patterns
// - 3 Week Historical Offset with Decay for seasonal patterns
type EMAWeekendDecayPredictor struct {
	historyStore store.UtilizationHistoryStore
	config       PredictorConfig
}

// PredictorConfig contains configuration parameters for the predictor.
type PredictorConfig struct { //nolint:revive
	// EMAPeriod is the number of data points to consider for EMA calculation.
	// Default: 3 (alpha = 2/4 = 0.5, highly responsive to recent data)
	EMAPeriod int

	// EMAWeight is the weight given to EMA vs historical data (0.0 to 1.0).
	// Higher values favor recent trends. Default: 0.85
	EMAWeight float64

	// WeekDecayFactors are the decay weights for week 1, 2, and 3 ago.
	// Week 1 gets highest weight, Week 3 gets lowest.
	// Default: [0.85, 0.10, 0.05]
	WeekDecayFactors [3]float64

	// ScalePercent is the percentage of predicted VMs to target (e.g., 115 = 115% of predicted, 80 = 80%).
	// Values above 100 over-provision, values below 100 under-provision.
	// Default: 100 (no adjustment)
	ScalePercent float64

	// MinInstances is the minimum number of instances to recommend.
	// Default: 0
	MinInstances int

	// MaxLookbackDays is the maximum number of days to look back to find weekday data.
	// Default: 4 (to ensure we capture at least 2 weekdays)
	MaxLookbackDays int

	// TargetWeekdays is the number of weekdays to use for EMA calculation.
	// Default: 2
	TargetWeekdays int
}

// DefaultPredictorConfig returns a PredictorConfig with sensible defaults.
func DefaultPredictorConfig() PredictorConfig {
	return PredictorConfig{
		EMAPeriod:        3,
		EMAWeight:        0.85,
		WeekDecayFactors: [3]float64{0.85, 0.10, 0.05},
		ScalePercent:     100,
		MinInstances:     0,
		MaxLookbackDays:  4,
		TargetWeekdays:   2,
	}
}

// NewEMAWeekendDecayPredictor creates a new predictor with the given history store and config.
func NewEMAWeekendDecayPredictor(historyStore store.UtilizationHistoryStore, config PredictorConfig) *EMAWeekendDecayPredictor { //nolint:gocritic
	return &EMAWeekendDecayPredictor{
		historyStore: historyStore,
		config:       config,
	}
}

// NewEMAWeekendDecayPredictorWithDefaults creates a new predictor with default configuration.
func NewEMAWeekendDecayPredictorWithDefaults(historyStore store.UtilizationHistoryStore) *EMAWeekendDecayPredictor {
	return NewEMAWeekendDecayPredictor(historyStore, DefaultPredictorConfig())
}

// Name returns the name of this predictor implementation.
func (p *EMAWeekendDecayPredictor) Name() string {
	return "ema-weekend-decay-predictor"
}

// Predict calculates the recommended number of instances using the combined algorithm.
func (p *EMAWeekendDecayPredictor) Predict(ctx context.Context, input *PredictionInput) (*PredictionResult, error) {
	// Check if target time is a weekend
	isWeekend := p.isWeekend(input.StartTimestamp)

	// Step 1: Calculate 3-week historical average with decay (always needed)
	historicalValue, err := p.calculateHistoricalWithDecay(ctx, input)
	if err != nil {
		return nil, err
	}

	var baseValue float64

	if isWeekend {
		// For weekends: rely only on 3-week historical decay
		baseValue = historicalValue
	} else {
		// For weekdays: use EMA from last 5 weekdays combined with historical
		emaValue, err := p.calculateEMA(ctx, input)
		if err != nil {
			return nil, err
		}

		// Combine EMA and historical values
		baseValue = p.combineValues(emaValue, historicalValue)
	}

	// Step 2: Apply scale percent adjustment
	finalValue := baseValue * (p.config.ScalePercent / 100.0)

	// Step 3: Round up and ensure minimum
	recommendedInstances := int(math.Ceil(finalValue))
	if recommendedInstances < p.config.MinInstances {
		recommendedInstances = p.config.MinInstances
	}

	return &PredictionResult{
		RecommendedInstances: recommendedInstances,
	}, nil
}

// calculateEMA computes the Exponential Moving Average from the last N weekdays' data.
// This function is only called for weekday predictions.
// It fetches only the same time window from each past weekday using a single batch query.
func (p *EMAWeekendDecayPredictor) calculateEMA(ctx context.Context, input *PredictionInput) (float64, error) {
	const secondsPerDay = 24 * 3600

	// Calculate the window duration to apply to each past day
	windowDuration := input.EndTimestamp - input.StartTimestamp

	// Build time ranges for the last N weekdays (configured via TargetWeekdays)
	var ranges []store.TimeRange
	for daysBack := 1; daysBack <= p.config.MaxLookbackDays && len(ranges) < p.config.TargetWeekdays; daysBack++ {
		offset := int64(daysBack * secondsPerDay)
		historicalStart := input.StartTimestamp - offset
		historicalEnd := historicalStart + windowDuration

		// Skip weekends
		if p.isWeekend(historicalStart) {
			continue
		}

		ranges = append(ranges, store.TimeRange{
			StartTime: historicalStart,
			EndTime:   historicalEnd,
		})
	}

	if len(ranges) == 0 {
		return 0, nil
	}

	// Fetch all time ranges in a single batch query
	batchResults, err := p.historyStore.GetUtilizationHistoryBatch(
		ctx,
		input.PoolName,
		input.VariantID,
		input.ImageName,
		ranges,
	)
	if err != nil {
		return 0, err
	}

	// Flatten all records (they're already sorted by time within the batch query)
	var allRecords []types.UtilizationRecord
	for _, records := range batchResults {
		allRecords = append(allRecords, records...)
	}

	if len(allRecords) == 0 {
		return 0, nil
	}

	// Sort records by timestamp (oldest first) for proper EMA calculation
	sort.Slice(allRecords, func(i, j int) bool {
		return allRecords[i].RecordedAt < allRecords[j].RecordedAt
	})

	// Calculate EMA
	// Alpha (smoothing factor) = 2 / (period + 1)
	alpha := 2.0 / float64(p.config.EMAPeriod+1)

	// Initialize EMA with the first value
	ema := float64(allRecords[0].InUseInstances)

	// Calculate EMA iteratively
	for i := 1; i < len(allRecords); i++ {
		currentValue := float64(allRecords[i].InUseInstances)
		ema = alpha*currentValue + (1-alpha)*ema
	}

	return ema, nil
}

// isWeekend returns true if the given timestamp falls on a Saturday or Sunday.
func (p *EMAWeekendDecayPredictor) isWeekend(timestamp int64) bool {
	t := time.Unix(timestamp, 0).UTC()
	weekday := t.Weekday()
	return weekday == time.Saturday || weekday == time.Sunday
}

// calculateHistoricalWithDecay computes the weighted average from 1, 2, and 3 weeks ago.
// It fetches all 3 weeks of data in a single batch query.
func (p *EMAWeekendDecayPredictor) calculateHistoricalWithDecay(ctx context.Context, input *PredictionInput) (float64, error) {
	const secondsPerWeek = 7 * 24 * 3600

	// Build time ranges for 1, 2, and 3 weeks ago
	ranges := make([]store.TimeRange, 3) //nolint:mnd
	for week := 1; week <= 3; week++ {
		offset := int64(week * secondsPerWeek)
		ranges[week-1] = store.TimeRange{ //nolint:gosec
			StartTime: input.StartTimestamp - offset,
			EndTime:   input.EndTimestamp - offset,
		}
	}

	// Fetch all 3 weeks in a single batch query
	batchResults, err := p.historyStore.GetUtilizationHistoryBatch(
		ctx,
		input.PoolName,
		input.VariantID,
		input.ImageName,
		ranges,
	)
	if err != nil {
		return 0, err
	}

	// Calculate weighted average with decay factors
	totalWeight := 0.0
	weightedSum := 0.0

	for i, records := range batchResults {
		if len(records) > 0 {
			peakValue := p.calculatePeakUtilization(records)
			weight := p.config.WeekDecayFactors[i]
			weightedSum += peakValue * weight
			totalWeight += weight
		}
	}

	if totalWeight == 0 {
		return 0, nil
	}

	return weightedSum / totalWeight, nil
}

// calculatePeakUtilization returns the peak (maximum) utilization from a set of records.
// Using peak instead of average provides a safety margin for capacity planning.
func (p *EMAWeekendDecayPredictor) calculatePeakUtilization(records []types.UtilizationRecord) float64 {
	if len(records) == 0 {
		return 0
	}

	peak := 0
	for _, record := range records {
		if record.InUseInstances > peak {
			peak = record.InUseInstances
		}
	}

	return float64(peak)
}

// combineValues combines EMA and historical values using configured weights.
func (p *EMAWeekendDecayPredictor) combineValues(emaValue, historicalValue float64) float64 {
	// Combine using configured weights
	// EMAWeight determines how much we trust recent trends vs historical patterns
	// When EMA is 0 (e.g. variant load dropped to zero), the weighted formula
	// naturally produces a low value (only historicalValue * (1-EMAWeight))
	// instead of falling back to 100% historical.
	return p.config.EMAWeight*emaValue + (1-p.config.EMAWeight)*historicalValue
}
