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

	"github.com/drone-runners/drone-runner-aws/command/harness/common"
	"github.com/drone-runners/drone-runner-aws/types"
)

// newHibernateResumeTestManager wires a plain (non-distributed) Manager around a
// mockInstanceStore and a flexibleMockDriver for hibernate/resume metric tests.
func newHibernateResumeTestManager(store *mockInstanceStore, driver *flexibleMockDriver, metrics *fakePurgerMetrics) (*Manager, *poolEntry) {
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

// TestHibernateOrStopWithRetries_Success_RecordsSuccessOutcome covers the happy path: the
// driver's Hibernate call succeeds first try, so exactly one attempt is recorded with
// outcome=success.
func TestHibernateOrStopWithRetries_Success_RecordsSuccessOutcome(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2"}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			return inst, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return nil
		},
	}
	driver := &flexibleMockDriver{
		canHibernate: true,
		HibernateFunc: func(_ context.Context, _, _, _ string) error {
			return nil
		},
	}
	metrics := &fakePurgerMetrics{}
	m, _ := newHibernateResumeTestManager(store, driver, metrics)

	err := m.hibernateOrStopWithRetries(context.Background(), "pool1", inst, false)

	require.NoError(t, err)
	require.Len(t, metrics.vmHibernateAtts, 1)
	att := metrics.vmHibernateAtts[0]
	assert.Equal(t, "pool1", att.poolID)
	assert.Equal(t, "us-east1-b", att.zone)
	assert.Equal(t, "n1-standard-2", att.vmType)
	assert.Equal(t, VMLifecycleOutcomeSuccess, att.outcome)
	assert.Equal(t, VMLifecycleReasonNone, att.reason)
	require.Len(t, metrics.vmHibernateDurs, 1)
	assert.Equal(t, VMLifecycleOutcomeSuccess, metrics.vmHibernateDurs[0].outcome)
	assert.GreaterOrEqual(t, metrics.vmHibernateDurs[0].duration.Seconds(), 0.0)
}

// TestHibernateOrStopWithRetries_CloudCallFails_RecordsCloudCallFailedReason covers a
// non-retryable driver-level failure: the plain error returned by pool.Driver.Hibernate isn't a
// *itypes.RetryableError, so hibernateOrStopWithRetries returns immediately after one internal
// hibernate() call, and that single logical attempt should be recorded as
// outcome=error/reason=cloud_call_failed.
func TestHibernateOrStopWithRetries_CloudCallFails_RecordsCloudCallFailedReason(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2"}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			return inst, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return nil
		},
	}
	driver := &flexibleMockDriver{
		canHibernate: true,
		HibernateFunc: func(_ context.Context, _, _, _ string) error {
			return errors.New("cloud provider rejected suspend")
		},
	}
	metrics := &fakePurgerMetrics{}
	m, _ := newHibernateResumeTestManager(store, driver, metrics)

	err := m.hibernateOrStopWithRetries(context.Background(), "pool1", inst, false)

	require.Error(t, err)
	require.Len(t, metrics.vmHibernateAtts, 1)
	att := metrics.vmHibernateAtts[0]
	assert.Equal(t, VMLifecycleOutcomeError, att.outcome)
	assert.Equal(t, VMLifecycleReasonCloudCallFailed, att.reason)
	require.Len(t, metrics.vmHibernateDurs, 1)
	assert.Equal(t, VMLifecycleOutcomeError, metrics.vmHibernateDurs[0].outcome)
}

// TestHibernateOrStopWithRetries_StateWriteFails_RecordsStateFailedReason covers a DB failure
// (the initial StateHibernating write) rather than a cloud-level failure, asserting the two are
// distinguishable via the reason label.
func TestHibernateOrStopWithRetries_StateWriteFails_RecordsStateFailedReason(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2"}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			return inst, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return errors.New("db unavailable")
		},
	}
	driver := &flexibleMockDriver{canHibernate: true}
	metrics := &fakePurgerMetrics{}
	m, _ := newHibernateResumeTestManager(store, driver, metrics)

	err := m.hibernateOrStopWithRetries(context.Background(), "pool1", inst, false)

	require.Error(t, err)
	require.Len(t, metrics.vmHibernateAtts, 1)
	att := metrics.vmHibernateAtts[0]
	assert.Equal(t, VMLifecycleOutcomeError, att.outcome)
	assert.Equal(t, VMLifecycleReasonStateFailed, att.reason)
}

