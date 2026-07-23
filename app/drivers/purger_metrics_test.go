// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"sync"
	"time"
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

// hotpoolClaimAttemptRecord captures one call to fakePurgerMetrics.RecordHotpoolClaimAttempt.
type hotpoolClaimAttemptRecord struct {
	poolID, zone, vmType, outcome, reason string
}

// hotpoolStateDurationRecord captures one call to fakePurgerMetrics.RecordHotpoolStateDuration.
type hotpoolStateDurationRecord struct {
	poolID, zone, vmType, state string
	dwell                       time.Duration
}

// fakePurgerMetrics is an in-memory MetricsRecorder test double (covering both the purger and
// hot-pool claim/dwell metrics) that records every call so tests can assert on exactly what was
// reported, instead of exercising real Prometheus collectors.
type fakePurgerMetrics struct {
	mu sync.Mutex

	lastRunPools     []string
	destroyAttempts  []destroyAttemptRecord
	forceDeleted     []forceDeletedRecord
	capacityAttempts []capacityAttemptRecord
	hotpoolClaims    []hotpoolClaimAttemptRecord
	hotpoolStateDurs []hotpoolStateDurationRecord
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

func (f *fakePurgerMetrics) RecordHotpoolClaimAttempt(poolID, zone, vmType, outcome, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hotpoolClaims = append(f.hotpoolClaims, hotpoolClaimAttemptRecord{poolID, zone, vmType, outcome, reason})
}

func (f *fakePurgerMetrics) RecordHotpoolStateDuration(poolID, zone, vmType, state string, dwell time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hotpoolStateDurs = append(f.hotpoolStateDurs, hotpoolStateDurationRecord{poolID, zone, vmType, state, dwell})
}
