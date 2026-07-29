// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/drone-runners/drone-runner-aws/types"
)

func newManagerPurgerTestPool(driver *flexibleMockDriver) *poolEntry {
	return &poolEntry{
		Pool: Pool{
			Name:   "pool1",
			Driver: driver,
		},
	}
}

func TestManager_PurgeStaleInstancesForPool_StuckProvisioningReason(t *testing.T) {
	now := time.Now()
	fresh := &types.Instance{ID: "busy-fresh", Pool: "pool1", State: types.StateInUse, Zone: "us-east1-a", Started: now.Add(-10 * time.Second).Unix()}
	stuckProvisioning := &types.Instance{ID: "prov-stuck", Pool: "pool1", State: types.StateProvisioning, Zone: "us-east1-b", Started: now.Add(-40 * time.Minute).Unix()}

	instanceStore := &mockInstanceStore{
		ListFunc: func(_ context.Context, _ string, _ *types.QueryParams) ([]*types.Instance, error) {
			return []*types.Instance{fresh, stuckProvisioning}, nil
		},
		DeleteFunc: func(_ context.Context, _ string) error { return nil },
	}

	var destroyed []string
	driver := &flexibleMockDriver{
		driverName: "mock",
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			for _, i := range instances {
				destroyed = append(destroyed, i.ID)
			}
			return nil, nil
		},
	}

	fakeMetrics := &fakePurgerMetrics{}
	m := &Manager{instanceStore: instanceStore, metrics: fakeMetrics}
	pool := newManagerPurgerTestPool(driver)

	err := m.purgeStaleInstancesForPool(context.Background(), pool, "server", time.Hour, time.Hour)
	assert.NoError(t, err)

	// Only the stuck-provisioning instance should have been destroyed; the fresh busy instance
	// is well within maxAgeBusy and must be left alone.
	assert.ElementsMatch(t, []string{"prov-stuck"}, destroyed)

	if assert.Len(t, fakeMetrics.destroyAttempts, 1) {
		rec := fakeMetrics.destroyAttempts[0]
		assert.Equal(t, "pool1", rec.poolID)
		assert.Equal(t, "us-east1-b", rec.zone)
		assert.Equal(t, PurgerReasonStuckProvisioning, rec.reason)
		assert.Equal(t, PurgerOutcomeDestroyed, rec.outcome)
	}

	// The last-run gauge must be recorded for the pool even though only one of the two listed
	// instances was actually stale.
	assert.Contains(t, fakeMetrics.lastRunPools, "pool1")
}

func TestManager_PurgeStaleInstancesForPool_DestroyFailureRecordsFailedOutcome(t *testing.T) {
	now := time.Now()
	staleBusy := &types.Instance{ID: "busy-stale", Pool: "pool1", State: types.StateInUse, Zone: "us-east1-a", Started: now.Add(-2 * time.Hour).Unix()}

	instanceStore := &mockInstanceStore{
		ListFunc: func(_ context.Context, _ string, _ *types.QueryParams) ([]*types.Instance, error) {
			return []*types.Instance{staleBusy}, nil
		},
	}

	driver := &flexibleMockDriver{
		driverName: "mock",
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			// Report every instance passed in as failed.
			return instances, assert.AnError
		},
	}

	fakeMetrics := &fakePurgerMetrics{}
	m := &Manager{instanceStore: instanceStore, metrics: fakeMetrics}
	pool := newManagerPurgerTestPool(driver)

	err := m.purgeStaleInstancesForPool(context.Background(), pool, "server", time.Hour, time.Hour)
	assert.NoError(t, err, "a failed destroy should not bubble up as a sweep error")

	if assert.Len(t, fakeMetrics.destroyAttempts, 1) {
		rec := fakeMetrics.destroyAttempts[0]
		assert.Equal(t, PurgerReasonBusyMaxAge, rec.reason)
		assert.Equal(t, PurgerOutcomeFailedLeftForRetry, rec.outcome, "failed destroy must not be reported as destroyed")
	}
}