// TestHibernateOrStopWithRetries_NoOpSkipsMetrics covers the driver-can't-hibernate,
// not-a-forced-stop no-op path: no driver call is attempted, so no metric should be recorded.
func TestHibernateOrStopWithRetries_NoOpSkipsMetrics(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1"}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			t.Fatal("Find should not be called for the no-op path")
			return nil, nil
		},
	}
	driver := &flexibleMockDriver{canHibernate: false}
	metrics := &fakePurgerMetrics{}
	m, _ := newHibernateResumeTestManager(store, driver, metrics)

	err := m.hibernateOrStopWithRetries(context.Background(), "pool1", inst, false)

	require.NoError(t, err)
	assert.Empty(t, metrics.vmHibernateAtts)
	assert.Empty(t, metrics.vmHibernateDurs)
}

// TestHibernateOrStopWithRetries_NilMetricsSafe ensures hibernate metric recording is safe when
// no metrics recorder is wired up.
func TestHibernateOrStopWithRetries_NilMetricsSafe(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2"}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			return inst, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return nil
		},
	}
	driver := &flexibleMockDriver{canHibernate: true}
	m, _ := newHibernateResumeTestManager(store, driver, nil)

	assert.NotPanics(t, func() {
		err := m.hibernateOrStopWithRetries(context.Background(), "pool1", inst, false)
		assert.NoError(t, err)
	})
}

// TestStartInstance_Success_RecordsSuccessOutcome covers the happy resume path: the driver's
// Start call succeeds and the DB write clearing IsHibernated succeeds.
func TestStartInstance_Success_RecordsSuccessOutcome(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2", IsHibernated: true}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			return inst, nil
		},
		UpdateFunc: func(_ context.Context, updated *types.Instance) error {
			assert.False(t, updated.IsHibernated)
			return nil
		},
	}
	driver := &flexibleMockDriver{
		StartFunc: func(_ context.Context, _ *types.Instance, _ string) (string, error) {
			return "10.0.0.5", nil
		},
	}
	metrics := &fakePurgerMetrics{}
	m, _ := newHibernateResumeTestManager(store, driver, metrics)

	out, err := m.StartInstance(context.Background(), "pool1", "inst-1", &common.InstanceInfo{})

	require.NoError(t, err)
	assert.Equal(t, "10.0.0.5", out.Address)
	require.Len(t, metrics.vmResumeAtts, 1)
	att := metrics.vmResumeAtts[0]
	assert.Equal(t, "pool1", att.poolID)
	assert.Equal(t, "us-east1-b", att.zone)
	assert.Equal(t, "n1-standard-2", att.vmType)
	assert.Equal(t, VMLifecycleOutcomeSuccess, att.outcome)
	assert.Equal(t, VMLifecycleReasonNone, att.reason)
	require.Len(t, metrics.vmResumeDurs, 1)
	assert.Equal(t, VMLifecycleOutcomeSuccess, metrics.vmResumeDurs[0].outcome)
}

