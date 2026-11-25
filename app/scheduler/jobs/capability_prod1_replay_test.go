package jobs

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/app/drivers"
	"github.com/drone-runners/drone-runner-aws/app/poolfile"
	"github.com/drone-runners/drone-runner-aws/app/predictor"
	"github.com/drone-runners/drone-runner-aws/command/config"
	"github.com/drone-runners/drone-runner-aws/command/harness/storage"
	"github.com/drone-runners/drone-runner-aws/metric"
	"github.com/drone-runners/drone-runner-aws/types"
)

const replayTenant = "CbUXX5rlTcuIsV8aAOjmuw"

// lockedStore makes MockInstanceStore safe for the setup goroutines the manager starts.
type lockedStore struct {
	*MockInstanceStore
	mu sync.Mutex
}

func (s *lockedStore) Find(ctx context.Context, id string) (*types.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, err := s.MockInstanceStore.Find(ctx, id)
	if inst == nil {
		return nil, fmt.Errorf("not found")
	}
	c := *inst
	return &c, err
}
func (s *lockedStore) List(ctx context.Context, pool string, p *types.QueryParams) ([]*types.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MockInstanceStore.List(ctx, pool, p)
}
func (s *lockedStore) Create(ctx context.Context, i *types.Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MockInstanceStore.Create(ctx, i)
}
func (s *lockedStore) Update(ctx context.Context, i *types.Instance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MockInstanceStore.Update(ctx, i)
}
func (s *lockedStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MockInstanceStore.Delete(ctx, id)
}
func (s *lockedStore) FindAndClaim(ctx context.Context, p *types.QueryParams, st types.InstanceState, allowed []types.InstanceState, u bool) (*types.Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.MockInstanceStore.FindAndClaim(ctx, p, st, allowed, u)
}
func (s *lockedStore) markCreated() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, i := range s.instances {
		if i.State == types.StateProvisioning {
			i.State = types.StateCreated
		}
	}
}
func (s *lockedStore) snapshot() []types.Instance {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]types.Instance, 0, len(s.instances))
	for _, i := range s.instances {
		out = append(out, *i)
	}
	return out
}

// lockedOutbox makes MockOutboxStore safe for the processor's concurrent job goroutines.
// It also honours the retry interval on a virtual clock: a failed job is not claimed again
// until retryAfter seconds have passed.
type lockedOutbox struct {
	*MockOutboxStore
	mu         sync.Mutex
	now        int64
	retryAfter int64
	failedAt   map[int64]int64
}

func (o *lockedOutbox) Create(ctx context.Context, j *types.OutboxJob) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.MockOutboxStore.Create(ctx, j)
}
func (o *lockedOutbox) FindAndClaimPending(ctx context.Context, r string, jt []types.OutboxJobType, n int, ri time.Duration) ([]*types.OutboxJob, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var held []*types.OutboxJob
	for _, j := range o.jobs {
		if at, ok := o.failedAt[j.ID]; ok && j.Status == types.OutboxJobStatusPending && o.now-at < o.retryAfter {
			j.Status = "held"
			held = append(held, j)
		}
	}
	out, err := o.MockOutboxStore.FindAndClaimPending(ctx, r, jt, n, ri)
	for _, j := range held {
		j.Status = types.OutboxJobStatusPending
	}
	return out, err
}
func (o *lockedOutbox) pendingSetup() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, j := range o.jobs {
		if j.JobType == types.OutboxJobTypeSetupInstance && j.Status == types.OutboxJobStatusPending {
			n++
		}
	}
	return n
}
func (o *lockedOutbox) UpdateStatus(ctx context.Context, id int64, s types.OutboxJobStatus, msg string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s == types.OutboxJobStatusPending && msg != "" {
		o.failedAt[id] = o.now
	}
	return o.MockOutboxStore.UpdateStatus(ctx, id, s, msg)
}
func (o *lockedOutbox) Delete(ctx context.Context, id int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.MockOutboxStore.Delete(ctx, id)
}
func (o *lockedOutbox) CountSetupJobs(ctx context.Context, pool string) ([]types.SetupJobCount, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.MockOutboxStore.CountSetupJobs(ctx, pool)
}

// gcpStub is the real google driver with only the cloud calls stubbed. Create stamps the image
// exactly like google.create: GetFullyQualifiedImage(opts.VMImageConfig) on the creating driver.
type gcpStub struct {
	drivers.Driver
	mu      *sync.Mutex
	seq     *int
	creates map[string]int
	fail    func(*types.InstanceCreateOpts) bool
}

