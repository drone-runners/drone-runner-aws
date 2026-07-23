// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/types"
)

// TestDistributedHibernate_Success_RecordsHibernatingStateDuration covers the one hot-pool
// dwell-time transition (see hotpool_metrics.go for why ready/busy/hibernated aren't
// instrumented) that's exactly measurable rather than approximated: the instance only exists in
// StateHibernating between the FindAndClaim below and the state flip back to Created, both
// inside DistributedManager.hibernate, so the recorded duration should equal the (near-zero, in
// this fast-succeeding test) time the driver's Hibernate call took.
func TestDistributedHibernate_Success_RecordsHibernatingStateDuration(t *testing.T) {
	claimed := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2"}
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, newState types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			require.Equal(t, types.StateHibernating, newState)
			return claimed, nil
		},
		UpdateFunc: func(_ context.Context, instance *types.Instance) error {
			assert.True(t, instance.IsHibernated)
			assert.Equal(t, types.StateCreated, instance.State)
			return nil
		},
	}
	driver := &flexibleMockDriver{
		HibernateFunc: func(_ context.Context, instanceID, poolName, zone string) error {
			assert.Equal(t, "inst-1", instanceID)
			assert.Equal(t, "pool1", poolName)
			assert.Equal(t, "us-east1-b", zone)
			return nil
		},
	}
	metrics := &fakePurgerMetrics{}
	d, _ := newProvisionTestManager(store, driver, metrics)

	err := d.hibernate(context.Background(), "pool1", claimed, true)

	require.NoError(t, err)
	require.Len(t, metrics.hotpoolStateDurs, 1)
	rec := metrics.hotpoolStateDurs[0]
	assert.Equal(t, "pool1", rec.poolID)
	assert.Equal(t, "us-east1-b", rec.zone)
	assert.Equal(t, "n1-standard-2", rec.vmType)
	assert.Equal(t, HotpoolStateHibernating, rec.state)
	assert.GreaterOrEqual(t, rec.dwell.Seconds(), 0.0)
}

// TestDistributedHibernate_ShouldHibernateFalse_NoMetricRecorded covers the early-return path
// (shouldHibernate=false) where hibernate never claims the instance, so no dwell time exists to
// record.
func TestDistributedHibernate_ShouldHibernateFalse_NoMetricRecorded(t *testing.T) {
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			t.Fatal("FindAndClaim should not be called when shouldHibernate is false")
			return nil, nil
		},
	}
	metrics := &fakePurgerMetrics{}
	d, _ := newProvisionTestManager(store, &flexibleMockDriver{}, metrics)

	err := d.hibernate(context.Background(), "pool1", &types.Instance{ID: "inst-1"}, false)

	require.NoError(t, err)
	assert.Empty(t, metrics.hotpoolStateDurs)
}

// TestDistributedHibernate_NilMetricsSafe ensures the hibernating dwell recording is safe when
// no metrics recorder is wired up (e.g. standalone commands).
func TestDistributedHibernate_NilMetricsSafe(t *testing.T) {
	claimed := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2"}
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			return claimed, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return nil
		},
	}
	driver := &flexibleMockDriver{
		HibernateFunc: func(_ context.Context, _, _, _ string) error {
			return nil
		},
	}
	d, _ := newProvisionTestManager(store, driver, nil)

	assert.NotPanics(t, func() {
		err := d.hibernate(context.Background(), "pool1", claimed, true)
		assert.NoError(t, err)
	})
}
