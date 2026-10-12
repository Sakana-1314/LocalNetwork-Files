package api

import (
	"os"
	"sync"
	"time"
)

// thumbIndex maps a preview digest (the content fingerprint returned in
// Entry.Thumb) back to the file it came from. Directories, results and stats
// all feed it as they hand entries to clients, so /media/thumb can be asked for
// an image by digest alone — no path in the URL, and no path parsing.
//
// It is a cache, not a source of truth: anything it does not know about simply
// yields 404. Bounded and LRU so a long-lived process cannot grow without limit.
type thumbIndex struct {
	mu    sync.Mutex
	max   int
	items map[string]thumbIndexEntry
	// order holds digests from least to most recently used.
	order []string
}

type thumbIndexEntry struct {
	real  string
	size  int64
	mtime time.Time
}

func newThumbIndex(max int) *thumbIndex {
	return &thumbIndex{max: max, items: make(map[string]thumbIndexEntry, max)}
}

// Put records which file a digest came from.
func (t *thumbIndex) Put(digest, real string, size int64, mtime time.Time) {
	if digest == "" || real == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.items[digest]; !ok {
		t.order = append(t.order, digest)
	}
	t.items[digest] = thumbIndexEntry{real: real, size: size, mtime: mtime}
	// Amortised eviction: trim a batch at a time instead of on every insert.
	for len(t.order) > t.max {
		old := t.order[0]
		t.order = t.order[1:]
		delete(t.items, old)
	}
}

// Lookup returns the file behind a digest. The file must still exist with the
// same size and modification time, otherwise the entry is dropped: the digest
// may have been replaced by different content at the same path.
//
// The modification time is compared at second resolution because the search
// index only stores seconds; a file rewritten within the same second as the
// original may therefore keep serving the old preview.
func (t *thumbIndex) Lookup(digest string) (string, bool) {
	t.mu.Lock()
	e, ok := t.items[digest]
	if ok {
		t.touchLocked(digest)
	}
	t.mu.Unlock()
	if !ok {
		return "", false
	}
	fi, err := os.Stat(e.real)
	if err != nil || fi.IsDir() || fi.Size() != e.size || fi.ModTime().Truncate(time.Second) != e.mtime.Truncate(time.Second) {
		t.forget(digest)
		return "", false
	}
	return e.real, true
}

// touchLocked marks digest as most recently used. The list is only reordered
// when a hit lands far from the tail, keeping lookup cheap.
func (t *thumbIndex) touchLocked(digest string) {
	n := len(t.order)
	if n == 0 || t.order[n-1] == digest {
		return
	}
	for i := n - 1; i >= 0; i-- {
		if t.order[i] == digest {
			t.order = append(t.order[:i], t.order[i+1:]...)
			t.order = append(t.order, digest)
			return
		}
	}
}

func (t *thumbIndex) forget(digest string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.items[digest]; !ok {
		return
	}
	delete(t.items, digest)
	for i := len(t.order) - 1; i >= 0; i-- {
		if t.order[i] == digest {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
}

func (t *thumbIndex) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.items)
}
