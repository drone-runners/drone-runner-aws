package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	cache := newCapacityCache(time.Minute)
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

func TestCapacityCache_PoolsAreIndependent(t *testing.T) {
	cache := newCapacityCache(time.Minute)
	var loads atomic.Int32

	a, _ := cache.get(context.Background(), "a", 1, loadOf(&loads, &poolCapacity{}))
	b, _ := cache.get(context.Background(), "b", 1, loadOf(&loads, &poolCapacity{}))
	a2, _ := cache.get(context.Background(), "a", 1, loadOf(&loads, &poolCapacity{}))

	assert.EqualValues(t, 2, loads.Load())
	assert.NotSame(t, a, b)
	assert.Same(t, a, a2)
}

// Counts change between windows (setup jobs finish, instances are claimed), so a window never
// reuses another's, however recent it is.
func TestCapacityCache_WindowsAreIndependent(t *testing.T) {
	cache := newCapacityCache(time.Hour)
	var loads atomic.Int32

	w1, _ := cache.get(context.Background(), "pool", 1000, loadOf(&loads, &poolCapacity{}))
	w2, _ := cache.get(context.Background(), "pool", 1300, loadOf(&loads, &poolCapacity{}))
	w1again, _ := cache.get(context.Background(), "pool", 1000, loadOf(&loads, &poolCapacity{}))

	assert.EqualValues(t, 2, loads.Load())
	assert.NotSame(t, w1, w2)
	assert.Same(t, w1, w1again)
}

func TestCapacityCache_ReloadsAfterTTL(t *testing.T) {
	cache := newCapacityCache(20 * time.Second)
	now := time.Unix(1000, 0)
	cache.now = func() time.Time { return now }
	var loads atomic.Int32

	first, _ := cache.get(context.Background(), "pool", 1, loadOf(&loads, &poolCapacity{}))
	now = now.Add(19 * time.Second)
	again, _ := cache.get(context.Background(), "pool", 1, loadOf(&loads, &poolCapacity{}))
	assert.Same(t, first, again)
	assert.EqualValues(t, 1, loads.Load())

	now = now.Add(2 * time.Second) // 21s after the load
	fresh, _ := cache.get(context.Background(), "pool", 1, loadOf(&loads, &poolCapacity{}))
	assert.NotSame(t, first, fresh)
	assert.EqualValues(t, 2, loads.Load())
}

// Windows pass and never come back, so their entries must not pile up.
func TestCapacityCache_ExpiredEntriesAreDropped(t *testing.T) {
	cache := newCapacityCache(time.Minute)
	now := time.Unix(1000, 0)
	cache.now = func() time.Time { return now }
	var loads atomic.Int32

	for w := int64(0); w < 10; w++ {
		_, err := cache.get(context.Background(), "pool", w, loadOf(&loads, &poolCapacity{}))
		require.NoError(t, err)
	}
	assert.Len(t, cache.entries, 10)

	now = now.Add(2 * time.Minute)
	_, err := cache.get(context.Background(), "pool", 99, loadOf(&loads, &poolCapacity{}))
	require.NoError(t, err)
	assert.Len(t, cache.entries, 1, "only the entry just loaded is kept")
}

func TestCapacityCache_InvalidateDropsThePoolInEveryWindow(t *testing.T) {
	cache := newCapacityCache(time.Hour)
	var loads atomic.Int32
	for _, pool := range []string{"a", "b"} {
		for _, w := range []int64{1, 2} {
			_, err := cache.get(context.Background(), pool, w, loadOf(&loads, &poolCapacity{}))
			require.NoError(t, err)
		}
	}
	require.EqualValues(t, 4, loads.Load())

	cache.invalidate("a")

	for _, w := range []int64{1, 2} {
		_, err := cache.get(context.Background(), "b", w, loadOf(&loads, &poolCapacity{}))
		require.NoError(t, err)
	}
	assert.EqualValues(t, 4, loads.Load(), "pool b is untouched")
	for _, w := range []int64{1, 2} {
		_, err := cache.get(context.Background(), "a", w, loadOf(&loads, &poolCapacity{}))
		require.NoError(t, err)
	}
	assert.EqualValues(t, 6, loads.Load(), "pool a is loaded again in both windows")
}

// A load that started before an invalidation may have read counts from before it, so callers
// that arrive after the invalidation must not wait for it.
func TestCapacityCache_InvalidateDuringLoad(t *testing.T) {
	cache := newCapacityCache(time.Hour)
	started, release := make(chan struct{}), make(chan struct{})
	stale, fresh := &poolCapacity{}, &poolCapacity{}

	var leaderGot *poolCapacity
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		leaderGot, _ = cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
			close(started)
			<-release
			return stale, nil
		})
	}()
	<-started
	cache.invalidate("pool")

	got, err := cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) { return fresh, nil })
	require.NoError(t, err)
	assert.Same(t, fresh, got, "a caller after the invalidation loads for itself")

	close(release)
	wg.Wait()
	assert.Same(t, stale, leaderGot, "the load in progress still returns to its own caller")

	again, err := cache.get(context.Background(), "pool", 1, func(context.Context) (*poolCapacity, error) {
		t.Error("the fresh load must have been kept")
		return nil, nil
	})
	require.NoError(t, err)
	assert.Same(t, fresh, again, "the stale load must not replace the fresh one")
}

