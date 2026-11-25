package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/app/capability"
	"github.com/drone-runners/drone-runner-aws/app/predictor"
	"github.com/drone-runners/drone-runner-aws/types"
)

const (
	testFQImage = "projects/p/global/images/img-v1"
	// waiterSettle gives goroutines that call get while a load is blocked time to start waiting.
	waiterSettle = 100 * time.Millisecond
)

func TestPoolCapacityCount(t *testing.T) {
	key := &types.CapabilityKey{TenantID: "default", VariantID: "variant_medium"}
	c := &poolCapacity{
		instances: []types.PoolInstanceCount{
			// Tenant and variant are stored empty for rows written before they existed.
			{TenantID: "", VariantID: "variant_medium", ImageName: testFQImage, State: types.StateCreated, Count: 3},
			{TenantID: "default", VariantID: "variant_medium", ImageName: testFQImage, State: types.StateInUse, Count: 5},
			{TenantID: "default", VariantID: "variant_medium", ImageName: testFQImage, State: types.StateProvisioning, Count: 2},
			{TenantID: "default", VariantID: "variant_medium", ImageName: testFQImage, State: types.StateHibernating, Count: 1},
			// Other image: used, but not free for this slot.
			{TenantID: "default", VariantID: "variant_medium", ImageName: "other", State: types.StateCreated, Count: 4},
			// Terminating is neither free nor used.
			{TenantID: "default", VariantID: "variant_medium", ImageName: testFQImage, State: types.StateTerminating, Count: 7},
			// Other variant and other tenant are not counted.
			{TenantID: "default", VariantID: "variant_large", ImageName: testFQImage, State: types.StateCreated, Count: 11},
			{TenantID: "acme", VariantID: "variant_medium", ImageName: testFQImage, State: types.StateCreated, Count: 13},
		},
		pending: []types.SetupJobCount{
			{TenantID: "default", VariantID: "variant_medium", Count: 2},
			{TenantID: "default", VariantID: "variant_large", Count: 17},
			{TenantID: "acme", VariantID: "variant_medium", Count: 19},
		},
	}

	free, used := c.count(key, testFQImage)
	assert.Equal(t, 3+2+1, free, "created, provisioning and hibernating instances of the image")
	assert.Equal(t, 3+5+2+1+4+2, used, "everything of the tenant and variant but terminating, plus pending setup jobs")

	free, used = c.count(key, "")
	assert.Equal(t, 3+2+1+4, free, "an empty image matches any image")
	assert.Equal(t, 3+5+2+1+4+2, used)

	free, used = c.count(&types.CapabilityKey{TenantID: "none", VariantID: "none"}, "")
	assert.Zero(t, free)
	assert.Zero(t, used)
}

func loadOf(loads *atomic.Int32, result *poolCapacity) func(context.Context) (*poolCapacity, error) {
	return func(context.Context) (*poolCapacity, error) {
		loads.Add(1)
		return result, nil
	}
}

func TestCapacityCache_ConcurrentCallersShareOneLoad(t *testing.T) {
	cache := newCapacityCache()
	var loads atomic.Int32
	release := make(chan struct{})
	want := &poolCapacity{}

	var wg sync.WaitGroup
	results := make([]*poolCapacity, 20)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
				loads.Add(1)
				<-release
				return want, nil
			})
			assert.NoError(t, err)
			results[i] = got
		}(i)
	}
	time.Sleep(waiterSettle)
	close(release)
	wg.Wait()

	assert.EqualValues(t, 1, loads.Load())
	for _, got := range results {
		assert.Same(t, want, got)
	}
}

// blockedGet starts a get whose load blocks until release is closed, and returns once it is loading.
func blockedGet(cache *capacityCache, pool string, window int64, result *poolCapacity) (release chan struct{}, got chan *poolCapacity) {
	started, release, got := make(chan struct{}), make(chan struct{}), make(chan *poolCapacity, 1)
	go func() {
		c, _ := cache.get(context.Background(), pool, window, func(context.Context) (*poolCapacity, error) {
			close(started)
			<-release
			return result, nil
		})
		got <- c
	}()
	<-started
	return release, got
}

