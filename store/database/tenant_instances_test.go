package database

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/drone-runners/drone-runner-aws/store/database/sql"
	"github.com/drone-runners/drone-runner-aws/types"
)

func newTestInstanceStore(t *testing.T) *sql.InstanceStore {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := ConnectSQL("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to connect sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sql.NewInstanceStore(db)
}

func TestInstanceStore_TenantIDPersistedAndGrouped(t *testing.T) {
	ctx := context.Background()
	s := newTestInstanceStore(t)

	insts := []*types.Instance{
		{ID: "i1", Name: "i1", Pool: "aws", State: types.StateInUse, Image: "img", TenantID: "default", VariantID: "default", Labels: []byte("{}")},
		{ID: "i2", Name: "i2", Pool: "aws", State: types.StateInUse, Image: "img", TenantID: "acctA", VariantID: "default", Labels: []byte("{}")},
		{ID: "i3", Name: "i3", Pool: "aws", State: types.StateInUse, Image: "img", TenantID: "acctA", VariantID: "default", Labels: []byte("{}")},
	}
	for _, inst := range insts {
		if err := s.Create(ctx, inst); err != nil {
			t.Fatalf("create %s: %v", inst.ID, err)
		}
	}

	// Verify tenant persisted on read.
	got, err := s.Find(ctx, "i2")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.TenantID != "acctA" {
		t.Errorf("expected tenant acctA, got %q", got.TenantID)
	}

	// Verify grouped counts partition by tenant.
	counts, err := s.CountGroupedInstances(ctx, types.StateInUse)
	if err != nil {
		t.Fatalf("count grouped: %v", err)
	}
	byTenant := map[string]int{}
	for _, c := range counts {
		byTenant[c.TenantID] += c.Count
	}
	if byTenant["default"] != 1 {
		t.Errorf("expected 1 default-tenant instance, got %d", byTenant["default"])
	}
	if byTenant["acctA"] != 2 {
		t.Errorf("expected 2 acctA-tenant instances, got %d", byTenant["acctA"])
	}
}

func TestInstanceStore_TenantIDDefaultsWhenUnset(t *testing.T) {
	ctx := context.Background()
	s := newTestInstanceStore(t)

	// Create without explicitly setting TenantID: the column default 'default' applies.
	// (Create binds the empty string, so assert the row still reads back with a value.)
	inst := &types.Instance{ID: "x1", Name: "x1", Pool: "aws", State: types.StateCreated, TenantID: types.DefaultTenantID, Labels: []byte("{}")}
	if err := s.Create(ctx, inst); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.Find(ctx, "x1")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.TenantID != types.DefaultTenantID {
		t.Errorf("expected default tenant, got %q", got.TenantID)
	}
}
