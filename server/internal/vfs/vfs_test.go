package vfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

type recNotifier struct {
	mu    sync.Mutex
	paths []string
}

func (r *recNotifier) Changed(p string) {
	r.mu.Lock()
	r.paths = append(r.paths, p)
	r.mu.Unlock()
}

func (r *recNotifier) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.paths
	r.paths = nil
	return out
}

// env creates two mount roots ("rw" writable, "ro" read-only) plus an
// "outside" directory that must never be reachable.
type env struct {
	t       *testing.T
	rw, ro  string
	outside string
	fs      *FS
	n       *recNotifier
}

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, rw: realDir(t), ro: realDir(t), outside: realDir(t), n: &recNotifier{}}
	e.write(filepath.Join(e.outside, "secret.txt"), "secret")
	os.MkdirAll(filepath.Join(e.outside, "sdir"), 0o755)
	e.write(filepath.Join(e.outside, "sdir", "inner.txt"), "inner")
	e.write(filepath.Join(e.ro, "readme.txt"), "ro")
	e.fs = New([]store.Mount{
		{Name: "rw", Path: e.rw, Writable: true},
		{Name: "ro", Path: e.ro, Writable: false},
	}, false, e.n)
	return e
}

func (e *env) write(p, content string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) symlink(target, link string) {
	e.t.Helper()
	if err := os.Symlink(target, link); err != nil {
		e.t.Skipf("symlinks unsupported: %v", err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func wantErr(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got err %v, want %v", what, err, want)
	}
}

func TestCleanPath(t *testing.T) {
	cases := map[string]string{
		"":              "/",
		"/":             "/",
		"a/b":           "/a/b",
		"/a/../b":       "/b",
		"/../../etc":    "/etc",
		`\a\..\..\b`:    "/b",
		"/a//b/./c/":    "/a/b/c",
		"/a/b/../../..": "/",
	}
	for in, want := range cases {
		if got := CleanPath(in); got != want {
			t.Errorf("CleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocateBasics(t *testing.T) {
	e := newEnv(t)
	l, err := e.fs.Locate("/")
	if err != nil || !l.IsVirtualRoot() {
		t.Fatalf("virtual root: %v %+v", err, l)
	}
	l, err = e.fs.Locate("/rw")
	if err != nil || !l.IsMountRoot() || l.Real != e.rw {
		t.Fatalf("mount root: %v %+v", err, l)
	}
	l, err = e.fs.Locate("rw/a/b.txt")
	if err != nil || l.Rel != "a/b.txt" || l.Real != filepath.Join(e.rw, "a", "b.txt") || l.VPath != "/rw/a/b.txt" {
		t.Fatalf("nested: %v %+v", err, l)
	}
	_, err = e.fs.Locate("/nope/x")
	wantErr(t, err, ErrNotFound, "unknown mount")
	_, err = e.fs.Locate("/RW/x")
	wantErr(t, err, ErrNotFound, "mount names are case sensitive")
	_, err = e.fs.Locate("/rw/a\x00b")
	wantErr(t, err, ErrInvalid, "NUL")
}

func TestLocateTraversalStaysInside(t *testing.T) {
	e := newEnv(t)
	tricky := []string{
		"/rw/../ro/readme.txt",
		"/rw/../../../../" + strings.TrimPrefix(e.outside, "/") + "/secret.txt",
		`/rw\..\..\secret.txt`,
		`\rw\..\..\..\etc\passwd`,
		"/rw/%2e%2e/%2e%2e/secret.txt",
		"/rw/..%2f..%2fsecret.txt",
		"/rw/./../rw/../../x",
		"//rw//..//..//x",
	}
	for _, p := range tricky {
		l, err := e.fs.Locate(p)
		if err != nil {
			continue // rejected is fine
		}
		if l.Root == nil {
			continue
		}
		if !within(l.Root.Path, l.Real) {
			t.Errorf("Locate(%q) escaped: %s", p, l.Real)
		}
		if strings.HasPrefix(l.Real, e.outside) {
			t.Errorf("Locate(%q) reached outside dir: %s", p, l.Real)
		}
	}
	// "%2e%2e" is a literal file name, never decoded.
	l, err := e.fs.Locate("/rw/%2e%2e/x")
	if err != nil || l.Real != filepath.Join(e.rw, "%2e%2e", "x") {
		t.Fatalf("encoded dots: %v %+v", err, l)
	}
}

func TestLocateSymlinks(t *testing.T) {
	e := newEnv(t)
	os.MkdirAll(filepath.Join(e.rw, "in", "deep"), 0o755)
	e.write(filepath.Join(e.rw, "in", "f.txt"), "hello")
	e.symlink(filepath.Join(e.outside, "secret.txt"), filepath.Join(e.rw, "outfile"))
	e.symlink(filepath.Join(e.outside, "sdir"), filepath.Join(e.rw, "outdir"))
	e.symlink("../"+filepath.Base(e.outside)+"/sdir", filepath.Join(e.rw, "reloutdir"))
	e.symlink(filepath.Join(e.outside, "missing"), filepath.Join(e.rw, "dangling-out"))
	e.symlink(filepath.Join(e.outside, "missing-dir", "x"), filepath.Join(e.rw, "dangling-out2"))
	e.symlink("in/f.txt", filepath.Join(e.rw, "infile"))
	e.symlink("in", filepath.Join(e.rw, "indir"))
	e.symlink(filepath.Join(e.rw, "in", "missing"), filepath.Join(e.rw, "dangling-in"))
	// chain: rw/chain -> rw/indir2 -> outside
	e.symlink(filepath.Join(e.outside, "sdir"), filepath.Join(e.rw, "in", "hop"))
	e.symlink("in/hop", filepath.Join(e.rw, "chain"))
	// loop
	e.symlink("loop2", filepath.Join(e.rw, "loop1"))
	e.symlink("loop1", filepath.Join(e.rw, "loop2"))

	denied := []string{
		"/rw/outfile",
		"/rw/outdir",
		"/rw/outdir/inner.txt",
		"/rw/outdir/new/not/existing.txt",
		"/rw/reloutdir/inner.txt",
		"/rw/dangling-out",
		"/rw/dangling-out2",
		"/rw/chain",
		"/rw/chain/inner.txt",
		"/rw/in/hop/x/y/z",
		"/rw/loop1",
		"/rw/loop1/x",
	}
	for _, p := range denied {
		if _, err := e.fs.Locate(p); err == nil {
			t.Errorf("Locate(%q) should be denied", p)
		} else if !errors.Is(err, ErrPermission) {
			t.Errorf("Locate(%q) = %v, want ErrPermission", p, err)
		}
	}
	allowed := []string{"/rw/infile", "/rw/indir", "/rw/indir/f.txt", "/rw/indir/deep/new/file", "/rw/dangling-in", "/rw/new/a/b"}
	for _, p := range allowed {
		if _, err := e.fs.Locate(p); err != nil {
			t.Errorf("Locate(%q) = %v, want ok", p, err)
		}
	}
	// Reading through an inside symlink works.
	fh, _, err := e.fs.Open("/rw/indir/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(fh)
	fh.Close()
	if string(b) != "hello" {
		t.Fatalf("read via link: %q", b)
	}
	// Writes through escaping links are refused and leave outside untouched.
	if _, err := e.fs.WriteFile("/rw/outdir/pwn.txt", strings.NewReader("x"), true); !errors.Is(err, ErrPermission) {
		t.Errorf("WriteFile via outdir: %v", err)
	}
	if fh, _, err := e.fs.OpenFile("/rw/dangling-out", os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fh.Close()
		t.Errorf("OpenFile(O_CREATE) through dangling outside link succeeded")
	}
	if _, err := e.fs.Mkdir("/rw/outdir/newdir"); !errors.Is(err, ErrPermission) {
		t.Errorf("Mkdir via outdir: %v", err)
	}
	if exists(filepath.Join(e.outside, "missing")) || exists(filepath.Join(e.outside, "sdir", "pwn.txt")) ||
		exists(filepath.Join(e.outside, "sdir", "newdir")) {
		t.Fatal("something was created outside the root")
	}
	// Removing an escaping link is refused as well (cannot even locate it).
	if err := e.fs.Remove("/rw/outdir"); !errors.Is(err, ErrPermission) {
		t.Errorf("Remove outdir: %v", err)
	}
	if _, err := e.fs.Stat("/rw/outfile"); !errors.Is(err, ErrPermission) {
		t.Errorf("Stat outfile: %v", err)
	}
	if _, _, err := e.fs.List("/rw/outdir"); !errors.Is(err, ErrPermission) {
		t.Errorf("List outdir: %v", err)
	}
}

func TestMountRootSymlinkResolved(t *testing.T) {
	base := realDir(t)
	target := filepath.Join(base, "data")
	os.MkdirAll(target, 0o755)
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	os.WriteFile(filepath.Join(target, "a.txt"), []byte("a"), 0o644)
	f := New([]store.Mount{{Name: "m", Path: link + "/", Writable: true}}, false, nil)
	if f.Root("m").Path != target {
		t.Fatalf("root path %q, want %q", f.Root("m").Path, target)
	}
	if _, err := f.Stat("/m/a.txt"); err != nil {
		t.Fatal(err)
	}
}

func names(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func TestList(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "b.txt"), "b")
	e.write(filepath.Join(e.rw, "A.txt"), "a")
	e.write(filepath.Join(e.rw, TempPrefix+"123"), "partial")
	os.MkdirAll(filepath.Join(e.rw, "zdir"), 0o755)
	os.MkdirAll(filepath.Join(e.rw, "Adir"), 0o755)
	e.symlink(filepath.Join(e.outside, "secret.txt"), filepath.Join(e.rw, "evil"))
	e.symlink(filepath.Join(e.outside, "sdir"), filepath.Join(e.rw, "evildir"))
	e.symlink("b.txt", filepath.Join(e.rw, "good"))
	e.symlink("zdir", filepath.Join(e.rw, "gooddir"))
	e.symlink("nothing", filepath.Join(e.rw, "broken"))

	es, writable, err := e.fs.List("/rw")
	if err != nil {
		t.Fatal(err)
	}
	if !writable {
		t.Error("rw should be writable")
	}
	got := strings.Join(names(es), ",")
	want := "Adir,gooddir,zdir,A.txt,b.txt,good"
	if got != want {
		t.Fatalf("List = %s, want %s", got, want)
	}
	for _, en := range es {
		if en.Name == "gooddir" && !en.IsDir {
			t.Error("gooddir should be reported as a dir")
		}
		if en.Name == "good" && (en.Size != 1 || en.Path != "/rw/good") {
			t.Errorf("good entry: %+v", en)
		}
	}

	es, writable, err = e.fs.List("/")
	if err != nil || writable {
		t.Fatalf("root list: %v %v", err, writable)
	}
	if got := strings.Join(names(es), ","); got != "rw,ro" {
		t.Fatalf("root list = %s", got)
	}
	for _, en := range es {
		if en.Name == "ro" && en.Writable {
			t.Error("ro root should not be writable")
		}
	}
	if _, _, err := e.fs.List("/rw/b.txt"); err == nil {
		t.Error("listing a file should fail")
	}
	if _, _, err := e.fs.List("/rw/missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("listing missing: %v", err)
	}
}

func TestStatAndLocalFile(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "doc.pdf"), "12345")
	st, err := e.fs.Stat("/rw/doc.pdf")
	if err != nil || st.Size != 5 || st.Kind != KindPDF || st.Mime != "application/pdf" || !st.Writable {
		t.Fatalf("stat: %v %+v", err, st)
	}
	st, err = e.fs.Stat("/ro")
	if err != nil || !st.IsDir || st.Writable || st.Name != "ro" {
		t.Fatalf("stat root: %v %+v", err, st)
	}
	st, err = e.fs.Stat("/")
	if err != nil || !st.IsDir || st.Path != "/" {
		t.Fatalf("stat vroot: %v %+v", err, st)
	}
	if _, _, err := e.fs.LocalFile("/rw"); !errors.Is(err, ErrIsDir) {
		t.Errorf("LocalFile dir: %v", err)
	}
	if _, _, err := e.fs.LocalFile("/"); !errors.Is(err, ErrIsDir) {
		t.Errorf("LocalFile vroot: %v", err)
	}
	if _, err := e.fs.Stat("/rw/doc.pdf/x"); err == nil {
		t.Error("stat below a file should fail")
	}
}

func writeOps(f *FS) map[string]func() error {
	return map[string]func() error{
		"WriteFile": func() error { _, err := f.WriteFile("/ro/new.txt", strings.NewReader("x"), true); return err },
		"WriteFileOverwrite": func() error {
			_, err := f.WriteFile("/ro/readme.txt", strings.NewReader("x"), true)
			return err
		},
		"Mkdir":       func() error { _, err := f.Mkdir("/ro/newdir"); return err },
		"Rename":      func() error { _, err := f.Rename("/ro/readme.txt", "x.txt"); return err },
		"MoveOut":     func() error { return f.Move("/ro/readme.txt", "/rw/x.txt", false) },
		"MoveIn":      func() error { return f.Move("/rw/w.txt", "/ro/w.txt", false) },
		"MoveInside":  func() error { return f.Move("/ro/readme.txt", "/ro/y.txt", false) },
		"CopyIn":      func() error { return f.Copy("/rw/w.txt", "/ro/w.txt", false) },
		"CopyInside":  func() error { return f.Copy("/ro/readme.txt", "/ro/copy.txt", false) },
		"Remove":      func() error { return f.Remove("/ro/readme.txt") },
		"OpenWrite":   func() error { return openErr(f, "/ro/readme.txt", os.O_WRONLY) },
		"OpenRDWR":    func() error { return openErr(f, "/ro/readme.txt", os.O_RDWR) },
		"OpenCreate":  func() error { return openErr(f, "/ro/c.txt", os.O_RDONLY|os.O_CREATE) },
		"OpenTrunc":   func() error { return openErr(f, "/ro/readme.txt", os.O_RDONLY|os.O_TRUNC) },
		"OpenAppend":  func() error { return openErr(f, "/ro/readme.txt", os.O_WRONLY|os.O_APPEND) },
		"OpenNewFile": func() error { return openErr(f, "/ro/sub/n.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL) },
	}
}

func openErr(f *FS, p string, flag int) error {
	fh, _, err := f.OpenFile(p, flag, 0o644)
	if fh != nil {
		fh.Close()
	}
	return err
}

func TestReadOnlyMountRejectsWrites(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "w.txt"), "w")
	for name, op := range writeOps(e.fs) {
		if err := op(); !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s on ro mount: got %v, want ErrReadOnly", name, err)
		}
	}
	if read(t, filepath.Join(e.ro, "readme.txt")) != "ro" || exists(filepath.Join(e.ro, "new.txt")) ||
		exists(filepath.Join(e.ro, "newdir")) || exists(filepath.Join(e.ro, "c.txt")) || !exists(filepath.Join(e.rw, "w.txt")) {
		t.Fatal("read-only mount was modified")
	}
	// Reading is fine.
	fh, _, err := e.fs.OpenFile("/ro/readme.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fh.Close()
	if len(e.n.take()) != 0 {
		t.Error("notifier should not fire for rejected writes")
	}
}

func TestReadOnlyFSRejectsAllWrites(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "w.txt"), "w")
	f := New([]store.Mount{{Name: "rw", Path: e.rw, Writable: true}, {Name: "ro", Path: e.ro}}, true, nil)
	if !f.ReadOnly() || f.Root("rw").Writable {
		t.Fatal("readOnly FS must make every root read-only")
	}
	ops := map[string]func() error{
		"WriteFile": func() error { _, err := f.WriteFile("/rw/n.txt", strings.NewReader("x"), true); return err },
		"Mkdir":     func() error { _, err := f.Mkdir("/rw/d"); return err },
		"Rename":    func() error { _, err := f.Rename("/rw/w.txt", "z.txt"); return err },
		"Move":      func() error { return f.Move("/rw/w.txt", "/rw/z.txt", true) },
		"Copy":      func() error { return f.Copy("/rw/w.txt", "/rw/z.txt", true) },
		"Remove":    func() error { return f.Remove("/rw/w.txt") },
		"OpenW":     func() error { return openErr(f, "/rw/w.txt", os.O_WRONLY) },
		"OpenC":     func() error { return openErr(f, "/rw/c.txt", os.O_CREATE|os.O_RDWR) },
	}
	for name, op := range ops {
		if err := op(); !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s on readOnly FS: got %v", name, err)
		}
	}
	es, writable, err := f.List("/rw")
	if err != nil || writable || len(es) != 1 || es[0].Writable {
		t.Fatalf("list on read-only FS: %v %v %+v", err, writable, es)
	}
	if read(t, filepath.Join(e.rw, "w.txt")) != "w" || exists(filepath.Join(e.rw, "z.txt")) {
		t.Fatal("read-only FS modified data")
	}
}

