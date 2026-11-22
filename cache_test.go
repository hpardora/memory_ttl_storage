package keepcache

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

// fakeClock is goroutine-safe: the concurrent stress test advances it while
// the cache reads it.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

type evicted struct {
	key    string
	value  int
	reason EvictReason
}

// newTestCache returns a cache driven by a fake clock that records evictions.
func newTestCache(t *testing.T, cfg Config[string, int]) (*Cache[string, int], *fakeClock, *[]evicted) {
	t.Helper()
	var got []evicted
	cfg.OnEvict = func(k string, v int, r EvictReason) { got = append(got, evicted{k, v, r}) }
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Unix(1000, 0)}
	c.now = clock.Now
	return c, clock, &got
}

func TestNewConfig(t *testing.T) {
	t.Run("zero value gets defaults", func(t *testing.T) {
		c, err := New(Config[string, int]{})
		if err != nil {
			t.Fatal(err)
		}
		if c.cfg.TTL != 10*time.Second || c.cfg.CleanupInterval != time.Second ||
			c.cfg.BackupInterval != 30*time.Second || c.cfg.MaxBackupSize != defaultMaxBackupSize || c.cfg.Logger == nil {
			t.Fatalf("defaults not applied: %+v", c.cfg)
		}
	})
	invalid := map[string]Config[string, int]{
		"negative TTL":             {TTL: -1},
		"negative cleanup":         {CleanupInterval: -1},
		"negative backup interval": {BackupInterval: -1},
		"negative max items":       {MaxItems: -1},
		"negative max backup size": {MaxBackupSize: -1},
	}
	for name, cfg := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestEvictReasonString(t *testing.T) {
	tests := map[EvictReason]string{
		Expired: "expired", Deleted: "deleted", Replaced: "replaced",
		Capacity: "capacity", EvictReason(99): "EvictReason(99)",
	}
	for r, want := range tests {
		if got := r.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int(r), got, want)
		}
	}
}

func TestSetGet(t *testing.T) {
	tests := []struct {
		name      string
		set       func(c *Cache[string, int])
		advance   time.Duration
		wantValue int
		wantOK    bool
		wantEvs   []evicted
	}{
		{
			name:      "live item",
			set:       func(c *Cache[string, int]) { c.Set("a", 1) },
			advance:   9 * time.Second,
			wantValue: 1, wantOK: true,
		},
		{
			name:    "default ttl elapsed",
			set:     func(c *Cache[string, int]) { c.Set("a", 1) },
			advance: 10 * time.Second,
			wantEvs: []evicted{{"a", 1, Expired}},
		},
		{
			name:      "custom ttl live",
			set:       func(c *Cache[string, int]) { c.SetWithTTL("a", 1, time.Minute) },
			advance:   30 * time.Second,
			wantValue: 1, wantOK: true,
		},
		{
			name:    "custom ttl elapsed",
			set:     func(c *Cache[string, int]) { c.SetWithTTL("a", 1, time.Second) },
			advance: time.Second,
			wantEvs: []evicted{{"a", 1, Expired}},
		},
		{
			name:      "non-positive ttl uses default",
			set:       func(c *Cache[string, int]) { c.SetWithTTL("a", 1, 0) },
			advance:   9 * time.Second,
			wantValue: 1, wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, clock, evs := newTestCache(t, Config[string, int]{})
			tt.set(c)
			clock.Advance(tt.advance)
			v, ok := c.Get("a")
			if v != tt.wantValue || ok != tt.wantOK {
				t.Fatalf("Get = %d,%v want %d,%v", v, ok, tt.wantValue, tt.wantOK)
			}
			if !equalEvicted(*evs, tt.wantEvs) {
				t.Fatalf("evictions = %v, want %v", *evs, tt.wantEvs)
			}
			if !ok && c.Len() != 0 {
				t.Fatalf("expired item still stored")
			}
		})
	}
	t.Run("missing key", func(t *testing.T) {
		c, _, _ := newTestCache(t, Config[string, int]{})
		if v, ok := c.Get("nope"); ok || v != 0 {
			t.Fatalf("Get = %d,%v", v, ok)
		}
	})
}

