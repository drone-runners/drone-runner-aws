package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/drone-runners/drone-runner-aws/types"
)

const testFQImage = "projects/p/global/images/img-v1"

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
			got, err := cache.get(context.Background(), "pool", func(context.Context) (*poolCapacity, error) {
				loads.Add(1)
				<-release
				return want, nil
			})
			assert.NoError(t, err)
			results[i] = got
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // let every caller reach get while the leader is loading
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
	load := func(context.Context) (*poolCapacity, error) { loads.Add(1); return &poolCapacity{}, nil }

	a, _ := cache.get(context.Background(), "a", load)
	b, _ := cache.get(context.Background(), "b", load)
	a2, _ := cache.get(context.Background(), "a", load)

	assert.EqualValues(t, 2, loads.Load())
	assert.NotSame(t, a, b)
	assert.Same(t, a, a2)
}

func TestCapacityCache_ReloadsAfterTTL(t *testing.T) {
	cache := newCapacityCache(20 * time.Second)
	now := time.Unix(1000, 0)
	cache.now = func() time.Time { return now }
	var loads atomic.Int32
	load := func(context.Context) (*poolCapacity, error) { loads.Add(1); return &poolCapacity{}, nil }

	first, _ := cache.get(context.Background(), "pool", load)
	now = now.Add(19 * time.Second)
	again, _ := cache.get(context.Background(), "pool", load)
	assert.Same(t, first, again)
	assert.EqualValues(t, 1, loads.Load())

	now = now.Add(2 * time.Second) // 21s after the load
	fresh, _ := cache.get(context.Background(), "pool", load)
	assert.NotSame(t, first, fresh)
	assert.EqualValues(t, 2, loads.Load())
}

func TestCapacityCache_ErrorsAreNotReused(t *testing.T) {
	cache := newCapacityCache(time.Minute)
	boom := errors.New("boom")
	var loads atomic.Int32

	_, err := cache.get(context.Background(), "pool", func(context.Context) (*poolCapacity, error) {
		loads.Add(1)
		return nil, boom
	})
	require.ErrorIs(t, err, boom)

	got, err := cache.get(context.Background(), "pool", func(context.Context) (*poolCapacity, error) {
		loads.Add(1)
		return &poolCapacity{}, nil
	})
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.EqualValues(t, 2, loads.Load())
}

func TestCapacityCache_WaiterStopsWhenItsContextEnds(t *testing.T) {
	cache := newCapacityCache(time.Minute)
	started, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = cache.get(context.Background(), "pool", func(context.Context) (*poolCapacity, error) {
			close(started)
			<-release
			return &poolCapacity{}, nil
		})
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := cache.get(ctx, "pool", func(context.Context) (*poolCapacity, error) {
		t.Error("a waiter must not load")
		return nil, nil
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
}