func TestRootsCannotBeModified(t *testing.T) {
	e := newEnv(t)
	if err := e.fs.Remove("/"); !errors.Is(err, ErrPermission) {
		t.Errorf("remove vroot: %v", err)
	}
	if err := e.fs.Remove("/rw"); !errors.Is(err, ErrPermission) {
		t.Errorf("remove mount root: %v", err)
	}
	if err := e.fs.Remove("/rw/"); !errors.Is(err, ErrPermission) {
		t.Errorf("remove mount root with slash: %v", err)
	}
	if err := e.fs.Remove("/rw/sub/.."); !errors.Is(err, ErrPermission) {
		t.Errorf("remove mount root via dots: %v", err)
	}
	if _, err := e.fs.Rename("/rw", "other"); !errors.Is(err, ErrPermission) {
		t.Errorf("rename mount root: %v", err)
	}
	if _, err := e.fs.Rename("/", "other"); err == nil {
		t.Error("rename vroot should fail")
	}
	if err := e.fs.Move("/rw", "/rw2", false); !errors.Is(err, ErrPermission) {
		t.Errorf("move mount root: %v", err)
	}
	e.write(filepath.Join(e.rw, "f.txt"), "f")
	if err := e.fs.Move("/rw/f.txt", "/rw", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("move onto mount root: %v", err)
	}
	if err := e.fs.Move("/rw/f.txt", "/", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("move onto vroot: %v", err)
	}
	if err := e.fs.Copy("/rw/f.txt", "/rw", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("copy onto mount root: %v", err)
	}
	if err := e.fs.Copy("/", "/rw/x", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("copy vroot: %v", err)
	}
	if _, err := e.fs.WriteFile("/rw", strings.NewReader("x"), true); !errors.Is(err, ErrInvalid) {
		t.Errorf("write mount root: %v", err)
	}
	if _, err := e.fs.Mkdir("/rw"); !errors.Is(err, ErrExists) {
		t.Errorf("mkdir mount root: %v", err)
	}
	if _, err := e.fs.Mkdir("/newmount"); !errors.Is(err, ErrNotFound) {
		t.Errorf("mkdir at vroot: %v", err)
	}
	if !exists(e.rw) || read(t, filepath.Join(e.rw, "f.txt")) != "f" {
		t.Fatal("root modified")
	}
}