func TestCapacityCache_PoolsAreIndependent(t *testing.T) {
	cache := newCapacityCache()
	var loads atomic.Int32
	a, b := &poolCapacity{}, &poolCapacity{}

	release, gotA := blockedGet(cache, "a", 1, a)
	gotB, err := cache.get(context.Background(), "b", 1, loadOf(&loads, b))
	require.NoError(t, err)

	assert.Same(t, b, gotB, "pool b does not wait for pool a's load")
	assert.EqualValues(t, 1, loads.Load())
	close(release)
	assert.Same(t, a, <-gotA)
}

// Counts change between windows (setup jobs finish, instances are claimed), so a window never
// shares another's load.
func TestCapacityCache_WindowsAreIndependent(t *testing.T) {
	cache := newCapacityCache()
	var loads atomic.Int32
	w1, w2 := &poolCapacity{}, &poolCapacity{}

	release, gotW1 := blockedGet(cache, "pool", 1000, w1)
	gotW2, err := cache.get(context.Background(), "pool", 1300, loadOf(&loads, w2))
	require.NoError(t, err)

	assert.Same(t, w2, gotW2, "the next window loads for itself")
	assert.EqualValues(t, 1, loads.Load())
	close(release)
	assert.Same(t, w1, <-gotW1)
}

// A finished load is never reused: the next caller, however soon, reads again. Counts change as
// setup jobs finish, so planning from a read that is done would create instances not needed.
func TestCapacityCache_NothingIsReusedOnceALoadIsDone(t *testing.T) {
	cache := newCapacityCache()
	var loads atomic.Int32

	first, _ := cache.get(context.Background(), "pool", 1, loadOf(&loads, &poolCapacity{}))
	second, _ := cache.get(context.Background(), "pool", 1, loadOf(&loads, &poolCapacity{}))
	third, _ := cache.get(context.Background(), "pool", 1, loadOf(&loads, &poolCapacity{}))

	assert.EqualValues(t, 3, loads.Load())
	assert.NotSame(t, first, second)
	assert.NotSame(t, second, third)
}

// Windows pass and never come back, so nothing may pile up once their loads are done, whether
// they succeeded, failed or panicked.
func TestCapacityCache_FinishedLoadsLeaveNothingBehind(t *testing.T) {
	cache := newCapacityCache()
	var loads atomic.Int32

	for w := int64(0); w < 10; w++ {
		_, err := cache.get(context.Background(), "pool", w, loadOf(&loads, &poolCapacity{}))
		require.NoError(t, err)
	}
	_, err := cache.get(context.Background(), "pool", 20, func(context.Context) (*poolCapacity, error) { return nil, errors.New("boom") })
	require.Error(t, err)
	func() {
		defer func() { _ = recover() }()
		_, _ = cache.get(context.Background(), "pool", 30, func(context.Context) (*poolCapacity, error) { panic("blew up") })
	}()

	assert.Empty(t, cache.inflight)
}

func TestCapacityCache_InvalidateDropsThePoolInEveryWindow(t *testing.T) {
	cache := newCapacityCache()
	var releases []chan struct{}
	for _, pool := range []string{"a", "b"} {
		for _, w := range []int64{1, 2} {
			release, _ := blockedGet(cache, pool, w, &poolCapacity{})
			releases = append(releases, release)
		}
	}

	cache.invalidate("a")

	var loads atomic.Int32
	joined := make(chan struct{})
	go func() { // pool b still has its loads in progress: a caller joins instead of loading
		for _, w := range []int64{1, 2} {
			_, _ = cache.get(context.Background(), "b", w, loadOf(&loads, &poolCapacity{}))
		}
		close(joined)
	}()
	time.Sleep(waiterSettle)
	assert.Zero(t, loads.Load(), "pool b is untouched")

	for _, w := range []int64{1, 2} { // pool a loads for itself in both windows
		_, err := cache.get(context.Background(), "a", w, loadOf(&loads, &poolCapacity{}))
		require.NoError(t, err)
	}
	assert.EqualValues(t, 2, loads.Load(), "pool a is loaded again in both windows")

	for _, r := range releases {
		close(r)
	}
	<-joined
}