func TestManager_PurgeStaleInstancesForPool_BusyDestroy_RecordsVMUsageDuration(t *testing.T) {
	now := time.Now()
	usageStart := now.Add(-90 * time.Minute)
	staleBusy := &types.Instance{
		ID: "busy-stale", Pool: "pool1", State: types.StateInUse, Zone: "us-east1-a",
		Size: "n1-standard-2", Source: types.InstanceSourcePool,
		Started: now.Add(-2 * time.Hour).Unix(), Updated: usageStart.Unix(),
	}

	instanceStore := &mockInstanceStore{
		ListFunc: func(_ context.Context, _ string, _ *types.QueryParams) ([]*types.Instance, error) {
			return []*types.Instance{staleBusy}, nil
		},
		DeleteFunc: func(_ context.Context, _ string) error { return nil },
	}

	driver := &flexibleMockDriver{
		driverName: "mock",
		DestroyFunc: func(_ context.Context, _ []*types.Instance) ([]*types.Instance, error) {
			return nil, nil // every instance destroyed successfully
		},
	}

	fakeMetrics := &fakePurgerMetrics{}
	m := &Manager{instanceStore: instanceStore, metrics: fakeMetrics}
	pool := newManagerPurgerTestPool(driver)

	err := m.purgeStaleInstancesForPool(context.Background(), pool, "server", time.Hour, time.Hour)
	assert.NoError(t, err)

	if assert.Len(t, fakeMetrics.vmUsageDurs, 1) {
		rec := fakeMetrics.vmUsageDurs[0]
		assert.Equal(t, "pool1", rec.poolID)
		assert.Equal(t, "us-east1-a", rec.zone)
		assert.Equal(t, "n1-standard-2", rec.vmType)
		assert.Equal(t, string(types.InstanceSourcePool), rec.source)
		assert.Equal(t, VMTerminationReasonPurgerStale, rec.terminationReason)
		// dwell should be ~90 minutes (usageStart to now), with generous slack for test runtime.
		assert.InDelta(t, 90*time.Minute.Seconds(), rec.dwell.Seconds(), 30)
	}
}

func TestManager_PurgeStaleInstancesForPool_FreeDestroy_DoesNotRecordVMUsageDuration(t *testing.T) {
	// Free (never-claimed) instances were never "in use", so no usage duration should be
	// recorded when the purger destroys a stale free instance.
	now := time.Now()
	staleFree := &types.Instance{
		ID: "free-stale", Pool: "pool1", State: types.StateCreated, Zone: "us-east1-a",
		Started: now.Add(-2 * time.Hour).Unix(), Updated: now.Add(-2 * time.Hour).Unix(),
	}

	instanceStore := &mockInstanceStore{
		ListFunc: func(_ context.Context, _ string, _ *types.QueryParams) ([]*types.Instance, error) {
			return []*types.Instance{staleFree}, nil
		},
		DeleteFunc: func(_ context.Context, _ string) error { return nil },
	}

	driver := &flexibleMockDriver{
		driverName: "mock",
		DestroyFunc: func(_ context.Context, _ []*types.Instance) ([]*types.Instance, error) {
			return nil, nil
		},
	}

	fakeMetrics := &fakePurgerMetrics{}
	m := &Manager{instanceStore: instanceStore, metrics: fakeMetrics}
	pool := newManagerPurgerTestPool(driver)

	err := m.purgeStaleInstancesForPool(context.Background(), pool, "server", time.Hour, time.Hour)
	assert.NoError(t, err)
	assert.Empty(t, fakeMetrics.vmUsageDurs)
}

func TestManager_PurgeStaleInstancesForPool_NilMetricsSafe(t *testing.T) {
	instanceStore := &mockInstanceStore{
		ListFunc: func(_ context.Context, _ string, _ *types.QueryParams) ([]*types.Instance, error) {
			return nil, nil
		},
	}
	m := &Manager{instanceStore: instanceStore}
	pool := newManagerPurgerTestPool(&flexibleMockDriver{driverName: "mock"})

	assert.NotPanics(t, func() {
		err := m.purgeStaleInstancesForPool(context.Background(), pool, "server", time.Hour, time.Hour)
		assert.NoError(t, err)
	})
}
