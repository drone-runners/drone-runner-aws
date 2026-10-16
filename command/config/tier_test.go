package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kelseyhightower/envconfig"

	"github.com/drone-runners/drone-runner-aws/types"
)

func TestParse_Tier(t *testing.T) {
	yaml := `
version: "1"
instances:
  - name: paid-default
    type: google
    spec:
      image: img
  - name: paid-explicit
    type: google
    tier: paid
    spec:
      image: img
  - name: free
    type: google
    tier: FREE
    spec:
      image: img
`
	pf, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	want := map[string]string{
		"paid-default":  types.TierPaid,
		"paid-explicit": types.TierPaid,
		"free":          types.TierFree,
	}
	for i := range pf.Instances {
		inst := &pf.Instances[i]
		if inst.Tier != want[inst.Name] {
			t.Errorf("pool %s: Tier = %q, want %q", inst.Name, inst.Tier, want[inst.Name])
		}
		if inst.GetTier() != want[inst.Name] {
			t.Errorf("pool %s: GetTier() = %q, want %q", inst.Name, inst.GetTier(), want[inst.Name])
		}
	}
}

func TestParse_InvalidTier(t *testing.T) {
	yaml := `
version: "1"
instances:
  - name: bad
    type: google
    tier: premium
    spec:
      image: img
`
	_, err := Parse(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for unknown tier")
	}
	if !strings.Contains(err.Error(), `pool "bad"`) || !strings.Contains(err.Error(), "premium") {
		t.Errorf("error should name the pool and tier, got: %v", err)
	}
}

func TestInstance_GetTierDefaultsToPaid(t *testing.T) {
	inst := Instance{Name: "constructed"}
	if got := inst.GetTier(); got != types.TierPaid {
		t.Errorf("GetTier() = %q, want %q", got, types.TierPaid)
	}
}

func TestSchedulerEnvDefaults(t *testing.T) {
	var cfg EnvConfig
	if err := envconfig.Process("", &cfg); err != nil {
		t.Fatalf("envconfig: %v", err)
	}
	if cfg.Scheduler.Algorithm != "pool" {
		t.Errorf("Algorithm = %q, want pool", cfg.Scheduler.Algorithm)
	}
	if !reflect.DeepEqual(cfg.Scheduler.PredictionDrivers, []string{"google", "amazon"}) {
		t.Errorf("PredictionDrivers = %v, want [google amazon]", cfg.Scheduler.PredictionDrivers)
	}
	if cfg.Scheduler.CapabilityBackfill {
		t.Error("CapabilityBackfill should default to false")
	}
}

func TestSchedulerEnvOverrides(t *testing.T) {
	t.Setenv("DLITE_SCHEDULER_ALGORITHM", "capability")
	t.Setenv("DLITE_SCHEDULER_PREDICTION_DRIVERS", "amazon")
	t.Setenv("DLITE_SCHEDULER_CAPABILITY_BACKFILL", "true")

	var cfg EnvConfig
	if err := envconfig.Process("", &cfg); err != nil {
		t.Fatalf("envconfig: %v", err)
	}
	if cfg.Scheduler.Algorithm != "capability" {
		t.Errorf("Algorithm = %q, want capability", cfg.Scheduler.Algorithm)
	}
	if !reflect.DeepEqual(cfg.Scheduler.PredictionDrivers, []string{"amazon"}) {
		t.Errorf("PredictionDrivers = %v, want [amazon]", cfg.Scheduler.PredictionDrivers)
	}
	if !cfg.Scheduler.CapabilityBackfill {
		t.Error("CapabilityBackfill should be true")
	}
}
