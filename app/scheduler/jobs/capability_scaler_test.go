package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/app/predictor"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/types"
)

const capEgressTenant = "egress-acct"

// capabilityPools: three paid linux/amd64 pools sharing variant_medium with different machine
// shapes, an image-pinned variant in the primary only, and an egress tenant in the primary only.
const capabilityPools = `
version: "1"
instances:
  - name: linux-amd64
    type: google
    pool: 2
    platform: {os: linux, arch: amd64}
    spec: {image: base}
    variants:
      - {variant_id: variant_medium, pool: 1, limit: 4, machine_type: c4d-standard-8}
      - {variant_id: variant_ubuntu24, pool: 0, machine_type: c4d-standard-8, image_name: ubuntu24-img}
    tenants:
      - ids: [` + capEgressTenant + `]
        pool: 1
  - name: linux-amd64-fallback
    type: google
    pool: 1
    platform: {os: linux, arch: amd64}
    spec: {image: base}
    variants:
      - {variant_id: variant_medium, pool: 0, machine_type: c4d-standard-8-lssd, disk_type: hyperdisk-balanced}
  - name: linux-amd64-west4
    type: google
    pool: 0
    platform: {os: linux, arch: amd64}
    spec: {image: base}
    variants:
      - {variant_id: variant_medium, pool: 0, machine_type: c4d-standard-8}
`

var (
	capBase   = types.CapabilityKey{Tier: types.TierPaid, OS: "linux", Arch: "amd64", VariantID: "default", TenantID: "default"}
	capMedium = types.CapabilityKey{Tier: types.TierPaid, OS: "linux", Arch: "amd64", VariantID: "variant_medium", TenantID: "default"}
	capUbuntu = types.CapabilityKey{Tier: types.TierPaid, OS: "linux", Arch: "amd64", VariantID: "variant_ubuntu24", TenantID: "default"}
	capEgress = types.CapabilityKey{Tier: types.TierPaid, OS: "linux", Arch: "amd64", VariantID: "default", TenantID: capEgressTenant}
)

type fakeScalerManager struct {
	destroyed []string
}

// FullyQualifiedImage mimics the driver: an empty image resolves to the pool default.
func (m *fakeScalerManager) FullyQualifiedImage(_ context.Context, pool, image string) (string, error) {
	if image == "" {
		return "projects/p/images/" + pool + "-default", nil
	}
	return "projects/p/images/" + image, nil
}

func (m *fakeScalerManager) Destroy(_ context.Context, pool, id string, _ *types.Instance, _ *storage.CleanupType) error {
	m.destroyed = append(m.destroyed, pool+"/"+id)
	return nil
}

type fixedPredictor struct {
	n      int
	inputs []predictor.PredictionInput
}

func (p *fixedPredictor) Predict(_ context.Context, in *predictor.PredictionInput) (*predictor.PredictionResult, error) {
	p.inputs = append(p.inputs, *in)
	return &predictor.PredictionResult{PredictedInstances: p.n}, nil
}

func (p *fixedPredictor) Name() string { return "fixed" }

type capHarness struct {
	scaler    *CapabilityScaler
	pred      *fixedPredictor
	instances *MockInstanceStore
	outbox    *MockOutboxStore
	manager   *fakeScalerManager
	capStore  *mockCapabilityHistoryStore
	metrics   *metric.Metrics
	nextID    int
}

func newCapHarness(t *testing.T, predicted int, mutate func(*types.ScalerConfig)) *capHarness {
	t.Helper()
	pf, err := config.Parse(strings.NewReader(capabilityPools))
	if err != nil {
		t.Fatalf("parse pools: %v", err)
	}
	catalog, err := capability.NewCatalog(pf, nil, nil)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	cfg := types.ScalerConfig{Enabled: true, RecentUsageLookbackDays: 7}
	if mutate != nil {
		mutate(&cfg)
	}
	h := &capHarness{
		pred:      &fixedPredictor{n: predicted},
		instances: NewMockInstanceStore(),
		outbox:    NewMockOutboxStore(),
		manager:   &fakeScalerManager{},
		capStore:  &mockCapabilityHistoryStore{},
		metrics: &metric.Metrics{
			ScalerCapabilityPredictedInstances: metric.ScalerCapabilityPredictedInstances(),
			ScalerCapabilityTargetInstances:    metric.ScalerCapabilityTargetInstances(),
			ScalerCapabilityFreeInstances:      metric.ScalerCapabilityFreeInstances(),
			ScalerCreateSpillCount:             metric.ScalerCreateSpillCount(),
		},
	}
	h.scaler = NewCapabilityScaler(h.manager, h.pred, h.instances, h.capStore, h.outbox, catalog, cfg, h.metrics)
	return h
}

