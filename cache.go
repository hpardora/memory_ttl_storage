// Package memory_ttl_storage provides a generic in-memory key/value cache
// whose items expire after a time-to-live.
//
// The cache starts no goroutines. Expired items are removed lazily on access
// and by DeleteExpired.
package memory_ttl_storage

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultTTL             = 10 * time.Second
	defaultCleanupInterval = time.Second
	defaultBackupInterval  = 30 * time.Second
)

// ErrInvalidConfig is wrapped by the error New returns for an invalid Config.
var ErrInvalidConfig = errors.New("invalid config")

// EvictReason says why an item left the cache.
type EvictReason int

// Eviction reasons passed to Config.OnEvict.
const (
	// Expired means the item's TTL elapsed.
	Expired EvictReason = iota
	// Deleted means the item was removed with Delete.
	Deleted
	// Replaced means Set overwrote a live item.
	Replaced
	// Capacity means the item was dropped to respect MaxItems.
	Capacity
)

// String returns the lowercase name of the reason.
func (r EvictReason) String() string {
	switch r {
	case Expired:
		return "expired"
	case Deleted:
		return "deleted"
	case Replaced:
		return "replaced"
	case Capacity:
		return "capacity"
	default:
		return fmt.Sprintf("EvictReason(%d)", int(r))
	}
}

// Config configures a Cache. The zero value is valid: zero fields take the
// documented defaults.
type Config[K comparable, V any] struct {
	// TTL is the default item lifetime. Default 10s.
	TTL time.Duration
	// CleanupInterval is how often Run sweeps expired items. Default 1s.
	// Used by Run.
	CleanupInterval time.Duration
	// MaxItems caps the item count; 0 means unlimited. When storing a new key
	// would exceed it, the items expiring soonest are dropped first (expired
	// ones as Expired, live ones as Capacity). Overwriting a key never evicts.
	// A backup larger than MaxItems is trimmed on load to the items expiring
	// latest; those are dropped without calling OnEvict or counting Stats.
	MaxItems int
	// OnEvict is called after an item leaves the cache, never while the
	// cache lock is held, so it may call back into the cache.
	OnEvict func(key K, value V, reason EvictReason)
	// BackupPath is the file used for persistence. New loads it if present;
	// Save and Run write it. Empty disables persistence. New also deletes stale
	// temp files that crashed saves left in the same directory. A corrupt
	// backup makes New return an error wrapping ErrCorruptBackup; a symlink,
	// FIFO or device, or a file writable by group or others, yields
	// ErrInsecureBackup. Callers that prefer to start empty can remove the file
	// and call New again.
	BackupPath string
	// MaxBackupSize is the largest backup New accepts, in bytes, for both the
	// file and its decompressed contents; a larger one is ErrCorruptBackup.
	// Default 64 MiB. Negative is invalid.
	MaxBackupSize int64
	// BackupInterval is how often Run saves a backup. Default 30s. Used by Run.
	BackupInterval time.Duration
	// Logger receives debug logs about evictions (reason only, never keys or
	// values). Nil discards logs. Whether eviction debug logging happens is
	// decided once, when New is called: raising a handler's level to Debug
	// later (for example through a slog.LevelVar) does not enable it.
	Logger *slog.Logger
}

// Stats holds cache counters. The counters are read independently, not under
// one atomic step, so under concurrent use they may be momentarily
// inconsistent with each other (for example Hits and Misses against
// Evictions).
type Stats struct {
	// Hits counts Get/GetAndRefresh calls that found a live item.
	Hits uint64
	// Misses counts Get/GetAndRefresh calls that found nothing live.
	Misses uint64
	// Evictions counts every removal or replacement, for all reasons.
	Evictions uint64
	// Expirations counts evictions with reason Expired.
	Expirations uint64
	// Items is the number of stored items, including expired ones not yet swept.
	Items int
}

type eviction[K comparable, V any] struct {
	key    K
	value  V
	reason EvictReason
}

// Cache is a concurrency-safe in-memory cache with per-item TTL.
type Cache[K comparable, V any] struct {
	cfg Config[K, V]
	now func() time.Time

	// trackEvictions is true when someone consumes evictions (OnEvict or debug
	// logging); otherwise record skips building the notify list.
	trackEvictions bool

	// hits and misses are atomic so Get's read-locked fast path can count.
	hits   atomic.Uint64
	misses atomic.Uint64

	// saveMu serializes Save so snapshots reach disk in the order taken.
	saveMu sync.Mutex
	// mu guards everything below. Mutators hold the write lock; Get, Len and
	// Stats hold only the read lock.
	mu          sync.RWMutex
	items       map[K]*entry[K, V]
	expiry      expiryHeap[K, V]
	evictions   uint64
	expirations uint64
}