// TestStartInstance_CloudStartFails_RecordsCloudCallFailedReason covers a cloud-level start
// failure - the scenario the "Tests" section explicitly calls out.
func TestStartInstance_CloudStartFails_RecordsCloudCallFailedReason(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2", IsHibernated: true}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			return inst, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			t.Fatal("Update should not be called when the cloud start call fails")
			return nil
		},
	}
	driver := &flexibleMockDriver{
		StartFunc: func(_ context.Context, _ *types.Instance, _ string) (string, error) {
			return "", errors.New("cloud provider rejected resume")
		},
	}
	metrics := &fakePurgerMetrics{}
	m, _ := newHibernateResumeTestManager(store, driver, metrics)

	out, err := m.StartInstance(context.Background(), "pool1", "inst-1", &common.InstanceInfo{})

	require.Error(t, err)
	assert.Nil(t, out)
	require.Len(t, metrics.vmResumeAtts, 1)
	att := metrics.vmResumeAtts[0]
	assert.Equal(t, VMLifecycleOutcomeError, att.outcome)
	assert.Equal(t, VMLifecycleReasonCloudCallFailed, att.reason)
	require.Len(t, metrics.vmResumeDurs, 1)
	assert.Equal(t, VMLifecycleOutcomeError, metrics.vmResumeDurs[0].outcome)
}

// TestStartInstance_StateWriteFails_RecordsStateFailedReason covers a successful cloud resume
// followed by a DB write failure, asserting it is distinguishable from a cloud call failure.
func TestStartInstance_StateWriteFails_RecordsStateFailedReason(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2", IsHibernated: true}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			return inst, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return errors.New("db unavailable")
		},
	}
	driver := &flexibleMockDriver{
		StartFunc: func(_ context.Context, _ *types.Instance, _ string) (string, error) {
			return "10.0.0.5", nil
		},
	}
	metrics := &fakePurgerMetrics{}
	m, _ := newHibernateResumeTestManager(store, driver, metrics)

	out, err := m.StartInstance(context.Background(), "pool1", "inst-1", &common.InstanceInfo{})

	require.Error(t, err)
	assert.Nil(t, out)
	require.Len(t, metrics.vmResumeAtts, 1)
	att := metrics.vmResumeAtts[0]
	assert.Equal(t, VMLifecycleOutcomeError, att.outcome)
	assert.Equal(t, VMLifecycleReasonStateFailed, att.reason)
	// The cloud-level call itself succeeded, so the narrower resume-duration metric should
	// still record outcome=success even though the overall attempt failed.
	require.Len(t, metrics.vmResumeDurs, 1)
	assert.Equal(t, VMLifecycleOutcomeSuccess, metrics.vmResumeDurs[0].outcome)
}

// TestStartInstance_NotHibernated_NoResumeMetrics covers the short-circuit for an instance that
// isn't hibernated: no driver call is made, so no resume metric should be recorded.
func TestStartInstance_NotHibernated_NoResumeMetrics(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1", IsHibernated: false}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			return inst, nil
		},
	}
	driver := &flexibleMockDriver{
		StartFunc: func(_ context.Context, _ *types.Instance, _ string) (string, error) {
			t.Fatal("Start should not be called for a non-hibernated instance")
			return "", nil
		},
	}
	metrics := &fakePurgerMetrics{}
	m, _ := newHibernateResumeTestManager(store, driver, metrics)

	out, err := m.StartInstance(context.Background(), "pool1", "inst-1", &common.InstanceInfo{})

	require.NoError(t, err)
	assert.Same(t, inst, out)
	assert.Empty(t, metrics.vmResumeAtts)
	assert.Empty(t, metrics.vmResumeDurs)
}

// TestStartInstance_NilMetricsSafe ensures resume metric recording is safe when no metrics
// recorder is wired up.
func TestStartInstance_NilMetricsSafe(t *testing.T) {
	inst := &types.Instance{ID: "inst-1", Pool: "pool1", Zone: "us-east1-b", Size: "n1-standard-2", IsHibernated: true}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, _ string) (*types.Instance, error) {
			return inst, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error {
			return nil
		},
	}
	driver := &flexibleMockDriver{
		StartFunc: func(_ context.Context, _ *types.Instance, _ string) (string, error) {
			return "10.0.0.5", nil
		},
	}
	m, _ := newHibernateResumeTestManager(store, driver, nil)

	assert.NotPanics(t, func() {
		_, err := m.StartInstance(context.Background(), "pool1", "inst-1", &common.InstanceInfo{})
		assert.NoError(t, err)
	})
}