// add creates instances in a pool. image "" means the pool's default image as resolved by the manager.
func (h *capHarness) add(n int, pool, tenant, variant, image string, state types.InstanceState, source types.InstanceSource) {
	fq, _ := h.manager.FullyQualifiedImage(context.Background(), pool, image)
	for i := 0; i < n; i++ {
		h.nextID++
		h.instances.AddInstance(&types.Instance{
			ID: fmt.Sprintf("i%d", h.nextID), Pool: pool, TenantID: tenant, VariantID: variant,
			Image: fq, State: state, Source: source,
		})
	}
}

type setupJob struct {
	pool   string
	params types.SetupInstanceParams
}

func (h *capHarness) setupJobs(t *testing.T) []setupJob {
	t.Helper()
	var out []setupJob
	for _, j := range h.outbox.GetJobsByType(types.OutboxJobTypeSetupInstance) {
		var p types.SetupInstanceParams
		if err := json.Unmarshal(*j.JobParams, &p); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		out = append(out, setupJob{pool: j.PoolName, params: p})
	}
	return out
}

// jobCounts returns [live, hibernated] setup job counts per pool.
func jobCounts(jobs []setupJob) map[string][2]int {
	out := map[string][2]int{}
	for i := range jobs {
		c := out[jobs[i].pool]
		if jobs[i].params.Hibernate {
			c[1]++
		} else {
			c[0]++
		}
		out[jobs[i].pool] = c
	}
	return out
}

func (h *capHarness) scale(t *testing.T, key *types.CapabilityKey) {
	t.Helper()
	if err := h.scaler.ScaleGroup(context.Background(), key.String(), 1000, 2800); err != nil {
		t.Fatalf("ScaleGroup: %v", err)
	}
}

func assertCounts(t *testing.T, got, want map[string][2]int) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("jobs per pool = %v, want %v", got, want)
		return
	}
	for pool, w := range want {
		if got[pool] != w {
			t.Errorf("jobs per pool = %v, want %v", got, want)
			return
		}
	}
}

func TestPlanCapabilityScale(t *testing.T) {
	slot := func(minSize, limit, free, used int) capabilitySlot {
		return capabilitySlot{member: capability.Member{MinSize: minSize, Limit: limit}, free: free, used: used}
	}
	cases := []struct {
		name         string
		slots        []capabilitySlot
		delta        int
		buffer       int
		hibernateAll bool
		want         []slotPlan
		unplaced     int
	}{
		{
			name:  "fills first pool when unlimited",
			slots: []capabilitySlot{slot(0, 0, 0, 0), slot(0, 0, 0, 0)},
			delta: 5,
			want:  []slotPlan{{live: 5}, {}},
		},
		{
			name:  "min sizes first, in order",
			slots: []capabilitySlot{slot(1, 0, 1, 1), slot(2, 0, 0, 0), slot(3, 0, 1, 1)},
			delta: 4,
			want:  []slotPlan{{}, {live: 2}, {live: 2}},
		},
		{
			name:  "limit moves the rest to the next pool",
			slots: []capabilitySlot{slot(0, 3, 1, 2), slot(0, 0, 0, 0)},
			delta: 4,
			want:  []slotPlan{{live: 1}, {live: 3}},
		},
		{
			name:     "reports what does not fit",
			slots:    []capabilitySlot{slot(0, 2, 0, 1), slot(0, 1, 0, 1)},
			delta:    3,
			want:     []slotPlan{{live: 1}, {}},
			unplaced: 2,
		},
		{
			name:   "buffer is hibernated after live",
			slots:  []capabilitySlot{slot(0, 3, 0, 0), slot(0, 0, 0, 0)},
			delta:  2,
			buffer: 2,
			want:   []slotPlan{{live: 2, hibernated: 1}, {hibernated: 1}},
		},
		{
			name:         "recent-usage floor is hibernated",
			slots:        []capabilitySlot{slot(0, 0, 0, 0)},
			delta:        3,
			hibernateAll: true,
			want:         []slotPlan{{hibernated: 3}},
		},
		{
			name:  "scale down from the end, keeping min sizes",
			slots: []capabilitySlot{slot(1, 0, 3, 3), slot(1, 0, 2, 2), slot(0, 0, 2, 2)},
			delta: -4,
			want:  []slotPlan{{remove: 1}, {remove: 1}, {remove: 2}},
		},
		{
			name:  "scale down never goes below min",
			slots: []capabilitySlot{slot(2, 0, 2, 2), slot(1, 0, 2, 2)},
			delta: -5,
			want:  []slotPlan{{}, {remove: 1}},
		},
		{
			name:  "no change",
			slots: []capabilitySlot{slot(1, 0, 1, 1)},
			want:  []slotPlan{{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, unplaced := planCapabilityScale(tc.slots, tc.delta, tc.buffer, tc.hibernateAll)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) || unplaced != tc.unplaced {
				t.Errorf("plan = %v unplaced=%d, want %v unplaced=%d", got, unplaced, tc.want, tc.unplaced)
			}
		})
	}
}

