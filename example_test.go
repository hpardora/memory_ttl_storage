package memory_ttl_storage_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	mts "github.com/hpardora/memory_ttl_storage"
)

func Example() {
	cache, err := mts.New(mts.Config[string, int]{})
	if err != nil {
		log.Fatal(err)
	}

	cache.Set("answer", 42)

	value, ok := cache.Get("answer")
	fmt.Println(value, ok)

	_, ok = cache.Get("missing")
	fmt.Println(ok)
	// Output:
	// 42 true
	// false
}

func ExampleNew_custom() {
	cache, err := mts.New(mts.Config[string, string]{
		TTL:      time.Hour,
		MaxItems: 2,
		OnEvict: func(key, _ string, reason mts.EvictReason) {
			fmt.Println("evicted", key, reason)
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	cache.SetWithTTL("a", "1", time.Hour)
	cache.SetWithTTL("b", "2", 2*time.Hour)
	// The cache is full: the item expiring soonest ("a") is dropped.
	cache.SetWithTTL("c", "3", 3*time.Hour)
	cache.Set("b", "updated")
	cache.Delete("c")
	fmt.Println(cache.Len())
	// Output:
	// evicted a capacity
	// evicted b replaced
	// evicted c deleted
	// 1
}

func ExampleCache_GetAndRefresh() {
	cache, err := mts.New(mts.Config[string, string]{TTL: time.Minute})
	if err != nil {
		log.Fatal(err)
	}

	cache.Set("session", "token")

	// A hit restarts the item's TTL; Get would leave it untouched.
	value, ok := cache.GetAndRefresh("session")
	fmt.Println(value, ok)
	// Output:
	// token true
}

func ExampleCache_GetOrLoad() {
	cache, err := mts.New(mts.Config[string, string]{})
	if err != nil {
		log.Fatal(err)
	}

	load := func(_ context.Context, key string) (string, error) {
		fmt.Println("loading", key)
		return "value of " + key, nil
	}
	// Concurrent calls for the same key share one load; here the second call
	// is served from the cache.
	for range 2 {
		value, err := cache.GetOrLoad(context.Background(), "k", load)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(value)
	}
	// Output:
	// loading k
	// value of k
	// value of k
}

func ExampleCache_GetWithExpiration() {
	cache, err := mts.New(mts.Config[string, int]{TTL: time.Hour})
	if err != nil {
		log.Fatal(err)
	}

	cache.Set("a", 1)

	value, expiresAt, ok := cache.GetWithExpiration("a")
	fmt.Println(value, ok, time.Until(expiresAt).Round(time.Hour))
	// Output:
	// 1 true 1h0m0s
}

func ExampleCache_Stats() {
	cache, err := mts.New(mts.Config[string, int]{})
	if err != nil {
		log.Fatal(err)
	}

	cache.Set("a", 1)
	cache.Get("a")
	cache.Get("b")
	cache.Delete("a")

	s := cache.Stats()
	fmt.Printf("hits=%d misses=%d evictions=%d items=%d\n", s.Hits, s.Misses, s.Evictions, s.Items)
	// Output:
	// hits=1 misses=1 evictions=1 items=0
}

func ExampleCache_Run() {
	dir, err := os.MkdirTemp("", "mts-example-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cfg := mts.Config[string, string]{
		TTL:        time.Hour,
		BackupPath: filepath.Join(dir, "cache.bak"),
	}
	cache, err := mts.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	cache.Set("greeting", "hello")

	// Run blocks until the context is done, then saves a final backup.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cache.Run(ctx); err != nil {
		fmt.Println(err)
		return
	}

	// A new cache with the same BackupPath restores the item.
	restored, err := mts.New(cfg)
	if err != nil {
		fmt.Println(err)
		return
	}
	value, ok := restored.Get("greeting")
	fmt.Println(value, ok)
	// Output:
	// hello true
}
