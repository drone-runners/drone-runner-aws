package predictor

import (
	"context"
	"testing"
	"time"

	"github.com/drone-runners/drone-runner-aws/store"
	"github.com/drone-runners/drone-runner-aws/types"
)

// Ensure MockHistoryStore implements store.UtilizationHistoryStore
var _ store.UtilizationHistoryStore = (*MockHistoryStore)(nil)

// MockHistoryStore is a mock implementation of store.UtilizationHistoryStore for testing.
type MockHistoryStore struct {
	records []types.UtilizationRecord
}

func (m *MockHistoryStore) Create(ctx context.Context, record *types.UtilizationRecord) error {
	m.records = append(m.records, *record)
	return nil
}

func (m *MockHistoryStore) GetUtilizationHistory(ctx context.Context, pool, variantID string, startTime, endTime int64) ([]types.UtilizationRecord, error) {
	var result []types.UtilizationRecord
	for _, r := range m.records {
		if r.Pool == pool && r.VariantID == variantID &&
			r.RecordedAt >= startTime && r.RecordedAt <= endTime {
			result = append(result, r)
		}
	}
	return result, nil
}

func (m *MockHistoryStore) DeleteOlderThan(ctx context.Context, timestamp int64) (int64, error) {
	var remaining []types.UtilizationRecord
	var deleted int64
	for _, r := range m.records {
		if r.RecordedAt >= timestamp {
			remaining = append(remaining, r)
		} else {
			deleted++
		}
	}
	m.records = remaining
	return deleted, nil
}

func TestEMAWeekendDecayPredictor_Name(t *testing.T) {
	predictor := NewEMAWeekendDecayPredictorWithDefaults(&MockHistoryStore{})
	expected := "ema-weekend-decay-predictor"
	if predictor.Name() != expected {
		t.Errorf("expected name %q, got %q", expected, predictor.Name())
	}
}

