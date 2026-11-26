package jobs

import (
	"context"
	"errors"
	"sync"
)

// maxLoadAttempts is how many times a caller waits for another caller's failed load, retrying
// the load itself, before it gives up and returns that failure.
const maxLoadAttempts = 3

// errLoadAborted is the failure waiters see when a load ends without a result (it panicked).
var errLoadAborted = errors.New("scaler: pool capacity load aborted")

// capacityKey identifies a pool's counts as read for one scaling window. Loads of different
// windows are never shared.
type capacityKey struct {
	pool        string
	windowStart int64
}

// capacityCache lets the callers that ask for a pool's counts at the same time share one read:
// one caller loads while the others wait for its result. It reuses nothing once a load is done,
// so a caller never gets counts older than the load it joined. Counts change as setup jobs are
// created and finish, and planning from older ones would create instances that are not needed.
type capacityCache struct {
	mu       sync.Mutex
	inflight map[capacityKey]*capacityLoad
}

// capacityLoad is a load in progress, and its result once done is closed.
type capacityLoad struct {
	done     chan struct{} // closed once capacity and err are set
	capacity *poolCapacity
	err      error
}

func newCapacityCache() *capacityCache {
	return &capacityCache{inflight: map[capacityKey]*capacityLoad{}}
}

// get returns the pool's counts for the window: the result of the load in progress if there is
// one, otherwise of a new load.
func (c *capacityCache) get(
	ctx context.Context, pool string, windowStart int64, load func(context.Context) (*poolCapacity, error),
) (*poolCapacity, error) {
	key := capacityKey{pool: pool, windowStart: windowStart}
	for attempt := 1; ; attempt++ {
		l, leader := c.join(key)
		if leader {
			return c.lead(ctx, key, l, load)
		}
		select {
		case <-l.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if l.err == nil {
			return l.capacity, nil
		}
		// The loader failed. If it was canceled while this caller is not, the failure says
		// nothing about this caller's own read, so load again instead of inheriting it.
		if !isContextError(l.err) || ctx.Err() != nil || attempt >= maxLoadAttempts {
			return nil, l.err
		}
	}
}

// join returns the load in progress for the key, and whether the caller must run it.
func (c *capacityCache) join(key capacityKey) (l *capacityLoad, leader bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.inflight[key]; ok {
		return existing, false
	}
	l = &capacityLoad{done: make(chan struct{})}
	c.inflight[key] = l
	return l, true
}

// lead runs the load. It always releases the load's waiters, even if load panics.
func (c *capacityCache) lead(
	ctx context.Context, key capacityKey, l *capacityLoad, load func(context.Context) (*poolCapacity, error),
) (*poolCapacity, error) {
	returned := false
	defer func() {
		if !returned {
			l.err = errLoadAborted
		}
		c.mu.Lock()
		if c.inflight[key] == l { // not already dropped by invalidate or replaced
			delete(c.inflight, key)
		}
		c.mu.Unlock()
		close(l.done)
	}()
	l.capacity, l.err = load(ctx)
	returned = true
	return l.capacity, l.err
}

// invalidate stops new callers from joining the loads in progress for the pool, whatever the
// window. Callers already waiting on one still get its result, but later callers load again.
func (c *capacityCache) invalidate(pool string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.inflight {
		if k.pool == pool {
			delete(c.inflight, k)
		}
	}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
