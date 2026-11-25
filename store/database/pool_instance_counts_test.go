package database

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestInstanceStore_CountPoolInstancesMatchesList(t *testing.T) {
	ctx := context.Background()
	s := newTestInstanceStore(t)

	n := 0
	add := func(pool, tenant, variant, image string, state types.InstanceState, count int) {
		for i := 0; i < count; i++ {
			n++
			id := fmt.Sprintf("i%d", n)
			inst := &types.Instance{
				ID: id, Name: id, Pool: pool, State: state, Image: image, TenantID: tenant, VariantID: variant,
				CAKey: []byte("secret"), TLSKey: []byte("secret"), Labels: []byte("{}"),
			}
			if err := s.Create(ctx, inst); err != nil {
				t.Fatalf("create %s: %v", id, err)
			}
		}
	}
	add("p1", "default", "variant_medium", "img-a", types.StateCreated, 3)
	add("p1", "default", "variant_medium", "img-a", types.StateInUse, 2)
	add("p1", "default", "variant_medium", "img-b", types.StateCreated, 1)
	add("p1", "default", "variant_large", "img-a", types.StateProvisioning, 2)
	add("p1", "acme", "variant_medium", "img-a", types.StateHibernating, 4)
	add("p1", "default", "variant_medium", "img-a", types.StateTerminating, 1)
	add("p2", "default", "variant_medium", "img-a", types.StateCreated, 5) // another pool: never counted for p1

	got, err := s.CountPoolInstances(ctx, "p1")
	if err != nil {
		t.Fatalf("CountPoolInstances: %v", err)
	}

	listed, err := s.List(ctx, "p1", nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	type group struct {
		tenant, variant, image string
		state                  types.InstanceState
	}
	want := map[group]int{}
	for _, inst := range listed {
		want[group{inst.TenantID, inst.VariantID, inst.Image, inst.State}]++
	}

	have := map[group]int{}
	for _, c := range got {
		k := group{c.TenantID, c.VariantID, c.ImageName, c.State}
		if _, dup := have[k]; dup {
			t.Errorf("group %+v returned twice", k)
		}
		have[k] = c.Count
	}
	if len(have) != len(want) {
		t.Errorf("got %d groups, want %d", len(have), len(want))
	}
	var keys []string
	for k, c := range want {
		if have[k] != c {
			keys = append(keys, fmt.Sprintf("%+v: got %d want %d", k, have[k], c))
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Error(k)
	}
}

func TestInstanceStore_CountPoolInstancesEmptyPool(t *testing.T) {
	s := newTestInstanceStore(t)
	got, err := s.CountPoolInstances(context.Background(), "nothing")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no groups, got %+v", got)
	}
}
