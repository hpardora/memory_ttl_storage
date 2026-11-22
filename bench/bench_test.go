package bench_test

import (
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jellydator/ttlcache/v3"
	gocache "github.com/patrickmn/go-cache"

	"github.com/hpardora/keepcache"
)

const (
	keyCount = 10_000
	ttl      = 5 * time.Minute
	// one write per writeEvery operations in the mixed workload (10% sets).
	writeEvery = 10
)

var keys = func() []string {
	ks := make([]string, keyCount)
	for i := range ks {
		ks[i] = "key-" + strconv.Itoa(i)
	}
	return ks
}()

// store is the minimal surface shared by the benchmarked caches.
type store struct {
	name string
	set  func(key string, value int)
	get  func(key string) bool
}

// stores returns freshly built, pre-filled caches. None starts a background
// goroutine: Run is not called, go-cache has no janitor with a zero cleanup
// interval, and ttlcache is not Started.
func stores(tb testing.TB) []store {
	tb.Helper()

	mc, err := keepcache.New(keepcache.Config[string, int]{TTL: ttl})
	if err != nil {
		tb.Fatalf("new keepcache: %v", err)
	}
	gc := gocache.New(ttl, 0)
	tc := ttlcache.New(
		ttlcache.WithTTL[string, int](ttl),
		ttlcache.WithDisableTouchOnHit[string, int](),
	)

	ss := []store{
		{
			name: "keepcache",
			set:  mc.Set,
			get:  func(k string) bool { _, ok := mc.Get(k); return ok },
		},
		{
			name: "go-cache",
			set:  func(k string, v int) { gc.Set(k, v, gocache.DefaultExpiration) },
			get:  func(k string) bool { _, ok := gc.Get(k); return ok },
		},
		{
			name: "ttlcache",
			set:  func(k string, v int) { tc.Set(k, v, ttlcache.DefaultTTL) },
			get:  func(k string) bool { return tc.Get(k) != nil },
		},
	}
	for _, s := range ss {
		for i, k := range keys {
			s.set(k, i)
		}
	}
	return ss
}

func run(b *testing.B, fn func(b *testing.B, s store)) {
	b.Helper()
	for _, s := range stores(b) {
		b.Run(s.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			fn(b, s)
		})
	}
}

func BenchmarkSet(b *testing.B) {
	run(b, func(b *testing.B, s store) {
		for i := 0; i < b.N; i++ {
			s.set(keys[i%keyCount], i)
		}
	})
}

func BenchmarkGet(b *testing.B) {
	run(b, func(b *testing.B, s store) {
		for i := 0; i < b.N; i++ {
			if !s.get(keys[i%keyCount]) {
				b.Fatal("unexpected miss")
			}
		}
	})
}

func BenchmarkGetParallel(b *testing.B) {
	run(b, func(b *testing.B, s store) {
		var next atomic.Uint64
		b.RunParallel(func(pb *testing.PB) {
			i := int(next.Add(1)) * 7919
			for pb.Next() {
				i++
				if !s.get(keys[i%keyCount]) {
					b.Error("unexpected miss")
					return
				}
			}
		})
	})
}

func BenchmarkMixedParallel(b *testing.B) {
	run(b, func(b *testing.B, s store) {
		var next atomic.Uint64
		b.RunParallel(func(pb *testing.PB) {
			i := int(next.Add(1)) * 7919
			for pb.Next() {
				i++
				k := keys[i%keyCount]
				if i%writeEvery == 0 {
					s.set(k, i)
					continue
				}
				s.get(k)
			}
		})
	})
}