func TestScaleGroup_ScalesUpAcrossPoolsInOrder(t *testing.T) {
	h := newCapHarness(t, 10, nil)
	// Primary: 1 free (counts), 1 free on a stale image (doesn't count as free), 1 in use.
	h.add(1, "linux-amd64", "default", "variant_medium", "", types.StateCreated, types.InstanceSourcePredictor)
	h.add(1, "linux-amd64", "default", "variant_medium", "old-img", types.StateCreated, types.InstanceSourcePredictor)
	h.add(1, "linux-amd64", "default", "variant_medium", "", types.StateInUse, types.InstanceSourcePredictor)
	// Other tenants, variants and terminating instances are ignored.
	h.add(5, "linux-amd64", capEgressTenant, "variant_medium", "", types.StateCreated, types.InstanceSourcePredictor)
	h.add(5, "linux-amd64-fallback", "default", "default", "", types.StateCreated, types.InstanceSourcePool)
	h.add(5, "linux-amd64-fallback", "default", "variant_medium", "", types.StateTerminating, types.InstanceSourcePredictor)

	h.scale(t, &capMedium)

	// target 10 - free 1 = 9; primary has room for 1 under limit 4 (3 used), the rest go to fallback.
	jobs := h.setupJobs(t)
	assertCounts(t, jobCounts(jobs), map[string][2]int{"linux-amd64": {1, 0}, "linux-amd64-fallback": {8, 0}})

	for _, j := range jobs {
		p := j.params
		if p.Capability == nil || *p.Capability != capMedium {
			t.Fatalf("job missing capability key: %+v", p)
		}
		if len(p.TriedPools) != 1 || p.TriedPools[0] != j.pool {
			t.Errorf("TriedPools = %v, want [%s]", p.TriedPools, j.pool)
		}
		if p.Source != types.InstanceSourcePredictor || p.VariantID != "variant_medium" || p.TenantID != "default" || p.ImageName != "" {
			t.Errorf("unexpected params for %s: %+v", j.pool, p)
		}
		wantMachine := map[string]string{"linux-amd64": "c4d-standard-8", "linux-amd64-fallback": "c4d-standard-8-lssd"}[j.pool]
		if p.MachineType != wantMachine {
			t.Errorf("%s: machine type %q, want the pool's own variant shape %q", j.pool, p.MachineType, wantMachine)
		}
		if j.pool == "linux-amd64-fallback" && p.DiskType != "hyperdisk-balanced" {
			t.Errorf("fallback variant disk type not applied: %+v", p)
		}
	}

	if got := h.pred.inputs; len(got) != 1 || got[0].Capability == nil || *got[0].Capability != capMedium {
		t.Errorf("expected one capability prediction for the group, got %+v", got)
	}

	labels := []string{"paid", "linux", "amd64", "variant_medium", "default"}
	if v := testutil.ToFloat64(h.metrics.ScalerCapabilityPredictedInstances.WithLabelValues(labels...)); v != 10 {
		t.Errorf("predicted metric = %v, want 10", v)
	}
	if v := testutil.ToFloat64(h.metrics.ScalerCapabilityFreeInstances.WithLabelValues(append(labels, "linux-amd64")...)); v != 1 {
		t.Errorf("free metric for primary = %v, want 1", v)
	}
	if v := testutil.ToFloat64(h.metrics.ScalerCapabilityTargetInstances.WithLabelValues(append(labels, "linux-amd64-fallback")...)); v != 8 {
		t.Errorf("target metric for fallback = %v, want 8", v)
	}
}