func TestInvalidNames(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "f.txt"), "f")
	for _, n := range []string{"", ".", "..", "a/b", `a\b`, "a\x00b", strings.Repeat("x", 256), TempPrefix + "x"} {
		if _, err := e.fs.Rename("/rw/f.txt", n); !errors.Is(err, ErrInvalid) {
			t.Errorf("Rename to %q: %v", n, err)
		}
	}
	if _, err := e.fs.WriteFile("/rw/"+TempPrefix+"abc", strings.NewReader("x"), true); !errors.Is(err, ErrInvalid) {
		t.Errorf("write temp name: %v", err)
	}
	if _, err := e.fs.WriteFile("/rw/"+TempPrefix+"dir/a.txt", strings.NewReader("x"), true); !errors.Is(err, ErrInvalid) {
		t.Errorf("write into temp-named dir: %v", err)
	}
	if _, err := e.fs.Mkdir("/rw/" + TempPrefix + "d"); !errors.Is(err, ErrInvalid) {
		t.Errorf("mkdir temp name: %v", err)
	}
	if err := e.fs.Move("/rw/f.txt", "/rw/"+TempPrefix+"f", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("move to temp name: %v", err)
	}
	if err := e.fs.Copy("/rw/f.txt", "/rw/"+TempPrefix+"f", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("copy to temp name: %v", err)
	}
}

