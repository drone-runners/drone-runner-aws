package database

import (
	"context"
	"os"
	"testing"

	"github.com/drone-runners/drone-runner-aws/store/database/sql"
	"github.com/drone-runners/drone-runner-aws/types"
)

// FindAndClaim uses postgres-only SQL (FOR UPDATE SKIP LOCKED).
func newPostgresInstanceStore(t *testing.T) *sql.InstanceStore {
	t.Helper()
	dsn := os.Getenv("DLITE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("DLITE_TEST_POSTGRES_DSN not set")
	}
	db, err := ConnectSQL("postgres", dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("TRUNCATE instances"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return sql.NewInstanceStore(db)
}

func TestInstanceStore_FindAndClaimAcrossPools(t *testing.T) {
	ctx := context.Background()
	s := newPostgresInstanceStore(t)

	free := func(id, pool, variant, tenant, image string) *types.Instance {
		return &types.Instance{
			ID: id, Name: id, Pool: pool, State: types.StateCreated, Image: image,
			VariantID: variant, TenantID: tenant, Labels: []byte("{}"),
			Platform: types.Platform{OS: "linux", Arch: "amd64"},
		}
	}
	for _, inst := range []*types.Instance{
		free("a-small", "pool-a", "small", "default", "img"),
		free("b-large", "pool-b", "large", "default", "img"),
		free("b-tenant", "pool-b", "large", "acct", "img"),
		free("b-other-image", "pool-b", "large", "default", "other"),
		free("c-large", "pool-c", "large", "default", "img"),
	} {
		if err := s.Create(ctx, inst); err != nil {
			t.Fatalf("create %s: %v", inst.ID, err)
		}
	}

	claim := func(params *types.QueryParams) *types.Instance {
		t.Helper()
		inst, err := s.FindAndClaim(ctx, params, types.StateInUse, []types.InstanceState{types.StateCreated}, true)
		if err != nil {
			return nil
		}
		return inst
	}

	// A single pool still filters by PoolName.
	if inst := claim(&types.QueryParams{PoolName: "pool-a", VariantID: "large", TenantID: "default", ImageName: "img"}); inst != nil {
		t.Fatalf("pool-a has no large instance, claimed %s", inst.ID)
	}

	// PoolNames claims from any listed pool that matches variant, tenant and image.
	across := &types.QueryParams{PoolName: "pool-a", PoolNames: []string{"pool-a", "pool-b"}, VariantID: "large", TenantID: "default", ImageName: "img"}
	inst := claim(across)
	if inst == nil || inst.ID != "b-large" {
		t.Fatalf("expected b-large, got %+v", inst)
	}
	if inst.State != types.StateInUse {
		t.Errorf("claimed state = %s, want in_use", inst.State)
	}

	// pool-c is not listed, and the remaining pool-b instances differ in tenant or image.
	if extra := claim(across); extra != nil {
		t.Fatalf("expected no match, claimed %s", extra.ID)
	}

	inst = claim(&types.QueryParams{PoolNames: []string{"pool-b"}, VariantID: "large", TenantID: "acct", ImageName: "img"})
	if inst == nil || inst.ID != "b-tenant" {
		t.Fatalf("expected b-tenant, got %+v", inst)
	}
}

func TestInstanceStore_FindAndClaimAnyPool(t *testing.T) {
	ctx := context.Background()
	s := newPostgresInstanceStore(t)

	for _, inst := range []*types.Instance{
		{ID: "free-pool", Pool: "linux-free", Platform: types.Platform{OS: "linux", Arch: "amd64"}},
		{ID: "arm", Pool: "linux-arm", Platform: types.Platform{OS: "linux", Arch: "arm64"}},
		{ID: "paid", Pool: "linux-paid", Platform: types.Platform{OS: "linux", Arch: "amd64"}},
	} {
		inst.Name, inst.State, inst.Image, inst.VariantID, inst.TenantID, inst.Labels =
			inst.ID, types.StateCreated, "img", "default", "default", []byte("{}")
		if err := s.Create(ctx, inst); err != nil {
			t.Fatalf("create %s: %v", inst.ID, err)
		}
	}

	params := &types.QueryParams{
		PoolName: "linux-other", AnyPool: true, ExcludePoolNames: []string{"linux-free"},
		Platform: &types.Platform{OS: "linux", Arch: "amd64"}, VariantID: "default", TenantID: "default", ImageName: "img",
	}
	inst, err := s.FindAndClaim(ctx, params, types.StateInUse, []types.InstanceState{types.StateCreated}, true)
	if err != nil || inst.ID != "paid" {
		t.Fatalf("expected paid, got %+v, %v", inst, err)
	}
	// The free pool is excluded and the arm64 instance is another platform.
	if extra, err := s.FindAndClaim(ctx, params, types.StateInUse, []types.InstanceState{types.StateCreated}, true); err == nil {
		t.Fatalf("expected no match, claimed %s", extra.ID)
	}
}

func TestInstanceStore_FindAndClaimWithTenOrMoreArgs(t *testing.T) {
	ctx := context.Background()
	s := newPostgresInstanceStore(t)

	inst := &types.Instance{
		ID: "target", Name: "target", Pool: "pool-d", State: types.StateCreated, Image: "img", Size: "m1",
		VariantID: "large", TenantID: "t1", RunnerName: "runner", Labels: []byte("{}"),
		Platform: types.Platform{OS: "linux", Arch: "amd64"},
	}
	if err := s.Create(ctx, inst); err != nil {
		t.Fatalf("create: %v", err)
	}

	params := &types.QueryParams{
		PoolNames: []string{"pool-a", "pool-b", "pool-c", "pool-d"}, RunnerName: "runner", ImageName: "img",
		MachineType: "m1", VariantID: "large", TenantID: "t1",
		Platform: &types.Platform{OS: "linux", Arch: "amd64"},
	}
	got, err := s.FindAndClaim(ctx, params, types.StateInUse, []types.InstanceState{types.StateCreated, types.StateHibernating}, true)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got.ID != "target" || got.State != types.StateInUse || got.Started == 0 {
		t.Fatalf("claimed %s state=%s started=%d", got.ID, got.State, got.Started)
	}
}
