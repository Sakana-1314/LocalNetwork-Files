package search

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/vfs"
)

func realTemp(t testing.TB) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func touch(t testing.TB, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openStore(t testing.TB) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := st.Settings()
	s.Search.Watch = false
	if _, err := st.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	return st
}

func addUser(t testing.TB, st *store.Store, name string, mounts ...store.Mount) *store.User {
	t.Helper()
	u := &store.User{Username: name, PasswordHash: "x", Role: store.RoleUser, Mounts: mounts}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	u, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func paths(r Response) []string {
	out := []string{}
	for _, x := range r.Results {
		out = append(out, x.Path)
	}
	sort.Strings(out)
	return out
}

// fixture:
//
//	data/                       (alice mounts as "data")
//	  docs/2024年度报告.docx
//	  docs/report.pdf
//	  docs/sub/notes.txt
//	  photos/beach.jpg
//	  photos/report-cover.png
//	  shared/                   (bob mounts as "shared", nested in data)
//	    report-shared.txt
//	  .git/config               (skipped)
//	  .DS_Store                 (skipped)
//	  .lnf-upload-xyz           (skipped)
//	other/                      (bob mounts as "other", read-only)
//	  report-other.md
type fixture struct {
	st         *store.Store
	x          *Indexer
	data, oth  string
	alice, bob *store.User
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := realTemp(t)
	f := &fixture{st: openStore(t), data: filepath.Join(base, "data"), oth: filepath.Join(base, "other")}
	for _, p := range []string{"docs/2024年度报告.docx", "docs/report.pdf", "docs/sub/notes.txt", "photos/beach.jpg",
		"photos/report-cover.png", "shared/report-shared.txt", ".git/config", ".DS_Store", vfs.TempPrefix + "xyz"} {
		touch(t, filepath.Join(f.data, filepath.FromSlash(p)))
	}
	touch(t, filepath.Join(f.oth, "report-other.md"))
	f.alice = addUser(t, f.st, "alice", store.Mount{Name: "data", Path: f.data, Writable: true})
	f.bob = addUser(t, f.st, "bob", store.Mount{Name: "shared", Path: filepath.Join(f.data, "shared"), Writable: true},
		store.Mount{Name: "other", Path: f.oth})
	f.x = New(f.st)
	f.x.Rebuild()
	return f
}

func (f *fixture) fs(u *store.User) *vfs.FS { return vfs.New(u.Mounts, false, f.x) }

func TestIndexBuildAndUserScoping(t *testing.T) {
	f := newFixture(t)
	st := f.x.Status()
	if st.Roots != 2 || st.Indexing || st.LastBuilt == nil {
		t.Fatalf("status %+v", st)
	}
	// data: docs, docs/2 files, docs/sub, notes, photos, 2 photos, shared, shared file = 10; other: 1
	if st.Entries != 11 {
		t.Fatalf("entries = %d, want 11", st.Entries)
	}

	r := f.x.Search(f.fs(f.alice), "/", "report", "", 0)
	want := []string{"/data/docs/report.pdf", "/data/photos/report-cover.png", "/data/shared/report-shared.txt"}
	if got := paths(r); !reflect.DeepEqual(got, want) || r.Total != 3 {
		t.Fatalf("alice report: %v total %d", got, r.Total)
	}
	if r.Results[0].Path != "/data/docs/report.pdf" {
		t.Errorf("prefix+shortest should rank first, got %s", r.Results[0].Path)
	}
	for _, res := range r.Results {
		if !res.Writable {
			t.Errorf("alice result should be writable: %s", res.Path)
		}
	}

	r = f.x.Search(f.fs(f.bob), "/", "report", "", 0)
	want = []string{"/other/report-other.md", "/shared/report-shared.txt"}
	if got := paths(r); !reflect.DeepEqual(got, want) {
		t.Fatalf("bob report: %v", got)
	}
	for _, res := range r.Results {
		if res.Path == "/other/report-other.md" && res.Writable {
			t.Error("read-only mount result must not be writable")
		}
		if res.Path == "/shared/report-shared.txt" && (res.Kind != vfs.KindText || res.Name != "report-shared.txt") {
			t.Errorf("entry fields: %+v", res.Entry)
		}
	}
	// Bob must not see alice's files even when a query matches only those.
	if r := f.x.Search(f.fs(f.bob), "/", "beach", "", 0); len(r.Results) != 0 || r.Total != 0 {
		t.Fatalf("bob sees beach: %v", paths(r))
	}
	// Skipped entries are never indexed.
	for _, q := range []string{"config", "DS_Store", "lnf-upload"} {
		if r := f.x.Search(f.fs(f.alice), "/", q, "", 0); len(r.Results) != 0 {
			t.Errorf("%q found %v", q, paths(r))
		}
	}
	// A user without mounts sees nothing.
	if r := f.x.Search(vfs.New(nil, false, nil), "/", "report", "", 0); len(r.Results) != 0 {
		t.Fatal("no mounts, results")
	}
	// Disabled users' mounts are not indexed.
	f.bob.Disabled = true
	f.st.UpdateUser(f.bob, nil)
	f.x.SyncRoots()
	if st := f.x.Status(); st.Roots != 1 {
		t.Fatalf("roots after disabling bob: %d", st.Roots)
	}
}

func TestSearchCJKMatches(t *testing.T) {
	f := newFixture(t)
	r := f.x.Search(f.fs(f.alice), "/", "报告", "", 0)
	if len(r.Results) != 1 || r.Results[0].Name != "2024年度报告.docx" {
		t.Fatalf("results %v", paths(r))
	}
	if !reflect.DeepEqual(r.Results[0].Matches, []int{6, 7}) {
		t.Fatalf("matches %v", r.Results[0].Matches)
	}
	r = f.x.Search(f.fs(f.alice), "/", "年报 docx", "", 0)
	if len(r.Results) != 1 || !reflect.DeepEqual(r.Results[0].Matches, []int{4, 6, 9, 10, 11, 12}) {
		t.Fatalf("multi-term matches %+v", r.Results)
	}
}

func TestSearchWithinKindLimit(t *testing.T) {
	f := newFixture(t)
	fsA := f.fs(f.alice)
	if got := paths(f.x.Search(fsA, "/data/docs", "re", "", 0)); !reflect.DeepEqual(got, []string{"/data/docs/report.pdf"}) {
		// "2024年度报告.docx" does not contain r..e
		t.Fatalf("within docs: %v", got)
	}
	if got := paths(f.x.Search(fsA, "/data/photos/", "report", "", 0)); !reflect.DeepEqual(got, []string{"/data/photos/report-cover.png"}) {
		t.Fatalf("within photos: %v", got)
	}
	// The scope dir itself is not a result, its children are.
	if got := paths(f.x.Search(fsA, "/data/docs", "docs", "", 0)); len(got) != 0 {
		t.Fatalf("within docs, query docs: %v", got)
	}
	// "within" a sibling with a common prefix must not leak ("/data/doc" != "/data/docs").
	if got := paths(f.x.Search(fsA, "/data/doc", "report", "", 0)); len(got) != 0 {
		t.Fatalf("within prefix sibling: %v", got)
	}
	if got := paths(f.x.Search(fsA, "/nope", "report", "", 0)); len(got) != 0 {
		t.Fatalf("within unknown mount: %v", got)
	}
	if got := paths(f.x.Search(fsA, "/data/../../", "beach", "", 0)); len(got) != 1 {
		t.Fatalf("dots in within: %v", got)
	}
	// Bob's nested mount scoping uses virtual paths.
	if got := paths(f.x.Search(f.fs(f.bob), "/shared", "report", "", 0)); !reflect.DeepEqual(got, []string{"/shared/report-shared.txt"}) {
		t.Fatalf("bob within shared: %v", got)
	}
	// kind filter
	if got := paths(f.x.Search(fsA, "/", "report", vfs.KindImage, 0)); !reflect.DeepEqual(got, []string{"/data/photos/report-cover.png"}) {
		t.Fatalf("kind image: %v", got)
	}
	if got := paths(f.x.Search(fsA, "/", "o", vfs.KindDir, 0)); !reflect.DeepEqual(got, []string{"/data/docs", "/data/photos"}) {
		t.Fatalf("kind dir: %v", got)
	}
	if got := paths(f.x.Search(fsA, "/", "report", "bogus", 0)); len(got) != 0 {
		t.Fatalf("unknown kind: %v", got)
	}
	// limit/total
	r := f.x.Search(fsA, "/", "e", "", 2)
	if len(r.Results) != 2 || r.Total <= 2 {
		t.Fatalf("limit: %d results, total %d", len(r.Results), r.Total)
	}
	if r.Results[0].Score < r.Results[1].Score {
		t.Fatal("results not sorted by score")
	}
	full := f.x.Search(fsA, "/", "e", "", 500)
	if full.Total != r.Total || len(full.Results) != full.Total {
		t.Fatalf("total mismatch %d vs %d (%d results)", full.Total, r.Total, len(full.Results))
	}
	if full.Results[0].Path != r.Results[0].Path || full.Results[1].Path != r.Results[1].Path {
		t.Fatal("top-k differs from full ranking")
	}
	if r := f.x.Search(fsA, "/", "   ", "", 0); len(r.Results) != 0 || r.Results == nil {
		t.Fatal("empty query should give empty non-nil results")
	}
}

func TestSearchMultiTermAndPath(t *testing.T) {
	f := newFixture(t)
	fsA := f.fs(f.alice)
	if got := paths(f.x.Search(fsA, "/", "report png", "", 0)); !reflect.DeepEqual(got, []string{"/data/photos/report-cover.png"}) {
		t.Fatalf("AND: %v", got)
	}
	if got := paths(f.x.Search(fsA, "/", "report zzz", "", 0)); len(got) != 0 {
		t.Fatalf("AND no match: %v", got)
	}
	if got := paths(f.x.Search(fsA, "/", "docs/rep", "", 0)); !reflect.DeepEqual(got, []string{"/data/docs/report.pdf"}) {
		t.Fatalf("path term: %v", got)
	}
	if got := paths(f.x.Search(fsA, "/", "photos/ report", "", 0)); !reflect.DeepEqual(got, []string{"/data/photos/report-cover.png"}) {
		t.Fatalf("path + name: %v", got)
	}
	if got := paths(f.x.Search(fsA, "/", "/data/docs/sub/", "", 0)); !reflect.DeepEqual(got, []string{"/data/docs/sub/notes.txt"}) {
		t.Fatalf("absolute path term: %v", got)
	}
	// Path terms see virtual paths only: bob's mount "shared" lives in alice's
	// "data" dir, but bob must not be able to match on the hidden real path.
	if got := paths(f.x.Search(f.fs(f.bob), "/", "data/", "", 0)); len(got) != 0 {
		t.Fatalf("bob matched hidden ancestor: %v", got)
	}
	if got := paths(f.x.Search(f.fs(f.bob), "/", "shared/rep", "", 0)); !reflect.DeepEqual(got, []string{"/shared/report-shared.txt"}) {
		t.Fatalf("bob mount-name path term: %v", got)
	}
	// Path terms do not contribute to matches.
	r := f.x.Search(fsA, "/", "docs/ report", "", 0)
	if len(r.Results) != 1 || !reflect.DeepEqual(r.Results[0].Matches, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatalf("matches with path term: %+v", r.Results)
	}
}

func TestIncrementalChanges(t *testing.T) {
	f := newFixture(t)
	fsA := f.fs(f.alice)
	search := func(q string) []string { return paths(f.x.Search(fsA, "/", q, "", 0)) }

	// create file via VFS (notifier = indexer)
	if _, err := fsA.WriteFile("/data/docs/budget.xlsx", strings.NewReader("x"), false); err != nil {
		t.Fatal(err)
	}
	if got := search("budget"); len(got) != 0 {
		t.Fatalf("visible before flush: %v", got)
	}
	f.x.flush()
	if got := search("budget"); !reflect.DeepEqual(got, []string{"/data/docs/budget.xlsx"}) {
		t.Fatalf("after create: %v", got)
	}
	// rename
	if _, err := fsA.Rename("/data/docs/budget.xlsx", "forecast.xlsx"); err != nil {
		t.Fatal(err)
	}
	f.x.flush()
	if got := search("budget"); len(got) != 0 {
		t.Fatalf("old name still found: %v", got)
	}
	if got := search("forecast"); !reflect.DeepEqual(got, []string{"/data/docs/forecast.xlsx"}) {
		t.Fatalf("after rename: %v", got)
	}
	// mkdir with content, then rename the directory
	if _, err := fsA.WriteFile("/data/proj/a/deep.txt", strings.NewReader("x"), false); err != nil {
		t.Fatal(err)
	}
	f.x.Changed(filepath.Join(f.data, "proj")) // e.g. a watcher event for the new parent
	f.x.flush()
	if got := search("deep"); !reflect.DeepEqual(got, []string{"/data/proj/a/deep.txt"}) {
		t.Fatalf("nested create: %v", got)
	}
	if _, err := fsA.Rename("/data/proj", "project"); err != nil {
		t.Fatal(err)
	}
	f.x.flush()
	if got := search("deep"); !reflect.DeepEqual(got, []string{"/data/project/a/deep.txt"}) {
		t.Fatalf("dir rename: %v", got)
	}
	if got := search("proj"); !reflect.DeepEqual(got, []string{"/data/project"}) {
		t.Fatalf("dir itself: %v", got)
	}
	// delete a directory tree
	if err := fsA.Remove("/data/project"); err != nil {
		t.Fatal(err)
	}
	f.x.flush()
	if got := search("deep"); len(got) != 0 {
		t.Fatalf("deleted tree still found: %v", got)
	}
	// move across dirs and copy
	if err := fsA.Copy("/data/docs", "/data/docs-copy", false); err != nil {
		t.Fatal(err)
	}
	if err := fsA.Move("/data/photos/beach.jpg", "/data/docs/beach.jpg", false); err != nil {
		t.Fatal(err)
	}
	f.x.flush()
	if got := search("beach"); !reflect.DeepEqual(got, []string{"/data/docs/beach.jpg"}) {
		t.Fatalf("after move: %v", got)
	}
	if got := search("notes"); !reflect.DeepEqual(got, []string{"/data/docs-copy/sub/notes.txt", "/data/docs/sub/notes.txt"}) {
		t.Fatalf("after copy: %v", got)
	}
	// Changes outside of any indexed root are ignored.
	f.x.Changed("/definitely/not/indexed")
	f.x.flush()
	// Bob sees changes in his nested mount immediately after flush, too.
	if _, err := fsA.WriteFile("/data/shared/new-shared.txt", strings.NewReader("x"), false); err != nil {
		t.Fatal(err)
	}
	f.x.flush()
	if got := paths(f.x.Search(f.fs(f.bob), "/", "new-shared", "", 0)); !reflect.DeepEqual(got, []string{"/shared/new-shared.txt"}) {
		t.Fatalf("bob nested: %v", got)
	}
	// The count reflects deletions.
	before := f.x.Status().Entries
	os.Remove(filepath.Join(f.data, "docs", "report.pdf"))
	f.x.Changed(filepath.Join(f.data, "docs", "report.pdf"))
	f.x.flush()
	if after := f.x.Status().Entries; after != before-1 {
		t.Fatalf("entries %d -> %d", before, after)
	}
}

func TestFlushLoop(t *testing.T) {
	f := newFixture(t)
	f.x.flushEvery = 10 * time.Millisecond
	go f.x.flushLoop()
	defer f.x.Stop()
	touch(t, filepath.Join(f.data, "loopfile.txt"))
	f.x.Changed(filepath.Join(f.data, "loopfile.txt"))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(f.x.Search(f.fs(f.alice), "/", "loopfile", "", 0).Results) == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("flush loop did not apply change")
}

func TestStopIdempotent(t *testing.T) {
	x := New(openStore(t))
	x.Stop()
	x.Stop()
}

func TestSyncRoots(t *testing.T) {
	f := newFixture(t)
	extra := realTemp(t)
	touch(t, filepath.Join(extra, "zebra.txt"))
	carol := addUser(t, f.st, "carol", store.Mount{Name: "z", Path: extra})
	if got := paths(f.x.Search(f.fs(carol), "/", "zebra", "", 0)); len(got) != 0 {
		t.Fatalf("before sync: %v", got)
	}
	// SyncRoots must not re-walk unchanged roots: add a file to data without notifying.
	touch(t, filepath.Join(f.data, "unnotified.txt"))
	f.x.SyncRoots()
	if got := paths(f.x.Search(f.fs(carol), "/", "zebra", "", 0)); !reflect.DeepEqual(got, []string{"/z/zebra.txt"}) {
		t.Fatalf("after sync: %v", got)
	}
	if got := paths(f.x.Search(f.fs(f.alice), "/", "unnotified", "", 0)); len(got) != 0 {
		t.Fatalf("unchanged root was re-walked: %v", got)
	}
	// Revoking drops the root.
	if err := f.st.DeleteUser(carol.ID); err != nil {
		t.Fatal(err)
	}
	f.x.SyncRoots()
	if st := f.x.Status(); st.Roots != 2 {
		t.Fatalf("roots after revoke: %d", st.Roots)
	}
	// A mount that is a parent of an existing root replaces it.
	parent := filepath.Dir(f.data)
	addUser(t, f.st, "dave", store.Mount{Name: "all", Path: parent})
	f.x.SyncRoots()
	if st := f.x.Status(); st.Roots != 1 {
		t.Fatalf("roots after parent mount: %d", st.Roots)
	}
	if got := paths(f.x.Search(f.fs(f.alice), "/", "unnotified", "", 0)); !reflect.DeepEqual(got, []string{"/data/unnotified.txt"}) {
		t.Fatalf("alice via parent root: %v", got)
	}
}

func TestCollapse(t *testing.T) {
	sep := string(filepath.Separator)
	j := func(parts ...string) string { return sep + strings.Join(parts, sep) }
	in := []string{j("a", "c"), j("a-b"), j("a"), j("a", "c", "d"), j("a-b", "x"), j("b"), j("a")}
	got := collapse(in)
	want := []string{j("a"), j("a-b"), j("b")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collapse = %v, want %v", got, want)
	}
}

func TestTopRootsNestedWithSiblingPrefix(t *testing.T) {
	st := openStore(t)
	base := realTemp(t)
	for _, d := range []string{"a", "a-b", "a/c"} {
		os.MkdirAll(filepath.Join(base, d), 0o755)
	}
	addUser(t, st, "u1", store.Mount{Name: "a", Path: filepath.Join(base, "a")})
	addUser(t, st, "u2", store.Mount{Name: "ab", Path: filepath.Join(base, "a-b")})
	addUser(t, st, "u3", store.Mount{Name: "ac", Path: filepath.Join(base, "a", "c")})
	addUser(t, st, "u4", store.Mount{Name: "missing", Path: filepath.Join(base, "missing")})
	x := New(st)
	got := x.topRoots()
	want := []string{filepath.Join(base, "a"), filepath.Join(base, "a-b")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("topRoots = %v, want %v", got, want)
	}
}

func TestSymlinksNotIndexed(t *testing.T) {
	f := newFixture(t)
	outside := realTemp(t)
	touch(t, filepath.Join(outside, "outsider.txt"))
	if err := os.Symlink(outside, filepath.Join(f.data, "link")); err != nil {
		t.Skip(err)
	}
	f.x.Rebuild()
	if got := paths(f.x.Search(f.fs(f.alice), "/", "outsider", "", 0)); len(got) != 0 {
		t.Fatalf("followed symlink: %v", got)
	}
	if got := paths(f.x.Search(f.fs(f.alice), "/", "link", "", 0)); len(got) != 0 {
		t.Fatalf("symlink itself indexed: %v", got)
	}
}

func TestCompaction(t *testing.T) {
	f := newFixture(t)
	bulk := filepath.Join(f.data, "bulk")
	for i := 0; i < 1500; i++ {
		touch(t, filepath.Join(bulk, fmt.Sprintf("f%04d.bin", i)))
	}
	f.x.Changed(bulk)
	f.x.flush()
	ri := f.x.indexes[f.data]
	if n := ri.count(); n != 1511 {
		t.Fatalf("count = %d", n)
	}
	os.RemoveAll(bulk)
	f.x.Changed(bulk)
	f.x.flush()
	ri.mu.RLock()
	items, deleted := len(ri.items), ri.deleted
	ri.mu.RUnlock()
	if deleted != 0 || items != 10 {
		t.Fatalf("after compaction: items=%d deleted=%d", items, deleted)
	}
	if got := paths(f.x.Search(f.fs(f.alice), "/", "report", "", 0)); len(got) != 3 {
		t.Fatalf("search after compaction: %v", got)
	}
	if got := paths(f.x.Search(f.fs(f.alice), "/", "f0001", "", 0)); len(got) != 0 {
		t.Fatalf("deleted bulk still found: %v", got)
	}
}

// Changes applied while a rebuild walk runs must survive the swap.
func TestChangeDuringRebuildIsReplayed(t *testing.T) {
	f := newFixture(t)
	f.x.beginWalk()
	touch(t, filepath.Join(f.data, "during.txt"))
	f.x.Changed(filepath.Join(f.data, "during.txt"))
	f.x.flush()
	// Simulate the walk having missed the file: swap in an index without it.
	f.x.mu.Lock()
	f.x.indexes[f.data] = newRootIndex(f.data)
	f.x.mu.Unlock()
	f.x.endWalk()
	f.x.flush()
	if got := paths(f.x.Search(f.fs(f.alice), "/", "during", "", 0)); !reflect.DeepEqual(got, []string{"/data/during.txt"}) {
		t.Fatalf("replayed change lost: %v", got)
	}
}

func TestConcurrentSearchAndUpdates(t *testing.T) {
	f := newFixture(t)
	fsA := f.fs(f.alice)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				f.x.Search(fsA, "/", "re", "", 10)
				f.x.Status()
			}
		}()
	}
	for i := 0; i < 50; i++ {
		fsA.WriteFile(fmt.Sprintf("/data/c/%d.txt", i), strings.NewReader("x"), true)
		f.x.flush()
		if i%10 == 0 {
			f.x.Rebuild()
			f.x.SyncRoots()
		}
	}
	close(stop)
	wg.Wait()
}