func TestWriteFile(t *testing.T) {
	e := newEnv(t)
	en, err := e.fs.WriteFile("/rw/a/b/c.txt", strings.NewReader("hello"), false)
	if err != nil {
		t.Fatal(err)
	}
	if en.Size != 5 || en.Path != "/rw/a/b/c.txt" || en.Name != "c.txt" {
		t.Fatalf("entry %+v", en)
	}
	if read(t, filepath.Join(e.rw, "a", "b", "c.txt")) != "hello" {
		t.Fatal("content")
	}
	if got := e.n.take(); len(got) != 1 || got[0] != filepath.Join(e.rw, "a", "b", "c.txt") {
		t.Fatalf("notify %v", got)
	}
	// no clobber
	if _, err := e.fs.WriteFile("/rw/a/b/c.txt", strings.NewReader("other"), false); !errors.Is(err, ErrExists) {
		t.Fatalf("overwrite=false: %v", err)
	}
	if read(t, filepath.Join(e.rw, "a", "b", "c.txt")) != "hello" {
		t.Fatal("clobbered")
	}
	// overwrite
	if _, err := e.fs.WriteFile("/rw/a/b/c.txt", strings.NewReader("new"), true); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(e.rw, "a", "b", "c.txt")) != "new" {
		t.Fatal("overwrite content")
	}
	// directory target
	if _, err := e.fs.WriteFile("/rw/a/b", strings.NewReader("x"), true); !errors.Is(err, ErrIsDir) {
		t.Fatalf("write over dir: %v", err)
	}
	// parent is a file
	if _, err := e.fs.WriteFile("/rw/a/b/c.txt/d", strings.NewReader("x"), true); err == nil {
		t.Fatal("write below a file should fail")
	}
	// reader error: no partial file, no temp leftovers, old content kept
	_, err = e.fs.WriteFile("/rw/a/b/c.txt", io.MultiReader(strings.NewReader("partial"), errReader{}), true)
	if err == nil {
		t.Fatal("expected reader error")
	}
	if read(t, filepath.Join(e.rw, "a", "b", "c.txt")) != "new" {
		t.Fatal("failed write replaced content")
	}
	_, err = e.fs.WriteFile("/rw/a/b/fresh.txt", io.MultiReader(strings.NewReader("partial"), errReader{}), false)
	if err == nil || exists(filepath.Join(e.rw, "a", "b", "fresh.txt")) {
		t.Fatalf("partial file left: %v", err)
	}
	des, _ := os.ReadDir(filepath.Join(e.rw, "a", "b"))
	for _, de := range des {
		if strings.HasPrefix(de.Name(), TempPrefix) {
			t.Fatalf("temp file left behind: %s", de.Name())
		}
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// A reader that creates the destination while the upload is in flight
// simulates a concurrent upload; overwrite=false must not clobber it.
type racingReader struct {
	dst  string
	done bool
}

func (r *racingReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	os.WriteFile(r.dst, []byte("winner"), 0o644)
	return copy(p, "loser"), nil
}

func TestWriteFileNoClobberRace(t *testing.T) {
	e := newEnv(t)
	dst := filepath.Join(e.rw, "race.txt")
	_, err := e.fs.WriteFile("/rw/race.txt", &racingReader{dst: dst}, false)
	if !errors.Is(err, ErrExists) {
		t.Fatalf("got %v, want ErrExists", err)
	}
	if read(t, dst) != "winner" {
		t.Fatal("concurrent file was clobbered")
	}
	des, _ := os.ReadDir(e.rw)
	if len(des) != 1 {
		t.Fatalf("leftovers: %v", des)
	}
}

func TestWriteFileConcurrentNoClobber(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	okCount := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.fs.WriteFile("/rw/same.txt", strings.NewReader(strings.Repeat("x", i+1)), false)
			if err == nil {
				mu.Lock()
				okCount++
				mu.Unlock()
			} else if !errors.Is(err, ErrExists) {
				t.Errorf("unexpected: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if okCount != 1 {
		t.Fatalf("%d writers succeeded, want exactly 1", okCount)
	}
}

func TestMkdir(t *testing.T) {
	e := newEnv(t)
	en, err := e.fs.Mkdir("/rw/x/y")
	if err != nil || !en.IsDir || en.Kind != KindDir {
		t.Fatalf("mkdir: %v %+v", err, en)
	}
	if _, err := e.fs.Mkdir("/rw/x/y"); !errors.Is(err, ErrExists) {
		t.Fatalf("mkdir again: %v", err)
	}
	if got := e.n.take(); len(got) != 1 {
		t.Fatalf("notify %v", got)
	}
}

func TestRenameAndCaseOnly(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "a.txt"), "a")
	e.write(filepath.Join(e.rw, "b.txt"), "b")
	if _, err := e.fs.Rename("/rw/a.txt", "b.txt"); !errors.Is(err, ErrExists) {
		t.Fatalf("rename onto existing: %v", err)
	}
	en, err := e.fs.Rename("/rw/a.txt", "c.txt")
	if err != nil || en.Path != "/rw/c.txt" {
		t.Fatalf("rename: %v %+v", err, en)
	}
	if exists(filepath.Join(e.rw, "a.txt")) || read(t, filepath.Join(e.rw, "c.txt")) != "a" {
		t.Fatal("rename result")
	}
	got := e.n.take()
	sort.Strings(got)
	if len(got) != 2 || got[0] != filepath.Join(e.rw, "a.txt") || got[1] != filepath.Join(e.rw, "c.txt") {
		t.Fatalf("notify %v", got)
	}
	// Case-only rename works both on case-sensitive and case-insensitive filesystems.
	en, err = e.fs.Rename("/rw/c.txt", "C.txt")
	if err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
	des, _ := os.ReadDir(e.rw)
	if n := strings.Join(func() []string {
		var s []string
		for _, d := range des {
			s = append(s, d.Name())
		}
		return s
	}(), ","); n != "C.txt,b.txt" {
		t.Fatalf("after case rename: %s", n)
	}
	if read(t, filepath.Join(e.rw, "C.txt")) != "a" {
		t.Fatal("content lost on case-only rename")
	}
	// Renaming to the same name is a no-op.
	if _, err := e.fs.Rename("/rw/C.txt", "C.txt"); err != nil {
		t.Fatalf("same-name rename: %v", err)
	}
	if _, err := e.fs.Rename("/rw/missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename missing: %v", err)
	}
}

