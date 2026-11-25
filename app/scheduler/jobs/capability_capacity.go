package jobs

import (
	"context"
	"errors"
	"sync"
	"time"
)

// capacityCacheTTL bounds how long a window's counts of a pool are shared. A window's scale jobs
// are claimed within about one outbox poll of each other.
const capacityCacheTTL = 20 * time.Second

// maxLoadAttempts is how many times a caller waits for another caller's failed load, retrying
// the load itself, before it gives up and returns that failure.
const maxLoadAttempts = 3

// errLoadAborted is the failure waiters see when a load ends without a result (it panicked).
var errLoadAborted = errors.New("scaler: pool capacity load aborted")

// capacityKey identifies a pool's counts as read for one scaling window. Counts are never
// shared between windows: instances and setup jobs change between them.
type capacityKey struct {
	pool        string
	windowStart int64
}

// capacityCache shares a pool's counts between the callers of one window: one caller loads while
// the others wait for its result. Entries expire after ttl, are dropped by invalidate, and
// failed loads are never reused.
type capacityCache struct {
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[capacityKey]*capacityEntry
}

type capacityEntry struct {
	done     chan struct{} // closed once capacity and err are set
	capacity *poolCapacity
	err      error
	loadedAt time.Time
}

func newCapacityCache(ttl time.Duration) *capacityCache {
	return &capacityCache{ttl: ttl, now: time.Now, entries: map[capacityKey]*capacityEntry{}}
}

// get returns the pool's counts for the window, loading them if no caller has recently.
func (c *capacityCache) get(
	ctx context.Context, pool string, windowStart int64, load func(context.Context) (*poolCapacity, error),
) (*poolCapacity, error) {
	key := capacityKey{pool: pool, windowStart: windowStart}
	for attempt := 1; ; attempt++ {
		e, leader := c.acquire(key)
		if leader {
			return c.lead(ctx, e, load)
		}
		select {
		case <-e.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if e.err == nil {
			return e.capacity, nil
		}
		// The loader failed. If it was canceled while this caller is not, the failure says
		// nothing about this caller's own read, so load again instead of inheriting it.
		if !isContextError(e.err) || ctx.Err() != nil || attempt >= maxLoadAttempts {
			return nil, e.err
		}
	}
}

// acquire returns the key's entry, and whether the caller must load it. It also drops entries
// that can no longer be used.
func (c *capacityCache) acquire(key capacityKey) (e *capacityEntry, leader bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, old := range c.entries {
		if old.finished() && !old.usable(now, c.ttl) {
			delete(c.entries, k)
		}
	}
	if existing, ok := c.entries[key]; ok {
		return existing, false
	}
	e = &capacityEntry{done: make(chan struct{})}
	c.entries[key] = e
	return e, true
}

// lead loads the entry. It always releases the entry's waiters, even if load panics.
func (c *capacityCache) lead(ctx context.Context, e *capacityEntry, load func(context.Context) (*poolCapacity, error)) (*poolCapacity, error) {
	returned := false
	defer func() {
		if !returned {
			e.err = errLoadAborted
		}
		e.loadedAt = c.now()
		close(e.done)
	}()
	e.capacity, e.err = load(ctx)
	returned = true
	return e.capacity, e.err
}

// invalidate drops every window's counts of the pool. Callers already waiting on a load that is
// in progress still get its result, but later callers load again.
func (c *capacityCache) invalidate(pool string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.entries {
		if k.pool == pool {
			delete(c.entries, k)
		}
	}
}

func (e *capacityEntry) finished() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// usable reports whether a finished entry's counts can still be shared.
func (e *capacityEntry) usable(now time.Time, ttl time.Duration) bool {
	return e.err == nil && now.Sub(e.loadedAt) < ttl
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
