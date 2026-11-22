package memory_ttl_storage

import (
	"compress/gzip"
	"container/heap"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const (
	snapshotVersion = 1
	// defaultMaxBackupSize is the Config.MaxBackupSize default.
	defaultMaxBackupSize = 64 << 20 // 64 MiB
	// finalSaveTimeout bounds the last Save Run performs after its context ends.
	finalSaveTimeout = 10 * time.Second

	backupDirMode = 0o700
	// tempFilePattern names the temp files of in-progress saves; it is both the
	// os.CreateTemp pattern and the filter for stale-file cleanup.
	tempFilePattern = ".mts-*"
	// groupWorldWrite is the permission bits that let others replace the backup.
	groupWorldWrite = 0o022
)

// ErrCorruptBackup is wrapped by the error New returns when the backup file
// cannot be decoded or has an unsupported format.
var ErrCorruptBackup = errors.New("corrupt backup")

// ErrInsecureBackup is wrapped by the error New returns when the backup path
// is not a regular file (symlink, FIFO, device) or is writable by group or
// others. Not checked on Windows, which has no such permission bits.
var ErrInsecureBackup = errors.New("insecure backup")

// snapshot is the versioned on-disk form of the cache.
type snapshot[K comparable, V any] struct {
	Version int
	Items   []snapshotItem[K, V]
}

type snapshotItem[K comparable, V any] struct {
	Key       K
	Value     V
	ExpiresAt time.Time
	TTL       time.Duration
}

// Save writes the live items to Config.BackupPath atomically. It is a no-op
// returning nil when BackupPath is empty.
func (c *Cache[K, V]) Save(ctx context.Context) error {
	if c.cfg.BackupPath == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Serializing saves makes the latest snapshot the last one renamed into place.
	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	return writeSnapshot(c.cfg.BackupPath, c.snapshot())
}

// snapshot copies the live items under the lock so encoding runs without it.
func (c *Cache[K, V]) snapshot() snapshot[K, V] {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	snap := snapshot[K, V]{Version: snapshotVersion, Items: make([]snapshotItem[K, V], 0, len(c.items))}
	for _, e := range c.items {
		if e.expired(now) {
			continue
		}
		snap.Items = append(snap.Items, snapshotItem[K, V]{Key: e.key, Value: e.value, ExpiresAt: e.expiresAt, TTL: e.ttl})
	}
	return snap
}

// writeSnapshot writes snap to path through a temp file in the same directory
// and renames it into place, so readers never see a partial file.
func writeSnapshot[K comparable, V any](path string, snap snapshot[K, V]) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, backupDirMode); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	f, err := os.CreateTemp(dir, tempFilePattern)
	if err != nil {
		return fmt.Errorf("create temp backup: %w", err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			// Best effort: the write error is what the caller needs.
			_ = os.Remove(tmp)
		}
	}()
	if err = writeAndClose(f, snap); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename backup: %w", err)
	}
	return nil
}

// writeAndClose encodes snap into f, syncs it and always closes it.
func writeAndClose[K comparable, V any](f *os.File, snap snapshot[K, V]) error {
	err := encodeSnapshot(f, snap)
	if err == nil {
		if syncErr := f.Sync(); syncErr != nil {
			err = fmt.Errorf("sync backup: %w", syncErr)
		}
	}
	if closeErr := f.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close backup: %w", closeErr))
	}
	return err
}

func encodeSnapshot[K comparable, V any](w io.Writer, snap snapshot[K, V]) error {
	zw := gzip.NewWriter(w)
	if err := gob.NewEncoder(zw).Encode(snap); err != nil {
		return fmt.Errorf("encode backup: %w", err)
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("compress backup: %w", err)
	}
	return nil
}

// decodeSnapshot reads a gzip+gob snapshot of at most limit decompressed
// bytes. Every failure wraps ErrCorruptBackup.
func decodeSnapshot[K comparable, V any](r io.Reader, limit int64) (snapshot[K, V], error) {
	var snap snapshot[K, V]
	zr, err := gzip.NewReader(r)
	if err != nil {
		return snap, fmt.Errorf("open gzip: %w: %w", ErrCorruptBackup, err)
	}
	// One byte past the limit lets us tell "exactly limit" from "too big".
	limited := &io.LimitedReader{R: zr, N: limit + 1}
	if err := gob.NewDecoder(limited).Decode(&snap); err != nil {
		return snapshot[K, V]{}, fmt.Errorf("decode: %w: %w", ErrCorruptBackup, err)
	}
	// Reading to the end makes gzip verify its checksum.
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return snapshot[K, V]{}, fmt.Errorf("checksum: %w: %w", ErrCorruptBackup, err)
	}
	if limited.N <= 0 {
		return snapshot[K, V]{}, fmt.Errorf("decompressed size over %d bytes: %w", limit, ErrCorruptBackup)
	}
	if snap.Version != snapshotVersion {
		return snapshot[K, V]{}, fmt.Errorf("version %d: %w", snap.Version, ErrCorruptBackup)
	}
	return snap, nil
}

