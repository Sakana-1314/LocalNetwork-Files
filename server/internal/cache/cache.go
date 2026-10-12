// Package cache manages the on-disk caches (preview images and transcoded
// HLS segments): layout, access tracking and lifecycle (TTL since last access
// plus a size cap enforced with LRU eviction).
//
// Access time is tracked through the entry's mtime, which is bumped (with
// throttling) whenever the entry is served. This survives restarts and needs
// no extra bookkeeping.
package cache

import (
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/config"
)

type Bucket string

const (
	Thumb     Bucket = "thumb"     // files: thumbnails, extracted subtitles
	Transcode Bucket = "transcode" // directories: one per transcode session
)

var buckets = []Bucket{Thumb, Transcode}

type Manager struct {
	dir string
	cfg config.CacheSettings

	// IsActive reports whether a transcode session directory is in use and must not be evicted.
	IsActive func(name string) bool
	// OnEvict is called after a transcode session directory was removed.
	OnEvict func(name string)

	mu          sync.Mutex
	lastCleanup time.Time
	stop        chan struct{}
	stopOnce    sync.Once
	kick        chan struct{}

	// Approximate bucket sizes between scans, used to trigger an early cleanup
	// when a bucket outgrows its cap (see Added).
	sizeMu    sync.Mutex
	size      map[Bucket]int64
	lastEarly time.Time
}

// earlyCleanupEvery rate-limits cleanups triggered by Added.
var earlyCleanupEvery = time.Minute

func New(dir string, cfg config.CacheSettings) (*Manager, error) {
	for _, b := range buckets {
		if err := os.MkdirAll(filepath.Join(dir, string(b)), 0o755); err != nil {
			return nil, err
		}
	}
	m := &Manager{dir: dir, cfg: cfg, stop: make(chan struct{}), kick: make(chan struct{}, 1), size: map[Bucket]int64{}}
	for _, b := range buckets {
		_, m.size[b] = m.scan(b)
	}
	return m, nil
}

func (m *Manager) Dir() string               { return m.dir }
func (m *Manager) BucketDir(b Bucket) string { return filepath.Join(m.dir, string(b)) }

// ThumbPath returns the file path for a thumbnail-bucket key (sharded by prefix).
func (m *Manager) ThumbPath(key, ext string) string {
	return filepath.Join(m.dir, string(Thumb), key[:2], key+ext)
}

// TranscodeDir returns the directory of a transcode session.
func (m *Manager) TranscodeDir(id string) string {
	return filepath.Join(m.dir, string(Transcode), id)
}

// Touch marks an entry as recently used.
func Touch(p string) {
	fi, err := os.Stat(p)
	if err != nil || time.Since(fi.ModTime()) < time.Minute {
		return
	}
	now := time.Now()
	os.Chtimes(p, now, now)
}

// Start runs the periodic cleanup loop.
func (m *Manager) Start() {
	go func() {
		for {
			interval := time.Duration(m.cfg.CleanupIntervalMinutes) * time.Minute
			select {
			case <-m.stop:
				return
			case <-m.kick:
			case <-time.After(interval):
			}
			m.Cleanup(nil)
		}
	}()
}

// Reschedule re-reads the interval (call after settings change).
func (m *Manager) Reschedule() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func (m *Manager) Stop() { m.stopOnce.Do(func() { close(m.stop) }) }

// Added records n bytes newly written to bucket b. When the bucket's
// estimated size exceeds its cap, a cleanup is scheduled right away (at most
// once per earlyCleanupEvery) instead of waiting for the next interval.
func (m *Manager) Added(b Bucket, n int64) {
	_, max := m.limits(b)
	m.sizeMu.Lock()
	m.size[b] += n
	trigger := m.size[b] > max && time.Since(m.lastEarly) >= earlyCleanupEvery
	if trigger {
		m.lastEarly = time.Now()
	}
	m.sizeMu.Unlock()
	if trigger {
		slog.Debug("cache over cap, scheduling cleanup", "bucket", b)
		m.Reschedule()
	}
}

type entry struct {
	path    string
	name    string
	size    int64
	files   int
	lastUse time.Time
}

