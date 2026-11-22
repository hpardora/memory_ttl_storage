# Changelog

## 1.0.0 (2026-11-22)


### ⚠ BREAKING CHANGES

* the interface{} API is replaced. New takes a Config and returns (*Cache, error); RegisterInterface is removed; Stop is replaced by cancelling the context passed to Run; durations use time.Duration.

### Features

* generic v1 cache with safe persistence and explicit lifecycle ([f35e5dc](https://github.com/hpardora/memory_ttl_storage/commit/f35e5dc57ec6aa2d499d9369c39d3a5fd94e42bf))

## Changelog