func TestScaleGroup_VariantImageAndPoolDefault(t *testing.T) {
	h := newCapHarness(t, 2, nil)
	// An instance on the variant image counts as free; it must not be confused with the pool default.
	h.add(1, "linux-amd64", "default", "variant_ubuntu24", "ubuntu24-img", types.StateCreated, types.InstanceSourcePredictor)
	h.add(3, "linux-amd64", "default", "variant_ubuntu24", "", types.StateCreated, types.InstanceSourcePredictor)

	h.scale(t, &capUbuntu)

	jobs := h.setupJobs(t)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job (target 2 - free 1), got %d", len(jobs))
	}
	if jobs[0].params.ImageName != "ubuntu24-img" {
		t.Errorf("variant image should be used, got %q", jobs[0].params.ImageName)
	}
}

func TestScaleGroup_MinSizesFirst(t *testing.T) {
	h := newCapHarness(t, 3, nil)
	h.scale(t, &capBase)
	// Base group min sizes: primary 2, fallback 1, west4 0.
	assertCounts(t, jobCounts(h.setupJobs(t)), map[string][2]int{"linux-amd64": {2, 0}, "linux-amd64-fallback": {1, 0}})
}

func TestScaleGroup_Buffer(t *testing.T) {
	h := newCapHarness(t, 7, func(c *types.ScalerConfig) { c.ScalePercent = 150 })
	h.add(3, "linux-amd64", "default", "default", "", types.StateCreated, types.InstanceSourcePool)
	h.scale(t, &capBase)
	// delta 4 live + ceil(4*0.5)=2 hibernated. Fallback's min of 1 is filled first, the rest go
	// to the primary, which has no limit.
	assertCounts(t, jobCounts(h.setupJobs(t)), map[string][2]int{"linux-amd64": {3, 2}, "linux-amd64-fallback": {1, 0}})
}

func TestScaleGroup_ScaleDownFromEndKeepsMin(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	h.add(3, "linux-amd64", "default", "default", "", types.StateCreated, types.InstanceSourcePredictor)
	h.add(2, "linux-amd64-fallback", "default", "default", "", types.StateCreated, types.InstanceSourcePredictor)
	h.add(2, "linux-amd64-west4", "default", "default", "", types.StateHibernating, types.InstanceSourcePredictor)

	h.scale(t, &capBase)

	// free 7, target = min total 3: remove 2 from west4, 1 from fallback, 1 from primary.
	per := map[string]int{}
	for _, d := range h.manager.destroyed {
		per[strings.Split(d, "/")[0]]++
	}
	if per["linux-amd64-west4"] != 2 || per["linux-amd64-fallback"] != 1 || per["linux-amd64"] != 1 || len(h.manager.destroyed) != 4 {
		t.Errorf("destroyed = %v", h.manager.destroyed)
	}
	if n := len(h.setupJobs(t)); n != 0 {
		t.Errorf("expected no scale-up jobs, got %d", n)
	}
}

func TestScaleGroup_ScaleDownOnlyPredictorInstances(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	h.add(4, "linux-amd64-west4", "default", "default", "", types.StateCreated, types.InstanceSourcePool)
	h.scale(t, &capBase)
	if len(h.manager.destroyed) != 0 {
		t.Errorf("pool-sourced instances must not be scaled down, destroyed %v", h.manager.destroyed)
	}
}

func TestScaleGroup_RecentUsageFloorIsHibernated(t *testing.T) {
	h := newCapHarness(t, 0, func(c *types.ScalerConfig) { c.RecentUsageMinInstances = 3 })
	h.capStore.records = []types.CapabilityUtilizationRecord{
		{CapabilityKey: capMedium, Pool: "linux-amd64-fallback", InUseInstances: 2, RecordedAt: time.Now().Add(-time.Hour).Unix()},
	}
	h.scale(t, &capMedium)
	// target 3 (primary min 1 first, then up to its limit 4), all hibernated.
	assertCounts(t, jobCounts(h.setupJobs(t)), map[string][2]int{"linux-amd64": {0, 3}})
}

