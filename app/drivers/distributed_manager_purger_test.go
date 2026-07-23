// Copyright 2020 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package drivers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/stretchr/testify/assert"

	"github.com/drone-runners/drone-runner-aws/types"
)

// purgerFakeStore is an in-memory fake that understands the three raw SQL
// statements the distributed purger emits: the UPDATE-to-terminating claim,
// the DELETE-by-id of successfully destroyed rows, and the force-delete of
// leak-candidate rows older than 2 * maxAge. Everything else on the
// InstanceStore interface panics - the purger path only touches
// DeleteAndReturn.
type purgerFakeStore struct {
	mockInstanceStore

	mu        sync.Mutex
	instances map[string]*types.Instance
}

func newPurgerFakeStore(seed ...*types.Instance) *purgerFakeStore {
	s := &purgerFakeStore{instances: make(map[string]*types.Instance)}
	for _, inst := range seed {
		s.instances[inst.ID] = inst
	}
	return s
}

func (s *purgerFakeStore) snapshot() map[string]*types.Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]*types.Instance, len(s.instances))
	for k, v := range s.instances {
		copied := *v
		out[k] = &copied
	}
	return out
}

func toInstanceState(v any) types.InstanceState {
	switch x := v.(type) {
	case types.InstanceState:
		return x
	case string:
		return types.InstanceState(x)
	default:
		return ""
	}
}

func (s *purgerFakeStore) DeleteAndReturn(_ context.Context, query string, args ...any) ([]*types.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case strings.HasPrefix(query, "UPDATE instances SET instance_state = "):
		// Claim path: args are [newState, poolName]. The purger passes
		// instance_state as types.InstanceState, so accept either it or a raw string.
		newState := toInstanceState(args[0])
		poolName, _ := args[1].(string)

		var claimed []*types.Instance
		now := time.Now().Unix()
		for _, inst := range s.instances {
			if inst.Pool != poolName {
				continue
			}
			inst.State = newState
			inst.Updated = now
			copied := *inst
			claimed = append(claimed, &copied)
		}
		return claimed, nil

	case strings.HasPrefix(query, "DELETE FROM instances WHERE instance_id IN "):
		// Delete-by-id path: args are the instance IDs.
		var deleted []*types.Instance
		for _, a := range args {
			id, _ := a.(string)
			if inst, ok := s.instances[id]; ok {
				copied := *inst
				deleted = append(deleted, &copied)
				delete(s.instances, id)
			}
		}
		return deleted, nil

	case strings.HasPrefix(query, "DELETE FROM instances WHERE (instance_pool = "):
		// Force-delete path: args are [poolName, startedCutoff].
		poolName, _ := args[0].(string)
		cutoff, _ := args[1].(int64)

		var deleted []*types.Instance
		for id, inst := range s.instances {
			if inst.Pool != poolName {
				continue
			}
			if inst.Started >= cutoff {
				continue
			}
			copied := *inst
			deleted = append(deleted, &copied)
			delete(s.instances, id)
		}
		return deleted, nil
	}

	return nil, errors.New("purgerFakeStore: unrecognized query: " + query)
}

// newPurgerTestManager wires a DistributedManager around the in-memory store
// and a driver the test can configure per-scenario.
func newPurgerTestManager(store *purgerFakeStore, driver *flexibleMockDriver) (*DistributedManager, *poolEntry) {
	return newPurgerTestManagerWithMetrics(store, driver, nil)
}

// newPurgerTestManagerWithMetrics is like newPurgerTestManager but also wires in a
// MetricsRecorder, for tests that assert on what was reported.
func newPurgerTestManagerWithMetrics(store *purgerFakeStore, driver *flexibleMockDriver, metrics MetricsRecorder) (*DistributedManager, *poolEntry) {
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
			metrics:       metrics,
		},
	}
	return d, pool
}

func TestDistributedPurger_ExecuteInstanceCleanup_HappyPath(t *testing.T) {
	// Dummy data: two busy rows that are well within the 2 * maxAge window
	// so forceDeleteLeakedInstances ignores them. Destroy succeeds for all.
	now := time.Now()
	maxAge := 5 * time.Minute
	store := newPurgerFakeStore(
		&types.Instance{ID: "inst-1", Name: "vm-1", Pool: "pool1", State: types.StateInUse, Started: now.Add(-10 * time.Second).Unix()},
		&types.Instance{ID: "inst-2", Name: "vm-2", Pool: "pool1", State: types.StateInUse, Started: now.Add(-10 * time.Second).Unix()},
	)

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

	d, pool := newPurgerTestManager(store, driver)
	conditions := squirrel.Or{squirrel.Eq{"instance_pool": "pool1"}}

	successful, err := d.executeInstanceCleanup(context.Background(), pool, conditions, "busy", maxAge)

	assert.NoError(t, err)
	assert.Len(t, successful, 2, "both rows should have been destroyed successfully")
	assert.ElementsMatch(t, []string{"inst-1", "inst-2"}, destroyed, "driver.Destroy should be called for both")
	assert.Empty(t, store.snapshot(), "store should be empty after successful destroy")
}