func TestMove(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "dir", "sub", "f.txt"), "f")
	e.write(filepath.Join(e.rw, "x.txt"), "x")
	e.write(filepath.Join(e.rw, "y.txt"), "y")

	if err := e.fs.Move("/rw/dir", "/rw/dir/sub/dir", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move into own subdir: %v", err)
	}
	if err := e.fs.Move("/rw/dir", "/rw/dir/new", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move into own child: %v", err)
	}
	// Overwriting a directory that contains the source would delete the source.
	if err := e.fs.Move("/rw/dir/sub", "/rw/dir", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move onto own ancestor: %v", err)
	}
	if read(t, filepath.Join(e.rw, "dir", "sub", "f.txt")) != "f" {
		t.Fatal("source damaged")
	}
	if err := e.fs.Move("/rw/x.txt", "/rw/y.txt", false); !errors.Is(err, ErrExists) {
		t.Fatalf("move no overwrite: %v", err)
	}
	if err := e.fs.Move("/rw/x.txt", "/rw/y.txt", true); err != nil {
		t.Fatalf("move overwrite: %v", err)
	}
	if exists(filepath.Join(e.rw, "x.txt")) || read(t, filepath.Join(e.rw, "y.txt")) != "x" {
		t.Fatal("overwrite result")
	}
	// Moving into a new nested destination creates parents.
	if err := e.fs.Move("/rw/y.txt", "/rw/new/deep/y.txt", false); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(e.rw, "new", "deep", "y.txt")) != "x" {
		t.Fatal("nested move")
	}
	// A dir named "dirx" is not inside "dir".
	if err := e.fs.Move("/rw/dir", "/rw/dirx", false); err != nil {
		t.Fatalf("sibling with common prefix: %v", err)
	}
	if err := e.fs.Move("/rw/missing", "/rw/z", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing source: %v", err)
	}
}

