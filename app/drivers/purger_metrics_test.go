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

// vmCreationAttemptRecord captures one call to fakePurgerMetrics.RecordVMCreationAttempt.
type vmCreationAttemptRecord struct {
	poolID, zone, vmType, source, outcome, reason string
}

// vmCreationDurationRecord captures one call to fakePurgerMetrics.RecordVMCreationDuration.
type vmCreationDurationRecord struct {
	poolID, zone, vmType, source, outcome string
	duration                              time.Duration
}

// vmUsageDurationRecord captures one call to fakePurgerMetrics.RecordVMUsageDuration.
type vmUsageDurationRecord struct {
	poolID, zone, vmType, source, terminationReason string
	dwell                                           time.Duration
}

// vmHibernateAttemptRecord captures one call to fakePurgerMetrics.RecordVMHibernateAttempt.
type vmHibernateAttemptRecord struct {
	poolID, zone, vmType, outcome, reason string
}

// vmHibernateDurationRecord captures one call to fakePurgerMetrics.RecordVMHibernateDuration.
type vmHibernateDurationRecord struct {
	poolID, zone, vmType, outcome string
	duration                      time.Duration
}

// vmResumeAttemptRecord captures one call to fakePurgerMetrics.RecordVMResumeAttempt.
type vmResumeAttemptRecord struct {
	poolID, zone, vmType, outcome, reason string
}

// vmResumeDurationRecord captures one call to fakePurgerMetrics.RecordVMResumeDuration.
type vmResumeDurationRecord struct {
	poolID, zone, vmType, outcome string
	duration                      time.Duration
}

// vmResumeToReadyDurationRecord captures one call to fakePurgerMetrics.RecordVMResumeToReadyDuration.
type vmResumeToReadyDurationRecord struct {
	poolID, zone, vmType, outcome string
	duration                      time.Duration
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
	vmCreationAtts   []vmCreationAttemptRecord
	vmCreationDurs   []vmCreationDurationRecord
	vmUsageDurs      []vmUsageDurationRecord
	vmHibernateAtts  []vmHibernateAttemptRecord
	vmHibernateDurs  []vmHibernateDurationRecord
	vmResumeAtts     []vmResumeAttemptRecord
	vmResumeDurs     []vmResumeDurationRecord
	vmResumeToReady  []vmResumeToReadyDurationRecord
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

func (f *fakePurgerMetrics) RecordVMCreationAttempt(poolID, zone, vmType, source, outcome, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmCreationAtts = append(f.vmCreationAtts, vmCreationAttemptRecord{poolID, zone, vmType, source, outcome, reason})
}

func (f *fakePurgerMetrics) RecordVMCreationDuration(poolID, zone, vmType, source, outcome string, duration time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmCreationDurs = append(f.vmCreationDurs, vmCreationDurationRecord{poolID, zone, vmType, source, outcome, duration})
}

func (f *fakePurgerMetrics) RecordVMUsageDuration(poolID, zone, vmType, source, terminationReason string, dwell time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmUsageDurs = append(f.vmUsageDurs, vmUsageDurationRecord{poolID, zone, vmType, source, terminationReason, dwell})
}

func (f *fakePurgerMetrics) RecordVMHibernateAttempt(poolID, zone, vmType, outcome, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmHibernateAtts = append(f.vmHibernateAtts, vmHibernateAttemptRecord{poolID, zone, vmType, outcome, reason})
}

func (f *fakePurgerMetrics) RecordVMHibernateDuration(poolID, zone, vmType, outcome string, duration time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmHibernateDurs = append(f.vmHibernateDurs, vmHibernateDurationRecord{poolID, zone, vmType, outcome, duration})
}

func (f *fakePurgerMetrics) RecordVMResumeAttempt(poolID, zone, vmType, outcome, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmResumeAtts = append(f.vmResumeAtts, vmResumeAttemptRecord{poolID, zone, vmType, outcome, reason})
}

func (f *fakePurgerMetrics) RecordVMResumeDuration(poolID, zone, vmType, outcome string, duration time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmResumeDurs = append(f.vmResumeDurs, vmResumeDurationRecord{poolID, zone, vmType, outcome, duration})
}

func (f *fakePurgerMetrics) RecordVMResumeToReadyDuration(poolID, zone, vmType, outcome string, duration time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vmResumeToReady = append(f.vmResumeToReady, vmResumeToReadyDurationRecord{poolID, zone, vmType, outcome, duration})
}