func TestDistributedPurger_ExecuteInstanceCleanup_DestroyFailureKeepsRow(t *testing.T) {
	// Dummy data: three rows, driver.Destroy reports inst-2 as failed.
	// Expectation (the fix): inst-1 and inst-3 are deleted, inst-2 stays
	// in the DB in StateTerminating so the next purger tick retries it.
	now := time.Now()
	maxAge := 5 * time.Minute
	store := newPurgerFakeStore(
		&types.Instance{ID: "inst-1", Name: "vm-1", Pool: "pool1", State: types.StateInUse, Started: now.Add(-30 * time.Second).Unix()},
		&types.Instance{ID: "inst-2", Name: "vm-2", Pool: "pool1", State: types.StateInUse, Started: now.Add(-30 * time.Second).Unix()},
		&types.Instance{ID: "inst-3", Name: "vm-3", Pool: "pool1", State: types.StateInUse, Started: now.Add(-30 * time.Second).Unix()},
	)

	driver := &flexibleMockDriver{
		driverName: "mock",
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			// Return inst-2 as failed; mirrors drivers returning a subset they couldn't destroy.
			for _, i := range instances {
				if i.ID == "inst-2" {
					return []*types.Instance{i}, errors.New("simulated cloud API error for inst-2")
				}
			}
			return nil, nil
		},
	}

	d, pool := newPurgerTestManager(store, driver)
	conditions := squirrel.Or{squirrel.Eq{"instance_pool": "pool1"}}

	successful, err := d.executeInstanceCleanup(context.Background(), pool, conditions, "busy", maxAge)
	assert.NoError(t, err, "executeInstanceCleanup itself should not bubble the driver error")
	assert.Len(t, successful, 2)

	snap := store.snapshot()
	assert.NotContains(t, snap, "inst-1", "successful row should be deleted")
	assert.NotContains(t, snap, "inst-3", "successful row should be deleted")
	if assert.Contains(t, snap, "inst-2", "failed row must remain for retry on the next tick") {
		assert.Equal(t, types.StateTerminating, snap["inst-2"].State,
			"failed row must be left in StateTerminating so stuckTerminatingCondition re-picks it")
	}
}

func TestDistributedPurger_ForceDeleteLeakCandidateAt2xMaxAge(t *testing.T) {
	// Dummy data:
	//   leak: older than 2 * maxAge  -> force-deleted regardless of state
	//   fresh: within maxAge         -> claimed & destroyed on this tick
	//   borderline: older than maxAge but younger than 2x -> also claimed this tick
	now := time.Now()
	maxAge := 5 * time.Minute

	store := newPurgerFakeStore(
		&types.Instance{ID: "leak", Name: "vm-leak", Pool: "pool1", State: types.StateTerminating, Started: now.Add(-3 * maxAge).Unix()},
		&types.Instance{ID: "fresh", Name: "vm-fresh", Pool: "pool1", State: types.StateInUse, Started: now.Add(-30 * time.Second).Unix()},
		&types.Instance{ID: "borderline", Name: "vm-borderline", Pool: "pool1", State: types.StateInUse, Started: now.Add(-90 * time.Second).Unix()},
	)

	var sawInDestroy []string
	driver := &flexibleMockDriver{
		driverName: "mock",
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			for _, i := range instances {
				sawInDestroy = append(sawInDestroy, i.ID)
			}
			return nil, nil
		},
	}

	d, pool := newPurgerTestManager(store, driver)
	conditions := squirrel.Or{squirrel.Eq{"instance_pool": "pool1"}}

	_, err := d.executeInstanceCleanup(context.Background(), pool, conditions, "busy", maxAge)
	assert.NoError(t, err)

	snap := store.snapshot()
	assert.NotContains(t, snap, "leak", "row older than 2 * maxAge must be force-deleted before claim")
	assert.NotContains(t, snap, "fresh", "successful row should be deleted")
	assert.NotContains(t, snap, "borderline", "successful row should be deleted")

	// The leak-candidate row must be removed BEFORE the claim so the driver
	// never sees it on this tick. (It will eventually be reconciled out-of-band.)
	assert.NotContains(t, sawInDestroy, "leak", "leak-candidate should be force-deleted before driver.Destroy is called")
	assert.ElementsMatch(t, []string{"fresh", "borderline"}, sawInDestroy, "driver.Destroy should see only in-window rows")
}