func TestScaleGroup_NoRecentUsageNoFloor(t *testing.T) {
	h := newCapHarness(t, 0, func(c *types.ScalerConfig) { c.RecentUsageMinInstances = 3 })
	h.add(1, "linux-amd64", "default", "variant_medium", "", types.StateCreated, types.InstanceSourcePool)
	h.scale(t, &capMedium)
	if n := len(h.setupJobs(t)); n != 0 {
		t.Errorf("expected no jobs without recent usage, got %d", n)
	}
}

func TestScaleGroup_DisabledPoolSkipped(t *testing.T) {
	h := newCapHarness(t, 5, func(c *types.ScalerConfig) { c.DisabledPools = []string{"linux-amd64-fallback"} })
	h.add(4, "linux-amd64", "default", "variant_medium", "", types.StateInUse, types.InstanceSourcePredictor)
	h.scale(t, &capMedium)
	// Primary is at its limit, fallback is disabled, so everything goes to west4.
	assertCounts(t, jobCounts(h.setupJobs(t)), map[string][2]int{"linux-amd64-west4": {5, 0}})
}

func TestScaleGroup_TenantGroupStaysInTenantPools(t *testing.T) {
	h := newCapHarness(t, 4, nil)
	h.scale(t, &capEgress)
	jobs := h.setupJobs(t)
	assertCounts(t, jobCounts(jobs), map[string][2]int{"linux-amd64": {4, 0}})
	if jobs[0].params.TenantID != capEgressTenant {
		t.Errorf("tenant id = %q", jobs[0].params.TenantID)
	}
}

func TestScaleGroup_DryRun(t *testing.T) {
	h := newCapHarness(t, 6, func(c *types.ScalerConfig) { c.DryRun = true })
	h.add(3, "linux-amd64-west4", "default", "default", "", types.StateCreated, types.InstanceSourcePredictor)
	h.scale(t, &capBase)
	if len(h.setupJobs(t)) != 0 || len(h.manager.destroyed) != 0 {
		t.Error("dry run must not create or destroy instances")
	}
	if v := testutil.ToFloat64(h.metrics.ScalerCapabilityPredictedInstances.WithLabelValues("paid", "linux", "amd64", "default", "default")); v != 6 {
		t.Errorf("dry run should still record the target, got %v", v)
	}
}

func TestCapabilityScaler_RunScaleJobSkipsPoolTargets(t *testing.T) {
	h := newCapHarness(t, 5, nil)
	// A pool-algorithm scale job left in the outbox across an algorithm switch.
	if err := h.scaler.RunScaleJob(context.Background(), "linux-amd64", 1000, 2800); err != nil {
		t.Fatalf("RunScaleJob: %v", err)
	}
	if len(h.setupJobs(t)) != 0 || len(h.pred.inputs) != 0 {
		t.Error("pool targets must be skipped by the capability scaler")
	}
}

func TestScaleGroup_UnknownGroup(t *testing.T) {
	h := newCapHarness(t, 5, nil)
	if err := h.scaler.ScaleGroup(context.Background(), "paid/linux/riscv/default/default", 0, 1); err != nil {
		t.Fatalf("unknown group should not fail: %v", err)
	}
	if len(h.setupJobs(t)) != 0 {
		t.Error("no jobs expected for an unknown group")
	}
}

func spillParams(key *types.CapabilityKey, tried ...string) *types.SetupInstanceParams {
	k := *key
	return &types.SetupInstanceParams{Capability: &k, TriedPools: tried, VariantID: key.VariantID, TenantID: key.TenantID, Hibernate: true}
}

func TestHandleSetupFailure_MovesToNextPool(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	cause := errors.New("ZONE_RESOURCE_POOL_EXHAUSTED")

	if !h.scaler.HandleSetupFailure(context.Background(), "linux-amd64", spillParams(&capMedium, "linux-amd64"), cause) {
		t.Fatal("capability job failure should be handled")
	}
	jobs := h.setupJobs(t)
	if len(jobs) != 1 || jobs[0].pool != "linux-amd64-fallback" {
		t.Fatalf("expected one job on the fallback pool, got %+v", jobs)
	}
	p := jobs[0].params
	if fmt.Sprint(p.TriedPools) != "[linux-amd64 linux-amd64-fallback]" {
		t.Errorf("TriedPools = %v", p.TriedPools)
	}
	if p.MachineType != "c4d-standard-8-lssd" || !p.Hibernate || p.Source != types.InstanceSourcePredictor {
		t.Errorf("spilled job should use the next pool's variant and keep hibernate: %+v", p)
	}
	if v := testutil.ToFloat64(h.metrics.ScalerCreateSpillCount.WithLabelValues("linux-amd64", "linux-amd64-fallback", metric.SpillOutcomeSpilled)); v != 1 {
		t.Errorf("spill metric = %v", v)
	}
}

