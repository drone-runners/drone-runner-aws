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

// TestDistributedHibernate_Success covers the happy path through DistributedManager.hibernate:
// FindAndClaim transitions the instance to StateHibernating, the driver's Hibernate call runs,
// and the instance is updated back to StateCreated with IsHibernated set.
func TestDistributedHibernate_Success(t *testing.T) {
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
	d, _ := newProvisionTestManager(store, driver)

	err := d.hibernate(context.Background(), "pool1", claimed, true)

	require.NoError(t, err)
}

// TestDistributedHibernate_ShouldHibernateFalse covers the early-return path (shouldHibernate=
// false) where hibernate never claims the instance.
func TestDistributedHibernate_ShouldHibernateFalse(t *testing.T) {
	store := &mockInstanceStore{
		FindAndClaimFunc: func(_ context.Context, _ *types.QueryParams, _ types.InstanceState, _ []types.InstanceState, _ bool) (*types.Instance, error) {
			t.Fatal("FindAndClaim should not be called when shouldHibernate is false")
			return nil, nil
		},
	}
	d, _ := newProvisionTestManager(store, &flexibleMockDriver{})

	err := d.hibernate(context.Background(), "pool1", &types.Instance{ID: "inst-1"}, false)

	require.NoError(t, err)
}
