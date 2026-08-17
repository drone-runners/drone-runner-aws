// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"sync"
)

// destroyAttemptRecord captures one call to fakePurgerMetrics.RecordInstanceDestroyAttempt.
type destroyAttemptRecord struct {
	poolID, zone, reason, outcome string
}

// capacityAttemptRecord captures one call to fakePurgerMetrics.RecordCapacityDestroyAttempt.
type capacityAttemptRecord struct {
	poolID, reason, outcome string
}

// forceDeletedRecord captures one call to fakePurgerMetrics.RecordInstancesForceDeleted.
type forceDeletedRecord struct {
	poolID, cleanupType string
	count               int
}

// fakePurgerMetrics is an in-memory MetricsRecorder test double that records every call so
// tests can assert on exactly what was reported, instead of exercising real Prometheus
// collectors.
type fakePurgerMetrics struct {
	mu sync.Mutex

	lastRunPools     []string
	destroyAttempts  []destroyAttemptRecord
	forceDeleted     []forceDeletedRecord
	capacityAttempts []capacityAttemptRecord
}

func (f *fakePurgerMetrics) RecordPurgerLastRun(poolID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRunPools = append(f.lastRunPools, poolID)
}

func (f *fakePurgerMetrics) RecordInstanceDestroyAttempt(poolID, zone, reason, outcome string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroyAttempts = append(f.destroyAttempts, destroyAttemptRecord{poolID, zone, reason, outcome})
}

func (f *fakePurgerMetrics) RecordInstancesForceDeleted(poolID, cleanupType string, count int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forceDeleted = append(f.forceDeleted, forceDeletedRecord{poolID, cleanupType, count})
}

func (f *fakePurgerMetrics) RecordCapacityDestroyAttempt(poolID, reason, outcome string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.capacityAttempts = append(f.capacityAttempts, capacityAttemptRecord{poolID, reason, outcome})
}