func TestHandleSetupFailure_SkipsTriedAndFullPools(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	// Primary variant_medium is at its limit of 4.
	h.add(4, "linux-amd64", "default", "variant_medium", "", types.StateInUse, types.InstanceSourcePredictor)

	// The failed pool is added to TriedPools even if the params didn't list it.
	if !h.scaler.HandleSetupFailure(context.Background(), "linux-amd64-fallback", spillParams(&capMedium), errors.New("boom")) {
		t.Fatal("expected handled")
	}
	jobs := h.setupJobs(t)
	if len(jobs) != 1 || jobs[0].pool != "linux-amd64-west4" {
		t.Fatalf("expected spill to west4, got %+v", jobs)
	}
	if fmt.Sprint(jobs[0].params.TriedPools) != "[linux-amd64-fallback linux-amd64-west4]" {
		t.Errorf("TriedPools = %v", jobs[0].params.TriedPools)
	}
}

func TestHandleSetupFailure_CountsPendingSetupJobsAgainstLimit(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	// The primary's own creates are still in the outbox (no instance rows yet) and fill its limit of 4.
	for i := 0; i < 4; i++ {
		if err := h.scaler.createSetupJob(context.Background(), "linux-amd64", spillParams(&capMedium)); err != nil {
			t.Fatal(err)
		}
	}
	h.scaler.HandleSetupFailure(context.Background(), "linux-amd64-fallback", spillParams(&capMedium, "linux-amd64-fallback"), errors.New("boom"))
	jobs := h.setupJobs(t)
	if last := jobs[len(jobs)-1]; len(jobs) != 5 || last.pool != "linux-amd64-west4" {
		t.Fatalf("expected the spill to skip the primary and go to west4, got %+v", jobs)
	}
}

func TestScaleGroup_CountsPendingSetupJobsAgainstLimit(t *testing.T) {
	h := newCapHarness(t, 3, nil)
	h.add(1, "linux-amd64", "default", "variant_medium", "", types.StateInUse, types.InstanceSourcePredictor)
	// Two creates from the previous window are still in flight for the primary (limit 4).
	for i := 0; i < 2; i++ {
		if err := h.scaler.createSetupJob(context.Background(), "linux-amd64", spillParams(&capMedium)); err != nil {
			t.Fatal(err)
		}
	}
	h.scale(t, &capMedium)
	// Free is 0 (in-flight jobs are not free), so delta 3; the primary only has room for 1.
	assertCounts(t, jobCounts(h.setupJobs(t)), map[string][2]int{"linux-amd64": {1, 2}, "linux-amd64-fallback": {2, 0}})
}

func TestHandleSetupFailure_SkipsDisabledPools(t *testing.T) {
	h := newCapHarness(t, 0, func(c *types.ScalerConfig) { c.DisabledPools = []string{"linux-amd64-fallback"} })
	h.scaler.HandleSetupFailure(context.Background(), "linux-amd64", spillParams(&capMedium, "linux-amd64"), errors.New("boom"))
	jobs := h.setupJobs(t)
	if len(jobs) != 1 || jobs[0].pool != "linux-amd64-west4" {
		t.Fatalf("expected spill past the disabled pool, got %+v", jobs)
	}
}

func TestHandleSetupFailure_ExhaustedDropsJob(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	handled := h.scaler.HandleSetupFailure(context.Background(), "linux-amd64-west4",
		spillParams(&capMedium, "linux-amd64", "linux-amd64-fallback", "linux-amd64-west4"), errors.New("boom"))
	if !handled {
		t.Fatal("exhausted spill should still be handled so the job is dropped")
	}
	if n := len(h.setupJobs(t)); n != 0 {
		t.Errorf("no new job expected, got %d", n)
	}
	if v := testutil.ToFloat64(h.metrics.ScalerCreateSpillCount.WithLabelValues("linux-amd64-west4", "", metric.SpillOutcomeExhausted)); v != 1 {
		t.Errorf("exhausted metric = %v", v)
	}
}