func TestRefresh(t *testing.T) {
	t.Run("GetAndRefresh extends expiry", func(t *testing.T) {
		c, clock, _ := newTestCache(t, Config[string, int]{TTL: 10 * time.Second})
		c.Set("a", 1)
		clock.Advance(8 * time.Second)
		if _, ok := c.GetAndRefresh("a"); !ok {
			t.Fatal("want hit")
		}
		clock.Advance(8 * time.Second)
		if _, ok := c.Get("a"); !ok {
			t.Fatal("refreshed item expired early")
		}
	})
	t.Run("GetAndRefresh uses item ttl", func(t *testing.T) {
		c, clock, _ := newTestCache(t, Config[string, int]{TTL: 10 * time.Second})
		c.SetWithTTL("a", 1, time.Minute)
		clock.Advance(50 * time.Second)
		c.GetAndRefresh("a")
		clock.Advance(59 * time.Second)
		if _, ok := c.Get("a"); !ok {
			t.Fatal("want hit within item ttl")
		}
	})
	t.Run("Get does not extend", func(t *testing.T) {
		c, clock, _ := newTestCache(t, Config[string, int]{TTL: 10 * time.Second})
		c.Set("a", 1)
		clock.Advance(8 * time.Second)
		c.Get("a")
		clock.Advance(3 * time.Second)
		if _, ok := c.Get("a"); ok {
			t.Fatal("Get must not extend expiry")
		}
	})
	t.Run("GetAndRefresh on expired item misses", func(t *testing.T) {
		c, clock, evs := newTestCache(t, Config[string, int]{TTL: time.Second})
		c.Set("a", 1)
		clock.Advance(time.Second)
		if _, ok := c.GetAndRefresh("a"); ok {
			t.Fatal("want miss")
		}
		if !equalEvicted(*evs, []evicted{{"a", 1, Expired}}) {
			t.Fatalf("evictions = %v", *evs)
		}
	})
}

func TestEvictionReasons(t *testing.T) {
	tests := []struct {
		name string
		run  func(c *Cache[string, int], clock *fakeClock)
		want []evicted
	}{
		{"delete present", func(c *Cache[string, int], _ *fakeClock) { c.Set("a", 1); c.Delete("a") },
			[]evicted{{"a", 1, Deleted}}},
		{"delete absent", func(c *Cache[string, int], _ *fakeClock) { c.Delete("a") }, nil},
		{"set over live is replaced", func(c *Cache[string, int], _ *fakeClock) { c.Set("a", 1); c.Set("a", 2) },
			[]evicted{{"a", 1, Replaced}}},
		{"set over expired is expired", func(c *Cache[string, int], clock *fakeClock) {
			c.Set("a", 1)
			clock.Advance(time.Hour)
			c.Set("a", 2)
		}, []evicted{{"a", 1, Expired}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, clock, evs := newTestCache(t, Config[string, int]{})
			tt.run(c, clock)
			if !equalEvicted(*evs, tt.want) {
				t.Fatalf("evictions = %v, want %v", *evs, tt.want)
			}
		})
	}
	t.Run("replaced value is readable", func(t *testing.T) {
		c, _, _ := newTestCache(t, Config[string, int]{})
		c.Set("a", 1)
		c.Set("a", 2)
		if v, _ := c.Get("a"); v != 2 || c.Len() != 1 {
			t.Fatalf("v = %d, len = %d", v, c.Len())
		}
	})
}

