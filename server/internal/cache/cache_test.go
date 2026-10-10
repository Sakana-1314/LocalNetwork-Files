package cache

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

func newManager(t *testing.T, mutate func(*store.CacheSettings)) *Manager {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if mutate != nil {
		s := st.Settings()
		mutate(&s.Cache)
		if _, err := st.SaveSettings(s); err != nil {
			t.Fatal(err)
		}
	}
	m, err := New(filepath.Join(dir, "cache"), st)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Stop)
	return m
}

const mb = 1 << 20

// put creates a (sparse) thumb-bucket file of size bytes last used age ago.
func put(t *testing.T, m *Manager, key, ext string, size int64, age time.Duration) string {
	t.Helper()
	p := m.ThumbPath(key, ext)
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(size)
	f.Close()
	ts := time.Now().Add(-age)
	os.Chtimes(p, ts, ts)
	return p
}

// session creates a transcode session dir with n segments of size bytes, last used age ago.
func session(t *testing.T, m *Manager, id string, n int, size int64, age time.Duration) string {
	t.Helper()
	d := m.TranscodeDir(id)
	os.MkdirAll(d, 0o755)
	for i := 0; i < n; i++ {
		f, _ := os.Create(filepath.Join(d, string(rune('0'+i))+".ts"))
		f.Truncate(size)
		f.Close()
	}
	ts := time.Now().Add(-age)
	os.Chtimes(d, ts, ts)
	return d
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func key(c byte) string {
	b := make([]byte, 40)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func TestTTLExpiry(t *testing.T) {
	m := newManager(t, func(c *store.CacheSettings) { c.ThumbTTLHours = 24; c.TranscodeTTLHours = 2 })
	old := put(t, m, key('a'), ".jpg", 100, 25*time.Hour)
	fresh := put(t, m, key('b'), ".jpg", 100, 23*time.Hour)
	failOld := put(t, m, key('c'), ".jpg.fail", 0, 30*time.Hour)
	sOld := session(t, m, "sold", 2, 10, 3*time.Hour)
	sNew := session(t, m, "snew", 2, 10, time.Hour)
	var evicted []string
	m.OnEvict = func(n string) { evicted = append(evicted, n) }

	res := m.Cleanup(nil, true)
	if exists(old) || exists(failOld) || !exists(fresh) || exists(sOld) || !exists(sNew) {
		t.Fatalf("ttl: old=%v fail=%v fresh=%v sOld=%v sNew=%v", exists(old), exists(failOld), exists(fresh), exists(sOld), exists(sNew))
	}
	if res.RemovedFiles != 4 || res.RemovedBytes != 120 {
		t.Fatalf("result %+v", res)
	}
	if len(evicted) != 1 || evicted[0] != "sold" {
		t.Fatalf("OnEvict %v", evicted)
	}
	// Empty shard directories are removed, non-empty ones kept.
	if exists(filepath.Dir(old)) || exists(filepath.Dir(failOld)) || !exists(filepath.Dir(fresh)) {
		t.Fatal("shard dirs")
	}
	if _, last := m.Stats(); last == nil {
		t.Fatal("last cleanup time not recorded")
	}
}

func TestSizeCapLRU(t *testing.T) {
	m := newManager(t, func(c *store.CacheSettings) { c.ThumbMaxMB = 16; c.ThumbTTLHours = 1000 })
	// 6 x 4 MB = 24 MB > 16 MB: the two least recently used go.
	var paths []string
	for i := 0; i < 6; i++ {
		paths = append(paths, put(t, m, key(byte('a'+i)), ".jpg", 4*mb, time.Duration(10-i)*time.Hour))
	}
	// Touching the oldest makes it the most recently used.
	now := time.Now()
	os.Chtimes(paths[0], now, now)
	res := m.Cleanup([]Bucket{Thumb}, true)
	var left []string
	for i, p := range paths {
		if exists(p) {
			left = append(left, string(rune('a'+i)))
		}
	}
	if res.RemovedBytes != 8*mb || len(left) != 4 || left[0] != "a" || left[1] != "d" {
		t.Fatalf("removed %d bytes, left %v", res.RemovedBytes, left)
	}
	st, _ := m.Stats()
	if st[Thumb].Bytes != 16*mb || st[Thumb].Files != 4 || st[Thumb].MaxBytes != 16*mb || st[Thumb].TTLHours != 1000 {
		t.Fatalf("stats %+v", st[Thumb])
	}
}

func TestActiveSessionsNeverEvicted(t *testing.T) {
	m := newManager(t, func(c *store.CacheSettings) { c.TranscodeMaxMB = 256; c.TranscodeTTLHours = 1 })
	session(t, m, "active", 3, 200*mb, 100*time.Hour) // old, huge, but in use
	session(t, m, "idle1", 2, 50*mb, 50*time.Hour)
	session(t, m, "idle2", 2, 10*mb, 10*time.Minute)
	var mu sync.Mutex
	var evicted []string
	m.IsActive = func(n string) bool { return n == "active" }
	m.OnEvict = func(n string) { mu.Lock(); evicted = append(evicted, n); mu.Unlock() }

	m.Cleanup(nil, true) // idle1 expired; total still > cap because of the active one: idle2 evicted too
	sort.Strings(evicted)
	if !exists(m.TranscodeDir("active")) || exists(m.TranscodeDir("idle1")) || exists(m.TranscodeDir("idle2")) {
		t.Fatalf("evicted %v", evicted)
	}
	if len(evicted) != 2 || evicted[0] != "idle1" || evicted[1] != "idle2" {
		t.Fatalf("OnEvict %v", evicted)
	}
	// Purge-all also spares active sessions.
	session(t, m, "idle3", 1, 1, 0)
	m.Cleanup(nil, false)
	if !exists(m.TranscodeDir("active")) || exists(m.TranscodeDir("idle3")) {
		t.Fatal("purge-all")
	}
}

func TestPurgeAllAndStats(t *testing.T) {
	m := newManager(t, nil)
	put(t, m, key('a'), ".jpg", 1000, 0)
	put(t, m, key('b'), ".vtt", 500, time.Hour)
	put(t, m, key('c'), ".jpg.fail", 0, 0)
	tmp := put(t, m, key('d'), ".jpg.tmp.jpg", 300, 0)             // ffmpeg output being written
	oldTmp := put(t, m, key('e'), ".jpg.tmp.jpg", 10, 2*time.Hour) // leftover from a crash
	session(t, m, "s1", 3, 100, 0)

	st, _ := m.Stats()
	if st[Thumb].Files != 5 || st[Thumb].Bytes != 1810 || st[Transcode].Files != 3 || st[Transcode].Bytes != 300 {
		t.Fatalf("stats %+v", st)
	}
	res := m.Cleanup(nil, false)
	if res.RemovedFiles != 7 || res.RemovedBytes != 1810-300+300 {
		t.Fatalf("purge %+v", res)
	}
	if !exists(tmp) || exists(oldTmp) {
		t.Fatal("in-progress temp file must survive, stale one must go")
	}
	st, _ = m.Stats()
	if st[Thumb].Files != 1 || st[Transcode].Files != 0 {
		t.Fatalf("after purge %+v", st)
	}
	des, _ := os.ReadDir(m.BucketDir(Thumb))
	if len(des) != 1 { // only the shard holding the temp file
		t.Fatalf("shard dirs left: %d", len(des))
	}
}

func TestTouchThrottled(t *testing.T) {
	m := newManager(t, nil)
	p := put(t, m, key('a'), ".jpg", 1, 0)
	recent := time.Now().Add(-30 * time.Second)
	os.Chtimes(p, recent, recent)
	Touch(p)
	if fi, _ := os.Stat(p); !fi.ModTime().Equal(recent) {
		t.Fatal("touch within a minute should be skipped")
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)
	Touch(p)
	if fi, _ := os.Stat(p); time.Since(fi.ModTime()) > time.Second {
		t.Fatal("touch did not update mtime")
	}
	Touch(filepath.Join(t.TempDir(), "missing")) // no panic
}

func TestAddedTriggersEarlyCleanup(t *testing.T) {
	oldEvery := earlyCleanupEvery
	earlyCleanupEvery = 300 * time.Millisecond
	defer func() { earlyCleanupEvery = oldEvery }()

	m := newManager(t, func(c *store.CacheSettings) { c.ThumbMaxMB = 16; c.CleanupIntervalMinutes = 600 })
	m.Start()
	var paths []string
	for i := 0; i < 5; i++ {
		paths = append(paths, put(t, m, key(byte('a'+i)), ".jpg", 4*mb, time.Duration(5-i)*time.Minute))
		m.Added(Thumb, 4*mb)
	}
	deadline := time.Now().Add(3 * time.Second)
	for exists(paths[0]) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if exists(paths[0]) {
		t.Fatal("writes beyond the cap did not trigger a cleanup")
	}
	if !exists(paths[4]) {
		t.Fatal("most recent entry evicted")
	}
	// Within the rate limit, further writes don't trigger another scan.
	m.mu.Lock()
	last := m.lastCleanup
	m.mu.Unlock()
	put(t, m, key('z'), ".jpg", 8*mb, 0)
	m.Added(Thumb, 8*mb)
	time.Sleep(100 * time.Millisecond)
	m.mu.Lock()
	again := m.lastCleanup
	m.mu.Unlock()
	if !again.Equal(last) {
		t.Fatal("early cleanup not rate limited")
	}
}
