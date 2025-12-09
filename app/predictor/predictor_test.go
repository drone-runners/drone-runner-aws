package predictor

import (
	"context"
	"testing"
	"time"
)

// MockHistoryStore is a mock implementation of HistoryStore for testing.
type MockHistoryStore struct {
	records []UtilizationRecord
}

func (m *MockHistoryStore) GetUtilizationHistory(ctx context.Context, pool, variantID string, startTime, endTime int64) ([]UtilizationRecord, error) {
	var result []UtilizationRecord
	for _, r := range m.records {
		if r.Pool == pool && r.VariantID == variantID &&
			r.RecordedAt >= startTime && r.RecordedAt <= endTime {
			result = append(result, r)
		}
	}
	return result, nil
}

func TestEMAWeekendDecayPredictor_Name(t *testing.T) {
	predictor := NewEMAWeekendDecayPredictorWithDefaults(&MockHistoryStore{})
	expected := "ema-weekend-decay-predictor"
	if predictor.Name() != expected {
		t.Errorf("expected name %q, got %q", expected, predictor.Name())
	}
}

func TestEMAWeekendDecayPredictor_Predict_EmptyHistory(t *testing.T) {
	store := &MockHistoryStore{records: []UtilizationRecord{}}
	predictor := NewEMAWeekendDecayPredictorWithDefaults(store)

	input := &PredictionInput{
		PoolName:       "test-pool",
		VariantID:      "variant-1",
		StartTimestamp: time.Now().Unix(),
		EndTimestamp:   time.Now().Add(time.Hour).Unix(),
	}

	result, err := predictor.Predict(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With no historical data, should return minimum instances
	if result.RecommendedInstances != 1 {
		t.Errorf("expected minimum instances 1, got %d", result.RecommendedInstances)
	}
}

func TestEMAWeekendDecayPredictor_Predict_WithRecentData(t *testing.T) {
	now := time.Now()
	// Create hourly data for the past 24 hours with varying utilization
	var records []UtilizationRecord
	for i := 24; i > 0; i-- {
		records = append(records, UtilizationRecord{
			Pool:           "test-pool",
			VariantID:      "variant-1",
			InUseInstances: 10 + (i % 5), // Values between 10-14
			RecordedAt:     now.Add(-time.Duration(i) * time.Hour).Unix(),
		})
	}

	store := &MockHistoryStore{records: records}
	predictor := NewEMAWeekendDecayPredictorWithDefaults(store)

	input := &PredictionInput{
		PoolName:       "test-pool",
		VariantID:      "variant-1",
		StartTimestamp: now.Unix(),
		EndTimestamp:   now.Add(time.Hour).Unix(),
	}

	result, err := predictor.Predict(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should recommend more than minimum based on historical data
	if result.RecommendedInstances < 10 {
		t.Errorf("expected recommended instances >= 10, got %d", result.RecommendedInstances)
	}
}

func TestEMAWeekendDecayPredictor_Predict_WithHistoricalWeekData(t *testing.T) {
	now := time.Now()
	var records []UtilizationRecord

	// Recent data (past 24 hours) - 10 instances avg
	for i := 24; i > 0; i-- {
		records = append(records, UtilizationRecord{
			Pool:           "test-pool",
			VariantID:      "variant-1",
			InUseInstances: 10,
			RecordedAt:     now.Add(-time.Duration(i) * time.Hour).Unix(),
		})
	}

	// 1 week ago data - 20 instances (peak)
	week1Ago := now.Add(-7 * 24 * time.Hour)
	records = append(records, UtilizationRecord{
		Pool:           "test-pool",
		VariantID:      "variant-1",
		InUseInstances: 20,
		RecordedAt:     week1Ago.Unix(),
	})

	// 2 weeks ago data - 15 instances
	week2Ago := now.Add(-14 * 24 * time.Hour)
	records = append(records, UtilizationRecord{
		Pool:           "test-pool",
		VariantID:      "variant-1",
		InUseInstances: 15,
		RecordedAt:     week2Ago.Unix(),
	})

	// 3 weeks ago data - 12 instances
	week3Ago := now.Add(-21 * 24 * time.Hour)
	records = append(records, UtilizationRecord{
		Pool:           "test-pool",
		VariantID:      "variant-1",
		InUseInstances: 12,
		RecordedAt:     week3Ago.Unix(),
	})

	store := &MockHistoryStore{records: records}
	predictor := NewEMAWeekendDecayPredictorWithDefaults(store)

	input := &PredictionInput{
		PoolName:       "test-pool",
		VariantID:      "variant-1",
		StartTimestamp: now.Unix(),
		EndTimestamp:   now.Add(time.Hour).Unix(),
	}

	result, err := predictor.Predict(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should blend EMA (around 10) with historical weighted avg
	// Historical: (20*0.5 + 15*0.3 + 12*0.2) / 1.0 = 16.9
	// Combined: 0.4*10 + 0.6*16.9 = 14.14
	// With 10% safety buffer: ~15.5, ceil = 16
	if result.RecommendedInstances < 10 || result.RecommendedInstances > 20 {
		t.Errorf("expected recommended instances between 10-20, got %d", result.RecommendedInstances)
	}
}

func TestEMAWeekendDecayPredictor_WeekendMultiplier(t *testing.T) {
	// Find a Saturday timestamp
	now := time.Now()
	daysUntilSaturday := (6 - int(now.Weekday()) + 7) % 7
	if daysUntilSaturday == 0 {
		daysUntilSaturday = 7
	}
	saturday := now.Add(time.Duration(daysUntilSaturday) * 24 * time.Hour)

	// Find a Tuesday timestamp
	daysUntilTuesday := (2 - int(now.Weekday()) + 7) % 7
	if daysUntilTuesday == 0 {
		daysUntilTuesday = 7
	}
	tuesday := now.Add(time.Duration(daysUntilTuesday) * 24 * time.Hour)

	// Create consistent historical data
	var records []UtilizationRecord
	for week := 1; week <= 3; week++ {
		for _, targetDay := range []time.Time{saturday, tuesday} {
			historicalTime := targetDay.Add(-time.Duration(week) * 7 * 24 * time.Hour)
			// Add data around the target time
			for i := -12; i <= 12; i++ {
				records = append(records, UtilizationRecord{
					Pool:           "test-pool",
					VariantID:      "variant-1",
					InUseInstances: 100,
					RecordedAt:     historicalTime.Add(time.Duration(i) * time.Hour).Unix(),
				})
			}
		}
	}

	store := &MockHistoryStore{records: records}

	config := DefaultPredictorConfig()
	config.WeekendMultiplier = 0.5 // 50% for weekends
	config.WeekdayMultiplier = 1.0
	config.SafetyBuffer = 0 // Disable buffer for easier testing
	config.EMAWeight = 0    // Use only historical data for predictable results

	predictor := NewEMAWeekendDecayPredictor(store, config)

	// Predict for Saturday
	saturdayInput := &PredictionInput{
		PoolName:       "test-pool",
		VariantID:      "variant-1",
		StartTimestamp: saturday.Unix(),
		EndTimestamp:   saturday.Add(time.Hour).Unix(),
	}

	saturdayResult, err := predictor.Predict(context.Background(), saturdayInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Predict for Tuesday
	tuesdayInput := &PredictionInput{
		PoolName:       "test-pool",
		VariantID:      "variant-1",
		StartTimestamp: tuesday.Unix(),
		EndTimestamp:   tuesday.Add(time.Hour).Unix(),
	}

	tuesdayResult, err := predictor.Predict(context.Background(), tuesdayInput)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Saturday should be lower than Tuesday due to weekend multiplier
	if saturdayResult.RecommendedInstances >= tuesdayResult.RecommendedInstances {
		t.Errorf("expected weekend (%d) < weekday (%d)",
			saturdayResult.RecommendedInstances, tuesdayResult.RecommendedInstances)
	}
}

func TestEMAWeekendDecayPredictor_DecayWeights(t *testing.T) {
	now := time.Now()

	// Only add data for 1 week ago - should use only that data
	var records []UtilizationRecord
	week1Ago := now.Add(-7 * 24 * time.Hour)
	records = append(records, UtilizationRecord{
		Pool:           "test-pool",
		VariantID:      "variant-1",
		InUseInstances: 50,
		RecordedAt:     week1Ago.Unix(),
	})

	store := &MockHistoryStore{records: records}
	config := DefaultPredictorConfig()
	config.SafetyBuffer = 0
	config.WeekdayMultiplier = 1.0
	config.WeekendMultiplier = 1.0
	config.EMAWeight = 0 // Use only historical

	predictor := NewEMAWeekendDecayPredictor(store, config)

	input := &PredictionInput{
		PoolName:       "test-pool",
		VariantID:      "variant-1",
		StartTimestamp: now.Unix(),
		EndTimestamp:   now.Add(time.Hour).Unix(),
	}

	result, err := predictor.Predict(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With only week 1 data at 50 instances, should recommend ~50
	if result.RecommendedInstances != 50 {
		t.Errorf("expected 50 instances, got %d", result.RecommendedInstances)
	}
}

func TestDefaultPredictorConfig(t *testing.T) {
	config := DefaultPredictorConfig()

	if config.EMAPeriod != 12 {
		t.Errorf("expected EMAPeriod 12, got %d", config.EMAPeriod)
	}
	if config.EMAWeight != 0.4 {
		t.Errorf("expected EMAWeight 0.4, got %f", config.EMAWeight)
	}
	if config.WeekDecayFactors[0] != 0.5 {
		t.Errorf("expected week 1 decay 0.5, got %f", config.WeekDecayFactors[0])
	}
	if config.WeekDecayFactors[1] != 0.3 {
		t.Errorf("expected week 2 decay 0.3, got %f", config.WeekDecayFactors[1])
	}
	if config.WeekDecayFactors[2] != 0.2 {
		t.Errorf("expected week 3 decay 0.2, got %f", config.WeekDecayFactors[2])
	}
	if config.WeekendMultiplier != 0.7 {
		t.Errorf("expected WeekendMultiplier 0.7, got %f", config.WeekendMultiplier)
	}
	if config.SafetyBuffer != 0.1 {
		t.Errorf("expected SafetyBuffer 0.1, got %f", config.SafetyBuffer)
	}
	if config.MinInstances != 1 {
		t.Errorf("expected MinInstances 1, got %d", config.MinInstances)
	}
}

func TestCalculatePeakUtilization(t *testing.T) {
	predictor := NewEMAWeekendDecayPredictorWithDefaults(&MockHistoryStore{})

	tests := []struct {
		name     string
		records  []UtilizationRecord
		expected float64
	}{
		{
			name:     "empty records",
			records:  []UtilizationRecord{},
			expected: 0,
		},
		{
			name: "single record",
			records: []UtilizationRecord{
				{InUseInstances: 10},
			},
			expected: 10,
		},
		{
			name: "multiple records - find peak",
			records: []UtilizationRecord{
				{InUseInstances: 5},
				{InUseInstances: 15},
				{InUseInstances: 10},
				{InUseInstances: 8},
			},
			expected: 15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := predictor.calculatePeakUtilization(tt.records)
			if result != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, result)
			}
		})
	}
}

func TestCombineValues(t *testing.T) {
	config := DefaultPredictorConfig()
	config.EMAWeight = 0.4
	predictor := NewEMAWeekendDecayPredictor(&MockHistoryStore{}, config)

	tests := []struct {
		name       string
		ema        float64
		historical float64
		expected   float64
	}{
		{
			name:       "both zero",
			ema:        0,
			historical: 0,
			expected:   0,
		},
		{
			name:       "ema only",
			ema:        10,
			historical: 0,
			expected:   10,
		},
		{
			name:       "historical only",
			ema:        0,
			historical: 10,
			expected:   10,
		},
		{
			name:       "both values - weighted combination",
			ema:        10,
			historical: 20,
			expected:   16, // 0.4*10 + 0.6*20 = 4 + 12 = 16
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := predictor.combineValues(tt.ema, tt.historical)
			if result != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, result)
			}
		})
	}
}

func TestApplyDayOfWeekOffset(t *testing.T) {
	config := DefaultPredictorConfig()
	config.WeekendMultiplier = 0.5
	config.WeekdayMultiplier = 1.0
	predictor := NewEMAWeekendDecayPredictor(&MockHistoryStore{}, config)

	// Create timestamps for each day of the week
	// Start from a known date: January 1, 2024 was a Monday
	monday := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		time     time.Time
		value    float64
		expected float64
	}{
		{"Monday", monday, 100, 100},
		{"Tuesday", monday.Add(24 * time.Hour), 100, 100},
		{"Wednesday", monday.Add(2 * 24 * time.Hour), 100, 100},
		{"Thursday", monday.Add(3 * 24 * time.Hour), 100, 100},
		{"Friday", monday.Add(4 * 24 * time.Hour), 100, 100},
		{"Saturday", monday.Add(5 * 24 * time.Hour), 100, 50},
		{"Sunday", monday.Add(6 * 24 * time.Hour), 100, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := predictor.applyDayOfWeekOffset(tt.value, tt.time.Unix())
			if result != tt.expected {
				t.Errorf("expected %f, got %f", tt.expected, result)
			}
		})
	}
}