// ---------- benchmark ----------

type fakeInfo struct {
	name string
	dir  bool
}

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 1234 }
func (f fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (f fakeInfo) ModTime() time.Time { return time.Unix(1700000000, 0) }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() any           { return nil }

var benchWords = []string{"report", "photo", "IMG", "budget", "会议记录", "年度报告", "invoice", "backup", "Screenshot",
	"README", "main", "index", "holiday", "beach", "family", "project", "draft", "final", "v2", "notes", "music", "video"}
var benchExts = []string{".jpg", ".png", ".mp4", ".mkv", ".docx", ".pdf", ".txt", ".go", ".md", ".zip", ".flac", ".xlsx"}

func syntheticIndexer(tb testing.TB, n int) (*Indexer, *vfs.FS) {
	root := realTemp(tb)
	ri := newRootIndex(root)
	seed := uint32(12345)
	rnd := func(k int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % k
	}
	dirs := []string{""}
	for len(ri.items) < n {
		d := dirs[rnd(len(dirs))]
		if rnd(12) == 0 && len(dirs) < n/20 {
			name := fmt.Sprintf("%s %d", benchWords[rnd(len(benchWords))], rnd(1000))
			ri.add(d, fakeInfo{name: name, dir: true})
			if d == "" {
				dirs = append(dirs, name)
			} else {
				dirs = append(dirs, d+"/"+name)
			}
			continue
		}
		name := fmt.Sprintf("%s_%s-%d%s", benchWords[rnd(len(benchWords))], benchWords[rnd(len(benchWords))], rnd(100000),
			benchExts[rnd(len(benchExts))])
		ri.add(d, fakeInfo{name: name})
	}
	x := New(nil)
	x.indexes[root] = ri
	return x, vfs.New([]store.Mount{{Name: "m", Path: root}}, false, nil)
}

var benchQueries = []string{"report", "rpt", "年度", "img 2024", "beach jpg", "s", "zzzzz", "holiday/ final", "budgetfinalxlsx"}

func BenchmarkSearch200k(b *testing.B) {
	x, fsys := syntheticIndexer(b, 200_000)
	for _, q := range benchQueries {
		b.Run(strings.ReplaceAll(q, "/", "_"), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				x.Search(fsys, "/", q, "", 100)
			}
		})
	}
}

func TestSearch200kUnder100ms(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	x, fsys := syntheticIndexer(t, 200_000)
	if c := x.indexes[fsys.Roots()[0].Path].count(); c != 200_000 {
		t.Fatalf("count %d", c)
	}
	for _, q := range benchQueries {
		x.Search(fsys, "/", q, "", 100) // warm up
		best := time.Hour
		for i := 0; i < 3; i++ {
			began := time.Now()
			r := x.Search(fsys, "/", q, "", 100)
			best = min(best, time.Since(began))
			if q == "report" && (r.Total == 0 || len(r.Results) != 100) {
				t.Fatalf("report: total %d", r.Total)
			}
		}
		// Generous bound so the race detector / slow CI do not flake; the
		// benchmark reports the real numbers.
		limit := 100 * time.Millisecond
		if raceEnabled {
			limit = 2 * time.Second
		}
		if best > limit {
			t.Errorf("query %q took %v", q, best)
		}
	}
}