// New returns a Cache. Negative durations, MaxItems or MaxBackupSize yield an
// error wrapping ErrInvalidConfig. When BackupPath is set, New loads it; see
// Config.BackupPath for ErrCorruptBackup and ErrInsecureBackup.
func New[K comparable, V any](cfg Config[K, V]) (*Cache[K, V], error) {
	if cfg.TTL < 0 || cfg.CleanupInterval < 0 || cfg.BackupInterval < 0 || cfg.MaxItems < 0 || cfg.MaxBackupSize < 0 {
		return nil, fmt.Errorf("negative TTL, interval, MaxItems or MaxBackupSize: %w", ErrInvalidConfig)
	}
	if cfg.TTL == 0 {
		cfg.TTL = defaultTTL
	}
	if cfg.CleanupInterval == 0 {
		cfg.CleanupInterval = defaultCleanupInterval
	}
	if cfg.BackupInterval == 0 {
		cfg.BackupInterval = defaultBackupInterval
	}
	if cfg.MaxBackupSize == 0 {
		cfg.MaxBackupSize = defaultMaxBackupSize
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	c := &Cache[K, V]{
		cfg:            cfg,
		now:            time.Now,
		trackEvictions: cfg.OnEvict != nil || cfg.Logger.Enabled(context.Background(), slog.LevelDebug),
		items:          make(map[K]*entry[K, V]),
	}
	if cfg.BackupPath != "" {
		if err := c.load(); err != nil {
			return nil, fmt.Errorf("load backup %q: %w", cfg.BackupPath, err)
		}
	}
	return c, nil
}

// Run sweeps expired items every CleanupInterval and, when BackupPath is set,
// saves a backup every BackupInterval. It blocks until ctx is done, then
// performs a final Save with its own bounded context and returns that error.
// Periodic Save errors are logged and do not stop Run. Run starts no
// goroutines; running it twice concurrently on one Cache is unsupported.
func (c *Cache[K, V]) Run(ctx context.Context) error {
	cleanup := time.NewTicker(c.cfg.CleanupInterval)
	defer cleanup.Stop()
	var backup <-chan time.Time
	if c.cfg.BackupPath != "" {
		t := time.NewTicker(c.cfg.BackupInterval)
		defer t.Stop()
		backup = t.C
	}
	for {
		select {
		case <-ctx.Done():
			return c.saveOnShutdown()
		case <-cleanup.C:
			c.DeleteExpired()
		case <-backup:
			if err := c.Save(ctx); err != nil {
				c.cfg.Logger.Warn("periodic backup failed", "path", c.cfg.BackupPath, "error", err)
			}
		}
	}
}

// saveOnShutdown uses a fresh context because the caller's is already done.
func (c *Cache[K, V]) saveOnShutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), finalSaveTimeout)
	defer cancel()
	if err := c.Save(ctx); err != nil {
		return fmt.Errorf("final backup: %w", err)
	}
	return nil
}

// Set stores value under key with the default TTL.
func (c *Cache[K, V]) Set(key K, value V) {
	c.SetWithTTL(key, value, c.cfg.TTL)
}

// SetWithTTL stores value under key with the given TTL; ttl <= 0 means the
// default TTL. An existing item is replaced and reported to OnEvict as
// Replaced, or Expired if it had already expired.
func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.cfg.TTL
	}
	c.mu.Lock()
	now := c.now()
	var evs []eviction[K, V]
	if e, ok := c.items[key]; ok {
		reason := Replaced
		if e.expired(now) {
			reason = Expired
		}
		evs = c.record(evs, e, reason)
		e.value, e.ttl, e.expiresAt = value, ttl, now.Add(ttl)
		heap.Fix(&c.expiry, e.index)
	} else {
		evs = c.makeRoom(evs, now)
		e := &entry[K, V]{key: key, value: value, ttl: ttl, expiresAt: now.Add(ttl)}
		c.items[key] = e
		heap.Push(&c.expiry, e)
	}
	c.mu.Unlock()
	c.notify(evs)
}

