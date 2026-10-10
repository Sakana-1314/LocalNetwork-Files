package search

import (
	"io/fs"
	"log/slog"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// maxWatches caps the number of watched directories to stay below OS limits.
const maxWatches = 200_000

// watchSupported: kqueue (macOS/BSD) needs one file descriptor per watched
// file, which does not scale to large trees, so watching is Linux/Windows only.
var watchSupported = runtime.GOOS == "linux" || runtime.GOOS == "windows"

type watcher struct {
	w       *fsnotify.Watcher
	x       *Indexer
	mu      sync.Mutex
	count   int
	full    bool
	watched map[string]bool
}

// syncWatcher starts/stops watching according to settings and re-registers roots.
func (x *Indexer) syncWatcher() {
	want := x.store.Settings().Search.Watch && watchSupported
	x.mu.Lock()
	if x.stopped {
		x.mu.Unlock()
		return
	}
	w := x.watcher
	if !want {
		x.watcher = nil
		x.mu.Unlock()
		if w != nil {
			w.close()
		}
		return
	}
	if w == nil {
		fw, err := fsnotify.NewWatcher()
		if err != nil {
			x.mu.Unlock()
			slog.Warn("search: cannot create watcher", "err", err)
			return
		}
		w = &watcher{w: fw, x: x, watched: map[string]bool{}}
		x.watcher = w
		go w.loop()
	}
	roots := make([]string, 0, len(x.indexes))
	for r := range x.indexes {
		roots = append(roots, r)
	}
	x.mu.Unlock()
	for _, r := range roots {
		w.addTree(r)
	}
}

func (w *watcher) addTree(root string) {
	filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			if de != nil && de.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !de.IsDir() {
			return nil
		}
		if p != root && skip(de.Name()) {
			return fs.SkipDir
		}
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.watched[p] {
			return nil
		}
		if w.count >= maxWatches {
			if !w.full {
				w.full = true
				slog.Warn("search: watch limit reached, relying on periodic reindex", "limit", maxWatches)
			}
			return fs.SkipAll
		}
		if err := w.w.Add(p); err != nil {
			if !w.full {
				w.full = true
				slog.Warn("search: cannot watch more directories, relying on periodic reindex", "err", err)
			}
			return fs.SkipAll
		}
		w.watched[p] = true
		w.count++
		return nil
	})
}

func (w *watcher) loop() {
	for {
		select {
		case ev, ok := <-w.w.Events:
			if !ok {
				return
			}
			if skip(filepath.Base(ev.Name)) {
				continue
			}
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
				w.mu.Lock()
				if w.watched[ev.Name] {
					delete(w.watched, ev.Name)
					w.count--
				}
				w.mu.Unlock()
			}
			// Chmod events are noisy and irrelevant for names/sizes.
			if ev.Op == fsnotify.Chmod {
				continue
			}
			w.x.Changed(ev.Name)
		case err, ok := <-w.w.Errors:
			if !ok {
				return
			}
			slog.Debug("search: watcher error", "err", err)
		}
	}
}

func (w *watcher) close() { w.w.Close() }
