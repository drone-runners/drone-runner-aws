package ldb

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"

	"github.com/drone-runners/drone-runner-aws/types"
)

func newTestStore(t *testing.T) *InstanceStore {
	t.Helper()
	db, err := leveldb.OpenFile(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("open leveldb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewInstanceStore(db)
}

func TestInstanceStore_CountPoolInstances(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	n := 0
	add := func(pool, tenant, variant, image string, state types.InstanceState, count int) {
		for i := 0; i < count; i++ {
			n++
			id := fmt.Sprintf("i%d", n)
			inst := &types.Instance{ID: id, Name: id, Pool: pool, TenantID: tenant, VariantID: variant, Image: image, State: state}
			if err := s.Create(ctx, inst); err != nil {
				t.Fatalf("create %s: %v", id, err)
			}
		}
	}
	add("p1", "default", "variant_medium", "img-a", types.StateCreated, 3)
	add("p1", "default", "variant_medium", "img-a", types.StateInUse, 2)
	add("p1", "acme", "variant_large", "img-b", types.StateProvisioning, 1)
	add("p2", "default", "variant_medium", "img-a", types.StateCreated, 5) // another pool

	got, err := s.CountPoolInstances(ctx, "p1")
	if err != nil {
		t.Fatalf("CountPoolInstances must not panic or fail: %v", err)
	}
	want := []types.PoolInstanceCount{
		{TenantID: "acme", VariantID: "variant_large", ImageName: "img-b", State: types.StateProvisioning, Count: 1},
		{TenantID: "default", VariantID: "variant_medium", ImageName: "img-a", State: types.StateCreated, Count: 3},
		{TenantID: "default", VariantID: "variant_medium", ImageName: "img-a", State: types.StateInUse, Count: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CountPoolInstances(p1) = %+v\nwant %+v", got, want)
	}
}

func TestInstanceStore_CountPoolInstancesMatchesTheNameOnly(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.Create(ctx, &types.Instance{ID: "i1", Name: "i1", Pool: "p1", State: types.StateCreated}); err != nil {
		t.Fatal(err)
	}

	// List treats an empty name as every pool. Counting matches the SQL stores: no pool, no rows.
	for _, pool := range []string{"", "unknown"} {
		got, err := s.CountPoolInstances(ctx, pool)
		if err != nil {
			t.Fatalf("CountPoolInstances(%q): %v", pool, err)
		}
		if len(got) != 0 {
			t.Errorf("CountPoolInstances(%q) = %+v, want none", pool, got)
		}
	}
}
