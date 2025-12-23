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
func (p *EMAWeekendDecayPredictor) calculateEMA(ctx context.Context, input *PredictionInput) (float64, error) {
	// Look back 9 days to ensure we capture at least 5 weekdays
	// (worst case: if today is Monday, we need Mon-Fri of last week + some of this week)
	const maxLookbackDays = 9
	lookbackEnd := input.StartTimestamp
	lookbackStart := lookbackEnd - int64(maxLookbackDays*24*3600) //nolint:mnd

	records, err := p.historyStore.GetUtilizationHistory(
		ctx,
		input.PoolName,
		input.VariantID,
		lookbackStart,
		lookbackEnd,
	)
	if err != nil {
		return 0, err
	}

	if len(records) == 0 {
		return 0, nil
	}

	// Filter out weekend records - only keep weekday data
	weekdayRecords := p.filterWeekdayRecords(records)

	if len(weekdayRecords) == 0 {
		return 0, nil
	}

	// Sort records by timestamp (oldest first)
	sort.Slice(weekdayRecords, func(i, j int) bool {
		return weekdayRecords[i].RecordedAt < weekdayRecords[j].RecordedAt
	})

	// Keep only records from the last 5 weekdays
	weekdayRecords = p.filterLast5Weekdays(weekdayRecords)

	if len(weekdayRecords) == 0 {
		return 0, nil
	}

	// Calculate EMA
	// Alpha (smoothing factor) = 2 / (period + 1)
	alpha := 2.0 / float64(p.config.EMAPeriod+1)

	// Initialize EMA with the first value
	ema := float64(weekdayRecords[0].InUseInstances)

	// Calculate EMA iteratively
	for i := 1; i < len(weekdayRecords); i++ {
		currentValue := float64(weekdayRecords[i].InUseInstances)
		ema = alpha*currentValue + (1-alpha)*ema
	}

	return ema, nil
}

// filterWeekdayRecords removes weekend records from the slice.
func (p *EMAWeekendDecayPredictor) filterWeekdayRecords(records []types.UtilizationRecord) []types.UtilizationRecord {
	result := make([]types.UtilizationRecord, 0, len(records))
	for _, record := range records {
		if !p.isWeekend(record.RecordedAt) {
			result = append(result, record)
		}
	}
	return result
}

// filterLast5Weekdays keeps only records from the last 5 distinct weekdays.
func (p *EMAWeekendDecayPredictor) filterLast5Weekdays(records []types.UtilizationRecord) []types.UtilizationRecord {
	if len(records) == 0 {
		return records
	}

	// Records are sorted oldest first, so we traverse from the end to find the last 5 weekdays
	// First, identify the distinct weekday dates (ignoring time)
	distinctDays := make(map[string]bool)
	var cutoffTimestamp int64

	// Traverse from newest to oldest
	for i := len(records) - 1; i >= 0; i-- {
		t := time.Unix(records[i].RecordedAt, 0).UTC()
		dayKey := t.Format("2006-01-02") // YYYY-MM-DD format

		if !distinctDays[dayKey] {
			distinctDays[dayKey] = true
			if len(distinctDays) == 5 { //nolint:mnd
				cutoffTimestamp = records[i].RecordedAt
				break
			}
		}
	}

	// If we found 5 days, filter records from cutoff onwards
	if cutoffTimestamp > 0 {
		result := make([]types.UtilizationRecord, 0)
		for _, record := range records {
			if record.RecordedAt >= cutoffTimestamp {
				result = append(result, record)
			}
		}
		return result
	}

	// If we don't have 5 distinct days, return all records
	return records
}

// isWeekend returns true if the given timestamp falls on a Saturday or Sunday.
func (p *EMAWeekendDecayPredictor) isWeekend(timestamp int64) bool {
	t := time.Unix(timestamp, 0).UTC()
	weekday := t.Weekday()
	return weekday == time.Saturday || weekday == time.Sunday
}

// calculateHistoricalWithDecay computes the weighted average from 1, 2, and 3 weeks ago.
func (p *EMAWeekendDecayPredictor) calculateHistoricalWithDecay(ctx context.Context, input *PredictionInput) (float64, error) {
	const secondsPerWeek = 7 * 24 * 3600

	weekValues := make([]float64, 3) //nolint:mnd
	weekHasData := make([]bool, 3)   //nolint:mnd

	// Get data from 1, 2, and 3 weeks ago at the same time window
	for week := 1; week <= 3; week++ {
		offset := int64(week * secondsPerWeek)
		historicalStart := input.StartTimestamp - offset
		historicalEnd := input.EndTimestamp - offset

		records, err := p.historyStore.GetUtilizationHistory(
			ctx,
			input.PoolName,
			input.VariantID,
			historicalStart,
			historicalEnd,
		)
		if err != nil {
			return 0, err
		}

		if len(records) > 0 {
			weekValues[week-1] = p.calculatePeakUtilization(records)
			weekHasData[week-1] = true
		}
	}

	// Calculate weighted average with decay factors
	totalWeight := 0.0
	weightedSum := 0.0

	for i := 0; i < 3; i++ {
		if weekHasData[i] {
			weight := p.config.WeekDecayFactors[i]
			weightedSum += weekValues[i] * weight
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
