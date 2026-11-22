package memory_ttl_storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

// enteredCtx closes entered when GetOrLoad's waiter first calls Done, which is
// the moment it starts selecting on the leader's result. It lets tests know a
// waiter has joined without sleeping.
type enteredCtx struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func newEnteredCtx(parent context.Context) *enteredCtx {
	return &enteredCtx{Context: parent, entered: make(chan struct{})}
}

func (e *enteredCtx) Done() <-chan struct{} {
	e.once.Do(func() { close(e.entered) })
	return e.Context.Done()
}

// result is what a GetOrLoad goroutine returned.
type result struct {
	value int
	err   error
}

// startLeader runs GetOrLoad in a goroutine and returns once load is running.
func startLeader(c *Cache[string, int], key string, wg *sync.WaitGroup, load func(context.Context, string) (int, error)) <-chan result {
	started := make(chan struct{})
	out := make(chan result, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		v, err := c.GetOrLoad(context.Background(), key, func(ctx context.Context, k string) (int, error) {
			close(started)
			return load(ctx, k)
		})
		out <- result{v, err}
	}()
	<-started
	return out
}

// startWaiter runs GetOrLoad in a goroutine and returns once it is waiting.
func startWaiter(ctx context.Context, c *Cache[string, int], key string, wg *sync.WaitGroup) <-chan result {
	ec := newEnteredCtx(ctx)
	out := make(chan result, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		v, err := c.GetOrLoad(ec, key, func(context.Context, string) (int, error) {
			return 0, errors.New("waiter loader must not run")
		})
		out <- result{v, err}
	}()
	<-ec.entered
	return out
}

