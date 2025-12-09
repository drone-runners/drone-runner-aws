package predictor

import (
	"context"
	"math"
	"sort"
	"time"
)

// UtilizationRecord represents a single record from the instance_utilization_history table.
type UtilizationRecord struct {
	Pool           string
	VariantID      string
	InUseInstances int
	RecordedAt     int64 // Unix timestamp
}

// HistoryStore defines the interface for fetching utilization history data.
// This interface should be implemented by the database layer.
type HistoryStore interface {
	// GetUtilizationHistory retrieves utilization records for a given pool and variant
	// within the specified time range.
	GetUtilizationHistory(ctx context.Context, pool, variantID string, startTime, endTime int64) ([]UtilizationRecord, error)
}

// EMAWeekendDecayPredictor implements the Predictor interface using a combination of:
// - Exponential Moving Average (EMA) for recent trend analysis
// - Weekend Offset adjustment for day-of-week patterns
// - 3 Week Historical Offset with Decay for seasonal patterns
type EMAWeekendDecayPredictor struct {
	historyStore HistoryStore
	config       PredictorConfig
}

// PredictorConfig contains configuration parameters for the predictor.
type PredictorConfig struct {
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

	// WeekendMultiplier adjusts predictions for weekends.
	// Values < 1.0 reduce weekend predictions. Default: 0.7
	WeekendMultiplier float64

	// WeekdayMultiplier adjusts predictions for weekdays.
	// Default: 1.0
	WeekdayMultiplier float64

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
		EMAPeriod:         12,
		EMAWeight:         0.4,
		WeekDecayFactors:  [3]float64{0.5, 0.3, 0.2},
		WeekendMultiplier: 0.7,
		WeekdayMultiplier: 1.0,
		SafetyBuffer:      0.1,
		MinInstances:      1,
		LookbackHours:     24,
	}
}

// NewEMAWeekendDecayPredictor creates a new predictor with the given history store and config.
func NewEMAWeekendDecayPredictor(store HistoryStore, config PredictorConfig) *EMAWeekendDecayPredictor {
	return &EMAWeekendDecayPredictor{
		historyStore: store,
		config:       config,
	}
}

// NewEMAWeekendDecayPredictorWithDefaults creates a new predictor with default configuration.
func NewEMAWeekendDecayPredictorWithDefaults(store HistoryStore) *EMAWeekendDecayPredictor {
	return NewEMAWeekendDecayPredictor(store, DefaultPredictorConfig())
}

// Name returns the name of this predictor implementation.
func (p *EMAWeekendDecayPredictor) Name() string {
	return "ema-weekend-decay-predictor"
}

// Predict calculates the recommended number of instances using the combined algorithm.
func (p *EMAWeekendDecayPredictor) Predict(ctx context.Context, input *PredictionInput) (*PredictionResult, error) {
	// Step 1: Calculate EMA from recent data
	emaValue, err := p.calculateEMA(ctx, input)
	if err != nil {
		return nil, err
	}

	// Step 2: Calculate 3-week historical average with decay
	historicalValue, err := p.calculateHistoricalWithDecay(ctx, input)
	if err != nil {
		return nil, err
	}

	// Step 3: Combine EMA and historical values
	combinedValue := p.combineValues(emaValue, historicalValue)

	// Step 4: Apply weekend/weekday offset
	adjustedValue := p.applyDayOfWeekOffset(combinedValue, input.StartTimestamp)

	// Step 5: Apply safety buffer
	finalValue := adjustedValue * (1.0 + p.config.SafetyBuffer)

	// Step 6: Round up and ensure minimum
	recommendedInstances := int(math.Ceil(finalValue))
	if recommendedInstances < p.config.MinInstances {
		recommendedInstances = p.config.MinInstances
	}

	return &PredictionResult{
		RecommendedInstances: recommendedInstances,
	}, nil
}

// calculateEMA computes the Exponential Moving Average from recent utilization data.
func (p *EMAWeekendDecayPredictor) calculateEMA(ctx context.Context, input *PredictionInput) (float64, error) {
	// Get recent data for EMA calculation
	lookbackEnd := input.StartTimestamp
	lookbackStart := lookbackEnd - int64(p.config.LookbackHours*3600)

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

	// Sort records by timestamp (oldest first)
	sort.Slice(records, func(i, j int) bool {
		return records[i].RecordedAt < records[j].RecordedAt
	})

	// Calculate EMA
	// Alpha (smoothing factor) = 2 / (period + 1)
	alpha := 2.0 / float64(p.config.EMAPeriod+1)

	// Initialize EMA with the first value
	ema := float64(records[0].InUseInstances)

	// Calculate EMA iteratively
	for i := 1; i < len(records); i++ {
		currentValue := float64(records[i].InUseInstances)
		ema = alpha*currentValue + (1-alpha)*ema
	}

	return ema, nil
}

// calculateHistoricalWithDecay computes the weighted average from 1, 2, and 3 weeks ago.
func (p *EMAWeekendDecayPredictor) calculateHistoricalWithDecay(ctx context.Context, input *PredictionInput) (float64, error) {
	const secondsPerWeek = 7 * 24 * 3600

	weekValues := make([]float64, 3)
	weekHasData := make([]bool, 3)

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
func (p *EMAWeekendDecayPredictor) calculatePeakUtilization(records []UtilizationRecord) float64 {
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

// applyDayOfWeekOffset adjusts the prediction based on whether the target time is a weekend or weekday.
func (p *EMAWeekendDecayPredictor) applyDayOfWeekOffset(value float64, timestamp int64) float64 {
	t := time.Unix(timestamp, 0).UTC()
	weekday := t.Weekday()

	// Saturday (6) or Sunday (0) = weekend
	if weekday == time.Saturday || weekday == time.Sunday {
		return value * p.config.WeekendMultiplier
	}

	return value * p.config.WeekdayMultiplier
}