func TestMoveCrossMount(t *testing.T) {
	a, b := realDir(t), realDir(t)
	n := &recNotifier{}
	f := New([]store.Mount{{Name: "a", Path: a, Writable: true}, {Name: "b", Path: b, Writable: true}}, false, n)
	os.MkdirAll(filepath.Join(a, "d", "e"), 0o755)
	os.WriteFile(filepath.Join(a, "d", "e", "f.txt"), []byte("data"), 0o644)
	if err := f.Move("/a/d", "/b/moved", false); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(a, "d")) || read(t, filepath.Join(b, "moved", "e", "f.txt")) != "data" {
		t.Fatal("cross mount move result")
	}
	got := n.take()
	sort.Strings(got)
	want := []string{filepath.Join(a, "d"), filepath.Join(b, "moved")}
	sort.Strings(want)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("notify %v want %v", got, want)
	}
}

func TestMoveSymlinkTricks(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "a", "f.txt"), "f")
	e.symlink("a", filepath.Join(e.rw, "alias"))
	// Moving a into a subdirectory of itself via an alias must fail, not loop.
	if err := e.fs.Move("/rw/a", "/rw/alias/inner", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("move via alias: %v", err)
	}
	if err := e.fs.Copy("/rw/a", "/rw/alias/inner", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("copy via alias: %v", err)
	}
	if read(t, filepath.Join(e.rw, "a", "f.txt")) != "f" || exists(filepath.Join(e.rw, "a", "inner")) {
		t.Fatal("source damaged")
	}
}