// A load that started before an invalidation may have read counts from before it, so callers
// that arrive after the invalidation must not wait for it, and its end must not drop the load
// that replaced it.
func TestCapacityCache_InvalidateDuringLoad(t *testing.T) {
	cache := newCapacityCache()
	stale, fresh := &poolCapacity{}, &poolCapacity{}

	releaseStale, staleGot := blockedGet(cache, "pool", 1, stale)
	cache.invalidate("pool")
	releaseFresh, freshGot := blockedGet(cache, "pool", 1, fresh)

	close(releaseStale)
	assert.Same(t, stale, <-staleGot, "the load in progress still returns to its own caller")

	joined := make(chan *poolCapacity, 1)
	go func() {
		got, _ := cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
			t.Error("the fresh load is still in progress and must be joined")
			return nil, nil
		})
		joined <- got
	}()
	time.Sleep(waiterSettle)
	close(releaseFresh)

	assert.Same(t, fresh, <-freshGot)
	assert.Same(t, fresh, <-joined, "the stale load's end must not have dropped the fresh one")
}

func TestCapacityCache_ErrorsAreNotReused(t *testing.T) {
	cache := newCapacityCache()
	boom := errors.New("boom")
	var loads atomic.Int32

	_, err := cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
		loads.Add(1)
		return nil, boom
	})
	require.ErrorIs(t, err, boom)

	got, err := cache.get(context.Background(), "pool", 1, loadOf(&loads, &poolCapacity{}))
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.EqualValues(t, 2, loads.Load())
}

// Waiters share a failure that is about the data (the database is down), and do not each hit it.
func TestCapacityCache_WaitersShareAnOrdinaryFailure(t *testing.T) {
	cache := newCapacityCache()
	boom := errors.New("boom")
	started, release := make(chan struct{}), make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
			close(started)
			<-release
			return nil, boom
		})
		assert.ErrorIs(t, err, boom)
	}()
	<-started

	waiterErr := make(chan error, 1)
	go func() {
		_, err := cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
			t.Error("a waiter must not load when the loader failed for an ordinary reason")
			return nil, nil
		})
		waiterErr <- err
	}()
	time.Sleep(waiterSettle)
	close(release)
	wg.Wait()

	assert.ErrorIs(t, <-waiterErr, boom)
}

// A loader canceled by its own context says nothing about a waiter whose context is fine.
func TestCapacityCache_WaiterRetriesWhenLoaderWasCancelled(t *testing.T) {
	cache := newCapacityCache()
	started, release := make(chan struct{}), make(chan struct{})
	want := &poolCapacity{}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := cache.get(leaderCtx, "pool", 1, func(ctx context.Context) (*poolCapacity, error) {
			close(started)
			<-release
			return nil, ctx.Err()
		})
		assert.ErrorIs(t, err, context.Canceled)
	}()
	<-started

	type result struct {
		got *poolCapacity
		err error
	}
	waiter := make(chan result, 1)
	go func() {
		got, err := cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) { return want, nil })
		waiter <- result{got, err}
	}()
	time.Sleep(waiterSettle)
	cancelLeader()
	close(release)
	wg.Wait()

	r := <-waiter
	require.NoError(t, r.err)
	assert.Same(t, want, r.got)
}

func TestCapacityCache_WaiterStopsWhenItsContextEnds(t *testing.T) {
	cache := newCapacityCache()
	started, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
			close(started)
			<-release
			return &poolCapacity{}, nil
		})
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := cache.get(ctx, "pool", 1, func(context.Context) (*poolCapacity, error) {
		t.Error("a waiter must not load")
		return nil, nil
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
}

// If a load panics, its waiters must not wait for ever and the pool must stay loadable.
func TestCapacityCache_PanickingLoadReleasesWaiters(t *testing.T) {
	cache := newCapacityCache()
	started, release := make(chan struct{}), make(chan struct{})

	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		_, _ = cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
			close(started)
			<-release
			panic("load blew up")
		})
	}()
	<-started

	waiterErr := make(chan error, 1)
	go func() {
		_, err := cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
			t.Error("a waiter must not load")
			return nil, nil
		})
		waiterErr <- err
	}()
	time.Sleep(waiterSettle)
	close(release)

	assert.Equal(t, "load blew up", <-panicked)
	select {
	case err := <-waiterErr:
		assert.ErrorIs(t, err, errLoadAborted)
	case <-time.After(5 * time.Second):
		t.Fatal("waiter is still blocked after the load panicked")
	}

	var loads atomic.Int32
	got, err := cache.get(context.Background(), "pool", 1, loadOf(&loads, &poolCapacity{}))
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.EqualValues(t, 1, loads.Load(), "the pool loads again after the panic")
}

