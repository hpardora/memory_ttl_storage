# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Generic in-memory TTL cache (`github.com/hpardora/memory_ttl_storage`, package `memory_ttl_storage`, go 1.22, no runtime dependencies) with per-item TTL, eviction callbacks, `MaxItems` limit and file persistence. API docs: `README.md` and `example_test.go`.

## Commands

```sh
go test -race -count=1 ./...                         # all tests
go test -race -run TestName ./...                    # single test
go test -run '^$' -fuzz FuzzLoad -fuzztime 30s       # fuzz backup loading
cd bench && go test -run '^$' -bench . -benchmem     # benchmarks (separate module)
```

## Architecture

- `cache.go` — `Cache[K, V]`: a map plus an expiry heap behind one `sync.Mutex`. Only exported methods lock; helpers (`evict`, `record`, `makeRoom`) assume the lock is held. `OnEvict` is called after unlock, from evictions collected under the lock (`notify`), so callbacks may re-enter the cache.
- `expiry.go` — min-heap ordered by expiry time; each entry stores its heap index so `heap.Fix`/`heap.Remove` are O(log n).
- `persist.go` — versioned gob+gzip snapshot. `Save` copies live items under the lock, encodes without it, writes a temp file in the same dir, syncs, then renames (atomic; `CreateTemp` already makes it 0600); `saveMu` serializes saves. `New` removes stale temp files, rejects non-regular or group/world-writable backups (`ErrInsecureBackup`) and recovers panics from crafted data. Size caps (`Config.MaxBackupSize`) apply to file and decompressed stream; failures wrap `ErrCorruptBackup`.
- `Run(ctx)` owns all background work (cleanup sweep, periodic backup, final save on cancel). `New` starts no goroutines; it only loads the backup.

## Testing conventions

- Tests replace the unexported `now` field with a fake clock (`c.now = clock.Now`) instead of sleeping.
- `TestMain` uses `goleak.VerifyTestMain`: tests must not leak goroutines.
- `example_test.go` examples are compile-tested and copied into `README.md`; keep them in sync.

## Release

Conventional Commits (`feat:`, `fix:`, `docs:`...) drive release-please: it opens a release PR, and merging it tags and creates the GitHub Release and updates `CHANGELOG.md`. CI pins actions by commit SHA.