func TestCapacityCache_ErrorsAreNotReused(t *testing.T) {
	cache := newCapacityCache(time.Minute)
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
	cache := newCapacityCache(time.Minute)
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
	cache := newCapacityCache(time.Minute)
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
	cache := newCapacityCache(time.Minute)
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
	cache := newCapacityCache(time.Minute)
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

// countingInstanceStore and countingOutboxStore count how often each pool's counts are read.
type countingInstanceStore struct {
	*MockInstanceStore
	mu    sync.Mutex
	calls map[string]int
}

func (s *countingInstanceStore) CountPoolInstances(ctx context.Context, pool string) ([]types.PoolInstanceCount, error) {
	s.mu.Lock()
	s.calls[pool]++
	s.mu.Unlock()
	return s.MockInstanceStore.CountPoolInstances(ctx, pool)
}

type countingOutboxStore struct {
	*MockOutboxStore
	mu    sync.Mutex
	calls map[string]int
}

func (s *countingOutboxStore) CountSetupJobs(ctx context.Context, pool string) ([]types.SetupJobCount, error) {
	s.mu.Lock()
	s.calls[pool]++
	s.mu.Unlock()
	return s.MockOutboxStore.CountSetupJobs(ctx, pool)
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

// A window's jobs share a pool's counts, so each pool is read once however many groups use it.
func TestScaleGroup_GroupsOfAWindowShareOnePoolRead(t *testing.T) {
	h := newCapHarness(t, 3, nil)
	insts := &countingInstanceStore{MockInstanceStore: h.instances, calls: map[string]int{}}
	jobsStore := &countingOutboxStore{MockOutboxStore: h.outbox, calls: map[string]int{}}
	h.scaler.instanceStore, h.scaler.outboxStore = insts, jobsStore
	// Every group already has its target (3) free, so nothing is created.
	h.add(3, "linux-amd64", "default", "default", "", types.StateCreated, types.InstanceSourcePredictor)
	h.add(3, "linux-amd64", "default", "variant_medium", "", types.StateCreated, types.InstanceSourcePredictor)
	h.add(3, "linux-amd64", "default", "variant_ubuntu24", "ubuntu24-img", types.StateCreated, types.InstanceSourcePredictor)

	h.scaleAt(t, &capBase, 1000)
	h.scaleAt(t, &capMedium, 1000)
	h.scaleAt(t, &capUbuntu, 1000)

	require.Empty(t, h.setupJobs(t), "this scenario must not create jobs, which would drop the shared counts")
	assert.Equal(t, map[string]int{"linux-amd64": 1, "linux-amd64-fallback": 1, "linux-amd64-west4": 1}, insts.calls)
	assert.Equal(t, map[string]int{"linux-amd64": 1, "linux-amd64-fallback": 1, "linux-amd64-west4": 1}, jobsStore.calls)
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

func TestCreateSetupJob_DropsOnlyThatPoolsCounts(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	ctx := context.Background()
	for _, pool := range []string{"linux-amd64", "linux-amd64-fallback"} {
		for _, w := range []int64{1000, 2800} {
			_, err := h.scaler.sharedPoolCapacity(ctx, pool, w)
			require.NoError(t, err)
		}
	}
	require.Len(t, h.scaler.capacity.entries, 4)

	require.NoError(t, h.scaler.createSetupJob(ctx, "linux-amd64", spillParams(&capMedium)))

	assert.Len(t, h.scaler.capacity.entries, 2)
	for k := range h.scaler.capacity.entries {
		assert.Equal(t, "linux-amd64-fallback", k.pool)
	}
}

// Even a failed Create may have stored the job, so the counts are dropped regardless.
func TestCreateSetupJob_DropsCountsWhenCreateFails(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	ctx := context.Background()
	_, err := h.scaler.sharedPoolCapacity(ctx, "linux-amd64", 1000)
	require.NoError(t, err)
	h.outbox.createErr = errors.New("db down")

	require.Error(t, h.scaler.createSetupJob(ctx, "linux-amd64", spillParams(&capMedium)))

	assert.Empty(t, h.scaler.capacity.entries)
}

// A spill follows a failed setup and must see the jobs just created, so it never reads shared counts.
func TestHandleSetupFailure_ReadsFreshCounts(t *testing.T) {
	h := newCapHarness(t, 0, nil)
	ctx := context.Background()
	// The shared counts of the primary (limit 4 for variant_medium) show it empty.
	_, err := h.scaler.sharedPoolCapacity(ctx, "linux-amd64", 1000)
	require.NoError(t, err)
	// Then it fills up with pending jobs, written straight to the store so nothing is invalidated.
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
