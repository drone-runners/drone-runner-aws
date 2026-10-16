package database

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/drone-runners/drone-runner-aws/store/database/sql"
	"github.com/drone-runners/drone-runner-aws/types"
)

// The outbox store is postgres-only (it filters on JSONB params).
func TestOutbox_CountSetupJobs(t *testing.T) {
	dsn := os.Getenv("DLITE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("DLITE_TEST_POSTGRES_DSN not set")
	}
	db, err := ConnectSQL("postgres", dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("TRUNCATE outbox_jobs"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	ctx := context.Background()
	s := sql.NewOutboxStore(db)

	add := func(pool string, jobType types.OutboxJobType, params any) {
		t.Helper()
		var raw *json.RawMessage
		if params != nil {
			b, _ := json.Marshal(params)
			m := json.RawMessage(b)
			raw = &m
		}
		if err := s.Create(ctx, &types.OutboxJob{PoolName: pool, JobType: jobType, JobParams: raw}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	add("p1", types.OutboxJobTypeSetupInstance, types.SetupInstanceParams{VariantID: "variant_medium"})
	add("p1", types.OutboxJobTypeSetupInstance, types.SetupInstanceParams{VariantID: "variant_medium", TenantID: "default"})
	add("p1", types.OutboxJobTypeSetupInstance, types.SetupInstanceParams{VariantID: "variant_medium", TenantID: "acct"})
	add("p1", types.OutboxJobTypeSetupInstance, types.SetupInstanceParams{})
	add("p1", types.OutboxJobTypeSetupInstance, nil)
	add("p2", types.OutboxJobTypeSetupInstance, types.SetupInstanceParams{VariantID: "variant_medium"})
	add("p1", types.OutboxJobTypeScale, types.ScaleJobParams{WindowStart: 1})

	cases := []struct {
		pool, tenant, variant string
		want                  int
	}{
		{"p1", "", "variant_medium", 2},
		{"p1", "default", "variant_medium", 2},
		{"p1", "acct", "variant_medium", 1},
		{"p1", "", "", 2},
		{"p1", "default", "default", 2},
		{"p2", "", "variant_medium", 1},
		{"p3", "", "variant_medium", 0},
	}
	for _, tc := range cases {
		got, err := s.CountSetupJobs(ctx, tc.pool, tc.tenant, tc.variant)
		if err != nil {
			t.Fatalf("CountSetupJobs: %v", err)
		}
		if got != tc.want {
			t.Errorf("CountSetupJobs(%q, %q, %q) = %d, want %d", tc.pool, tc.tenant, tc.variant, got, tc.want)
		}
	}
}