// load restores the backup file into an empty cache. A missing file is not an
// error. Expired items are dropped.
func (c *Cache[K, V]) load() error {
	if err := removeStaleTempFiles(filepath.Dir(c.cfg.BackupPath)); err != nil {
		return err
	}
	f, info, err := openBackup(c.cfg.BackupPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Read-only file: a Close error cannot lose data, so it is ignored.
	defer func() { _ = f.Close() }()

	if info.Size() > c.cfg.MaxBackupSize {
		return fmt.Errorf("backup is %d bytes, limit %d: %w", info.Size(), c.cfg.MaxBackupSize, ErrCorruptBackup)
	}
	return c.decodeAndRestore(f)
}

// decodeAndRestore decodes r and fills the cache from it.
func (c *Cache[K, V]) decodeAndRestore(r io.Reader) (err error) {
	// The data is untrusted: a crafted snapshot can hold values that panic in
	// map or heap operations (e.g. unhashable dynamic types behind an interface
	// key). A panic here must not escape New.
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("restore: %w: panic: %v", ErrCorruptBackup, p)
		}
	}()
	snap, err := decodeSnapshot[K, V](r, c.cfg.MaxBackupSize)
	if err != nil {
		return err
	}
	c.restore(snap)
	return nil
}

// openBackup opens path read-only after checking that it is a regular file
// not writable by group or others. The file is compared again after opening to
// close the window in which the path could be swapped. A missing file yields
// an error matching fs.ErrNotExist.
func openBackup(path string) (*os.File, os.FileInfo, error) {
	linfo, err := os.Lstat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat backup: %w", err)
	}
	if !linfo.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("not a regular file (mode %v): %w", linfo.Mode().Type(), ErrInsecureBackup)
	}
	// syscall.O_NONBLOCK does not exist on every platform (plan9), so a FIFO
	// swapped in after the Lstat is caught by the SameFile check below instead.
	f, err := os.Open(path) //nolint:gosec // G304: path is the developer-configured BackupPath, not external input.
	if err != nil {
		return nil, nil, fmt.Errorf("open backup: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, fmt.Errorf("stat backup: %w", err)
	}
	if !os.SameFile(linfo, info) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("file changed while opening: %w", ErrInsecureBackup)
	}
	// Windows reports 0666 for every writable file, so the check cannot apply.
	if runtime.GOOS != "windows" && info.Mode().Perm()&groupWorldWrite != 0 {
		_ = f.Close()
		return nil, nil, fmt.Errorf("mode %v is writable by group or others: %w", info.Mode().Perm(), ErrInsecureBackup)
	}
	return f, info, nil
}

// removeStaleTempFiles deletes temp files that crashed saves left in dir; they
// may hold cache contents. Only New calls it, so it assumes no other process
// is saving into dir at the same time.
func removeStaleTempFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("list backup dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		// The pattern is a constant, so Match cannot return ErrBadPattern.
		if ok, _ := filepath.Match(tempFilePattern, entry.Name()); !ok {
			continue
		}
		err := os.Remove(filepath.Join(dir, entry.Name()))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stale temp backup: %w", err)
		}
	}
	return nil
}

// restore fills the maps from snap, then trims to MaxItems by dropping the
// soonest-expiring items silently. It runs only from New, before the cache is shared.
func (c *Cache[K, V]) restore(snap snapshot[K, V]) {
	now := c.now()
	for _, it := range snap.Items {
		e := &entry[K, V]{key: it.Key, value: it.Value, ttl: it.TTL, expiresAt: it.ExpiresAt}
		// A key not equal to itself (NaN) could never be looked up or deleted.
		if e.key != e.key { //nolint:gocritic,staticcheck // self-comparison is the NaN test
			continue
		}
		if e.expired(now) {
			continue
		}
		if _, dup := c.items[e.key]; dup {
			continue
		}
		if e.ttl <= 0 {
			e.ttl = c.cfg.TTL
		}
		c.items[e.key] = e
		heap.Push(&c.expiry, e)
	}
	for c.cfg.MaxItems > 0 && len(c.items) > c.cfg.MaxItems && len(c.expiry) > 0 {
		delete(c.items, heap.Pop(&c.expiry).(*entry[K, V]).key)
	}
}
