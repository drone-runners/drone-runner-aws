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
// flexibleMockDriver for provisionFromPool tests.
func newProvisionTestManager(store *mockInstanceStore, driver *flexibleMockDriver) (*DistributedManager, *poolEntry) {
	const poolName = "pool1"
	pool := &poolEntry{
		Pool: Pool{
			Name:   poolName,
			Driver: driver,
		},
	}
	d := &DistributedManager{
		Manager: Manager{
			poolMap:       map[string]*poolEntry{poolName: pool},
			instanceStore: store,
			runnerName:    "test-runner",
		},
	}
	return d, pool
}

func TestDistributedProvisionFromPool_SuccessfulClaim(t *testing.T) {
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
	d, pool := newProvisionTestManager(store, &flexibleMockDriver{})

	inst, _, warmed, _, err := d.provisionFromPool( //nolint:dogsled
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
	)

	require.NoError(t, err)
	assert.True(t, warmed)
	require.NotNil(t, inst)
	assert.Equal(t, "inst-1", inst.ID)
}

func TestDistributedProvisionFromPool_EmptyPool_FallsBackToColdCreate(t *testing.T) {
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			return nil, sql.ErrNoRows
		},
	}
	// The cold-create fallback (Case 3) also runs after an empty pool; make it fail fast so the
	// test doesn't depend on unrelated setupInstance/instanceStore.Create behavior.
	driver := &flexibleMockDriver{
		CreateFunc: func(_ context.Context, _ *types.InstanceCreateOpts) (*types.Instance, error) {
			return nil, errors.New("no capacity in test driver")
		},
	}
	d, pool := newProvisionTestManager(store, driver)

	_, _, warmed, _, err := d.provisionFromPool( //nolint:dogsled
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
	)

	require.Error(t, err)
	assert.False(t, warmed)
}

func TestDistributedProvisionFromPool_ClaimStoreError(t *testing.T) {
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			return nil, errors.New("db connection reset")
		},
	}
	d, pool := newProvisionTestManager(store, &flexibleMockDriver{})

	_, _, warmed, _, err := d.provisionFromPool( //nolint:dogsled
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
	)

	require.Error(t, err)
	assert.False(t, warmed)
}

func TestDistributedProvisionFromPool_PostClaimUpdateFails(t *testing.T) {
	claimed := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2"}
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			return claimed, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return errors.New("row vanished")
		},
	}
	d, pool := newProvisionTestManager(store, &flexibleMockDriver{})

	_, _, warmed, _, err := d.provisionFromPool( //nolint:dogsled
		context.Background(), pool, "tls", "owner1",
		&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
	)

	require.Error(t, err)
	assert.False(t, warmed)
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
	d, pool := newProvisionTestManager(store, driver)

	assert.NotPanics(t, func() {
		_, _, _, _, _ = d.provisionFromPool(
			context.Background(), pool, "tls", "owner1",
			&types.ProvisionParams{}, nil, nil, 60, pool.Name, nil, false,
		)
	})
}
