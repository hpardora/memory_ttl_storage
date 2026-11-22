# memory_ttl_storage

Generic in-memory TTL cache for Go with per-item TTL, eviction callbacks, a size limit and crash-safe file persistence. Zero dependencies.

[![CI](https://github.com/hpardora/memory_ttl_storage/actions/workflows/ci.yml/badge.svg)](https://github.com/hpardora/memory_ttl_storage/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/hpardora/memory_ttl_storage.svg)](https://pkg.go.dev/github.com/hpardora/memory_ttl_storage)
[![Go Report Card](https://goreportcard.com/badge/github.com/hpardora/memory_ttl_storage)](https://goreportcard.com/report/github.com/hpardora/memory_ttl_storage)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)

## Install

```sh
go get github.com/hpardora/memory_ttl_storage@latest
```

## Quick start

```go
cache, err := mts.New(mts.Config[string, int]{})
if err != nil {
	log.Fatal(err)
}

cache.Set("answer", 42)

value, ok := cache.Get("answer")
fmt.Println(value, ok) // 42 true

_, ok = cache.Get("missing")
fmt.Println(ok) // false
```

(`mts` is the import alias for `github.com/hpardora/memory_ttl_storage`.)

### Lifecycle and persistence

- `New` starts no goroutines. `Get` never returns expired items; an expired item is removed on access.
- `go c.Run(ctx)` adds the background work: the cleanup sweep and, when `BackupPath` is set, the periodic backup.
- Cancelling `ctx` makes `Run` write a final backup and return.
- `New` loads `BackupPath` if the file exists.

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

// New restores /var/lib/myapp/cache.bak if it exists.
cache, err := mts.New(mts.Config[string, string]{
	TTL:            time.Hour,
	BackupPath:     "/var/lib/myapp/cache.bak",
	BackupInterval: 30 * time.Second,
})
if err != nil {
	log.Fatal(err) // errors.Is(err, mts.ErrCorruptBackup) for an unreadable backup
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

Other methods: `SetWithTTL`, `GetAndRefresh` (a hit restarts the item's TTL), `Delete`, `DeleteExpired`, `Len`, `Stats`, `Save`.

## Comparison

| | memory_ttl_storage | patrickmn/go-cache | jellydator/ttlcache/v3 |
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

go1.26.2, linux/amd64, 12th Gen Intel i7-1255U, `-benchtime 1s -count 3`, mean ns/op.

| Benchmark | memory_ttl_storage | go-cache | ttlcache v3 |
|---|---|---|---|
| Set | 243 ns, 32 B, 2 allocs | 90 ns, 7 B, 0 allocs | 310 ns, 0 B, 0 allocs |
| Get | 52 ns | 52 ns | 94 ns |
| Get (parallel) | 151 ns | 73 ns | 208 ns |
| 90% get / 10% set (parallel) | 155 ns | 119 ns | 199 ns |

go-cache is faster on Set and parallel reads because it keeps no expiry index; memory_ttl_storage pays for an expiry heap that makes cleanup O(expired·log n) instead of a full scan under the lock.

Reproduce: `cd bench && go test -run '^$' -bench . -benchmem`

## Migration from v0

- `New(cfg)` now returns `(*Cache[K, V], error)` and is generic over key and value types instead of `interface{}`.
- `RegisterInterface` is removed.
- `Stop()` is replaced by cancelling the context passed to `Run`.
- TTL fields are `time.Duration`.

## Contributing

Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, ...): releases and `CHANGELOG.md` are generated by release-please. Run `go test -race ./...` before opening a PR.

## License

GNU General Public License v3.0. See [LICENSE](LICENSE).
