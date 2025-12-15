package predictor

import (
	"time"
)

// GenerateMockHistoryData creates 3 weeks of utilization history data
// with records every 2 minutes. The data simulates realistic usage patterns:
// - Higher utilization during weekday business hours (9 AM - 6 PM UTC)
// - Lower utilization during nights and weekends
// - Some variance to simulate real-world fluctuations
func GenerateMockHistoryData(pool, variantID string, referenceTime time.Time) []UtilizationRecord {
	var records []UtilizationRecord

	const (
		recordInterval   = 2 * time.Minute // Record every 2 minutes
		threeWeeks       = 3 * 7 * 24 * time.Hour
		baseUtilization  = 5  // Minimum instances
		peakUtilization  = 25 // Peak during busy hours
		nightUtilization = 3  // Night time minimum
	)

	// Start from 3 weeks ago
	startTime := referenceTime.Add(-threeWeeks)

	for t := startTime; t.Before(referenceTime); t = t.Add(recordInterval) {
		hour := t.Hour()
		weekday := t.Weekday()

		var instances int

		// Determine base utilization based on time of day and day of week
		isWeekend := weekday == time.Saturday || weekday == time.Sunday
		isBusinessHours := hour >= 9 && hour <= 18

		if isWeekend {
			// Weekend: lower utilization
			if isBusinessHours {
				instances = baseUtilization + 3 // Light weekend activity
			} else {
				instances = nightUtilization
			}
		} else {
			// Weekday
			if isBusinessHours {
				// Peak hours: ramp up and down
				if hour >= 10 && hour <= 16 {
					instances = peakUtilization // Core business hours
				} else {
					instances = baseUtilization + 10 // Shoulder hours
				}
			} else if hour >= 6 && hour < 9 {
				// Morning ramp up
				instances = baseUtilization + (hour-6)*3
			} else if hour > 18 && hour <= 21 {
				// Evening ramp down
				instances = baseUtilization + (21-hour)*2
			} else {
				// Night time
				instances = nightUtilization
			}
		}

		// Add some variance based on minute to simulate fluctuations
		minute := t.Minute()
		variance := (minute % 5) - 2 // -2 to +2 variance
		instances += variance
		if instances < 1 {
			instances = 1
		}

		records = append(records, UtilizationRecord{
			Pool:           pool,
			VariantID:      variantID,
			InUseInstances: instances,
			RecordedAt:     t.Unix(),
		})
	}

	return records
}

// GenerateMockHistoryDataWithSpikes creates 3 weeks of utilization history
// with occasional traffic spikes to test predictor's handling of anomalies.
func GenerateMockHistoryDataWithSpikes(pool, variantID string, referenceTime time.Time) []UtilizationRecord {
	records := GenerateMockHistoryData(pool, variantID, referenceTime)

	// Add spikes at specific times (simulating deployments or traffic bursts)
	spikeHours := []int{10, 14, 16} // Hours when spikes occur

	for i := range records {
		t := time.Unix(records[i].RecordedAt, 0).UTC()

		// Add spike on Tuesdays and Thursdays at specific hours
		if t.Weekday() == time.Tuesday || t.Weekday() == time.Thursday {
			for _, spikeHour := range spikeHours {
				if t.Hour() == spikeHour && t.Minute() < 30 {
					records[i].InUseInstances += 15 // Add spike
				}
			}
		}
	}

	return records
}

// GenerateMockHistoryDataGradualIncrease creates 3 weeks of data with a
// gradual increase trend to test predictor's trend detection.
func GenerateMockHistoryDataGradualIncrease(pool, variantID string, referenceTime time.Time) []UtilizationRecord {
	records := GenerateMockHistoryData(pool, variantID, referenceTime)

	threeWeeksAgo := referenceTime.Add(-3 * 7 * 24 * time.Hour)

	for i := range records {
		t := time.Unix(records[i].RecordedAt, 0).UTC()

		// Calculate how many days from start
		daysSinceStart := t.Sub(threeWeeksAgo).Hours() / 24

		// Add 1 instance per 3 days of progression (gradual increase)
		increase := int(daysSinceStart / 3) //nolint:mnd
		records[i].InUseInstances += increase
	}

	return records
}