func TestDistributedPurger_ForceDelete_NoopWhenMaxAgeZero(t *testing.T) {
	// Safety: maxAge == 0 disables force-delete (guards against accidental
	// whole-pool purges when the purger is misconfigured).
	now := time.Now()
	store := newPurgerFakeStore(
		&types.Instance{ID: "old", Name: "vm-old", Pool: "pool1", State: types.StateInUse, Started: now.Add(-24 * time.Hour).Unix()},
	)
	driver := &flexibleMockDriver{driverName: "mock"}
	d, pool := newPurgerTestManager(store, driver)

	d.forceDeleteLeakedInstances(context.Background(), pool, "busy", 0)

	assert.Contains(t, store.snapshot(), "old", "maxAge=0 must be a no-op")
}

func TestDistributedPurger_StuckTerminatingRowIsRePicked(t *testing.T) {
	// Regression for the original CI-22293 leak: a row stuck in StateTerminating
	// from an earlier tick must be picked up again on the next tick. The real
	// cleanupBusyInstances path relies on stuckTerminatingCondition for this;
	// here we model a second tick by issuing the same claim and asserting the
	// stuck row is re-claimed and destroyed successfully the second time.
	now := time.Now()
	maxAge := 5 * time.Minute
	store := newPurgerFakeStore(
		&types.Instance{ID: "stuck", Name: "vm-stuck", Pool: "pool1", State: types.StateTerminating, Started: now.Add(-30 * time.Second).Unix(), Updated: now.Add(-10 * time.Minute).Unix()},
	)

	firstCall := true
	driver := &flexibleMockDriver{
		driverName: "mock",
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			if firstCall {
				firstCall = false
				// Simulate transient failure on the first tick.
				return instances, errors.New("transient cloud error")
			}
			return nil, nil
		},
	}

	d, pool := newPurgerTestManager(store, driver)
	conditions := squirrel.Or{squirrel.Eq{"instance_pool": "pool1"}}

	// Tick 1: Destroy fails -> row stays in terminating.
	_, err := d.executeInstanceCleanup(context.Background(), pool, conditions, "busy", maxAge)
	assert.NoError(t, err)
	snap := store.snapshot()
	if assert.Contains(t, snap, "stuck") {
		assert.Equal(t, types.StateTerminating, snap["stuck"].State)
	}

	// Tick 2: the purger re-picks the terminating row; Destroy now succeeds.
	_, err = d.executeInstanceCleanup(context.Background(), pool, conditions, "busy", maxAge)
	assert.NoError(t, err)
	assert.NotContains(t, store.snapshot(), "stuck", "stuck row should be cleaned up on the retry tick")
}

func TestDistributedPurger_ExecuteInstanceCleanup_RecordsDestroyAttemptMetrics(t *testing.T) {
	// Three rows: inst-1/inst-3 succeed, inst-2 fails. The metrics recorder must see one
	// destroy-attempt per row, with inst-2 reported as failed_left_for_retry (never destroyed).
	now := time.Now()
	maxAge := 5 * time.Minute
	store := newPurgerFakeStore(
		&types.Instance{ID: "inst-1", Name: "vm-1", Pool: "pool1", State: types.StateInUse, Zone: "us-east1-a", Started: now.Add(-30 * time.Second).Unix()},
		&types.Instance{ID: "inst-2", Name: "vm-2", Pool: "pool1", State: types.StateInUse, Zone: "us-east1-b", Started: now.Add(-30 * time.Second).Unix()},
		&types.Instance{ID: "inst-3", Name: "vm-3", Pool: "pool1", State: types.StateInUse, Zone: "us-east1-a", Started: now.Add(-30 * time.Second).Unix()},
	)

	driver := &flexibleMockDriver{
		driverName: "mock",
		DestroyFunc: func(_ context.Context, instances []*types.Instance) ([]*types.Instance, error) {
			for _, i := range instances {
				if i.ID == "inst-2" {
					return []*types.Instance{i}, errors.New("simulated cloud API error for inst-2")
				}
			}
			return nil, nil
		},
	}

	fakeMetrics := &fakePurgerMetrics{}
	d, pool := newPurgerTestManagerWithMetrics(store, driver, fakeMetrics)
	conditions := squirrel.Or{squirrel.Eq{"instance_pool": "pool1"}}

	_, err := d.executeInstanceCleanup(context.Background(), pool, conditions, "busy", maxAge)
	assert.NoError(t, err)

	assert.Len(t, fakeMetrics.destroyAttempts, 3)
	// us-east1-b is unique to inst-2 in this scenario, so the zone label lets us identify which
	// record belongs to the failed instance without the recorder needing to carry instance IDs.
	failedCount, destroyedCount := 0, 0
	for _, rec := range fakeMetrics.destroyAttempts {
		assert.Equal(t, "pool1", rec.poolID)
		assert.Equal(t, PurgerReasonBusyMaxAge, rec.reason)
		if rec.outcome == PurgerOutcomeFailedLeftForRetry {
			failedCount++
			assert.Equal(t, "us-east1-b", rec.zone, "only inst-2 should be reported as failed")
		} else {
			assert.Equal(t, PurgerOutcomeDestroyed, rec.outcome)
			destroyedCount++
		}
	}
	assert.Equal(t, 1, failedCount)
	assert.Equal(t, 2, destroyedCount)
}