func TestHandleSetupFailure_TenantGroupHasNoOtherPools(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	h.scaler.HandleSetupFailure(context.Background(), "linux-amd64", spillParams(&capEgress, "linux-amd64"), errors.New("boom"))
	if n := len(h.setupJobs(t)); n != 0 {
		t.Errorf("egress tenant must never spill to pools without the tenant, got %d jobs", n)
	}
}

func TestHandleSetupFailure_IgnoresNonCapabilityJobs(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	if h.scaler.HandleSetupFailure(context.Background(), "linux-amd64", &types.SetupInstanceParams{VariantID: "default"}, errors.New("boom")) {
		t.Error("pool-mode jobs must keep the normal retry path")
	}
	if h.scaler.HandleSetupFailure(context.Background(), "linux-amd64", nil, errors.New("boom")) {
		t.Error("jobs without params must keep the normal retry path")
	}
}

func TestOutboxProcessor_DispatchesCapabilityScaleJob(t *testing.T) {
	h := newCapHarness(t, 2, nil)
	p := &OutboxProcessor{}
	p.SetScaleJobRunner(h.scaler)
	params, _ := json.Marshal(types.ScaleJobParams{WindowStart: 1000, WindowEnd: 2800})
	raw := json.RawMessage(params)
	job := &types.OutboxJob{PoolName: types.CapabilityScaleJobName(&capBase), JobType: types.OutboxJobTypeScale, JobParams: &raw}

	if err := p.processScaleJob(context.Background(), job); err != nil {
		t.Fatalf("processScaleJob: %v", err)
	}
	if len(h.pred.inputs) != 1 || h.pred.inputs[0].StartTimestamp != 1000 || h.pred.inputs[0].EndTimestamp != 2800 {
		t.Errorf("expected one capability prediction for the window, got %+v", h.pred.inputs)
	}
	// Prediction 2 is floored by the group's min sizes (2 + 1 + 0).
	assertCounts(t, jobCounts(h.setupJobs(t)), map[string][2]int{"linux-amd64": {2, 0}, "linux-amd64-fallback": {1, 0}})
}

func TestScalerTrigger_CapabilityTargetsOneJobPerGroup(t *testing.T) {
	pf, err := config.Parse(strings.NewReader(capabilityPools))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	catalog, err := capability.NewCatalog(pf, nil, nil)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	outbox := NewMockOutboxStore()
	cfg := types.ScalerConfig{
		Enabled: true, WindowDuration: time.Hour, LeadTime: time.Hour,
		// variant_ubuntu24 only exists in the primary, so its group has no enabled pool.
		DisabledPools: []string{"linux-amd64"},
	}
	job := NewScalerTriggerJob(outbox, cfg, "runner", NewCapabilityScaleTargets(catalog, cfg.DisabledPools))
	if err := job.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := map[string]bool{}
	for _, j := range outbox.GetJobsByType(types.OutboxJobTypeScale) {
		got[j.PoolName] = true
	}
	want := []string{types.CapabilityScaleJobName(&capBase), types.CapabilityScaleJobName(&capMedium)}
	if len(got) != len(want) {
		t.Errorf("scale jobs = %v, want %v", got, want)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing scale job %s (got %v)", w, got)
		}
	}

	// A second trigger in the same window (e.g. another replica) finds the existing jobs.
	job2 := NewScalerTriggerJob(outbox, cfg, "runner-2", NewCapabilityScaleTargets(catalog, cfg.DisabledPools))
	if err := job2.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if n := len(outbox.GetJobsByType(types.OutboxJobTypeScale)); n != len(want) {
		t.Errorf("duplicate scale jobs created: %d", n)
	}
}

func TestOutboxProcessor_SetupFailureHandler(t *testing.T) {
	job := &types.OutboxJob{PoolName: "linux-amd64"}
	params := spillParams(&capMedium, "linux-amd64")

	p := &OutboxProcessor{}
	if p.handleSetupFailure(context.Background(), job, params, errors.New("boom")) {
		t.Error("without a handler failed setup jobs must be retried")
	}

	h := newCapHarness(t, 0, nil)
	p.SetSetupFailureHandler(h.scaler)
	if !p.handleSetupFailure(context.Background(), job, params, errors.New("boom")) {
		t.Fatal("capability setup failure should be handled")
	}
	if jobs := h.setupJobs(t); len(jobs) != 1 || jobs[0].pool != "linux-amd64-fallback" {
		t.Errorf("expected a spill job on the fallback pool, got %+v", jobs)
	}
}