func TestDeleteExpired(t *testing.T) {
	t.Run("removes only expired", func(t *testing.T) {
		c, clock, evs := newTestCache(t, Config[string, int]{TTL: 10 * time.Second})
		c.SetWithTTL("short", 1, time.Second)
		c.SetWithTTL("mid", 2, 5*time.Second)
		c.Set("long", 3)
		clock.Advance(5 * time.Second)
		c.DeleteExpired()
		if c.Len() != 1 {
			t.Fatalf("Len = %d, want 1", c.Len())
		}
		if len(*evs) != 2 {
			t.Fatalf("evictions = %v", *evs)
		}
		for _, ev := range *evs {
			if ev.reason != Expired {
				t.Fatalf("reason = %v", ev.reason)
			}
		}
		if _, ok := c.Get("long"); !ok {
			t.Fatal("long item removed")
		}
	})
	t.Run("survives re-set and refreshed keys", func(t *testing.T) {
		c, clock, evs := newTestCache(t, Config[string, int]{TTL: 10 * time.Second})
		c.Set("reset", 1)
		c.Set("refreshed", 2)
		clock.Advance(9 * time.Second)
		c.Set("reset", 11)
		c.GetAndRefresh("refreshed")
		clock.Advance(2 * time.Second)
		c.DeleteExpired()
		if c.Len() != 2 {
			t.Fatalf("Len = %d, want 2", c.Len())
		}
		if !equalEvicted(*evs, []evicted{{"reset", 1, Replaced}}) {
			t.Fatalf("evictions = %v", *evs)
		}
	})
}