// countingInstanceStore counts how often each pool's instance counts are read, and can hold reads.
type countingInstanceStore struct {
	*MockInstanceStore
	mu    sync.Mutex
	calls map[string]int
	gate  chan struct{} // if set, reads wait until it is closed
}

func (s *countingInstanceStore) CountPoolInstances(ctx context.Context, pool string) ([]types.PoolInstanceCount, error) {
	s.mu.Lock()
	s.calls[pool]++
	s.mu.Unlock()
	if s.gate != nil {
		<-s.gate
	}
	return s.MockInstanceStore.CountPoolInstances(ctx, pool)
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func (h *capHarness) scaleAt(t *testing.T, key *types.CapabilityKey, windowStart int64) {
	t.Helper()
	if err := h.scaler.ScaleGroup(context.Background(), key.String(), windowStart, windowStart+1800); err != nil {
		t.Fatalf("ScaleGroup: %v", err)
	}
}

// finishSetupJobs plays the outbox processor: every pending setup job becomes a ready instance.
func (h *capHarness) finishSetupJobs(t *testing.T) {
	t.Helper()
	for _, j := range h.outbox.GetJobsByType(types.OutboxJobTypeSetupInstance) {
		var p types.SetupInstanceParams
		require.NoError(t, json.Unmarshal(*j.JobParams, &p))
		h.add(1, j.PoolName, p.TenantID, p.VariantID, p.ImageName, types.StateCreated, types.InstanceSourcePredictor)
		require.NoError(t, h.outbox.Delete(context.Background(), j.ID))
	}
}

// lockedPredictor lets scale jobs run concurrently over a predictor that records its inputs.
type lockedPredictor struct {
	mu    sync.Mutex
	inner predictor.Predictor
}

func (p *lockedPredictor) Predict(ctx context.Context, in *predictor.PredictionInput) (*predictor.PredictionResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inner.Predict(ctx, in)
}

func (p *lockedPredictor) Name() string { return p.inner.Name() }

// Groups that scale at the same time share a pool's read: the base and medium groups both start on
// linux-amd64, so while its read is in progress they wait for it instead of reading it twice.
func TestScaleGroup_ConcurrentGroupsShareOnePoolRead(t *testing.T) {
	h := newCapHarness(t, 3, nil)
	insts := &countingInstanceStore{MockInstanceStore: h.instances, calls: map[string]int{}, gate: make(chan struct{})}
	h.scaler.instanceStore = insts
	h.scaler.predictor = &lockedPredictor{inner: h.pred}
	// Both groups already have their target (3) free, so nothing is created and no counts are dropped.
	h.add(3, "linux-amd64", "default", "default", "", types.StateCreated, types.InstanceSourcePredictor)
	h.add(3, "linux-amd64", "default", "variant_medium", "", types.StateCreated, types.InstanceSourcePredictor)

	var wg sync.WaitGroup
	for _, key := range []*types.CapabilityKey{&capBase, &capMedium} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.scaleAt(t, key, 1000)
		}()
	}
	time.Sleep(waiterSettle) // both are now reading linux-amd64
	close(insts.gate)
	wg.Wait()

	require.Empty(t, h.setupJobs(t), "this scenario must not create jobs, which would drop the shared counts")
	insts.mu.Lock()
	defer insts.mu.Unlock()
	assert.Equal(t, 1, insts.calls["linux-amd64"], "the two groups share one read of the pool")
}

// The replay of prod1 found this: counts read in one window must not be used in the next, even
// when the next one starts moments later, as in a test or after a short window setting.
func TestScaleGroup_CountsAreNotSharedAcrossWindows(t *testing.T) {
	h := newCapHarness(t, 3, nil)
	// capBase has its target free: it creates nothing and only reads the pool.
	h.add(3, "linux-amd64", "default", "default", "", types.StateCreated, types.InstanceSourcePredictor)

	h.scaleAt(t, &capMedium, 1000)
	require.Len(t, h.setupJobs(t), 3)
	h.scaleAt(t, &capBase, 1000) // reads the pool after medium's jobs: they are pending now
	h.finishSetupJobs(t)         // the jobs finish: medium has 3 free instances and nothing pending

	h.scaleAt(t, &capMedium, 2800)

	assert.Empty(t, h.setupJobs(t), "the next window must see medium's 3 free instances, not the pending jobs of the last")
}

