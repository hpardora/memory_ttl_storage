# Changelog

## [1.0.1](https://github.com/hpardora/memory_ttl_storage/compare/v1.0.0...v1.0.1) (2026-11-22)


### Performance Improvements

* read lock on the Get fast path and no unused eviction records ([bf536d3](https://github.com/hpardora/memory_ttl_storage/commit/bf536d33346769b967ab9920f83f3fb20aa916b3))

## 1.0.0 (2026-11-22)


### ⚠ BREAKING CHANGES

* the interface{} API is replaced. New takes a Config and returns (*Cache, error); RegisterInterface is removed; Stop is replaced by cancelling the context passed to Run; durations use time.Duration.

### Features

* generic v1 cache with safe persistence and explicit lifecycle ([f35e5dc](https://github.com/hpardora/memory_ttl_storage/commit/f35e5dc57ec6aa2d499d9369c39d3a5fd94e42bf))
