package drivers

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/drone-runners/drone-runner-aws/types"
)

// TestDistributedHibernate_UsesTenantDriver proves multi-tenant hibernate calls the
// instance's tenant driver, not the pool default driver.
func TestDistributedHibernate_UsesTenantDriver(t *testing.T) {
	var defaultCalls, tenantCalls atomic.Int32

	def := &flexibleMockDriver{
		canHibernate: true,
		HibernateFunc: func(context.Context, string, string, string) error {
			defaultCalls.Add(1)
			return nil
		},
	}
	acct := &flexibleMockDriver{
		canHibernate: true,
		HibernateFunc: func(context.Context, string, string, string) error {
			tenantCalls.Add(1)
			return nil
		},
	}

	inst := &types.Instance{
		ID:       "vm-1",
		Pool:     "gcp-mt",
		TenantID: "acctA",
		State:    types.StateCreated,
		Zone:     "us-central1-a",
	}

	store := &mockInstanceStore{
		FindAndClaimFunc: func(
			_ context.Context, _ *types.QueryParams, newState types.InstanceState, _ []types.InstanceState, _ bool,
		) (*types.Instance, error) {
			claimed := *inst
			claimed.State = newState
			return &claimed, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error { return nil },
	}

	mgr := &Manager{
		instanceStore: store,
		poolMap: map[string]*poolEntry{
			"gcp-mt": {
				Pool: Pool{
					Name:   "gcp-mt",
					Driver: def,
					TenantDrivers: map[string]Driver{
						types.DefaultTenantID: def,
						"acctA":               acct,
					},
				},
			},
		},
	}
	dm := &DistributedManager{Manager: *mgr}

	if err := dm.hibernate(context.Background(), "gcp-mt", inst, true); err != nil {
		t.Fatalf("hibernate: %v", err)
	}
	if tenantCalls.Load() != 1 {
		t.Errorf("tenant driver Hibernate calls: got %d want 1", tenantCalls.Load())
	}
	if defaultCalls.Load() != 0 {
		t.Errorf("default driver must not be used: got %d calls", defaultCalls.Load())
	}
}

// TestHibernateOrStop_UsesTenantCanHibernate verifies Suspend/hibernate gating consults the
// tenant driver, not the pool default.
func TestHibernateOrStop_UsesTenantCanHibernate(t *testing.T) {
	var hibernateCalls atomic.Int32
	def := &flexibleMockDriver{canHibernate: false}
	acct := &flexibleMockDriver{
		canHibernate: true,
		HibernateFunc: func(context.Context, string, string, string) error {
			hibernateCalls.Add(1)
			return nil
		},
	}

	inst := &types.Instance{
		ID:       "vm-2",
		Pool:     "gcp-mt",
		TenantID: "acctA",
		State:    types.StateCreated,
		Zone:     "us-central1-a",
	}
	store := &mockInstanceStore{
		FindFunc: func(_ context.Context, id string) (*types.Instance, error) {
			out := *inst
			out.ID = id
			return &out, nil
		},
		UpdateFunc: func(_ context.Context, _ *types.Instance) error { return nil },
	}

	mgr := &Manager{
		instanceStore: store,
		poolMap: map[string]*poolEntry{
			"gcp-mt": {
				Pool: Pool{
					Name:   "gcp-mt",
					Driver: def,
					TenantDrivers: map[string]Driver{
						types.DefaultTenantID: def,
						"acctA":               acct,
					},
				},
			},
		},
	}

	if err := mgr.hibernateOrStopWithRetries(context.Background(), "gcp-mt", inst, false); err != nil {
		t.Fatalf("hibernateOrStopWithRetries: %v", err)
	}
	if hibernateCalls.Load() != 1 {
		t.Errorf("expected tenant hibernate (CanHibernate=true), got %d calls", hibernateCalls.Load())
	}

	// Same pool default tenant cannot hibernate and fallbackStop=false → no-op.
	defInst := &types.Instance{ID: "vm-3", Pool: "gcp-mt", TenantID: types.DefaultTenantID, State: types.StateCreated}
	if err := mgr.hibernateOrStopWithRetries(context.Background(), "gcp-mt", defInst, false); err != nil {
		t.Fatalf("default tenant: %v", err)
	}
	if hibernateCalls.Load() != 1 {
		t.Errorf("default tenant should skip hibernate: got %d calls", hibernateCalls.Load())
	}
}
