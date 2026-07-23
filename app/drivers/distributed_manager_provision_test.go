// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/types"
)

// newProvisionTestManager wires a DistributedManager around a mockInstanceStore and a
// flexibleMockDriver for provisionFromPool tests, optionally recording hot-pool claim metrics
// via fakePurgerMetrics so runner_hotpool_claim_attempts_total can be asserted on directly.
func newProvisionTestManager(store *mockInstanceStore, driver *flexibleMockDriver, metrics *fakePurgerMetrics) (*DistributedManager, *poolEntry) {
	const poolName = "pool1"
	pool := &poolEntry{
		Pool: Pool{
			Name:   poolName,
			Driver: driver,
		},
	}
	var recorder MetricsRecorder
	if metrics != nil {
		recorder = metrics
	}
	d := &DistributedManager{
		Manager: Manager{
			poolMap:       map[string]*poolEntry{poolName: pool},
			instanceStore: store,
			runnerName:    "test-runner",
			metrics:       recorder,
		},
	}
	return d, pool
}

func TestDistributedProvisionFromPool_SuccessfulClaim_RecordsClaimedOutcome(t *testing.T) {
	claimed := &types.Instance{
		ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2",
		State: types.StateInUse, Source: types.InstanceSourceOnDemand,
	}
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			return claimed, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return nil
		},
	}
	metrics := &fakePurgerMetrics{}
	d, pool := newProvisionTestManager(store, &flexibleMockDriver{}, metrics)

	inst, _, warmed, _, err := d.provisionFromPool( //nolint:dogsled
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
	)

	require.NoError(t, err)
	assert.True(t, warmed)
	require.NotNil(t, inst)
	assert.Equal(t, "inst-1", inst.ID)
	require.Len(t, metrics.hotpoolClaims, 1)
	assert.Equal(t, hotpoolClaimAttemptRecord{
		poolID: "pool1", zone: "us-east1-b", vmType: "n1-standard-2",
		outcome: HotpoolClaimOutcomeClaimed, reason: HotpoolClaimReasonNone,
	}, metrics.hotpoolClaims[0])
}

func TestDistributedProvisionFromPool_EmptyPool_RecordsNoReadyCapacity(t *testing.T) {
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			return nil, sql.ErrNoRows
		},
	}
	// The cold-create fallback (Case 3) also runs after an empty pool; make it fail fast so the
	// test doesn't depend on unrelated setupInstance/instanceStore.Create behavior. The claim
	// metric we're asserting on is recorded before this fallback even starts.
	driver := &flexibleMockDriver{
		CreateFunc: func(_ context.Context, _ *types.InstanceCreateOpts) (*types.Instance, error) {
			return nil, errors.New("no capacity in test driver")
		},
	}
	metrics := &fakePurgerMetrics{}
	d, pool := newProvisionTestManager(store, driver, metrics)

	_, _, warmed, _, err := d.provisionFromPool( //nolint:dogsled
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
	)

	require.Error(t, err)
	assert.False(t, warmed)
	require.Len(t, metrics.hotpoolClaims, 1)
	assert.Equal(t, HotpoolClaimOutcomeNoReadyCapacity, metrics.hotpoolClaims[0].outcome)
	assert.Equal(t, HotpoolClaimReasonNone, metrics.hotpoolClaims[0].reason)
	assert.Equal(t, "pool1", metrics.hotpoolClaims[0].poolID)
}

func TestDistributedProvisionFromPool_ClaimStoreError_RecordsClaimFailed(t *testing.T) {
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			return nil, errors.New("db connection reset")
		},
	}
	metrics := &fakePurgerMetrics{}
	d, pool := newProvisionTestManager(store, &flexibleMockDriver{}, metrics)

	_, _, warmed, _, err := d.provisionFromPool( //nolint:dogsled
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
	)

	require.Error(t, err)
	assert.False(t, warmed)
	require.Len(t, metrics.hotpoolClaims, 1)
	assert.Equal(t, HotpoolClaimOutcomeClaimFailed, metrics.hotpoolClaims[0].outcome)
	assert.Equal(t, HotpoolClaimReasonStoreError, metrics.hotpoolClaims[0].reason)
}

func TestDistributedProvisionFromPool_PostClaimUpdateFails_RecordsClaimFailed(t *testing.T) {
	claimed := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2"}
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			return claimed, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return errors.New("row vanished")
		},
	}
	metrics := &fakePurgerMetrics{}
	d, pool := newProvisionTestManager(store, &flexibleMockDriver{}, metrics)

	_, _, warmed, _, err := d.provisionFromPool( //nolint:dogsled
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
	)

	require.Error(t, err)
	assert.False(t, warmed)
	require.Len(t, metrics.hotpoolClaims, 1)
	assert.Equal(t, hotpoolClaimAttemptRecord{
		poolID: "pool1", zone: "us-east1-b", vmType: "n1-standard-2",
		outcome: HotpoolClaimOutcomeClaimFailed, reason: HotpoolClaimReasonStoreError,
	}, metrics.hotpoolClaims[0])
}

func TestDistributedProvisionFromPool_NilMetricsSafe(t *testing.T) {
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			return nil, sql.ErrNoRows
		},
	}
	driver := &flexibleMockDriver{
		CreateFunc: func(_ context.Context, _ *types.InstanceCreateOpts) (*types.Instance, error) {
			return nil, errors.New("no capacity in test driver")
		},
	}
	d, pool := newProvisionTestManager(store, driver, nil)

	assert.NotPanics(t, func() {
		_, _, _, _, _ = d.provisionFromPool(
			context.Background(), pool, "tls", "owner1",
			&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
		)
	})
}
