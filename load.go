package memory_ttl_storage

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrNilLoader is returned by GetOrLoad when load is nil.
	ErrNilLoader = errors.New("nil loader")
	// ErrLoadPanicked is returned to GetOrLoad waiters when the loader they
	// were waiting for panicked.
	ErrLoadPanicked = errors.New("loader panicked")
)

// loadCall is one in-flight load. val and err are written before done is
// closed and only read after.
type loadCall[V any] struct {
	done chan struct{}
	val  V
	err  error
}

// GetOrLoad returns the live value for key, or loads it with load, stores it
// with the default TTL and returns it. Concurrent calls for the same key share
// one load: the first caller (the leader) runs load without holding any cache
// lock, the others wait for its result. Errors are returned to every waiter
// and never cached; the next call loads again. load must not be nil, or
// GetOrLoad returns ErrNilLoader.
//
// The loaded value is stored when load finishes, so it overwrites a Set made
// during the load and re-adds a key deleted during the load (last writer wins).
//
// load runs with the leader's ctx: if that ctx is cancelled, every waiter for
// that key receives that error. A waiter whose own ctx is done
// stops waiting and returns ctx.Err(); the load continues for the others. If
// load panics, waiters get an error wrapping ErrLoadPanicked and the panic
// propagates in the leader.
//
// Stats: each call counts exactly one hit or one miss. The leader counts a
// miss when it runs load; a call served from the cache, or from the leader's
// successful result, counts a hit; a waiter that returns an error counts a
// miss.
func (c *Cache[K, V]) GetOrLoad(ctx context.Context, key K, load func(ctx context.Context, key K) (V, error)) (V, error) {
	var zero V
	if load == nil {
		return zero, ErrNilLoader
	}
	if value, _, ok := c.peek(key); ok {
		c.hits.Add(1)
		return value, nil
	}

	c.loadMu.Lock()
	if call, ok := c.inflight[key]; ok {
		c.loadMu.Unlock()
		return c.wait(ctx, call)
	}
	if c.inflight == nil {
		c.inflight = make(map[K]*loadCall[V])
	}
	call := &loadCall[V]{done: make(chan struct{})}
	c.inflight[key] = call
	c.loadMu.Unlock()

	return c.lead(ctx, key, call, load)
}

// wait blocks until call finishes or ctx is done.
func (c *Cache[K, V]) wait(ctx context.Context, call *loadCall[V]) (V, error) {
	select {
	case <-call.done:
		c.count(call.err == nil)
		return call.val, call.err
	case <-ctx.Done():
		c.misses.Add(1)
		var zero V
		return zero, ctx.Err()
	}
}

// lead runs the load registered in call and always completes it, even when
// load panics, so waiters are never stuck.
func (c *Cache[K, V]) lead(ctx context.Context, key K, call *loadCall[V], load func(ctx context.Context, key K) (V, error)) (V, error) {
	finished := false
	defer func() {
		if !finished {
			call.err = fmt.Errorf("load: %w", ErrLoadPanicked)
		}
		c.loadMu.Lock()
		delete(c.inflight, key)
		c.loadMu.Unlock()
		close(call.done)
	}()

	// A concurrent Set may have filled the key between the caller's miss and
	// the registration of this call.
	if value, _, ok := c.peek(key); ok {
		c.hits.Add(1)
		call.val, finished = value, true
		return value, nil
	}
	c.misses.Add(1)
	value, err := load(ctx, key)
	if err == nil {
		c.SetWithTTL(key, value, c.cfg.TTL)
	}
	call.val, call.err, finished = value, err, true
	return value, err
}
