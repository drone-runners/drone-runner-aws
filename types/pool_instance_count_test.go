package types

import (
	"reflect"
	"testing"
)

func TestAggregatePoolInstances(t *testing.T) {
	inst := func(tenant, variant, image string, state InstanceState) *Instance {
		return &Instance{TenantID: tenant, VariantID: variant, Image: image, State: state}
	}
	got := AggregatePoolInstances([]*Instance{
		inst("", "variant_medium", "img-a", StateCreated),
		inst("", "variant_medium", "img-a", StateCreated),
		inst("", "variant_medium", "img-a", StateInUse),
		inst("", "variant_medium", "img-b", StateCreated),
		inst("acme", "variant_medium", "img-a", StateCreated),
		inst("acme", "", "img-a", StateTerminating),
	})

	// Tenant and variant are kept as stored (an empty one is not resolved to the default), and
	// the groups come in a fixed order.
	want := []PoolInstanceCount{
		{TenantID: "", VariantID: "variant_medium", ImageName: "img-a", State: StateCreated, Count: 2},
		{TenantID: "", VariantID: "variant_medium", ImageName: "img-a", State: StateInUse, Count: 1},
		{TenantID: "", VariantID: "variant_medium", ImageName: "img-b", State: StateCreated, Count: 1},
		{TenantID: "acme", VariantID: "", ImageName: "img-a", State: StateTerminating, Count: 1},
		{TenantID: "acme", VariantID: "variant_medium", ImageName: "img-a", State: StateCreated, Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AggregatePoolInstances = %+v\nwant %+v", got, want)
	}
}

func TestAggregatePoolInstances_Empty(t *testing.T) {
	if got := AggregatePoolInstances(nil); len(got) != 0 {
		t.Errorf("expected no groups, got %+v", got)
	}
}