func (g *gcpStub) Create(ctx context.Context, opts *types.InstanceCreateOpts) (*types.Instance, error) {
	if g.fail != nil && g.fail(opts) {
		return nil, fmt.Errorf("ZONE_RESOURCE_POOL_EXHAUSTED")
	}
	image, err := g.Driver.GetFullyQualifiedImage(ctx, &opts.VMImageConfig)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	*g.seq++
	id := fmt.Sprintf("vm-%d", *g.seq)
	g.creates[opts.PoolName+"|"+opts.TenantID]++
	g.mu.Unlock()
	return &types.Instance{
		ID: id, Name: id, Provider: types.Google, State: types.StateProvisioning, Pool: opts.PoolName,
		Image: image, Size: opts.MachineType, Platform: opts.Platform, Address: "127.0.0.1",
		CACert: opts.CACert, CAKey: opts.CAKey, TLSCert: opts.TLSCert, TLSKey: opts.TLSKey,
	}, nil
}
func (g *gcpStub) Destroy(context.Context, []*types.Instance) ([]*types.Instance, error) {
	return nil, nil
}
func (g *gcpStub) DestroyInstanceAndStorage(context.Context, []*types.Instance, *storage.CleanupType) ([]*types.Instance, error) {
	return nil, nil
}
func (g *gcpStub) Hibernate(context.Context, string, string, string) error { return nil }

type keyedPredictor map[string]int

func (p keyedPredictor) Predict(_ context.Context, in *predictor.PredictionInput) (*predictor.PredictionResult, error) {
	return &predictor.PredictionResult{PredictedInstances: p[in.Capability.TenantID+"/"+in.Capability.VariantID]}, nil
}
func (p keyedPredictor) Name() string { return "replay" }

// TestReplayProd1CapabilityScaling replays the prod1 capability rollout (pool file and scaler
// config as deployed on 2026-11-17) for 9 hours of 5-minute windows with no demand.
// Run with PROD1_POOL_FILE=/path/to/rendered/pool.yml.
func TestReplayProd1CapabilityScaling(t *testing.T) {
	t.Run("healthy", func(t *testing.T) { replayProd1(t, false, 0) })
	// Every tenant create in linux-amd64 fails, so tenant capacity spills to linux-amd64-fallback.
	t.Run("primary_tenant_creates_fail", func(t *testing.T) { replayProd1(t, true, 0) })
	// Tenant creates fail in every pool for the first 2 hours (stockout), then recover.
	t.Run("tenant_stockout_2h_then_recover", func(t *testing.T) { replayProd1(t, false, 24) })
}