func TestOnEvictMayCallBack(t *testing.T) {
	c, clock, _ := newTestCache(t, Config[string, int]{TTL: time.Second})
	var calls int
	c.cfg.OnEvict = func(k string, _ int, _ EvictReason) {
		calls++
		c.Len()
		c.Set(k+"-again", 0)
	}
	c.Set("a", 1)
	clock.Advance(time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.DeleteExpired()
		c.Delete("a-again")
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock in OnEvict")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestStats(t *testing.T) {
	c, clock, _ := newTestCache(t, Config[string, int]{TTL: 10 * time.Second})
	c.Set("a", 1)
	c.SetWithTTL("b", 2, time.Second)
	c.Get("a")       // hit
	c.Get("missing") // miss
	c.Set("a", 3)    // replaced
	clock.Advance(time.Second)
	c.Get("b")    // expired: miss + expiration
	c.Delete("a") // deleted
	want := Stats{Hits: 1, Misses: 2, Evictions: 3, Expirations: 1, Items: 0}
	if got := c.Stats(); got != want {
		t.Fatalf("Stats = %+v, want %+v", got, want)
	}
}

func equalEvicted(a, b []evicted) bool {
	return slices.Equal(a, b)
}

func TestMaxItems(t *testing.T) {
	const maxItems = 3
	tests := []struct {
		name      string
		setup     func(c *Cache[string, int], clock *fakeClock)
		wantKeys  []string
		wantEvict []evicted
	}{
		{
			name: "evicts soonest-expiring live item",
			setup: func(c *Cache[string, int], _ *fakeClock) {
				c.SetWithTTL("a", 1, 30*time.Second)
				c.SetWithTTL("b", 2, 10*time.Second)
				c.SetWithTTL("c", 3, 20*time.Second)
				c.SetWithTTL("d", 4, time.Minute)
			},
			wantKeys:  []string{"a", "c", "d"},
			wantEvict: []evicted{{"b", 2, Capacity}},
		},
		{
			name: "reclaims expired before live",
			setup: func(c *Cache[string, int], clock *fakeClock) {
				c.SetWithTTL("a", 1, time.Minute)
				c.SetWithTTL("b", 2, time.Second)
				c.SetWithTTL("c", 3, 2*time.Minute)
				clock.Advance(time.Second)
				c.SetWithTTL("d", 4, time.Minute)
			},
			wantKeys:  []string{"a", "c", "d"},
			wantEvict: []evicted{{"b", 2, Expired}},
		},
		{
			name: "overwrite at capacity evicts nothing but the replaced value",
			setup: func(c *Cache[string, int], _ *fakeClock) {
				c.SetWithTTL("a", 1, time.Minute)
				c.SetWithTTL("b", 2, time.Minute)
				c.SetWithTTL("c", 3, time.Minute)
				c.SetWithTTL("a", 9, time.Minute)
			},
			wantKeys:  []string{"a", "b", "c"},
			wantEvict: []evicted{{"a", 1, Replaced}},
		},
		{
			name: "below capacity evicts nothing",
			setup: func(c *Cache[string, int], _ *fakeClock) {
				c.Set("a", 1)
				c.Set("b", 2)
			},
			wantKeys: []string{"a", "b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, clock, got := newTestCache(t, Config[string, int]{MaxItems: maxItems})
			tt.setup(c, clock)
			if c.Len() > maxItems {
				t.Fatalf("Len = %d, exceeds %d", c.Len(), maxItems)
			}
			for _, k := range tt.wantKeys {
				if _, ok := c.items[k]; !ok {
					t.Errorf("key %q missing", k)
				}
			}
			if len(c.items) != len(tt.wantKeys) {
				t.Errorf("Len = %d, want %d", len(c.items), len(tt.wantKeys))
			}
			if !equalEvicted(*got, tt.wantEvict) {
				t.Errorf("evictions = %v, want %v", *got, tt.wantEvict)
			}
		})
	}
}

func TestMaxItemsNeverExceeded(t *testing.T) {
	c, clock, _ := newTestCache(t, Config[string, int]{MaxItems: 5})
	for i := range 100 {
		c.SetWithTTL(string(rune('a'+i%26)), i, time.Duration(i%7+1)*time.Second)
		clock.Advance(time.Duration(i%3) * time.Second)
		if c.Len() > 5 {
			t.Fatalf("Len = %d after %d sets", c.Len(), i+1)
		}
	}
}

func TestMaxItemsStats(t *testing.T) {
	c, _, _ := newTestCache(t, Config[string, int]{MaxItems: 1})
	c.Set("a", 1)
	c.Set("b", 2)
	if got := c.Stats(); got.Evictions != 1 || got.Expirations != 0 || got.Items != 1 {
		t.Fatalf("Stats = %+v", got)
	}
}

func TestRandomOperationsMatchModel(t *testing.T) {
	type modelItem struct {
		value     int
		ttl       time.Duration
		expiresAt time.Time
	}
	const (
		steps      = 5000
		keyCount   = 8
		defaultTTL = 10 * time.Second
		tick       = 500 * time.Millisecond
	)
	rng := rand.New(rand.NewPCG(1, 2))
	c, clock, _ := newTestCache(t, Config[string, int]{TTL: defaultTTL})
	model := map[string]modelItem{}
	live := func(it modelItem) bool { return clock.Now().Before(it.expiresAt) }
	set := func(key string, value int, ttl time.Duration) {
		model[key] = modelItem{value, ttl, clock.Now().Add(ttl)}
	}

	for step := range steps {
		key := fmt.Sprintf("k%d", rng.IntN(keyCount))
		ttl := time.Duration(1+rng.IntN(10)) * time.Second
		op := rng.IntN(7)
		switch op {
		case 0:
			c.Set(key, step)
			set(key, step, defaultTTL)
		case 1:
			c.SetWithTTL(key, step, ttl)
			set(key, step, ttl)
		case 2, 3:
			refresh := op == 3
			var got int
			var ok bool
			if refresh {
				got, ok = c.GetAndRefresh(key)
			} else {
				got, ok = c.Get(key)
			}
			it, present := model[key]
			wantOK := present && live(it)
			switch {
			case !wantOK:
				delete(model, key)
			case refresh:
				it.expiresAt = clock.Now().Add(it.ttl)
				model[key] = it
			}
			if ok != wantOK || (ok && got != it.value) {
				t.Fatalf("step %d: Get(%q) = %d,%v want %d,%v", step, key, got, ok, it.value, wantOK)
			}
		case 4:
			c.Delete(key)
			delete(model, key)
		case 5:
			c.DeleteExpired()
			for k, it := range model {
				if !live(it) {
					delete(model, k)
				}
			}
		case 6:
			clock.Advance(time.Duration(rng.IntN(6)) * tick)
		}
		if c.Len() != len(model) {
			t.Fatalf("step %d op %d: Len = %d, model %d", step, op, c.Len(), len(model))
		}
		assertHeapConsistent(t, c, step)
	}
}

func assertHeapConsistent(t *testing.T, c *Cache[string, int], step int) {
	t.Helper()
	if len(c.expiry) != len(c.items) {
		t.Fatalf("step %d: heap len %d, items %d", step, len(c.expiry), len(c.items))
	}
	for i, e := range c.expiry {
		if e.index != i {
			t.Fatalf("step %d: entry %q index %d at position %d", step, e.key, e.index, i)
		}
		if c.items[e.key] != e {
			t.Fatalf("step %d: entry %q not the one in items", step, e.key)
		}
		if parent := (i - 1) / 2; i > 0 && c.expiry[parent].expiresAt.After(e.expiresAt) {
			t.Fatalf("step %d: heap order broken at %d", step, i)
		}
	}
}

func TestEvictionLogOmitsKeysAndValues(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c, _, _ := newTestCache(t, Config[string, int]{Logger: logger})
	c.Set("secret-key", 987654)
	c.Delete("secret-key")
	out := logs.String()
	if !strings.Contains(out, "reason=deleted") {
		t.Fatalf("log = %q, want the reason", out)
	}
	if strings.Contains(out, "secret-key") || strings.Contains(out, "987654") {
		t.Fatalf("log leaks key or value: %q", out)
	}
}

func TestSetExistingKeyAllocatesNothingWithoutConsumers(t *testing.T) {
	c, err := New(Config[string, int]{})
	if err != nil {
		t.Fatal(err)
	}
	c.Set("k", 1)
	if allocs := testing.AllocsPerRun(100, func() { c.Set("k", 2) }); allocs != 0 {
		t.Fatalf("allocs = %v, want 0", allocs)
	}
}

func TestConcurrentOperations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		onEvict bool
	}{
		{"without OnEvict", false},
		{"with OnEvict", true},
	} {
		t.Run(tc.name, func(t *testing.T) { runConcurrentOperations(t, tc.onEvict) })
	}
}