func TestCopy(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "tree", "a.txt"), "a")
	e.write(filepath.Join(e.rw, "tree", "sub", "b.txt"), "b")
	os.MkdirAll(filepath.Join(e.rw, "tree", "empty"), 0o755)
	e.symlink(filepath.Join(e.outside, "secret.txt"), filepath.Join(e.rw, "tree", "evil"))
	e.symlink(filepath.Join(e.outside, "sdir"), filepath.Join(e.rw, "tree", "sub", "evildir"))
	e.symlink("a.txt", filepath.Join(e.rw, "tree", "inlink"))
	mt := filepath.Join(e.rw, "tree", "a.txt")
	fi0, _ := os.Stat(mt)

	if err := e.fs.Copy("/rw/tree", "/rw/tree/sub/copy", false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("copy into own subdir: %v", err)
	}
	if err := e.fs.Copy("/rw/tree", "/rw/tree", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("copy onto itself: %v", err)
	}
	if err := e.fs.Copy("/rw/tree/sub", "/rw/tree", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("copy onto own ancestor: %v", err)
	}
	if err := e.fs.Copy("/rw/tree", "/rw/copy", false); err != nil {
		t.Fatal(err)
	}
	c := filepath.Join(e.rw, "copy")
	if read(t, filepath.Join(c, "a.txt")) != "a" || read(t, filepath.Join(c, "sub", "b.txt")) != "b" {
		t.Fatal("copy content")
	}
	if fi, err := os.Stat(filepath.Join(c, "empty")); err != nil || !fi.IsDir() {
		t.Fatal("empty dir not copied")
	}
	for _, l := range []string{"evil", "inlink", filepath.Join("sub", "evildir")} {
		if exists(filepath.Join(c, l)) {
			t.Errorf("symlink %s was copied", l)
		}
	}
	if fi, _ := os.Stat(filepath.Join(c, "a.txt")); !fi.ModTime().Equal(fi0.ModTime()) {
		t.Error("mtime not preserved")
	}
	// Source untouched.
	if read(t, mt) != "a" {
		t.Fatal("source modified")
	}
	if got := e.n.take(); len(got) != 1 || got[0] != c {
		t.Fatalf("notify %v", got)
	}
	// Overwrite semantics.
	e.write(filepath.Join(e.rw, "one.txt"), "1")
	e.write(filepath.Join(e.rw, "two.txt"), "2")
	if err := e.fs.Copy("/rw/one.txt", "/rw/two.txt", false); !errors.Is(err, ErrExists) {
		t.Fatalf("copy no overwrite: %v", err)
	}
	if err := e.fs.Copy("/rw/one.txt", "/rw/two.txt", true); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(e.rw, "two.txt")) != "1" || read(t, filepath.Join(e.rw, "one.txt")) != "1" {
		t.Fatal("copy overwrite result")
	}
	// Copy from a read-only mount into a writable one is allowed.
	if err := e.fs.Copy("/ro/readme.txt", "/rw/fromro.txt", false); err != nil {
		t.Fatal(err)
	}
	// Copying a symlink (inside the root) copies its target's content.
	if err := e.fs.Copy("/rw/tree/inlink", "/rw/linkcopy.txt", false); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(e.rw, "linkcopy.txt")) != "a" {
		t.Fatal("copy of in-root symlink")
	}
	if err := e.fs.Copy("/rw/missing", "/rw/x", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("copy missing: %v", err)
	}
}