func replayProd1(t *testing.T, failPrimaryTenant bool, stockoutWindows int) {
	window := 0
	path := os.Getenv("PROD1_POOL_FILE")
	if path == "" {
		t.Skip("PROD1_POOL_FILE not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pf, err := config.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	google := pf.Instances[:0]
	for i := range pf.Instances {
		if pf.Instances[i].Type == "google" {
			google = append(google, pf.Instances[i])
		}
	}
	pf.Instances = google

	pools, err := poolfile.ProcessPool(pf, "runner", types.Passwords{}, nil)
	if err != nil {
		t.Fatalf("ProcessPool: %v", err)
	}
	var mu sync.Mutex
	seq := 0
	creates := map[string]int{}
	wrap := func(d drivers.Driver) drivers.Driver {
		return &gcpStub{Driver: d, mu: &mu, seq: &seq, creates: creates, fail: func(o *types.InstanceCreateOpts) bool {
			if o.TenantID != replayTenant {
				return false
			}
			return (failPrimaryTenant && o.PoolName == "linux-amd64") || window < stockoutWindows
		}}
	}
	for i := range pools {
		pools[i].Driver = wrap(pools[i].Driver)
		for id, d := range pools[i].TenantDrivers {
			pools[i].TenantDrivers[id] = wrap(d)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &lockedStore{MockInstanceStore: NewMockInstanceStore()}
	outbox := &lockedOutbox{MockOutboxStore: NewMockOutboxStore(), retryAfter: 300, failedAt: map[int64]int64{}}
	mgr := drivers.NewManagerFromConfig(&drivers.ManagerConfig{GlobalCtx: ctx, InstanceStore: store, RunnerName: "runner"})
	if err = mgr.Add(pools...); err != nil {
		t.Fatal(err)
	}
	dm := drivers.NewDistributedManager(mgr, outbox)
	catalog, err := capability.NewCatalog(pf, []string{"google"}, func(pool string) types.Platform {
		p, _, _ := dm.Inspect(pool)
		return p
	})
	if err != nil {
		t.Fatal(err)
	}
	dm.SetCapabilityCatalog(catalog, types.SchedulerAlgorithmCapability)

	// Observed prod1 predictions at 16:30 UTC.
	pred := keyedPredictor{
		replayTenant + "/variant_large": 3, replayTenant + "/variant_medium": 2,
		replayTenant + "/variant_medium_ubuntu24": 1, replayTenant + "/variant_small_ubuntu24": 1,
		"default/variant_large": 9, "default/variant_medium": 129, "default/variant_small": 5,
	}
	cfg := types.ScalerConfig{
		Enabled: true, ScalePercent: 100, RecentUsageLookbackDays: 7,
		DisabledPools: []string{"linux-amd64-aws", "linux-amd64-fallback-aws"},
	}
	scaler := NewCapabilityScaler(dm, pred, store, &mockCapabilityHistoryStore{}, outbox, catalog, cfg, &metric.Metrics{})
	proc := NewOutboxProcessor(dm, outbox, 0, 25, 20)
	proc.SetSetupFailureHandler(scaler)

	var groups []capability.Group
	for _, g := range catalog.Groups() {
		k := g.Key
		if k.Tier != types.TierPaid || k.OS != "linux" || k.Arch != "amd64" {
			continue
		}
		if _, ok := pred[k.TenantID+"/"+k.VariantID]; ok {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		t.Fatal("no replay groups found in catalog")
	}

	totalCreates := func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, c := range creates {
			n += c
		}
		return n
	}
	const windows = 108
	perWindow := make([]int, windows)
	peakPending, peakVMs := 0, 0
	for w := 0; w < windows; w++ {
		before := totalCreates()
		start := int64(1_800_000_000 + w*300)
		for i := range groups {
			if err := scaler.ScaleGroup(ctx, groups[i].Key.String(), start, start+300); err != nil {
				t.Fatalf("window %d %s: %v", w, groups[i].Key.String(), err)
			}
		}
		window = w
		for pass := 0; pass < 10; pass++ {
			outbox.mu.Lock()
			outbox.now = int64(w*300 + pass*30)
			outbox.mu.Unlock()
			if err := proc.ProcessPendingJobs(ctx); err != nil {
				t.Fatalf("window %d: process outbox: %v", w, err)
			}
			store.markCreated()
		}
		perWindow[w] = totalCreates() - before
		if pending := outbox.pendingSetup(); pending > peakPending {
			peakPending = pending
		}
		if n := len(store.snapshot()); n > peakVMs {
			peakVMs = n
		}
	}

	idle := map[string]int{}
	for _, i := range store.snapshot() {
		if i.State == types.StateCreated {
			idle[i.Pool+" "+i.TenantID+" "+i.VariantID]++
		}
	}
	keys := make([]string, 0, len(idle))
	for k := range idle {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		t.Logf("idle after %d windows: %-55s %d", windows, k, idle[k])
	}
	t.Logf("VMs created per window: %v", perWindow)
	t.Logf("total created %d, peak VMs %d, peak pending setup jobs %d, final VMs %d, final pending %d",
		totalCreates(), peakVMs, peakPending, len(store.snapshot()), outbox.pendingSetup())

	for w := stockoutWindows + 1; w < windows; w++ {
		if perWindow[w] != 0 {
			t.Errorf("window %d created %d VMs; steady state must create none", w, perWindow[w])
		}
	}
	for i := range groups {
		k := groups[i].Key
		got := 0
		for _, inst := range store.snapshot() {
			if inst.TenantID == k.TenantID && inst.VariantID == k.VariantID && inst.State == types.StateCreated {
				got++
			}
		}
		minTotal := 0
		for _, m := range groups[i].Members {
			minTotal += m.MinSize
		}
		want := pred[k.TenantID+"/"+k.VariantID] + minTotal
		if got != want {
			t.Errorf("%s: %d idle VMs, want target %d", k.String(), got, want)
		}
	}

	if failPrimaryTenant {
		for _, inst := range store.snapshot() {
			if inst.Pool == "linux-amd64" && inst.TenantID == replayTenant {
				t.Errorf("tenant VM %s created in failing pool linux-amd64", inst.ID)
			}
		}
		return
	}
	// A tenant build must claim the tenant's warm VM rather than cold-create.
	for _, rc := range []string{"large", "medium"} {
		before := totalCreates()
		inst, _, warmed, variant, err := dm.Provision(ctx, "linux-amd64", "runner", "owner",
			&types.ProvisionParams{AccountID: replayTenant, ResourceClass: rc}, nil, nil, nil, 60, false, nil, false)
		if err != nil {
			t.Fatalf("provision %s: %v", rc, err)
		}
		t.Logf("tenant %s build: warmed=%v variant=%s pool=%s source=%s image=%s", rc, warmed, variant, inst.Pool, inst.Source, inst.Image)
		if !warmed || inst.Source != types.InstanceSourcePredictor || inst.TenantID != replayTenant {
			t.Errorf("tenant %s build did not claim a warm tenant VM: warmed=%v source=%s tenant=%s", rc, warmed, inst.Source, inst.TenantID)
		}
		if n := totalCreates() - before; n > 1 {
			t.Errorf("tenant %s build created %d VMs", rc, n)
		}
	}
}