// A scale job retried inside the sharing window must not use counts from before its own jobs.
func TestScaleGroup_RetryOfAWindowSeesItsOwnJobs(t *testing.T) {
	h := newCapHarness(t, 10, nil)

	h.scaleAt(t, &capMedium, 1000)
	first := jobCounts(h.setupJobs(t))
	require.Equal(t, 4, first["linux-amd64"][0], "the primary is filled to its limit of 4")

	h.scaleAt(t, &capMedium, 1000) // the same window again, as a retry would

	assert.Equal(t, 4, jobCounts(h.setupJobs(t))["linux-amd64"][0], "the retry must see the 4 pending jobs and not pass the limit")
}

func TestCreateSetupJob_DropsOnlyThatPoolsLoads(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	ctx := context.Background()
	for _, pool := range []string{"linux-amd64", "linux-amd64-fallback"} {
		for _, w := range []int64{1000, 2800} {
			h.scaler.capacity.inflight[capacityKey{pool, w}] = &capacityLoad{done: make(chan struct{})}
		}
	}

	require.NoError(t, h.scaler.createSetupJob(ctx, "linux-amd64", spillParams(&capMedium)))

	assert.Len(t, h.scaler.capacity.inflight, 2)
	for k := range h.scaler.capacity.inflight {
		assert.Equal(t, "linux-amd64-fallback", k.pool)
	}
}

func TestCreateSetupJob_DropsLoadsWhenCreateFails(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	ctx := context.Background()
	h.scaler.capacity.inflight[capacityKey{"linux-amd64", 1000}] = &capacityLoad{done: make(chan struct{})}
	h.outbox.createErr = errors.New("db down")

	require.Error(t, h.scaler.createSetupJob(ctx, "linux-amd64", spillParams(&capMedium)))

	assert.Empty(t, h.scaler.capacity.inflight)
}

// A spill follows a failed setup and must see the jobs just created, so it never reads shared counts.
func TestHandleSetupFailure_ReadsFreshCounts(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	ctx := context.Background()
	// The primary (limit 4 for variant_medium) fills up with pending jobs, written straight to the
	// store so nothing is dropped from the shared loads.
	for i := 0; i < 4; i++ {
		raw := mustJSON(t, spillParams(&capMedium))
		require.NoError(t, h.outbox.Create(ctx, &types.OutboxJob{
			PoolName: "linux-amd64", JobType: types.OutboxJobTypeSetupInstance, JobParams: &raw, Status: types.OutboxJobStatusPending,
		}))
	}

	// A failure in the fallback spills to the next pool that has room: the full primary is skipped.
	require.True(t, h.scaler.HandleSetupFailure(ctx, "linux-amd64-fallback", spillParams(&capMedium, "linux-amd64-fallback"), errors.New("boom")))

	jobs := h.setupJobs(t)
	require.Len(t, jobs, 5)
	assert.Equal(t, "linux-amd64-west4", jobs[len(jobs)-1].pool)
}

// Counts shared with a group must be as new as the read that group would have made itself. Here
// the base group reads the pool while the medium group's 3 setup jobs are still pending. They
// finish, and then the medium group scales: it must see its 3 ready instances and create nothing.
func TestScaleGroup_NoStaleCountsAfterALoadFinishes(t *testing.T) {
	h := newCapHarness(t, 3, nil)
	ctx := context.Background()
	h.add(3, "linux-amd64", "default", "default", "", types.StateCreated, types.InstanceSourcePredictor)
	for i := 0; i < 3; i++ {
		raw := mustJSON(t, spillParams(&capMedium))
		require.NoError(t, h.outbox.Create(ctx, &types.OutboxJob{
			PoolName: "linux-amd64", JobType: types.OutboxJobTypeSetupInstance, JobParams: &raw, Status: types.OutboxJobStatusPending,
		}))
	}

	h.scaleAt(t, &capBase, 1000) // has its target free: reads the pool and creates nothing
	require.Len(t, h.setupJobs(t), 3, "only the 3 pending jobs of the medium group exist")
	h.finishSetupJobs(t)
	require.Empty(t, h.setupJobs(t))

	h.scaleAt(t, &capMedium, 1000)

	assert.Empty(t, h.setupJobs(t), "the medium group has 3 ready instances, so creating more is over-provisioning")
}