func TestRemove(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.rw, "d", "f.txt"), "f")
	e.symlink(filepath.Join(e.rw, "d"), filepath.Join(e.rw, "link"))
	if err := e.fs.Remove("/rw/link"); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(e.rw, "d", "f.txt")) {
		t.Fatal("removing a link removed its target")
	}
	if err := e.fs.Remove("/rw/d"); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(e.rw, "d")) {
		t.Fatal("not removed")
	}
	if err := e.fs.Remove("/rw/d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
	if got := e.n.take(); len(got) != 2 {
		t.Fatalf("notify %v", got)
	}
}

func TestOpenFileWrite(t *testing.T) {
	e := newEnv(t)
	fh, l, err := e.fs.OpenFile("/rw/n.txt", os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fh.WriteString("abc")
	fh.Close()
	e.fs.Changed(l)
	if got := e.n.take(); len(got) != 1 || got[0] != filepath.Join(e.rw, "n.txt") {
		t.Fatalf("notify %v", got)
	}
	if _, _, err := e.fs.OpenFile("/", os.O_RDONLY, 0); !errors.Is(err, ErrIsDir) {
		t.Fatalf("open vroot: %v", err)
	}
	e.fs.Changed(nil) // must not panic
}

func TestNilNotifier(t *testing.T) {
	d := realDir(t)
	f := New([]store.Mount{{Name: "m", Path: d, Writable: true}}, false, nil)
	if _, err := f.WriteFile("/m/a", strings.NewReader("x"), false); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove("/m/a"); err != nil {
		t.Fatal(err)
	}
}

func TestKindMime(t *testing.T) {
	cases := []struct {
		name  string
		dir   bool
		kind  string
		mime  string
		thumb bool // without ffmpeg
		thumF bool // with ffmpeg
	}{
		{"photo.JPG", false, KindImage, "image/jpeg", true, true},
		{"a.png", false, KindImage, "image/png", true, true},
		{"a.svg", false, KindImage, "image/svg+xml", false, false},
		{"a.heic", false, KindImage, "image/heic", false, true},
		{"movie.mkv", false, KindVideo, "video/x-matroska", false, true},
		{"clip.ts", false, KindVideo, "video/mp2t", false, true},
		{"song.flac", false, KindAudio, "audio/flac", false, true},
		{"notes.md", false, KindText, "text/markdown", false, false},
		{"main.go", false, KindText, "", false, false},
		{"Makefile", false, KindText, "text/plain; charset=utf-8", false, false},
		{"doc.pdf", false, KindPDF, "application/pdf", false, false},
		{"a.tar.gz", false, KindArchive, "", false, false},
		{"blob.bin", false, KindOther, "application/octet-stream", false, false},
		{"noext", false, KindOther, "application/octet-stream", false, false},
		{"photos.jpg", true, KindDir, "inode/directory", false, false},
	}
	for _, c := range cases {
		if k := KindOf(c.name, c.dir); k != c.kind {
			t.Errorf("KindOf(%q) = %s, want %s", c.name, k, c.kind)
		}
		if c.mime != "" {
			if m := MimeOf(c.name, c.dir); m != c.mime {
				t.Errorf("MimeOf(%q) = %s, want %s", c.name, m, c.mime)
			}
		} else if m := MimeOf(c.name, c.dir); m == "" {
			t.Errorf("MimeOf(%q) empty", c.name)
		}
		if th := Thumbable(c.name, c.dir, false); th != c.thumb {
			t.Errorf("Thumbable(%q, noffmpeg) = %v", c.name, th)
		}
		if th := Thumbable(c.name, c.dir, true); th != c.thumF {
			t.Errorf("Thumbable(%q, ffmpeg) = %v", c.name, th)
		}
	}
	if Ext("A.TXT") != "txt" || Ext("Makefile") != "makefile" || Ext(".gitignore") != "gitignore" {
		t.Error("Ext")
	}
}

func TestConcurrentOps(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := "/rw/c/" + string(rune('a'+i)) + ".txt"
			for j := 0; j < 20; j++ {
				e.fs.WriteFile(p, strings.NewReader("data"), true)
				e.fs.List("/rw/c")
				e.fs.Stat(p)
				e.fs.Copy(p, p+".bak", true)
				e.fs.Remove(p + ".bak")
			}
		}(i)
	}
	wg.Wait()
	if runtime.GOOS != "windows" {
		es, _, err := e.fs.List("/rw/c")
		if err != nil || len(es) != 8 {
			t.Fatalf("after concurrent ops: %v %v", err, names(es))
		}
	}
}
