// Package search maintains an in-memory file name index over all authorized
// directories and answers fuzzy queries scoped to a user's virtual filesystem.
//
// The index is built by walking every distinct top-level mount directory
// (nested mounts share their ancestor's index), refreshed periodically, and
// kept current incrementally from VFS write notifications and, on Linux and
// Windows, filesystem watches.
package search

import (
	"container/heap"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Sakana-1314/MList/server/internal/config"
	"github.com/Sakana-1314/MList/server/internal/store"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

// skipNames are well-known junk/system entries that are never indexed.
var skipNames = map[string]bool{
	".DS_Store": true, "Thumbs.db": true, "desktop.ini": true, "$RECYCLE.BIN": true, "System Volume Information": true,
	"@eaDir": true, ".Trashes": true, ".Spotlight-V100": true, ".fseventsd": true, ".TemporaryItems": true,
	"lost+found": true, "#recycle": true, ".git": true, "node_modules": true,
}

func skip(name string) bool { return skipNames[name] || strings.HasPrefix(name, vfs.TempPrefix) }

type item struct {
	name    string
	lower   string
	dir     int32 // index into rootIndex.dirs
	isDir   bool
	deleted bool
	kind    uint8 // index into kinds
	size    int64
	mtime   int64
}

// kinds enumerates vfs kinds so items can store their kind in one byte.
var kinds = []string{vfs.KindOther, vfs.KindDir, vfs.KindImage, vfs.KindVideo, vfs.KindAudio, vfs.KindText, vfs.KindPDF, vfs.KindArchive}

func kindIndex(k string) uint8 {
	for i, x := range kinds {
		if x == k {
			return uint8(i)
		}
	}
	return 0
}

// rootIndex indexes one top-level directory.
type rootIndex struct {
	root string

	mu      sync.RWMutex
	items   []item
	dirs    []string         // relative directory paths ("" = root)
	dirIDs  map[string]int32 // rel dir -> index
	deleted int
}

func newRootIndex(root string) *rootIndex {
	return &rootIndex{root: root, dirs: []string{""}, dirIDs: map[string]int32{"": 0}}
}

func lowerRunes(s string) string {
	b := []rune(s)
	changed := false
	for i, r := range b {
		if l := unicode.ToLower(r); l != r {
			b[i] = l
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}

func (ri *rootIndex) dirID(rel string) int32 {
	if id, ok := ri.dirIDs[rel]; ok {
		return id
	}
	id := int32(len(ri.dirs))
	ri.dirs = append(ri.dirs, rel)
	ri.dirIDs[rel] = id
	return id
}

func (ri *rootIndex) add(relDir string, fi fs.FileInfo) {
	name := fi.Name()
	it := item{name: name, lower: lowerRunes(name), dir: ri.dirID(relDir), isDir: fi.IsDir(), mtime: fi.ModTime().Unix()}
	it.kind = kindIndex(vfs.KindOf(name, it.isDir))
	if !it.isDir {
		it.size = fi.Size()
	}
	ri.items = append(ri.items, it)
}

func (ri *rootIndex) rel(it *item) string {
	d := ri.dirs[it.dir]
	if d == "" {
		return it.name
	}
	return d + "/" + it.name
}

// walk adds everything below relStart (which itself must already be indexed, or be "").
func (ri *rootIndex) walk(relStart string, limit *int) {
	start := ri.root
	if relStart != "" {
		start = filepath.Join(ri.root, filepath.FromSlash(relStart))
	}
	filepath.WalkDir(start, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			if de != nil && de.IsDir() && p != start {
				return fs.SkipDir
			}
			return nil
		}
		if p == start {
			return nil
		}
		if skip(de.Name()) {
			if de.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		fi, err := de.Info()
		if err != nil {
			return nil
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil
		}
		relP, _ := filepath.Rel(ri.root, p)
		relDir := path.Dir(filepath.ToSlash(relP))
		if relDir == "." {
			relDir = ""
		}
		ri.add(relDir, fi)
		if limit != nil {
			*limit--
		}
		return nil
	})
}

// update re-syncs the subtree at rel ("" means the whole root).
func (ri *rootIndex) update(rel string) {
	ri.mu.Lock()
	defer ri.mu.Unlock()
	if rel == "" {
		fresh := newRootIndex(ri.root)
		fresh.walk("", nil)
		ri.items, ri.dirs, ri.dirIDs, ri.deleted = fresh.items, fresh.dirs, fresh.dirIDs, 0
		return
	}
	prefix := rel + "/"
	parent, base := path.Dir(rel), path.Base(rel)
	if parent == "." {
		parent = ""
	}
	for i := range ri.items {
		it := &ri.items[i]
		if it.deleted {
			continue
		}
		d := ri.dirs[it.dir]
		self := d == parent && it.name == base
		descendant := d == rel || strings.HasPrefix(d, prefix)
		if self || descendant {
			it.deleted = true
			ri.deleted++
		}
	}
	real := filepath.Join(ri.root, filepath.FromSlash(rel))
	if fi, err := os.Lstat(real); err == nil && fi.Mode()&fs.ModeSymlink == 0 && !skip(fi.Name()) {
		ri.add(parent, fi)
		if fi.IsDir() {
			ri.walk(rel, nil)
		}
	}
	if ri.deleted > 1024 && ri.deleted > len(ri.items)/4 {
		ri.compact()
	}
}

func (ri *rootIndex) compact() {
	out := ri.items[:0]
	for _, it := range ri.items {
		if !it.deleted {
			out = append(out, it)
		}
	}
	// Clear the tail so dropped strings can be collected.
	for i := len(out); i < len(ri.items); i++ {
		ri.items[i] = item{}
	}
	ri.items = out
	ri.deleted = 0
}

func (ri *rootIndex) count() int {
	ri.mu.RLock()
	defer ri.mu.RUnlock()
	return len(ri.items) - ri.deleted
}

// ---------- indexer ----------

type Indexer struct {
	store  *store.Store
	search config.SearchSettings

	mu        sync.RWMutex
	indexes   map[string]*rootIndex // top-level root -> index
	building  bool
	lastBuilt time.Time
	buildDur  time.Duration

	// walkMu serializes full walks (Rebuild, SyncRoots) so that they cannot
	// overwrite each other's result with a stale set of roots.
	walkMu sync.Mutex
	// walking is >0 while a walk is in progress; changes applied meanwhile
	// are replayed on the fresh indexes once they are swapped in.
	walking int

	pendingMu sync.Mutex
	pending   map[string]struct{}
	replay    []string

	watcher  *watcher
	kick     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	stopped  bool

	// flushEvery is the debounce interval of incremental updates.
	flushEvery time.Duration
}

func New(st *store.Store, cfg config.SearchSettings) *Indexer {
	return &Indexer{store: st, search: cfg, indexes: map[string]*rootIndex{}, pending: map[string]struct{}{},
		kick: make(chan struct{}, 1), stop: make(chan struct{}), flushEvery: 1500 * time.Millisecond}
}

// Start builds the index in the background and keeps it fresh.
func (x *Indexer) Start() {
	go x.Rebuild()
	go x.flushLoop()
	go func() {
		for {
			mins := x.search.ReindexIntervalMinutes
			var tick <-chan time.Time
			if mins > 0 {
				tick = time.After(time.Duration(mins) * time.Minute)
			}
			select {
			case <-x.stop:
				return
			case <-x.kick:
				continue
			case <-tick:
				x.Rebuild()
			}
		}
	}()
}

func (x *Indexer) Stop() {
	x.stopOnce.Do(func() {
		close(x.stop)
		x.mu.Lock()
		x.stopped = true
		w := x.watcher
		x.watcher = nil
		x.mu.Unlock()
		if w != nil {
			w.close()
		}
	})
}

// topRoots computes the distinct mount directories, dropping those nested in another.
func (x *Indexer) topRoots() []string {
	paths, err := x.store.AllMountPaths()
	if err != nil {
		slog.Error("search: list mounts", "err", err)
		return nil
	}
	var real []string
	for _, p := range paths {
		if rp, err := filepath.EvalSymlinks(p); err == nil {
			if fi, err := os.Stat(rp); err == nil && fi.IsDir() {
				real = append(real, rp)
			}
		}
	}
	return collapse(real)
}

// collapse sorts paths and drops those equal to or nested inside another.
// Comparing only with the previous element is not enough: "/a-b" sorts
// between "/a" and "/a/c".
func collapse(paths []string) []string {
	// Sorting by length first guarantees ancestors come before descendants.
	sort.Slice(paths, func(i, j int) bool {
		if len(paths[i]) != len(paths[j]) {
			return len(paths[i]) < len(paths[j])
		}
		return paths[i] < paths[j]
	})
	var out []string
outer:
	for _, p := range paths {
		for _, o := range out {
			if isWithin(o, p) {
				continue outer
			}
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func isWithin(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Rebuild re-walks every root. Searches keep using the old data until done.
func (x *Indexer) Rebuild() {
	x.mu.Lock()
	if x.building {
		x.mu.Unlock()
		return
	}
	x.building = true
	x.mu.Unlock()
	x.walkMu.Lock()
	defer x.walkMu.Unlock()
	x.beginWalk()

	began := time.Now()
	roots := x.topRoots()
	fresh := map[string]*rootIndex{}
	total := 0
	for _, r := range roots {
		ri := newRootIndex(r)
		ri.walk("", nil)
		fresh[r] = ri
		total += len(ri.items)
	}
	x.mu.Lock()
	x.indexes = fresh
	x.building = false
	x.lastBuilt = time.Now()
	x.buildDur = time.Since(began)
	x.mu.Unlock()
	x.endWalk()
	slog.Info("search index built", "roots", len(roots), "entries", total, "took", x.buildDur.Round(time.Millisecond))
	x.syncWatcher()
}

// SyncRoots indexes newly authorized directories and drops revoked ones
// without re-walking roots that did not change.
func (x *Indexer) SyncRoots() {
	x.walkMu.Lock()
	defer x.walkMu.Unlock()
	want := x.topRoots()
	x.mu.RLock()
	same := len(want) == len(x.indexes)
	for _, r := range want {
		if x.indexes[r] == nil {
			same = false
		}
	}
	x.mu.RUnlock()
	if same {
		return
	}
	x.beginWalk()
	fresh := map[string]*rootIndex{}
	for _, r := range want {
		x.mu.RLock()
		ri := x.indexes[r]
		x.mu.RUnlock()
		if ri == nil {
			ri = newRootIndex(r)
			ri.walk("", nil)
		}
		fresh[r] = ri
	}
	x.mu.Lock()
	x.indexes = fresh
	x.mu.Unlock()
	x.endWalk()
	slog.Info("search roots updated", "roots", len(fresh))
	x.syncWatcher()
}

// RebuildAsync starts a rebuild unless one is running.
func (x *Indexer) RebuildAsync() { go x.Rebuild() }

// Changed implements vfs.Notifier: re-sync the given local path soon.
func (x *Indexer) Changed(real string) {
	x.pendingMu.Lock()
	x.pending[real] = struct{}{}
	x.pendingMu.Unlock()
}

func (x *Indexer) beginWalk() {
	x.mu.Lock()
	x.walking++
	x.mu.Unlock()
}

// endWalk re-queues changes applied to the old indexes while walking, since
// the walk may have visited those directories before they changed.
func (x *Indexer) endWalk() {
	x.mu.Lock()
	x.walking--
	x.mu.Unlock()
	x.pendingMu.Lock()
	for _, p := range x.replay {
		x.pending[p] = struct{}{}
	}
	x.replay = nil
	x.pendingMu.Unlock()
}

func (x *Indexer) flushLoop() {
	t := time.NewTicker(x.flushEvery)
	defer t.Stop()
	for {
		select {
		case <-x.stop:
			return
		case <-t.C:
		}
		x.flush()
	}
}

// flush applies all pending change notifications.
func (x *Indexer) flush() {
	x.pendingMu.Lock()
	paths := make([]string, 0, len(x.pending))
	for p := range x.pending {
		paths = append(paths, p)
	}
	x.pending = map[string]struct{}{}
	x.pendingMu.Unlock()
	if len(paths) == 0 {
		return
	}
	// Collapse paths that are inside another changed path.
	for _, p := range collapse(paths) {
		x.apply(p)
	}
}

func (x *Indexer) apply(real string) {
	x.mu.RLock()
	var target *rootIndex
	for root, ri := range x.indexes {
		if isWithin(root, real) {
			target = ri
			break
		}
	}
	w := x.watcher
	walking := x.walking > 0
	x.mu.RUnlock()
	if walking {
		x.pendingMu.Lock()
		x.replay = append(x.replay, real)
		x.pendingMu.Unlock()
	}
	if target == nil {
		return
	}
	rel, _ := filepath.Rel(target.root, real)
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = ""
	}
	target.update(rel)
	if w != nil {
		if fi, err := os.Stat(real); err == nil && fi.IsDir() {
			w.addTree(real)
		}
	}
}

type Status struct {
	Indexing   bool       `json:"indexing"`
	Entries    int        `json:"entries"`
	Roots      int        `json:"roots"`
	LastBuilt  *time.Time `json:"last_built"`
	DurationMS int64      `json:"duration_ms"`
	Watching   bool       `json:"watching"`
}

func (x *Indexer) Status() Status {
	x.mu.RLock()
	defer x.mu.RUnlock()
	st := Status{Indexing: x.building, Roots: len(x.indexes), DurationMS: x.buildDur.Milliseconds(), Watching: x.watcher != nil}
	for _, ri := range x.indexes {
		st.Entries += ri.count()
	}
	if !x.lastBuilt.IsZero() {
		t := x.lastBuilt
		st.LastBuilt = &t
	}
	return st
}

// ---------- querying ----------

type Result struct {
	vfs.Entry
	Score   int   `json:"score"`
	Matches []int `json:"matches"`
}

type Response struct {
	Results  []Result `json:"results"`
	Total    int      `json:"total"`
	Indexing bool     `json:"indexing"`
	TookMS   float64  `json:"took_ms"`
}

type scope struct {
	ri      *rootIndex
	prefix  string // rel path inside ri that maps to the scope root ("" = whole index)
	vprefix string // virtual path of the scope root
	strip   string // rel path inside ri of the mount root (to build virtual paths)
	mount   *vfs.Root
	// dirK[i][dirID] is how many runes of pattern i the directory's virtual
	// path consumes as a subsequence; only built for path patterns.
	dirK [][]int32
}

type hit struct {
	score   int
	ri      *rootIndex
	idx     int
	sc      *scope
	nameLen int
}

type hitHeap []hit

func (h hitHeap) Len() int           { return len(h) }
func (h hitHeap) Less(i, j int) bool { return better(h[j], h[i]) } // min-heap: worst hit on top
func (h hitHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *hitHeap) Push(v any)        { *h = append(*h, v.(hit)) }
func (h *hitHeap) Pop() any {
	old := *h
	v := old[len(old)-1]
	*h = old[:len(old)-1]
	return v
}

// Search runs a fuzzy query over the parts of the index visible through fsys.
func (x *Indexer) Search(fsys *vfs.FS, within, query, kind string, limit int) Response {
	began := time.Now()
	resp := Response{Results: []Result{}}
	pats := parseQuery(query)
	if len(pats) == 0 {
		return resp
	}
	if limit <= 0 {
		limit = 100
	}
	limit = min(limit, 500)
	kindFilter := -1
	if kind != "" {
		kindFilter = int(kindIndex(kind))
		if kinds[kindFilter] != kind {
			return resp // unknown kind matches nothing
		}
	}

	x.mu.RLock()
	resp.Indexing = x.building
	scopes := x.scopes(fsys, within)
	x.mu.RUnlock()

	workers := runtime.GOMAXPROCS(0)
	type job struct {
		sc       *scope
		from, to int
	}
	var jobs []job
	locked := map[*rootIndex]bool{}
	for i := range scopes {
		sc := &scopes[i]
		if !locked[sc.ri] {
			sc.ri.mu.RLock()
			locked[sc.ri] = true
		}
		if hasPathPattern(pats) {
			sc.buildDirK(pats)
		}
		n := len(sc.ri.items)
		chunk := max(n/workers+1, 4096)
		for f := 0; f < n; f += chunk {
			jobs = append(jobs, job{sc, f, min(f+chunk, n)})
		}
	}
	defer func() {
		for ri := range locked {
			ri.mu.RUnlock()
		}
	}()
	workers = max(1, min(workers, len(jobs)))

	var wg sync.WaitGroup
	var mu sync.Mutex
	global := &hitHeap{}
	jobCh := make(chan job)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := &hitHeap{}
			count := 0
			m := newMatcher(pats)
			dirOK := map[int32]bool{}
			for j := range jobCh {
				clear(dirOK)
				m.reset(j.sc)
				ri := j.sc.ri
				for i := j.from; i < j.to; i++ {
					it := &ri.items[i]
					if it.deleted {
						continue
					}
					if kindFilter >= 0 && int(it.kind) != kindFilter {
						continue
					}
					if j.sc.prefix != "" {
						ok, seen := dirOK[it.dir]
						if !seen {
							d := ri.dirs[it.dir]
							ok = d == j.sc.prefix || strings.HasPrefix(d, j.sc.prefix+"/")
							dirOK[it.dir] = ok
						}
						if !ok {
							continue
						}
					}
					score, ok := m.score(it)
					if !ok {
						continue
					}
					count++
					h := hit{score: score, ri: ri, idx: i, sc: j.sc, nameLen: len(it.name)}
					if local.Len() < limit {
						heap.Push(local, h)
					} else if better(h, (*local)[0]) {
						(*local)[0] = h
						heap.Fix(local, 0)
					}
				}
			}
			mu.Lock()
			resp.Total += count
			for _, h := range *local {
				if global.Len() < limit {
					heap.Push(global, h)
				} else if better(h, (*global)[0]) {
					(*global)[0] = h
					heap.Fix(global, 0)
				}
			}
			mu.Unlock()
		}()
	}
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)
	wg.Wait()

	hits := *global
	sort.Slice(hits, func(i, j int) bool { return better(hits[i], hits[j]) })
	for _, h := range hits {
		it := &h.ri.items[h.idx]
		vp := h.sc.virtualDir(h.ri.dirs[it.dir]) + it.name
		pos := matchPositions(it, pats)
		var digest string
		if !it.isDir {
			real := filepath.Join(h.ri.root, h.ri.dirs[it.dir], it.name)
			digest = vfs.DigestFor(real, it.name, it.size, time.Unix(it.mtime, 0))
		}
		e := vfs.Entry{
			Name: it.name, Path: vp, IsDir: it.isDir, Size: it.size, ModTime: time.Unix(it.mtime, 0),
			Mime: vfs.MimeOf(it.name, it.isDir), Kind: kinds[it.kind], Writable: h.sc.mount.Writable,
			Thumb: digest,
		}
		resp.Results = append(resp.Results, Result{Entry: e, Score: h.score, Matches: pos})
	}
	resp.TookMS = float64(time.Since(began).Microseconds()) / 1000
	return resp
}

// better orders hits: higher score first, then shorter names, then path order
// so that results are deterministic.
func better(a, b hit) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if a.nameLen != b.nameLen {
		return a.nameLen < b.nameLen
	}
	if a.ri != b.ri {
		return a.ri.root < b.ri.root
	}
	return a.idx < b.idx
}

// virtualDir maps a directory (relative to the index root) to the virtual
// path of that directory inside the mount, with a trailing slash.
func (sc *scope) virtualDir(d string) string {
	vrel := strings.TrimPrefix(strings.TrimPrefix(d, sc.strip), "/")
	if vrel == "" {
		return "/" + sc.mount.Name + "/"
	}
	return "/" + sc.mount.Name + "/" + vrel + "/"
}

func hasPathPattern(pats []pattern) bool {
	for _, p := range pats {
		if p.path {
			return true
		}
	}
	return false
}

// buildDirK precomputes, for every directory in scope, how far each path
// pattern gets through the directory's lower-cased virtual path, so that
// per-item work is reduced to matching the remainder against the name.
// Caller holds sc.ri.mu for reading.
func (sc *scope) buildDirK(pats []pattern) {
	sc.dirK = make([][]int32, len(pats))
	for i, p := range pats {
		if !p.path {
			continue
		}
		ks := make([]int32, len(sc.ri.dirs))
		for id, d := range sc.ri.dirs {
			if sc.strip == "" || d == sc.strip || strings.HasPrefix(d, sc.strip+"/") {
				ks[id] = int32(consume(lowerRunes(sc.virtualDir(d)), p.runes, 0))
			}
		}
		sc.dirK[i] = ks
	}
}

// scopes maps the user's mounts (optionally narrowed to the virtual dir `within`)
// onto index ranges. Caller holds x.mu.
func (x *Indexer) scopes(fsys *vfs.FS, within string) []scope {
	within = vfs.CleanPath(within)
	var out []scope
	for _, r := range fsys.Roots() {
		var sub string
		if within != "/" {
			name, rest, _ := strings.Cut(within[1:], "/")
			if name != r.Name {
				continue
			}
			sub = rest
		}
		for root, ri := range x.indexes {
			if !isWithin(root, r.Path) {
				continue
			}
			strip, _ := filepath.Rel(root, r.Path)
			strip = filepath.ToSlash(strip)
			if strip == "." {
				strip = ""
			}
			prefix := strip
			if sub != "" {
				prefix = strings.TrimPrefix(path.Join(strip, sub), "/")
			}
			out = append(out, scope{ri: ri, prefix: prefix, strip: strip, mount: r})
			break
		}
	}
	return out
}

// matcher scores items against a query, reusing scratch buffers. One per worker.
type matcher struct {
	pats        []pattern
	sc          *scope
	orig, lower []rune
}

func newMatcher(pats []pattern) *matcher { return &matcher{pats: pats} }

func (m *matcher) reset(sc *scope) { m.sc = sc }

func (m *matcher) score(it *item) (int, bool) {
	total := 0
	decoded := false
	for pi, p := range m.pats {
		if p.path {
			// Path patterns match the virtual path ("/mount/dir/name") and only filter.
			k := int(m.sc.dirK[pi][it.dir])
			if k < len(p.runes) && consume(it.lower, p.runes, k) < len(p.runes) {
				return 0, false
			}
			total += len(p.runes) * 2
			continue
		}
		if quickReject(it.lower, p.runes) {
			return 0, false
		}
		if !decoded {
			m.orig = appendRunes(m.orig[:0], it.name)
			m.lower = appendRunes(m.lower[:0], it.lower)
			decoded = true
		}
		s, _, ok := matchOne(m.orig, m.lower, p.runes, false)
		if !ok {
			return 0, false
		}
		total += s
	}
	return total, true
}

func appendRunes(dst []rune, s string) []rune {
	for _, r := range s {
		dst = append(dst, r)
	}
	return dst
}

// consume advances k over p while p[k:] appears in s as a subsequence and
// returns the number of pattern runes matched so far.
func consume(s string, p []rune, k int) int {
	i := 0
	for k < len(p) {
		j := strings.IndexRune(s[i:], p[k])
		if j < 0 {
			return k
		}
		i += j + utf8.RuneLen(p[k])
		k++
	}
	return k
}

func matchPositions(it *item, pats []pattern) []int {
	orig, lower := []rune(it.name), []rune(it.lower)
	seen := map[int]bool{}
	pos := []int{}
	for _, p := range pats {
		if p.path {
			continue
		}
		_, ps, _ := matchOne(orig, lower, p.runes, true)
		for _, i := range ps {
			if !seen[i] {
				seen[i] = true
				pos = append(pos, i)
			}
		}
	}
	sort.Ints(pos)
	return pos
}
