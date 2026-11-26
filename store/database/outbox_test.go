package database

import (
	"context"
	"encoding/json"
	"os"
	"sort"
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

	// Jobs without a tenant or variant count as the default, and other pools and job types are
	// not counted.
	cases := []struct {
		pool string
		want []types.SetupJobCount
	}{
		{"p1", []types.SetupJobCount{
			{TenantID: "acct", VariantID: "variant_medium", Count: 1},
			{TenantID: "default", VariantID: "default", Count: 2},
			{TenantID: "default", VariantID: "variant_medium", Count: 2},
		}},
		{"p2", []types.SetupJobCount{{TenantID: "default", VariantID: "variant_medium", Count: 1}}},
		{"p3", nil},
	}
	for _, tc := range cases {
		got, err := s.CountSetupJobs(ctx, tc.pool)
		if err != nil {
			t.Fatalf("CountSetupJobs(%q): %v", tc.pool, err)
		}
		sort.Slice(got, func(i, j int) bool {
			return got[i].TenantID+"/"+got[i].VariantID < got[j].TenantID+"/"+got[j].VariantID
		})
		if len(got) != len(tc.want) {
			t.Fatalf("CountSetupJobs(%q) = %v, want %v", tc.pool, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("CountSetupJobs(%q)[%d] = %v, want %v", tc.pool, i, got[i], tc.want[i])
			}
		}
	}
}