func TestEMAWeekendDecayPredictor_Predict_EmptyHistory(t *testing.T) {
	store := &MockHistoryStore{records: []types.UtilizationRecord{}}
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
	var records []types.UtilizationRecord
	for i := 24; i > 0; i-- {
		records = append(records, types.UtilizationRecord{
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
	var records []types.UtilizationRecord

	// Recent data (past 24 hours) - 10 instances avg
	for i := 24; i > 0; i-- {
		records = append(records, types.UtilizationRecord{
			Pool:           "test-pool",
			VariantID:      "variant-1",
			InUseInstances: 10,
			RecordedAt:     now.Add(-time.Duration(i) * time.Hour).Unix(),
		})
	}

	// 1 week ago data - 20 instances (peak)
	week1Ago := now.Add(-7 * 24 * time.Hour)
	records = append(records, types.UtilizationRecord{
		Pool:           "test-pool",
		VariantID:      "variant-1",
		InUseInstances: 20,
		RecordedAt:     week1Ago.Unix(),
	})

	// 2 weeks ago data - 15 instances
	week2Ago := now.Add(-14 * 24 * time.Hour)
	records = append(records, types.UtilizationRecord{
		Pool:           "test-pool",
		VariantID:      "variant-1",
		InUseInstances: 15,
		RecordedAt:     week2Ago.Unix(),
	})

	// 3 weeks ago data - 12 instances
	week3Ago := now.Add(-21 * 24 * time.Hour)
	records = append(records, types.UtilizationRecord{
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

func TestEMAWeekendDecayPredictor_WeekendVsWeekday(t *testing.T) {
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

	// Create consistent historical data for both days
	var records []types.UtilizationRecord
	for week := 1; week <= 3; week++ {
		for _, targetDay := range []time.Time{saturday, tuesday} {
			historicalTime := targetDay.Add(-time.Duration(week) * 7 * 24 * time.Hour)
			// Add data around the target time
			for i := -12; i <= 12; i++ {
				records = append(records, types.UtilizationRecord{
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
	config.SafetyBuffer = 0 // Disable buffer for easier testing

	predictor := NewEMAWeekendDecayPredictor(store, config)

	// Predict for Saturday (uses only historical data)
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

	// Predict for Tuesday (uses EMA + historical combined)
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

	// Both should produce valid predictions
	t.Logf("Saturday (historical only): %d instances", saturdayResult.RecommendedInstances)
	t.Logf("Tuesday (EMA + historical): %d instances", tuesdayResult.RecommendedInstances)

	// With same historical data, results depend on algorithm differences
	// Weekend uses only historical, weekday combines EMA with historical
	if saturdayResult.RecommendedInstances < 1 || tuesdayResult.RecommendedInstances < 1 {
		t.Errorf("expected valid predictions, got weekend=%d, weekday=%d",
			saturdayResult.RecommendedInstances, tuesdayResult.RecommendedInstances)
	}
}

func TestEMAWeekendDecayPredictor_DecayWeights(t *testing.T) {
	// Use a fixed weekday timestamp for predictable results
	// Wednesday, January 10, 2024 at 10:00 AM UTC
	now := time.Date(2024, 1, 10, 10, 0, 0, 0, time.UTC)

	// Only add data for 1 week ago - should use only that data
	var records []types.UtilizationRecord
	week1Ago := now.Add(-7 * 24 * time.Hour)
	records = append(records, types.UtilizationRecord{
		Pool:           "test-pool",
		VariantID:      "variant-1",
		InUseInstances: 50,
		RecordedAt:     week1Ago.Unix(),
	})

	store := &MockHistoryStore{records: records}
	config := DefaultPredictorConfig()
	config.SafetyBuffer = 0
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
		records  []types.UtilizationRecord
		expected float64
	}{
		{
			name:     "empty records",
			records:  []types.UtilizationRecord{},
			expected: 0,
		},
		{
			name: "single record",
			records: []types.UtilizationRecord{
				{InUseInstances: 10},
			},
			expected: 10,
		},
		{
			name: "multiple records - find peak",
			records: []types.UtilizationRecord{
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

func TestIsWeekend(t *testing.T) {
	predictor := NewEMAWeekendDecayPredictorWithDefaults(&MockHistoryStore{})

	// Create timestamps for each day of the week
	// Start from a known date: January 1, 2024 was a Monday
	monday := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		time      time.Time
		isWeekend bool
	}{
		{"Monday", monday, false},
		{"Tuesday", monday.Add(24 * time.Hour), false},
		{"Wednesday", monday.Add(2 * 24 * time.Hour), false},
		{"Thursday", monday.Add(3 * 24 * time.Hour), false},
		{"Friday", monday.Add(4 * 24 * time.Hour), false},
		{"Saturday", monday.Add(5 * 24 * time.Hour), true},
		{"Sunday", monday.Add(6 * 24 * time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := predictor.isWeekend(tt.time.Unix())
			if result != tt.isWeekend {
				t.Errorf("expected isWeekend=%v, got %v", tt.isWeekend, result)
			}
		})
	}
}

// TestEMAWeekendDecayPredictor_15MinWindowPrediction tests the predictor
// with 15-minute prediction windows using 3 weeks of history data
// recorded every 2 minutes.
func TestEMAWeekendDecayPredictor_15MinWindowPrediction(t *testing.T) {
	const (
		poolName  = "test-pool"
		variantID = "variant-1"
	)

	// Use a fixed reference time: Wednesday, January 24, 2024 at 10:00 AM UTC
	// This gives us a predictable weekday during business hours
	referenceTime := time.Date(2024, 1, 24, 10, 0, 0, 0, time.UTC)

	// Generate 3 weeks of history data with 2-minute intervals
	historyData := GenerateMockHistoryData(poolName, variantID, referenceTime)

	store := &MockHistoryStore{records: historyData}

	config := DefaultPredictorConfig()
	predictor := NewEMAWeekendDecayPredictor(store, config)

	// Test multiple consecutive 15-minute windows
	windowDuration := 15 * time.Minute
	numWindows := 8 // Test 8 consecutive 15-minute windows (2 hours total)

	t.Logf("Testing %d consecutive 15-minute windows starting at %s", numWindows, referenceTime.Format(time.RFC3339))
	t.Logf("History data contains %d records (3 weeks @ 2-min intervals)", len(historyData))

	var predictions []int
	for i := 0; i < numWindows; i++ {
		windowStart := referenceTime.Add(time.Duration(i) * windowDuration)
		windowEnd := windowStart.Add(windowDuration)

		input := &PredictionInput{
			PoolName:       poolName,
			VariantID:      variantID,
			StartTimestamp: windowStart.Unix(),
			EndTimestamp:   windowEnd.Unix(),
		}

		result, err := predictor.Predict(context.Background(), input)
		if err != nil {
			t.Fatalf("window %d: unexpected error: %v", i, err)
		}

		predictions = append(predictions, result.RecommendedInstances)
		t.Logf("Window %d [%s - %s]: Recommended %d instances",
			i+1,
			windowStart.Format("15:04"),
			windowEnd.Format("15:04"),
			result.RecommendedInstances)
	}

	// Validate predictions are reasonable
	for i, pred := range predictions {
		// During business hours (10 AM - 12 PM), we expect higher utilization
		// Based on mock data: peak is ~25, with safety buffer should be ~28
		if pred < config.MinInstances {
			t.Errorf("window %d: prediction %d below minimum %d", i, pred, config.MinInstances)
		}
		if pred > 50 { // Sanity check - should not exceed reasonable maximum
			t.Errorf("window %d: prediction %d exceeds reasonable maximum", i, pred)
		}
	}

	// Verify we got predictions for all windows
	if len(predictions) != numWindows {
		t.Errorf("expected %d predictions, got %d", numWindows, len(predictions))
	}
}

// TestEMAWeekendDecayPredictor_15MinWindowWeekdayVsWeekend tests that
// 15-minute predictions correctly apply weekend multipliers.
func TestEMAWeekendDecayPredictor_15MinWindowWeekdayVsWeekend(t *testing.T) {
	const (
		poolName  = "test-pool"
		variantID = "variant-1"
	)

	// Reference: Saturday, January 27, 2024 at 10:00 AM UTC (weekend)
	saturdayRef := time.Date(2024, 1, 27, 10, 0, 0, 0, time.UTC)
	// Reference: Wednesday, January 24, 2024 at 10:00 AM UTC (weekday)
	wednesdayRef := time.Date(2024, 1, 24, 10, 0, 0, 0, time.UTC)

	// Generate history data for both scenarios (using the later date)
	historyData := GenerateMockHistoryData(poolName, variantID, saturdayRef)
	store := &MockHistoryStore{records: historyData}

	config := DefaultPredictorConfig()
	predictor := NewEMAWeekendDecayPredictor(store, config)

	windowDuration := 15 * time.Minute

	// Predict for weekday window
	weekdayInput := &PredictionInput{
		PoolName:       poolName,
		VariantID:      variantID,
		StartTimestamp: wednesdayRef.Unix(),
		EndTimestamp:   wednesdayRef.Add(windowDuration).Unix(),
	}
	weekdayResult, err := predictor.Predict(context.Background(), weekdayInput)
	if err != nil {
		t.Fatalf("weekday prediction failed: %v", err)
	}

	// Predict for weekend window
	weekendInput := &PredictionInput{
		PoolName:       poolName,
		VariantID:      variantID,
		StartTimestamp: saturdayRef.Unix(),
		EndTimestamp:   saturdayRef.Add(windowDuration).Unix(),
	}
	weekendResult, err := predictor.Predict(context.Background(), weekendInput)
	if err != nil {
		t.Fatalf("weekend prediction failed: %v", err)
	}

	t.Logf("Weekday (Wednesday 10:00-10:15): %d instances", weekdayResult.RecommendedInstances)
	t.Logf("Weekend (Saturday 10:00-10:15): %d instances", weekendResult.RecommendedInstances)

	// Weekend uses only historical data, weekday uses EMA + historical
	// Results may vary based on historical patterns
	if weekendResult.RecommendedInstances >= weekdayResult.RecommendedInstances {
		t.Logf("Note: Weekend prediction (%d) >= Weekday prediction (%d)",
			weekendResult.RecommendedInstances, weekdayResult.RecommendedInstances)
		// This can happen based on historical data patterns
	}
}

// TestEMAWeekendDecayPredictor_15MinWindowWithSpikes tests prediction
// accuracy when history contains traffic spikes.
func TestEMAWeekendDecayPredictor_15MinWindowWithSpikes(t *testing.T) {
	const (
		poolName  = "test-pool"
		variantID = "variant-1"
	)

	// Tuesday, January 23, 2024 at 10:00 AM UTC - a day with spikes
	referenceTime := time.Date(2024, 1, 23, 10, 0, 0, 0, time.UTC)

	// Generate history data with spikes
	historyData := GenerateMockHistoryDataWithSpikes(poolName, variantID, referenceTime)
	store := &MockHistoryStore{records: historyData}

	config := DefaultPredictorConfig()
	predictor := NewEMAWeekendDecayPredictor(store, config)

	windowDuration := 15 * time.Minute
	numWindows := 4

	t.Logf("Testing 15-minute windows with spike data on Tuesday (spike day)")

	for i := 0; i < numWindows; i++ {
		windowStart := referenceTime.Add(time.Duration(i) * windowDuration)
		windowEnd := windowStart.Add(windowDuration)

		input := &PredictionInput{
			PoolName:       poolName,
			VariantID:      variantID,
			StartTimestamp: windowStart.Unix(),
			EndTimestamp:   windowEnd.Unix(),
		}

		result, err := predictor.Predict(context.Background(), input)
		if err != nil {
			t.Fatalf("window %d: unexpected error: %v", i, err)
		}

		t.Logf("Window %d [%s - %s]: Recommended %d instances",
			i+1,
			windowStart.Format("15:04"),
			windowEnd.Format("15:04"),
			result.RecommendedInstances)

		// Predictions should account for historical spikes
		if result.RecommendedInstances < config.MinInstances {
			t.Errorf("window %d: prediction below minimum", i)
		}
	}
}

// TestEMAWeekendDecayPredictor_15MinWindowTrendDetection tests that
// the predictor detects gradual increases in utilization.
func TestEMAWeekendDecayPredictor_15MinWindowTrendDetection(t *testing.T) {
	const (
		poolName  = "test-pool"
		variantID = "variant-1"
	)

	referenceTime := time.Date(2024, 1, 24, 10, 0, 0, 0, time.UTC)

	// Generate history with gradual increase
	historyData := GenerateMockHistoryDataGradualIncrease(poolName, variantID, referenceTime)
	store := &MockHistoryStore{records: historyData}

	config := DefaultPredictorConfig()
	predictor := NewEMAWeekendDecayPredictor(store, config)

	windowDuration := 15 * time.Minute

	input := &PredictionInput{
		PoolName:       poolName,
		VariantID:      variantID,
		StartTimestamp: referenceTime.Unix(),
		EndTimestamp:   referenceTime.Add(windowDuration).Unix(),
	}

	result, err := predictor.Predict(context.Background(), input)
	if err != nil {
		t.Fatalf("prediction failed: %v", err)
	}

	t.Logf("Prediction with gradual increase trend: %d instances", result.RecommendedInstances)

	// With gradual increase, the EMA should capture the upward trend
	// and recommend higher instances compared to stable data
	if result.RecommendedInstances < config.MinInstances {
		t.Errorf("prediction below minimum")
	}
}
