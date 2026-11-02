package harness

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/types"
)

// fakeCapabilityManager is a pool manager with capability scheduling: it returns scope and
// records what ClaimScope was asked.
type fakeCapabilityManager struct {
	fakeIManager
	scope         *types.ClaimScope
	scopePool     string
	scopeParams   *types.ProvisionParams
	mappedByScope string
}

func (f *fakeCapabilityManager) ClaimScope(_ context.Context, pool string, params *types.ProvisionParams, mapPool func(string) string) *types.ClaimScope {
	f.scopePool, f.scopeParams, f.mappedByScope = pool, params, mapPool(pool)
	return f.scope
}

var _ drivers.CapabilityProvisioner = (*fakeCapabilityManager)(nil)

var poolMapForAcct = map[string]map[string]string{"acct-m": {"pool1": "mapped"}}

func TestRequestPools(t *testing.T) {
	ctx := context.Background()
	params := &types.ProvisionParams{AccountID: "acct"}

	t.Run("pool manager without capability scheduling uses the request's fallbacks", func(t *testing.T) {
		pools, scope := requestPools(ctx, &fakeIManager{}, "pool1", []string{"fb1"}, []string{"env1"}, params, "acct", nil)
		assert.Equal(t, []string{"pool1", "fb1"}, pools)
		assert.Nil(t, scope)
	})
	t.Run("env fallbacks apply when the request has none", func(t *testing.T) {
		pools, _ := requestPools(ctx, &fakeIManager{}, "pool1", nil, []string{"env1"}, params, "acct", nil)
		assert.Equal(t, []string{"pool1", "env1"}, pools)
	})
	t.Run("capability manager without a scope keeps the fallbacks", func(t *testing.T) {
		pools, scope := requestPools(ctx, &fakeCapabilityManager{}, "pool1", []string{"fb1"}, nil, params, "acct", nil)
		assert.Equal(t, []string{"pool1", "fb1"}, pools)
		assert.Nil(t, scope)
	})
	t.Run("scope pools replace the request and env fallbacks", func(t *testing.T) {
		want := &types.ClaimScope{Pools: []string{"pool1", "pool2"}}
		pm := &fakeCapabilityManager{scope: want}
		pools, scope := requestPools(ctx, pm, "pool1", []string{"fb1"}, []string{"env1"}, params, "acct-m", poolMapForAcct)
		assert.Equal(t, []string{"pool1", "pool2"}, pools)
		assert.Same(t, want, scope)
		assert.Equal(t, "pool1", pm.scopePool)
		assert.Same(t, params, pm.scopeParams)
		assert.Equal(t, "mapped", pm.mappedByScope, "ClaimScope maps pools with the account's POOL_MAP")
	})
}

func TestTargetPool(t *testing.T) {
	assert.Equal(t, "mapped", targetPool("pool1", nil, "acct-m", poolMapForAcct), "POOL_MAP applies without a scope")
	assert.Equal(t, "pool2", targetPool("pool2", nil, "acct-m", poolMapForAcct))
	assert.Equal(t, "pool1", targetPool("pool1", &types.ClaimScope{}, "acct-m", poolMapForAcct), "scope pools are already mapped")
}

func TestReservedPoolFirst(t *testing.T) {
	reserved := func(pool string) *types.CapacityReservation { return &types.CapacityReservation{PoolName: pool} }
	scope := &types.ClaimScope{}
	cases := []struct {
		name     string
		capacity *types.CapacityReservation
		scope    *types.ClaimScope
		want     []string
	}{
		{"no reservation", nil, nil, []string{"a", "b", "c"}},
		{"reservation without a pool", reserved(""), scope, []string{"a", "b", "c"}},
		{"reserved pool moves to the front", reserved("c"), nil, []string{"c", "a", "b"}},
		{"reserved first pool stays", reserved("a"), scope, []string{"a", "b", "c"}},
		{"pool outside the list is ignored without a scope", reserved("x"), nil, []string{"a", "b", "c"}},
		{"pool claimed outside the scope's pools is tried first", reserved("x"), scope, []string{"x", "a", "b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, reservedPoolFirst([]string{"a", "b", "c"}, tc.capacity, tc.scope))
		})
	}
}

type fakeCapacityStore struct {
	created []*types.CapacityReservation
}