func runConcurrentOperations(t *testing.T, withOnEvict bool) {
	t.Helper()
	const (
		workers    = 8
		opsPerWork = 2000
		keyCount   = 32
		tick       = 100 * time.Millisecond
	)
	// Built directly: newTestCache's OnEvict appends to an unsynchronized slice.
	var (
		cbMu     sync.Mutex
		byReason = map[EvictReason]uint64{}
		cfg      = Config[string, int]{TTL: time.Second}
	)
	if withOnEvict {
		cfg.OnEvict = func(_ string, _ int, reason EvictReason) {
			cbMu.Lock()
			defer cbMu.Unlock()
			byReason[reason]++
		}
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Unix(1000, 0)}
	c.now = clock.Now
	var reads atomic.Uint64
	stop := make(chan struct{})
	var clockDone, wg sync.WaitGroup

	clockDone.Add(1)
	go func() {
		defer clockDone.Done()
		for {
			select {
			case <-stop:
				return
			default:
				clock.Advance(tick)
				time.Sleep(10 * time.Microsecond)
			}
		}
	}()
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(w), 7))
			for i := range opsPerWork {
				key := fmt.Sprintf("k%d", rng.IntN(keyCount))
				switch rng.IntN(6) {
				case 0:
					c.Get(key)
					reads.Add(1)
				case 1:
					c.GetAndRefresh(key)
					reads.Add(1)
				case 2:
					c.Set(key, i)
				case 3:
					c.SetWithTTL(key, i, time.Duration(1+rng.IntN(3))*tick)
				case 4:
					c.Delete(key)
				case 5:
					c.DeleteExpired()
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	clockDone.Wait()

	assertHeapConsistent(t, c, 0)
	got := c.Stats()
	if got.Hits+got.Misses != reads.Load() {
		t.Fatalf("hits+misses = %d, want %d reads", got.Hits+got.Misses, reads.Load())
	}
	if !withOnEvict {
		return
	}
	cbMu.Lock()
	defer cbMu.Unlock()
	var total uint64
	for _, n := range byReason {
		total += n
	}
	if total != got.Evictions {
		t.Fatalf("OnEvict calls = %d, want Stats.Evictions %d", total, got.Evictions)
	}
	if byReason[Expired] != got.Expirations {
		t.Fatalf("OnEvict Expired calls = %d, want Stats.Expirations %d", byReason[Expired], got.Expirations)
	}
}