func TestDistributedPurger_ForceDeleteLeakCandidate_RecordsForceDeletedMetric(t *testing.T) {
	now := time.Now()
	maxAge := 5 * time.Minute
	store := newPurgerFakeStore(
		&types.Instance{ID: "leak-1", Name: "vm-leak-1", Pool: "pool1", State: types.StateTerminating, Started: now.Add(-3 * maxAge).Unix()},
		&types.Instance{ID: "leak-2", Name: "vm-leak-2", Pool: "pool1", State: types.StateTerminating, Started: now.Add(-4 * maxAge).Unix()},
	)
	driver := &flexibleMockDriver{driverName: "mock"}

	fakeMetrics := &fakePurgerMetrics{}
	d, pool := newPurgerTestManagerWithMetrics(store, driver, fakeMetrics)

	d.forceDeleteLeakedInstances(context.Background(), pool, "busy", maxAge)

	assert.Empty(t, store.snapshot(), "both leak candidates should have been force-deleted")
	if assert.Len(t, fakeMetrics.forceDeleted, 1) {
		rec := fakeMetrics.forceDeleted[0]
		assert.Equal(t, "pool1", rec.poolID)
		assert.Equal(t, "busy", rec.cleanupType)
		assert.Equal(t, 2, rec.count, "force-deleted count must equal exactly len(leaked)")
	}
}

func TestDistributedPurger_ForceDelete_NoopWhenMaxAgeZero_RecordsNoMetric(t *testing.T) {
	store := newPurgerFakeStore(
		&types.Instance{ID: "old", Name: "vm-old", Pool: "pool1", State: types.StateInUse, Started: time.Now().Add(-24 * time.Hour).Unix()},
	)
	driver := &flexibleMockDriver{driverName: "mock"}
	fakeMetrics := &fakePurgerMetrics{}
	d, pool := newPurgerTestManagerWithMetrics(store, driver, fakeMetrics)

	d.forceDeleteLeakedInstances(context.Background(), pool, "busy", 0)

	assert.Empty(t, fakeMetrics.forceDeleted, "maxAge=0 must not record any force-deleted metric")
}