func (m *Manager) scan(b Bucket) ([]entry, int64) {
	root := m.BucketDir(b)
	var out []entry
	var total int64
	switch b {
	case Thumb:
		filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() {
				return nil
			}
			fi, err := de.Info()
			if err != nil {
				return nil
			}
			out = append(out, entry{path: p, name: de.Name(), size: fi.Size(), files: 1, lastUse: fi.ModTime()})
			total += fi.Size()
			return nil
		})
	case Transcode:
		des, _ := os.ReadDir(root)
		for _, de := range des {
			if !de.IsDir() {
				continue
			}
			p := filepath.Join(root, de.Name())
			e := entry{path: p, name: de.Name()}
			if fi, err := de.Info(); err == nil {
				e.lastUse = fi.ModTime()
			}
			filepath.WalkDir(p, func(fp string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					if fi, err := d.Info(); err == nil {
						e.size += fi.Size()
						e.files++
					}
				}
				return nil
			})
			out = append(out, e)
			total += e.size
		}
	}
	return out, total
}

type BucketStats struct {
	Files    int   `json:"files"`
	Bytes    int64 `json:"bytes"`
	MaxBytes int64 `json:"max_bytes"`
	TTLHours int   `json:"ttl_hours"`
}

func (m *Manager) limits(b Bucket) (ttl time.Duration, max int64) {
	c := m.cfg
	if b == Thumb {
		return time.Duration(c.ThumbTTLHours) * time.Hour, int64(c.ThumbMaxMB) << 20
	}
	return time.Duration(c.TranscodeTTLHours) * time.Hour, int64(c.TranscodeMaxMB) << 20
}

func (m *Manager) Stats() (map[Bucket]BucketStats, *time.Time) {
	out := map[Bucket]BucketStats{}
	for _, b := range buckets {
		es, total := m.scan(b)
		files := 0
		for _, e := range es {
			files += e.files
		}
		ttl, max := m.limits(b)
		out[b] = BucketStats{Files: files, Bytes: total, MaxBytes: max, TTLHours: int(ttl / time.Hour)}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lastCleanup.IsZero() {
		return out, nil
	}
	t := m.lastCleanup
	return out, &t
}

type CleanResult struct {
	RemovedFiles int   `json:"removed_files"`
	RemovedBytes int64 `json:"removed_bytes"`
}

// Cleanup enforces the TTL and size limits on the given buckets (all when
// nil). Only expired entries and entries beyond a bucket's cap are removed;
// there is deliberately no "wipe everything" mode.
func (m *Manager) Cleanup(only []Bucket) CleanResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if only == nil {
		only = buckets
	}
	var res CleanResult
	for _, b := range only {
		ttl, max := m.limits(b)
		es, total := m.scan(b)
		sort.Slice(es, func(i, j int) bool { return es[i].lastUse.Before(es[j].lastUse) })
		cutoff := time.Now().Add(-ttl)
		for _, e := range es {
			if b == Transcode && m.IsActive != nil && m.IsActive(e.name) {
				continue
			}
			if b == Thumb && isTemp(e.name) && time.Since(e.lastUse) < time.Hour {
				continue // being written right now
			}
			// A zero TTL means "never expires".
			expired := ttl > 0 && e.lastUse.Before(cutoff)
			over := total > max
			if !expired && !over {
				continue
			}
			if err := os.RemoveAll(e.path); err != nil {
				slog.Warn("cache evict failed", "path", e.path, "err", err)
				continue
			}
			total -= e.size
			res.RemovedBytes += e.size
			res.RemovedFiles += e.files
			if b == Transcode && m.OnEvict != nil {
				m.OnEvict(e.name)
			}
		}
		if b == Thumb {
			removeEmptyDirs(m.BucketDir(b))
		}
		m.sizeMu.Lock()
		m.size[b] = total
		m.sizeMu.Unlock()
	}
	m.lastCleanup = time.Now()
	if res.RemovedFiles > 0 {
		slog.Info("cache cleanup", "removed_files", res.RemovedFiles, "removed_bytes", res.RemovedBytes)
	}
	return res
}

// isTemp reports names of files that are still being written (see media's writeAtomic/ffmpeg outputs).
func isTemp(name string) bool {
	return strings.HasPrefix(name, ".tmp-") || strings.Contains(name, ".tmp.")
}

func removeEmptyDirs(root string) {
	des, _ := os.ReadDir(root)
	for _, de := range des {
		if de.IsDir() && !strings.HasPrefix(de.Name(), ".") {
			os.Remove(filepath.Join(root, de.Name())) // fails when not empty, which is fine
		}
	}
}
