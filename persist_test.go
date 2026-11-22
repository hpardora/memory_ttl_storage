package memory_ttl_storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

const pollDeadline = 5 * time.Second

func backupPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "sub", "cache.bak")
}

func gzipBytes(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func validSnapshotBytes(t testing.TB) []byte {
	t.Helper()
	var buf bytes.Buffer
	snap := snapshot[string, int]{Version: snapshotVersion, Items: []snapshotItem[string, int]{
		{Key: "a", Value: 1, ExpiresAt: time.Now().Add(time.Hour), TTL: time.Hour},
	}}
	if err := encodeSnapshot(&buf, snap); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := backupPath(t)
	src, err := New(Config[string, int]{BackupPath: path})
	if err != nil {
		t.Fatal(err)
	}
	src.SetWithTTL("short", 1, time.Hour)
	src.SetWithTTL("long", 2, 48*time.Hour)
	if err := src.Save(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := New(Config[string, int]{BackupPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != 2 {
		t.Fatalf("Len = %d, want 2", got.Len())
	}
	for key, want := range map[string]int{"short": 1, "long": 2} {
		v, ok := got.Get(key)
		if !ok || v != want {
			t.Fatalf("Get(%q) = %d,%v want %d", key, v, ok, want)
		}
		if gotExp, wantExp := got.items[key].expiresAt, src.items[key].expiresAt; !gotExp.Equal(wantExp) {
			t.Fatalf("%q expiresAt = %v, want %v", key, gotExp, wantExp)
		}
		if gotTTL, wantTTL := got.items[key].ttl, src.items[key].ttl; gotTTL != wantTTL {
			t.Fatalf("%q ttl = %v, want %v", key, gotTTL, wantTTL)
		}
	}
	if got.expiry.Len() != 2 {
		t.Fatalf("heap len = %d, want 2", got.expiry.Len())
	}
}

func TestLoadDropsExpired(t *testing.T) {
	path := backupPath(t)
	src, clock, _ := newTestCache(t, Config[string, int]{BackupPath: path})
	clock.t = time.Now().Add(-time.Hour)
	src.SetWithTTL("old", 1, 30*time.Minute)
	src.SetWithTTL("live", 2, 2*time.Hour)
	if err := src.Save(context.Background()); err != nil {
		t.Fatal(err)
	}

	got, err := New(Config[string, int]{BackupPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != 1 {
		t.Fatalf("Len = %d, want 1", got.Len())
	}
	if _, ok := got.Get("live"); !ok {
		t.Fatal("live item missing")
	}
}

func TestLoadMissingFile(t *testing.T) {
	c, err := New(Config[string, int]{BackupPath: backupPath(t)})
	if err != nil {
		t.Fatal(err)
	}
	if c.Len() != 0 {
		t.Fatalf("Len = %d", c.Len())
	}
}

func TestLoadCorrupt(t *testing.T) {
	valid := validSnapshotBytes(t)
	var wrongVersion bytes.Buffer
	if err := encodeSnapshot(&wrongVersion, snapshot[string, int]{Version: snapshotVersion + 1}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		data []byte
	}{
		{"random bytes", []byte("this is definitely not a backup file")},
		{"empty file", nil},
		{"truncated gzip", valid[:len(valid)/2]},
		{"gzip of garbage", gzipBytes(t, []byte("not gob at all"))},
		{"wrong version", wrongVersion.Bytes()},
		{"flipped checksum", func() []byte {
			b := bytes.Clone(valid)
			b[len(b)-5] ^= 0xff
			return b
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := backupPath(t)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tt.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := New(Config[string, int]{BackupPath: path}); !errors.Is(err, ErrCorruptBackup) {
				t.Fatalf("err = %v, want ErrCorruptBackup", err)
			}
		})
	}
	t.Run("file over size cap", func(t *testing.T) {
		path := backupPath(t)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		const limit = 1024
		if err := os.WriteFile(path, make([]byte, limit+1), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(Config[string, int]{BackupPath: path, MaxBackupSize: limit}); !errors.Is(err, ErrCorruptBackup) {
			t.Fatalf("err = %v, want ErrCorruptBackup", err)
		}
	})
}

func TestSaveFilePermsAndNoTempFiles(t *testing.T) {
	path := backupPath(t)
	c, _, _ := newTestCache(t, Config[string, int]{BackupPath: path})
	c.Set("a", 1)
	if err := c.Save(context.Background()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	if n := dirEntries(t, filepath.Dir(path)); n != 1 {
		t.Fatalf("dir has %d entries, want 1", n)
	}
}

func TestSaveNoBackupPath(t *testing.T) {
	c, _, _ := newTestCache(t, Config[string, int]{})
	if err := c.Save(context.Background()); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestSaveCancelledContext(t *testing.T) {
	path := backupPath(t)
	c, _, _ := newTestCache(t, Config[string, int]{BackupPath: path})
	c.Set("a", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Save(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err == nil && dirEntries(t, filepath.Dir(path)) != 0 {
		t.Fatal("files left behind")
	}
}

func TestSaveFailureRemovesTempFile(t *testing.T) {
	path := backupPath(t)
	c, _, _ := newTestCache(t, Config[string, int]{BackupPath: path})
	c.Set("a", 1)
	// A directory at the target makes the final rename fail.
	if err := os.MkdirAll(filepath.Join(path, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if n := dirEntries(t, filepath.Dir(path)); n != 1 {
		t.Fatalf("dir has %d entries, want only the blocking dir", n)
	}
}

func TestRunFinalSaveOnCancel(t *testing.T) {
	path := backupPath(t)
	c, err := New(Config[string, int]{BackupPath: path, BackupInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	c.Set("a", 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(pollDeadline):
		t.Fatal("Run did not return")
	}
	got, err := New(Config[string, int]{BackupPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := got.Get("a"); !ok || v != 1 {
		t.Fatalf("Get = %d,%v", v, ok)
	}
}

func TestRunPeriodicSaveAndCleanup(t *testing.T) {
	path := backupPath(t)
	c, clock, _ := newTestCache(t, Config[string, int]{
		BackupPath: path, BackupInterval: 5 * time.Millisecond, CleanupInterval: 5 * time.Millisecond,
	})
	c.SetWithTTL("gone", 1, time.Second)
	clock.Advance(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	eventually(t, "cleanup sweeps expired item", func() bool { return c.Len() == 0 })
	eventually(t, "periodic backup written", func() bool {
		_, err := os.Stat(path)
		return err == nil
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestRunNoBackupPath(t *testing.T) {
	c, _, _ := newTestCache(t, Config[string, int]{CleanupInterval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func FuzzLoad(f *testing.F) {
	f.Add([]byte(nil))
	f.Add([]byte("garbage"))
	valid := validSnapshotBytes(f)
	f.Add(valid)
	f.Add(valid[:len(valid)/2])
	f.Fuzz(func(t *testing.T, data []byte) {
		_, err := decodeSnapshot[string, int](bytes.NewReader(data), defaultMaxBackupSize)
		if err != nil && !errors.Is(err, ErrCorruptBackup) {
			t.Fatalf("err = %v, want ErrCorruptBackup", err)
		}
	})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(pollDeadline)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func dirEntries(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestLoadRespectsMaxItems(t *testing.T) {
	path := backupPath(t)
	src, clock, _ := newTestCache(t, Config[string, int]{BackupPath: path})
	clock.t = time.Now()
	src.SetWithTTL("soon", 1, time.Hour)
	src.SetWithTTL("mid", 2, 2*time.Hour)
	src.SetWithTTL("late", 3, 3*time.Hour)
	if err := src.Save(context.Background()); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		max      int
		wantKeys []string
	}{
		{"unlimited keeps all", 0, []string{"soon", "mid", "late"}},
		{"above count keeps all", 5, []string{"soon", "mid", "late"}},
		{"keeps latest expiring", 2, []string{"mid", "late"}},
		{"single", 1, []string{"late"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called int
			got, err := New(Config[string, int]{
				BackupPath: path,
				MaxItems:   tt.max,
				OnEvict:    func(string, int, EvictReason) { called++ },
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Len() != len(tt.wantKeys) {
				t.Fatalf("Len = %d, want %d", got.Len(), len(tt.wantKeys))
			}
			for _, k := range tt.wantKeys {
				if _, ok := got.Get(k); !ok {
					t.Errorf("key %q missing", k)
				}
			}
			if called != 0 || got.Stats().Evictions != 0 {
				t.Errorf("load trimming must be silent: callbacks=%d stats=%+v", called, got.Stats())
			}
		})
	}
}

func writeBackupFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // WriteFile is subject to umask
		t.Fatal(err)
	}
}

func TestLoadDecompressedSizeCap(t *testing.T) {
	// Highly compressible: tiny on disk, large once decompressed.
	var buf bytes.Buffer
	snap := snapshot[string, string]{Version: snapshotVersion, Items: []snapshotItem[string, string]{
		{Key: "a", Value: string(make([]byte, 1<<20)), ExpiresAt: time.Now().Add(time.Hour), TTL: time.Hour},
	}}
	if err := encodeSnapshot(&buf, snap); err != nil {
		t.Fatal(err)
	}
	path := backupPath(t)
	writeBackupFile(t, path, buf.Bytes(), 0o600)

	const limit = 64 << 10
	if buf.Len() >= limit {
		t.Fatalf("fixture compressed to %d bytes, want under %d", buf.Len(), limit)
	}
	tests := []struct {
		name    string
		max     int64
		wantErr error
	}{
		{"cap below decompressed size", limit, ErrCorruptBackup},
		{"default cap accepts", 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(Config[string, string]{BackupPath: path, MaxBackupSize: tt.max})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoadNaNKeys(t *testing.T) {
	nan := math.NaN()
	exp := time.Now().Add(time.Hour)
	var buf bytes.Buffer
	snap := snapshot[float64, int]{Version: snapshotVersion, Items: []snapshotItem[float64, int]{
		{Key: nan, Value: 1, ExpiresAt: exp, TTL: time.Hour},
		{Key: nan, Value: 2, ExpiresAt: exp, TTL: time.Hour},
		{Key: 1, Value: 3, ExpiresAt: exp, TTL: time.Hour},
	}}
	if err := encodeSnapshot(&buf, snap); err != nil {
		t.Fatal(err)
	}
	path := backupPath(t)
	writeBackupFile(t, path, buf.Bytes(), 0o600)

	c, err := New(Config[float64, int]{BackupPath: path, MaxItems: 1})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := c.Get(1); c.Len() != 1 || !ok || v != 3 {
		t.Fatalf("Len = %d, Get(1) = %d,%v; want only key 1", c.Len(), v, ok)
	}
}

func TestLoadUnhashableKeyIsCorrupt(t *testing.T) {
	var buf bytes.Buffer
	snap := snapshot[any, int]{Version: snapshotVersion, Items: []snapshotItem[any, int]{
		{Key: []int{1}, Value: 1, ExpiresAt: time.Now().Add(time.Hour), TTL: time.Hour},
	}}
	if err := encodeSnapshot(&buf, snap); err != nil {
		t.Fatal(err)
	}
	path := backupPath(t)
	writeBackupFile(t, path, buf.Bytes(), 0o600)

	if _, err := New(Config[any, int]{BackupPath: path}); !errors.Is(err, ErrCorruptBackup) {
		t.Fatalf("err = %v, want ErrCorruptBackup", err)
	}
}

func TestLoadRejectsInsecureFile(t *testing.T) {
	valid := validSnapshotBytes(t)
	tests := []struct {
		name  string
		setup func(t *testing.T, path string)
		skip  string
	}{
		{"world-writable", func(t *testing.T, path string) { writeBackupFile(t, path, valid, 0o666) }, "windows"},
		{"group-writable", func(t *testing.T, path string) { writeBackupFile(t, path, valid, 0o620) }, "windows"},
		{"symlink", func(t *testing.T, path string) {
			target := filepath.Join(t.TempDir(), "real.bak")
			writeBackupFile(t, target, valid, 0o600)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("cannot symlink: %v", err)
			}
		}, ""},
		{"fifo", func(t *testing.T, path string) {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := mkfifo(path); err != nil {
				t.Skipf("cannot create fifo: %v", err)
			}
		}, "windows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip == runtime.GOOS {
				t.Skipf("not applicable on %s", runtime.GOOS)
			}
			path := backupPath(t)
			tt.setup(t, path)
			if _, err := New(Config[string, int]{BackupPath: path}); !errors.Is(err, ErrInsecureBackup) {
				t.Fatalf("err = %v, want ErrInsecureBackup", err)
			}
		})
	}
}

func TestNewRemovesStaleTempFiles(t *testing.T) {
	path := backupPath(t)
	dir := filepath.Dir(path)
	writeBackupFile(t, path, validSnapshotBytes(t), 0o600)
	stale := filepath.Join(dir, ".mts-123456")
	writeBackupFile(t, stale, []byte("secret"), 0o600)
	unrelated := filepath.Join(dir, "other.txt")
	writeBackupFile(t, unrelated, []byte("keep"), 0o600)

	c, err := New(Config[string, int]{BackupPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("backup not loaded")
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp file still there: %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file removed: %v", err)
	}
}

func TestConcurrentSaves(t *testing.T) {
	path := backupPath(t)
	c, err := New(Config[string, int]{BackupPath: path})
	if err != nil {
		t.Fatal(err)
	}
	c.Set("a", 1)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Save(context.Background())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := dirEntries(t, filepath.Dir(path)); n != 1 {
		t.Fatalf("dir has %d entries, want 1", n)
	}
	got, err := New(Config[string, int]{BackupPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if got.Len() != 1 {
		t.Fatalf("Len = %d, want 1", got.Len())
	}
}