func (s *fakeCapacityStore) Find(context.Context, string) (*types.CapacityReservation, error) {
	return nil, sql.ErrNoRows
}
func (s *fakeCapacityStore) Create(_ context.Context, c *types.CapacityReservation) error {
	s.created = append(s.created, c)
	return nil
}
func (s *fakeCapacityStore) Delete(context.Context, string) error { return nil }
func (s *fakeCapacityStore) List(context.Context, *types.CapacityReservationQueryParams, []types.CapacityReservationState) ([]*types.CapacityReservation, error) {
	return nil, nil
}
func (s *fakeCapacityStore) FindAndClaim(
	context.Context, *types.CapacityReservationQueryParams, types.CapacityReservationState, []types.CapacityReservationState,
) ([]*types.CapacityReservation, error) {
	return nil, nil
}

func newTestCapacityMetrics() *metric.Metrics {
	return &metric.Metrics{
		CapacityReservationCount:                metric.CapacityReservationCount(),
		CapacityReservationDurationCount:        metric.CapacityReservationDurationCount(),
		CapacityReservationPerPoolDurationCount: metric.CapacityReservationPerPoolDurationCount(),
		CapacityReservationFallbackCount:        metric.CapacityReservationFallbackCount(),
		CapacityReservationFailedCount:          metric.CapacityReservationFailedCount(),
	}
}

func TestHandleCapacityReservation_ClaimScope(t *testing.T) {
	cases := []struct {
		name        string
		req         CapacityReservationRequest
		wantAccount string
		wantOwner   string
	}{
		{"paid account", CapacityReservationRequest{ID: "stage", PoolID: "pool1", Context: Context{AccountID: "acct-m"}}, "acct-m", "acct-m"},
		{"free account", CapacityReservationRequest{ID: "stage", PoolID: "pool1", Context: Context{AccountID: "acct-m", IsFreeAccount: true}}, types.FreeAccountID, types.FreeAccountID},
		{"freeCI tag", CapacityReservationRequest{ID: "stage", PoolID: "pool1", Tags: map[string]string{"freeCI": "true"}, Context: Context{AccountID: "acct-m"}}, types.FreeAccountID, types.FreeAccountID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pm := &fakeCapabilityManager{scope: &types.ClaimScope{Pools: []string{"pool1", "pool2", "pool3"}}}
			pm.provisionPoolFunc = func(pool string) (*types.CapacityReservation, error) {
				if pool == "pool1" {
					return nil, errors.New("stockout")
				}
				return &types.CapacityReservation{PoolName: "pool9", InstanceID: "inst-9"}, nil
			}
			crs := &fakeCapacityStore{}
			req := tc.req

			capacity, err := HandleCapacityReservation(context.Background(), &req, crs, poolMapForAcct, "runner", pm, newTestCapacityMetrics(), []string{"env1"})

			require.NoError(t, err)
			assert.Equal(t, tc.wantAccount, pm.scopeParams.AccountID, "scope is resolved for the owner")
			assert.Equal(t, "mapped", pm.mappedByScope, "POOL_MAP keyed by the context account")
			assert.Equal(t, []string{"pool1", "pool2"}, pm.provisionedPools, "scope pools in order, unmapped, no fallbacks")
			assert.Equal(t, tc.wantOwner, pm.provisionOwners[0])
			assert.Same(t, pm.scope, pm.provisionParams.Claim)
			assert.Equal(t, "pool9", capacity.PoolName, "reservation records the pool the instance came from")
			require.Len(t, crs.created, 1)
			assert.Equal(t, "stage", crs.created[0].StageID)
		})
	}
}

func TestHandleCapacityReservation_WithoutScopeMapsFallbacks(t *testing.T) {
	pm := &fakeIManager{provisionPoolFunc: func(pool string) (*types.CapacityReservation, error) {
		if pool == "mapped" {
			return nil, errors.New("stockout")
		}
		return &types.CapacityReservation{PoolName: pool}, nil
	}}
	req := CapacityReservationRequest{ID: "stage", PoolID: "pool1", FallbackPoolIDs: []string{"fb1"}, Context: Context{AccountID: "acct-m"}}

	capacity, err := HandleCapacityReservation(context.Background(), &req, &fakeCapacityStore{}, poolMapForAcct, "runner", pm, newTestCapacityMetrics(), nil)

	require.NoError(t, err)
	assert.Equal(t, []string{"mapped", "fb1"}, pm.provisionedPools)
	assert.Nil(t, pm.provisionParams.Claim)
	assert.Equal(t, "fb1", capacity.PoolName)
}