// referenceSlot counts a slot's free and used instances the way the scaler did before counts were
// aggregated and shared: every instance of the pool listed, and each setup job read from the outbox.
func referenceSlot(t *testing.T, h *capHarness, key *types.CapabilityKey, m *capability.Member) (free, used int) {
	t.Helper()
	ctx := context.Background()
	image := ""
	if m.Variant != nil {
		image = m.Variant.ImageName
	}
	fq, err := h.manager.FullyQualifiedImage(ctx, m.Pool, key.TenantID, image)
	require.NoError(t, err)

	insts, err := h.instances.List(ctx, m.Pool, nil)
	require.NoError(t, err)
	for _, inst := range insts {
		if tenantOrDefault(inst.TenantID) != key.TenantID || variantOrDefault(inst.VariantID) != key.VariantID {
			continue
		}
		if inst.State == types.StateTerminating {
			continue
		}
		used++
		if fq != "" && inst.Image != fq {
			continue
		}
		if inst.State == types.StateCreated || inst.State == types.StateHibernating || inst.State == types.StateProvisioning {
			free++
		}
	}
	for _, j := range h.outbox.GetJobsByType(types.OutboxJobTypeSetupInstance) {
		var p types.SetupInstanceParams
		require.NoError(t, json.Unmarshal(*j.JobParams, &p))
		if j.PoolName == m.Pool && tenantOrDefault(p.TenantID) == key.TenantID && variantOrDefault(p.VariantID) == key.VariantID {
			used++
		}
	}
	return free, used
}

// Over-provisioning is a scaler planning from counts lower than the truth, so after any history of
// instances, setup jobs, claims, deletions and scale runs, the counts a scale job plans from must
// equal what a read of its own would give.
func TestScaleGroup_PlansFromTheSameCountsAsAFreshRead(t *testing.T) {
	states := []types.InstanceState{
		types.StateCreated, types.StateInUse, types.StateProvisioning, types.StateHibernating, types.StateTerminating,
	}
	for seed := int64(1); seed <= 5; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic test data
			h := newCapHarness(t, 3, nil)
			ctx := context.Background()
			groups := h.scaler.catalog.Groups()
			require.NotEmpty(t, groups)
			pools := h.scaler.catalog.Pools()
			require.NotEmpty(t, pools)

			for step := 0; step < 150; step++ {
				g := groups[rng.Intn(len(groups))]
				switch rng.Intn(6) {
				case 0, 1: // instances of any pool, tenant, variant, image and state
					m := g.Members[rng.Intn(len(g.Members))]
					image := ""
					if rng.Intn(3) == 0 {
						image = "other-img"
					}
					h.add(1+rng.Intn(3), m.Pool, g.Key.TenantID, g.Key.VariantID, image, states[rng.Intn(len(states))], types.InstanceSourcePredictor)
				case 2: // a pending setup job
					m := g.Members[rng.Intn(len(g.Members))]
					raw := mustJSON(t, spillParams(&g.Key))
					require.NoError(t, h.outbox.Create(ctx, &types.OutboxJob{
						PoolName: m.Pool, JobType: types.OutboxJobTypeSetupInstance, JobParams: &raw, Status: types.OutboxJobStatusPending,
					}))
				case 3: // setup jobs finish
					h.finishSetupJobs(t)
				case 4: // an instance goes away
					for _, m := range g.Members {
						if insts, _ := h.instances.List(ctx, m.Pool, nil); len(insts) > 0 {
							require.NoError(t, h.instances.Delete(ctx, insts[rng.Intn(len(insts))].ID))
							break
						}
					}
				case 5: // a scale run, which may enqueue jobs
					h.scaleAt(t, &g.Key, int64(1000+rng.Intn(3)*300))
				}

				for _, gg := range groups {
					members := h.scaler.activeMembers(&gg)
					key := gg.Key
					slots, err := h.scaler.capabilitySlots(ctx, &key, members, 1000)
					require.NoError(t, err)
					for i := range members {
						free, used := referenceSlot(t, h, &key, &members[i])
						require.Equalf(t, [2]int{free, used}, [2]int{slots[i].free, slots[i].used},
							"step %d, group %s, pool %s: free/used the scaler plans from differ from a fresh read", step, key.String(), members[i].Pool)
					}
				}
			}
		})
	}
}