// Get returns the value for key. An expired item is removed, reported to
// OnEvict as Expired, and treated as missing. Get does not extend the TTL.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.RLock()
	e, ok := c.items[key]
	if !ok {
		c.mu.RUnlock()
		c.misses.Add(1)
		var zero V
		return zero, false
	}
	if !e.expired(c.now()) {
		value := e.value
		c.mu.RUnlock()
		c.hits.Add(1)
		return value, true
	}
	c.mu.RUnlock()
	// Expired: removal needs the write lock, which re-looks-up the key because
	// it may have been replaced or deleted since the read lock was released.
	return c.get(key, false)
}

// GetAndRefresh is like Get but, on a hit, atomically restarts the item's
// own TTL.
func (c *Cache[K, V]) GetAndRefresh(key K) (V, bool) {
	return c.get(key, true)
}

// get is the exclusive path shared by GetAndRefresh and Get's expired fallback.
// It counts exactly one hit or miss.
func (c *Cache[K, V]) get(key K, refresh bool) (V, bool) {
	var zero V
	c.mu.Lock()
	e, ok := c.items[key]
	if !ok {
		c.misses.Add(1)
		c.mu.Unlock()
		return zero, false
	}
	now := c.now()
	if e.expired(now) {
		evs := c.evict(nil, e, Expired)
		c.misses.Add(1)
		c.mu.Unlock()
		c.notify(evs)
		return zero, false
	}
	if refresh {
		e.expiresAt = now.Add(e.ttl)
		heap.Fix(&c.expiry, e.index)
	}
	c.hits.Add(1)
	value := e.value
	c.mu.Unlock()
	return value, true
}

// Delete removes key and reports it to OnEvict as Deleted if it was present.
func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	var evs []eviction[K, V]
	if e, ok := c.items[key]; ok {
		evs = c.evict(evs, e, Deleted)
	}
	c.mu.Unlock()
	c.notify(evs)
}

// DeleteExpired removes every expired item and reports each to OnEvict as
// Expired.
func (c *Cache[K, V]) DeleteExpired() {
	c.mu.Lock()
	now := c.now()
	var evs []eviction[K, V]
	for len(c.expiry) > 0 && c.expiry[0].expired(now) {
		evs = c.evict(evs, c.expiry[0], Expired)
	}
	c.mu.Unlock()
	c.notify(evs)
}

// Len returns the number of stored items. It may include expired items that
// have not been swept yet.
func (c *Cache[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Stats returns the cache counters. Hits and Misses are atomics updated
// outside the lock while the other fields are read under it, so the result is
// not a point-in-time snapshot and may be momentarily inconsistent under
// concurrent use.
func (c *Cache[K, V]) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Stats{
		Hits:        c.hits.Load(),
		Misses:      c.misses.Load(),
		Evictions:   c.evictions,
		Expirations: c.expirations,
		Items:       len(c.items),
	}
}

// makeRoom evicts from the top of the expiry heap until one more item fits
// under MaxItems. The soonest-expiring item goes first, so expired items are
// reclaimed before live ones. Write lock must be held.
func (c *Cache[K, V]) makeRoom(evs []eviction[K, V], now time.Time) []eviction[K, V] {
	if c.cfg.MaxItems == 0 {
		return evs
	}
	for len(c.items) >= c.cfg.MaxItems {
		oldest := c.expiry[0]
		reason := Capacity
		if oldest.expired(now) {
			reason = Expired
		}
		evs = c.evict(evs, oldest, reason)
	}
	return evs
}

// evict removes e from the cache and records the eviction. Write lock must be held.
func (c *Cache[K, V]) evict(evs []eviction[K, V], e *entry[K, V], reason EvictReason) []eviction[K, V] {
	delete(c.items, e.key)
	heap.Remove(&c.expiry, e.index)
	return c.record(evs, e, reason)
}

// record counts an eviction and queues it for notify, without removing anything.
// Write lock must be held.
func (c *Cache[K, V]) record(evs []eviction[K, V], e *entry[K, V], reason EvictReason) []eviction[K, V] {
	c.evictions++
	if reason == Expired {
		c.expirations++
	}
	if !c.trackEvictions {
		return evs
	}
	return append(evs, eviction[K, V]{key: e.key, value: e.value, reason: reason})
}

// notify logs each eviction (reason only: keys and values may be sensitive) and
// calls OnEvict. Lock must NOT be held.
func (c *Cache[K, V]) notify(evs []eviction[K, V]) {
	for _, ev := range evs {
		c.cfg.Logger.Debug("cache eviction", "reason", ev.reason.String())
		if c.cfg.OnEvict == nil {
			continue
		}
		c.cfg.OnEvict(ev.key, ev.value, ev.reason)
	}
}