// TestDistributedPurger_CleanupCapacities_OrphanedInUseReason exercises cleanupCapacities end to
// end for a single orphaned in-use capacity reservation: the reservation's instance no longer
// exists in the DB (GetInstanceByStageID finds nothing), so findOrphanedInUseCapacities should
// claim it and destroyCapacityFromReservation should destroy it and report
// reason=orphaned_inuse.
func TestDistributedPurger_CleanupCapacities_OrphanedInUseReason(t *testing.T) {
	const stageID = "stage-orphan"
	orphan := &types.CapacityReservation{
		StageID:          stageID,
		PoolName:         "pool1",
		ReservationID:    "res-orphan",
		InstanceID:       "", // no instance: this is exactly what makes it orphaned
		ReservationState: types.CapacityReservationStateInUse,
	}

	capacityStore := &mockCapacityReservationStore{
		ListFunc: func(_ context.Context, _ *types.CapacityReservationQueryParams, states []types.CapacityReservationState) ([]*types.CapacityReservation, error) {
			if len(states) == 1 && states[0] == types.CapacityReservationStateInUse {
				return []*types.CapacityReservation{orphan}, nil
			}
			// stale-terminating lookup: nothing stuck in terminating for this test.
			return nil, nil
		},
		FindAndClaimFunc: func(_ context.Context, params *types.CapacityReservationQueryParams, newState types.CapacityReservationState,
			allowedStates []types.CapacityReservationState) ([]*types.CapacityReservation, error) {
			// findOrphanedInUseCapacities claims the orphan from InUse -> Terminating.
			if len(allowedStates) == 1 && allowedStates[0] == types.CapacityReservationStateInUse && params.StageID == stageID {
				claimed := *orphan
				claimed.ReservationState = newState
				return []*types.CapacityReservation{&claimed}, nil
			}
			// Everything else (free-capacity claim, claimCapacityForTermination inside
			// DestroyCapacity) finds no matching rows in this scenario.
			return nil, nil
		},
	}

	instanceStore := &mockInstanceStore{
		ListFunc: func(_ context.Context, _ string, query *types.QueryParams) ([]*types.Instance, error) {
			// GetInstanceByStageID looks up by Stage; returning empty means "instance not found",
			// which is what makes this capacity reservation an orphan.
			assert.Equal(t, stageID, query.Stage)
			return nil, nil
		},
	}

	driver := &flexibleMockDriver{
		driverName: "mock",
		DestroyCapacityFunc: func(_ context.Context, _ *types.CapacityReservation) error {
			return nil
		},
	}

	fakeMetrics := &fakePurgerMetrics{}
	store := newPurgerFakeStore()
	d, pool := newPurgerTestManagerWithMetrics(store, driver, fakeMetrics)
	d.instanceStore = instanceStore
	d.capacityReservationStore = capacityStore
	pool.Pool.Driver = driver

	d.cleanupCapacities(context.Background(), pool, time.Hour)

	if assert.Len(t, fakeMetrics.capacityAttempts, 1) {
		rec := fakeMetrics.capacityAttempts[0]
		assert.Equal(t, "pool1", rec.poolID)
		assert.Equal(t, PurgerCapacityReasonOrphanedInUse, rec.reason)
		assert.Equal(t, PurgerOutcomeDestroyed, rec.outcome)
	}
}

// TestDistributedPurger_CleanupCapacities_StuckTerminatingReason covers the simpler,
// non-orphan path: a capacity reservation already stuck in the Terminating state past the
// cutoff must be reported with reason=stuck_terminating.
func TestDistributedPurger_CleanupCapacities_StuckTerminatingReason(t *testing.T) {
	stuck := &types.CapacityReservation{
		StageID:          "stage-stuck",
		PoolName:         "pool1",
		ReservationID:    "res-stuck",
		ReservationState: types.CapacityReservationStateTerminating,
	}

	capacityStore := &mockCapacityReservationStore{
		ListFunc: func(_ context.Context, _ *types.CapacityReservationQueryParams, states []types.CapacityReservationState) ([]*types.CapacityReservation, error) {
			if len(states) == 1 && states[0] == types.CapacityReservationStateTerminating {
				return []*types.CapacityReservation{stuck}, nil
			}
			return nil, nil // no InUse orphans, no Created free-capacities in this scenario
		},
		FindAndClaimFunc: func(_ context.Context, _ *types.CapacityReservationQueryParams, _ types.CapacityReservationState,
			_ []types.CapacityReservationState) ([]*types.CapacityReservation, error) {
			return nil, nil
		},
	}
	instanceStore := &mockInstanceStore{
		ListFunc: func(_ context.Context, _ string, _ *types.QueryParams) ([]*types.Instance, error) {
			return nil, nil
		},
	}
	driver := &flexibleMockDriver{
		driverName:          "mock",
		DestroyCapacityFunc: func(_ context.Context, _ *types.CapacityReservation) error { return nil },
	}

	fakeMetrics := &fakePurgerMetrics{}
	store := newPurgerFakeStore()
	d, pool := newPurgerTestManagerWithMetrics(store, driver, fakeMetrics)
	d.instanceStore = instanceStore
	d.capacityReservationStore = capacityStore
	pool.Pool.Driver = driver

	d.cleanupCapacities(context.Background(), pool, time.Hour)

	if assert.Len(t, fakeMetrics.capacityAttempts, 1) {
		rec := fakeMetrics.capacityAttempts[0]
		assert.Equal(t, PurgerCapacityReasonStuckTerminating, rec.reason)
		assert.Equal(t, PurgerOutcomeDestroyed, rec.outcome)
	}
}
