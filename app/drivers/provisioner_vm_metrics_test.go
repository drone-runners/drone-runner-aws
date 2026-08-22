// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/types"
)

// newSetupInstanceTestManager wires a bare Manager around a mockInstanceStore and a
// flexibleMockDriver for setupInstance tests, optionally recording VM creation metrics via
// fakePurgerMetrics so runner_vm_creation_attempts_total/duration_seconds can be asserted on
// directly.
func newSetupInstanceTestManager(store *mockInstanceStore, driver *flexibleMockDriver, metrics *fakePurgerMetrics) (*Manager, *poolEntry) {
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
	m := &Manager{
		poolMap:       map[string]*poolEntry{poolName: pool},
		instanceStore: store,
		runnerName:    "test-runner",
		metrics:       recorder,
	}
	return m, pool
}

func TestSetupInstance_CreateSucceeds_RecordsSuccessOutcome(t *testing.T) {
	created := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2"}
	store := &mockInstanceStore{
		CreateFunc: func(_ context.Context, _ *types.Instance) error { return nil },
	}
	driver := &flexibleMockDriver{
		CreateFunc: func(_ context.Context, _ *types.InstanceCreateOpts) (*types.Instance, error) {
			return created, nil
		},
	}
	metrics := &fakePurgerMetrics{}
	m, pool := newSetupInstanceTestManager(store, driver, metrics)

	inst, _, err := m.setupInstance(
		context.Background(), pool, "tls-server", "owner1",
		&types.SetupInstanceParams{MachineType: "n1-standard-2"}, nil, true, nil, nil, 60, nil, nil, false,
	)

	require.NoError(t, err)
	require.NotNil(t, inst)

	require.Len(t, metrics.vmCreationAtts, 1)
	assert.Equal(t, vmCreationAttemptRecord{
		poolID: "pool1", zone: "us-east1-b", vmType: "n1-standard-2",
		source: string(types.InstanceSourcePool), outcome: VMCreationOutcomeSuccess, reason: VMCreationReasonNone,
	}, metrics.vmCreationAtts[0])

	require.Len(t, metrics.vmCreationDurs, 1)
	assert.Equal(t, "pool1", metrics.vmCreationDurs[0].poolID)
	assert.Equal(t, VMCreationOutcomeSuccess, metrics.vmCreationDurs[0].outcome)
	assert.GreaterOrEqual(t, metrics.vmCreationDurs[0].duration.Seconds(), 0.0)
}

// TestSetupInstance_CreateFails_StillRecordsAttemptAndDuration is the P0 case called out by the
// VM creation/usage metrics prompt: the failure path must still emit a metric, not just success.
func TestSetupInstance_CreateFails_StillRecordsAttemptAndDuration(t *testing.T) {
	driver := &flexibleMockDriver{
		CreateFunc: func(_ context.Context, _ *types.InstanceCreateOpts) (*types.Instance, error) {
			return nil, errors.New("googleapi: Error 503: The zone appears to have exhausted the resource, ZONE_RESOURCE_POOL_EXHAUSTED")
		},
	}
	metrics := &fakePurgerMetrics{}
	m, pool := newSetupInstanceTestManager(&mockInstanceStore{}, driver, metrics)

	inst, _, err := m.setupInstance(
		context.Background(), pool, "tls-server", "owner1",
		&types.SetupInstanceParams{MachineType: "n1-standard-2", Zones: []string{"us-east1-b"}}, nil, true, nil, nil, 60, nil, nil, false,
	)

	require.Error(t, err)
	require.Nil(t, inst)

	require.Len(t, metrics.vmCreationAtts, 1)
	assert.Equal(t, vmCreationAttemptRecord{
		poolID: "pool1", zone: "us-east1-b", vmType: "n1-standard-2",
		source: string(types.InstanceSourcePool), outcome: VMCreationOutcomeError, reason: VMCreationReasonStockout,
	}, metrics.vmCreationAtts[0])

	require.Len(t, metrics.vmCreationDurs, 1)
	assert.Equal(t, VMCreationOutcomeError, metrics.vmCreationDurs[0].outcome)
	assert.GreaterOrEqual(t, metrics.vmCreationDurs[0].duration.Seconds(), 0.0)
}

func TestSetupInstance_CreateFails_QuotaExceededClassification(t *testing.T) {
	driver := &flexibleMockDriver{
		CreateFunc: func(_ context.Context, _ *types.InstanceCreateOpts) (*types.Instance, error) {
			return nil, errors.New("Quota 'CPUS' exceeded. Limit: 24.0 in region us-east1")
		},
	}
	metrics := &fakePurgerMetrics{}
	m, pool := newSetupInstanceTestManager(&mockInstanceStore{}, driver, metrics)

	_, _, err := m.setupInstance(
		context.Background(), pool, "tls-server", "owner1",
		&types.SetupInstanceParams{MachineType: "n1-standard-2"}, nil, true, nil, nil, 60, nil, nil, false,
	)

	require.Error(t, err)
	require.Len(t, metrics.vmCreationAtts, 1)
	assert.Equal(t, VMCreationOutcomeError, metrics.vmCreationAtts[0].outcome)
	assert.Equal(t, VMCreationReasonQuotaExceeded, metrics.vmCreationAtts[0].reason)
}

func TestSetupInstance_CreateFails_GenericErrorClassifiedAsDriverError(t *testing.T) {
	driver := &flexibleMockDriver{
		CreateFunc: func(_ context.Context, _ *types.InstanceCreateOpts) (*types.Instance, error) {
			return nil, errors.New("some unrelated failure")
		},
	}
	metrics := &fakePurgerMetrics{}
	m, pool := newSetupInstanceTestManager(&mockInstanceStore{}, driver, metrics)

	_, _, err := m.setupInstance(
		context.Background(), pool, "tls-server", "owner1",
		&types.SetupInstanceParams{MachineType: "n1-standard-2"}, nil, true, nil, nil, 60, nil, nil, false,
	)

	require.Error(t, err)
	require.Len(t, metrics.vmCreationAtts, 1)
	assert.Equal(t, VMCreationOutcomeError, metrics.vmCreationAtts[0].outcome)
	assert.Equal(t, VMCreationReasonDriverError, metrics.vmCreationAtts[0].reason)
}

func TestSetupInstance_NilMetricsSafe(t *testing.T) {
	driver := &flexibleMockDriver{
		CreateFunc: func(_ context.Context, _ *types.InstanceCreateOpts) (*types.Instance, error) {
			return nil, errors.New("failure")
		},
	}
	m, pool := newSetupInstanceTestManager(&mockInstanceStore{}, driver, nil)

	assert.NotPanics(t, func() {
		_, _, _ = m.setupInstance(
			context.Background(), pool, "tls-server", "owner1",
			&types.SetupInstanceParams{MachineType: "n1-standard-2"}, nil, true, nil, nil, 60, nil, nil, false,
		)
	})
}
