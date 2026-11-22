# keepcache

Generic in-memory TTL cache for Go with per-item TTL, eviction callbacks, a size limit and crash-safe file persistence. Zero dependencies.

[![CI](https://github.com/hpardora/keepcache/actions/workflows/ci.yml/badge.svg)](https://github.com/hpardora/keepcache/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/hpardora/keepcache.svg)](https://pkg.go.dev/github.com/hpardora/keepcache)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

> **Migrating from memory_ttl_storage:** change the import path to `github.com/hpardora/keepcache` and the package qualifier to `keepcache`. The API is unchanged from memory_ttl_storage v1.1.0. The minimum Go version is now 1.24.

## Install

```sh
go get github.com/hpardora/keepcache@latest
```

## Quick start

```go
cache, err := keepcache.New(keepcache.Config[string, int]{})
if err != nil {
	log.Fatal(err)
}

cache.Set("answer", 42)

value, ok := cache.Get("answer")
fmt.Println(value, ok) // 42 true

_, ok = cache.Get("missing")
fmt.Println(ok) // false
```

### Lifecycle and persistence

- `New` starts no goroutines. `Get` never returns expired items; an expired item is removed on access.
- `go c.Run(ctx)` adds the background work: the cleanup sweep and, when `BackupPath` is set, the periodic backup.
- Cancelling `ctx` makes `Run` write a final backup and return.
- `New` loads `BackupPath` if the file exists.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

// New restores /var/lib/myapp/cache.bak if it exists.
cache, err := keepcache.New(keepcache.Config[string, string]{
	TTL:            time.Hour,
	BackupPath:     "/var/lib/myapp/cache.bak",
	BackupInterval: 30 * time.Second,
})
if err != nil {
	log.Fatal(err) // errors.Is(err, keepcache.ErrCorruptBackup) for an unreadable backup
}

// Run sweeps expired items and saves periodically until ctx is cancelled,
// then writes a final backup and returns.
done := make(chan error, 1)
go func() { done <- cache.Run(ctx) }()

cache.Set("greeting", "hello")
// ... serve traffic ...

<-ctx.Done()
if err := <-done; err != nil {
	log.Printf("final backup: %v", err)
}
```

`ExampleCache_Run` in `example_test.go` shows a runnable save-and-restore round trip.

## Config

The zero value is valid.

| Field | Default | Meaning |
|---|---|---|
| `TTL` | 10s | Default item lifetime. |
| `CleanupInterval` | 1s | How often `Run` sweeps expired items. |
| `MaxItems` | 0 (unlimited) | Item cap. Storing a new key beyond it drops the items expiring soonest (`Expired` if already expired, else `Capacity`). Overwriting a key never evicts. A larger backup is trimmed on load without calling `OnEvict`. |
| `OnEvict` | nil | `func(key, value, reason)` called after an item leaves the cache, never under the lock, so it may call back into the cache. Reasons: `Expired`, `Deleted`, `Replaced`, `Capacity`. |
| `BackupPath` | empty (off) | Backup file. `New` loads it (and deletes stale temp files of crashed saves in its directory); `Save` and `Run` write it. |
| `MaxBackupSize` | 64 MiB | Largest backup `New` accepts, in bytes, for both the file and its decompressed contents. |
| `BackupInterval` | 30s | How often `Run` saves a backup. |
| `Logger` | nil (discard) | `*slog.Logger` for debug logs about evictions (reason only, never keys or values). |

Negative durations, `MaxItems` or `MaxBackupSize` make `New` return an error wrapping `ErrInvalidConfig`. An undecodable or oversized backup yields `ErrCorruptBackup`; a backup that is not a regular file (symlink, FIFO, device) or is writable by group or others yields `ErrInsecureBackup`. To start empty instead, remove the file and call `New` again. `OnEvict` calls from different goroutines are not ordered relative to each other.

### Loading on a miss

`GetOrLoad` returns the live value or calls your loader, stores the result with the default `TTL` and returns it. Concurrent calls for the same key share one load (single-flight). Loader errors go to every waiter and are never cached; a waiter whose own `ctx` is done returns `ctx.Err()` without stopping the load. The loader runs with the first caller's `ctx`. A nil loader returns `ErrNilLoader`; a panicking loader gives the waiters an error wrapping `ErrLoadPanicked` and re-panics in the first caller. The loaded value is stored when the load finishes, so it overwrites a `Set` made during the load and re-adds a key deleted during the load (last writer wins). If the first caller's `ctx` is cancelled, every waiter for that key gets that error. Each call counts one hit or one miss in `Stats`.

```go
value, err := cache.GetOrLoad(ctx, "user:42", func(ctx context.Context, key string) (User, error) {
	return fetchUser(ctx, key)
})
```

`GetWithExpiration` is `Get` that also returns the expiry time:

```go
value, expiresAt, ok := cache.GetWithExpiration("user:42")
```

Other methods: `SetWithTTL`, `GetAndRefresh` (a hit restarts the item's TTL), `Delete`, `DeleteExpired`, `Len`, `Stats`, `Save`.

## Comparison

| | keepcache | patrickmn/go-cache | jellydator/ttlcache/v3 |
|---|---|---|---|
| Generics | yes | no | yes |
| Per-item TTL | yes | yes | yes |
| Eviction callback with reason | yes | no (callback without reason) | yes |
| Size limit | yes | no | yes |
| File persistence | yes | yes (`Save`/`Load`, not atomic) | no |
| Atomic backup writes | yes (temp file + rename) | no | n/a |
| Explicit goroutine lifecycle (no finalizer) | yes (`Run(ctx)`) | no (janitor stopped by finalizer) | yes (`Start`/`Stop`) |
| Maintained | yes | no (last release 2017) | yes |

## Benchmarks

go1.26.2, linux/amd64, 12th Gen Intel i7-1255U, GOMAXPROCS 12, `-count 10`, median per benchstat.

| Benchmark | keepcache | go-cache | ttlcache v3 |
|---|---|---|---|
| Set (existing key) | 190 ns | 72 ns, 7 B/op | 268 ns |
| Set (parallel) | 243 ns | 240 ns, 8 B/op | 313 ns |
| Get | 52 ns | 49 ns | 82 ns |
| Get (parallel) | 78 ns | 71 ns | 211 ns |
| 90% get / 10% set (parallel) | 83 ns | 85 ns | 214 ns |

All keepcache operations allocate 0 B/op; ttlcache also allocates 0 B/op; go-cache allocates 7–8 B/op on Set.

Reads take a shared read lock, so parallel reads scale like go-cache, and parallel writes are on par with it. A single goroutine's Set is slower than go-cache because each write updates the expiry heap (O(log n)), which lets cleanup touch only expired items instead of scanning the whole map under the lock.

Reproduce: `cd bench && go test -run '^$' -bench . -benchmem -count 10`

## Migration from v0

- `New(cfg)` now returns `(*Cache[K, V], error)` and is generic over key and value types instead of `interface{}`.
- `RegisterInterface` is removed.
- `Stop()` is replaced by cancelling the context passed to `Run`.
- TTL fields are `time.Duration`.

## Contributing

Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, ...): releases and `CHANGELOG.md` are generated by release-please. Run `go test -race ./...` before opening a PR.

## License

GNU General Public License v3.0. See [LICENSE](LICENSE).