func TestGetOrLoadSingleFlight(t *testing.T) {
	const waiters = 49
	c, clock, _ := newTestCache(t, Config[string, int]{})
	var calls atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup

	leader := startLeader(c, "a", &wg, func(context.Context, string) (int, error) {
		calls.Add(1)
		<-release
		return 7, nil
	})
	outs := make([]<-chan result, waiters)
	for i := range outs {
		outs[i] = startWaiter(context.Background(), c, "a", &wg)
	}
	close(release)
	wg.Wait()

	for _, out := range append(outs, leader) {
		if r := <-out; r.err != nil || r.value != 7 {
			t.Fatalf("result = %+v, want 7, nil", r)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("loader called %d times, want 1", n)
	}
	v, expiresAt, ok := c.GetWithExpiration("a")
	if !ok || v != 7 || !expiresAt.Equal(clock.Now().Add(10*time.Second)) {
		t.Fatalf("cached = %d,%v,%v; want 7 with default TTL", v, expiresAt, ok)
	}
	if s := c.Stats(); s.Misses != 1 || s.Hits != waiters+1 { // +1: the GetWithExpiration above
		t.Fatalf("stats = %+v, want 1 miss and %d hits", s, waiters+1)
	}
}

func TestGetOrLoadDifferentKeysConcurrent(t *testing.T) {
	c, _, _ := newTestCache(t, Config[string, int]{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	block := func(context.Context, string) (int, error) {
		<-release
		return 1, nil
	}
	// Each startLeader returns only once its loader is running, so both are
	// blocked inside load at the same time or this test never finishes.
	a := startLeader(c, "a", &wg, block)
	b := startLeader(c, "b", &wg, block)
	close(release)
	wg.Wait()
	for _, out := range []<-chan result{a, b} {
		if r := <-out; r.err != nil || r.value != 1 {
			t.Fatalf("result = %+v, want 1, nil", r)
		}
	}
}

func TestGetOrLoadErrorNotCached(t *testing.T) {
	const waiters = 3
	c, _, _ := newTestCache(t, Config[string, int]{})
	var calls int
	release := make(chan struct{})
	var wg sync.WaitGroup

	leader := startLeader(c, "a", &wg, func(context.Context, string) (int, error) {
		calls++
		<-release
		return 0, errBoom
	})
	outs := []<-chan result{leader}
	for range waiters {
		outs = append(outs, startWaiter(context.Background(), c, "a", &wg))
	}
	close(release)
	wg.Wait()

	for _, out := range outs {
		if r := <-out; !errors.Is(r.err, errBoom) {
			t.Fatalf("err = %v, want errBoom", r.err)
		}
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d, want 0: errors must not be cached", c.Len())
	}
	if s := c.Stats(); s.Misses != waiters+1 || s.Hits != 0 {
		t.Fatalf("stats = %+v, want %d misses, 0 hits", s, waiters+1)
	}
	v, err := c.GetOrLoad(context.Background(), "a", func(context.Context, string) (int, error) {
		calls++
		return 5, nil
	})
	if err != nil || v != 5 || calls != 2 {
		t.Fatalf("retry = %d,%v after %d calls; want 5,nil after 2", v, err, calls)
	}
}

func TestGetOrLoadWaiterContextCancelled(t *testing.T) {
	c, _, _ := newTestCache(t, Config[string, int]{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	leader := startLeader(c, "a", &wg, func(context.Context, string) (int, error) {
		<-release
		return 3, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	waiter := startWaiter(ctx, c, "a", &wg)
	cancel()
	if r := <-waiter; !errors.Is(r.err, context.Canceled) {
		t.Fatalf("waiter err = %v, want context.Canceled", r.err)
	}

	close(release)
	wg.Wait()
	if r := <-leader; r.err != nil || r.value != 3 {
		t.Fatalf("leader = %+v, want 3, nil", r)
	}
	if v, ok := c.Get("a"); !ok || v != 3 {
		t.Fatalf("Get = %d,%v want 3,true", v, ok)
	}
}

func TestGetOrLoadPanic(t *testing.T) {
	c, _, _ := newTestCache(t, Config[string, int]{})
	release := make(chan struct{})
	started := make(chan struct{})
	panicked := make(chan any, 1)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() { panicked <- recover() }()
		_, _ = c.GetOrLoad(context.Background(), "a", func(context.Context, string) (int, error) {
			close(started)
			<-release
			panic("kaboom")
		})
	}()
	<-started
	waiter := startWaiter(context.Background(), c, "a", &wg)
	close(release)
	wg.Wait()

	if got := <-panicked; got != "kaboom" {
		t.Fatalf("leader recovered %v, want kaboom", got)
	}
	if r := <-waiter; !errors.Is(r.err, ErrLoadPanicked) {
		t.Fatalf("waiter err = %v, want ErrLoadPanicked", r.err)
	}
	v, err := c.GetOrLoad(context.Background(), "a", func(context.Context, string) (int, error) { return 9, nil })
	if err != nil || v != 9 {
		t.Fatalf("next call = %d,%v want 9,nil: inflight not cleared", v, err)
	}
}

// A Set landing after the caller's first miss but before the leader runs must
// be returned without calling load. The test registers the call and invokes
// lead directly, the same two steps GetOrLoad performs, to place the Set
// between them deterministically.
func TestGetOrLoadLeaderRechecksCache(t *testing.T) {
	c, _, _ := newTestCache(t, Config[string, int]{})
	call := &loadCall[int]{done: make(chan struct{})}
	c.loadMu.Lock()
	c.inflight = map[string]*loadCall[int]{"a": call}
	c.loadMu.Unlock()

	c.Set("a", 4)
	v, err := c.lead(context.Background(), "a", call, func(context.Context, string) (int, error) {
		return 0, errors.New("must not load")
	})
	if err != nil || v != 4 {
		t.Fatalf("lead = %d,%v want 4,nil", v, err)
	}
	<-call.done
	if s := c.Stats(); s.Hits != 1 || s.Misses != 0 {
		t.Fatalf("stats = %+v, want 1 hit 0 misses", s)
	}
}

func TestGetOrLoadOverwritesSetDuringLoad(t *testing.T) {
	c, _, _ := newTestCache(t, Config[string, int]{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	leader := startLeader(c, "a", &wg, func(context.Context, string) (int, error) {
		<-release
		return 2, nil
	})
	c.Set("a", 1)
	close(release)
	wg.Wait()

	if r := <-leader; r.err != nil || r.value != 2 {
		t.Fatalf("leader = %+v, want 2, nil", r)
	}
	if v, ok := c.Get("a"); !ok || v != 2 {
		t.Fatalf("Get = %d,%v want 2,true: load result is the last writer", v, ok)
	}
}

func TestGetOrLoadNilLoader(t *testing.T) {
	c, _, _ := newTestCache(t, Config[string, int]{})
	if _, err := c.GetOrLoad(context.Background(), "a", nil); !errors.Is(err, ErrNilLoader) {
		t.Fatalf("err = %v, want ErrNilLoader", err)
	}
}

func TestGetOrLoadCounting(t *testing.T) {
	tests := []struct {
		name       string
		load       func(context.Context, string) (int, error)
		prefill    bool
		wantErr    error
		wantHits   uint64
		wantMisses uint64
	}{
		{"hit", nil, true, nil, 1, 0},
		{"load success", func(context.Context, string) (int, error) { return 1, nil }, false, nil, 0, 1},
		{"load error", func(context.Context, string) (int, error) { return 0, errBoom }, false, errBoom, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _, _ := newTestCache(t, Config[string, int]{})
			if tt.prefill {
				c.Set("a", 1)
			}
			load := tt.load
			if load == nil {
				load = func(context.Context, string) (int, error) { return 0, errors.New("must not load") }
			}
			if _, err := c.GetOrLoad(context.Background(), "a", load); !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if s := c.Stats(); s.Hits != tt.wantHits || s.Misses != tt.wantMisses {
				t.Fatalf("stats = %+v, want %d hits %d misses", s, tt.wantHits, tt.wantMisses)
			}
		})
	}
}

func TestGetOrLoadExpiredItemReloads(t *testing.T) {
	c, clock, evs := newTestCache(t, Config[string, int]{})
	c.Set("a", 1)
	clock.Advance(10 * time.Second)
	v, err := c.GetOrLoad(context.Background(), "a", func(context.Context, string) (int, error) { return 2, nil })
	if err != nil || v != 2 {
		t.Fatalf("GetOrLoad = %d,%v want 2,nil", v, err)
	}
	if !equalEvicted(*evs, []evicted{{"a", 1, Expired}}) {
		t.Fatalf("evictions = %v, want a expired", *evs)
	}
}

func TestGetWithExpiration(t *testing.T) {
	t.Run("hit", func(t *testing.T) {
		c, clock, _ := newTestCache(t, Config[string, int]{})
		setAt := clock.Now()
		c.SetWithTTL("a", 1, time.Minute)
		clock.Advance(5 * time.Second)
		v, expiresAt, ok := c.GetWithExpiration("a")
		if !ok || v != 1 || !expiresAt.Equal(setAt.Add(time.Minute)) {
			t.Fatalf("got %d,%v,%v; want 1,%v,true", v, expiresAt, ok, setAt.Add(time.Minute))
		}
	})
	t.Run("missing", func(t *testing.T) {
		c, _, _ := newTestCache(t, Config[string, int]{})
		if _, _, ok := c.GetWithExpiration("a"); ok {
			t.Fatal("ok = true for missing key")
		}
	})
	t.Run("expired", func(t *testing.T) {
		c, clock, evs := newTestCache(t, Config[string, int]{})
		c.Set("a", 1)
		clock.Advance(10 * time.Second)
		if _, _, ok := c.GetWithExpiration("a"); ok {
			t.Fatal("ok = true for expired key")
		}
		if !equalEvicted(*evs, []evicted{{"a", 1, Expired}}) {
			t.Fatalf("evictions = %v, want a expired", *evs)
		}
	})
}
