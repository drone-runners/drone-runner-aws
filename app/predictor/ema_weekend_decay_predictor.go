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
	// Default: 12 (e.g., 12 hours if data is recorded hourly)
	EMAPeriod int

	// EMAWeight is the weight given to EMA vs historical data (0.0 to 1.0).
	// Higher values favor recent trends. Default: 0.4
	EMAWeight float64

	// WeekDecayFactors are the decay weights for week 1, 2, and 3 ago.
	// Week 1 gets highest weight, Week 3 gets lowest.
	// Default: [0.5, 0.3, 0.2]
	WeekDecayFactors [3]float64

	// SafetyBuffer is a percentage buffer added to predictions (e.g., 0.1 = 10%).
	// Default: 0.1
	SafetyBuffer float64

	// MinInstances is the minimum number of instances to recommend.
	// Default: 1
	MinInstances int

	// LookbackHours is how many hours of recent data to use for EMA calculation.
	// Default: 24
	LookbackHours int
}

// DefaultPredictorConfig returns a PredictorConfig with sensible defaults.
func DefaultPredictorConfig() PredictorConfig {
	return PredictorConfig{
		EMAPeriod:        12,
		EMAWeight:        0.4,
		WeekDecayFactors: [3]float64{0.5, 0.3, 0.2},
		SafetyBuffer:     0.1,
		MinInstances:     1,
		LookbackHours:    24,
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

	// Step 2: Apply safety buffer
	finalValue := baseValue * (1.0 + p.config.SafetyBuffer)

	// Step 3: Round up and ensure minimum
	recommendedInstances := int(math.Ceil(finalValue))
	if recommendedInstances < p.config.MinInstances {
		recommendedInstances = p.config.MinInstances
	}

	return &PredictionResult{
		RecommendedInstances: recommendedInstances,
	}, nil
}

// calculateEMA computes the Exponential Moving Average from the last 5 weekdays' data.
// This function is only called for weekday predictions.
// It fetches only the same time window from each past weekday using a single batch query.
func (p *EMAWeekendDecayPredictor) calculateEMA(ctx context.Context, input *PredictionInput) (float64, error) {
	const (
		maxLookbackDays = 9 // Look back up to 9 days to ensure we capture at least 5 weekdays
		targetWeekdays  = 5
		secondsPerDay   = 24 * 3600
	)

	// Calculate the window duration to apply to each past day
	windowDuration := input.EndTimestamp - input.StartTimestamp

	// Build time ranges for the last 5 weekdays
	var ranges []store.TimeRange
	for daysBack := 1; daysBack <= maxLookbackDays && len(ranges) < targetWeekdays; daysBack++ {
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
		ranges[week-1] = store.TimeRange{
			StartTime: input.StartTimestamp - offset,
			EndTime:   input.EndTimestamp - offset,
		}
	}

	// Fetch all 3 weeks in a single batch query
	batchResults, err := p.historyStore.GetUtilizationHistoryBatch(
		ctx,
		input.PoolName,
		input.VariantID,
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
	// Handle cases where one or both values might be zero
	if emaValue == 0 && historicalValue == 0 {
		return 0
	}

	if emaValue == 0 {
		return historicalValue
	}

	if historicalValue == 0 {
		return emaValue
	}

	// Combine using configured weights
	// EMAWeight determines how much we trust recent trends vs historical patterns
	return p.config.EMAWeight*emaValue + (1-p.config.EMAWeight)*historicalValue
}
