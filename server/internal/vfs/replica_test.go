package vfs

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sakana-1314/MList/server/internal/store"
)

// tree returns rel path -> content ("/" suffix for directories) of dir.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if de.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func put(t *testing.T, p, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dirs(t *testing.T, names ...string) []string {
	t.Helper()
	base := t.TempDir()
	if rp, err := filepath.EvalSymlinks(base); err == nil {
		base = rp
	}
	var out []string
	for _, n := range names {
		d := filepath.Join(base, n)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

func sameTrees(t *testing.T, a, b string) {
	t.Helper()
	ta, tb := tree(t, a), tree(t, b)
	if !reflect.DeepEqual(ta, tb) {
		t.Fatalf("directories differ:\n%s: %v\n%s: %v", a, ta, b, tb)
	}
}

func TestMirrorEveryWrite(t *testing.T) {
	d := dirs(t, "main", "backup", "other", "other-backup")
	main, backup := d[0], d[1]
	f := New([]store.Mount{
		{Name: "b", Path: main, Mirrors: []string{backup}, Writable: true},
		{Name: "o", Path: d[2], Mirrors: []string{d[3]}, Writable: true},
	}, false, nil)

	steps := []struct {
		name string
		fn   func() error
	}{
		{"write", func() error { _, err := f.WriteFile("/b/docs/a.txt", strings.NewReader("hello"), false); return err }},
		{"overwrite", func() error { _, err := f.WriteFile("/b/docs/a.txt", strings.NewReader("hello2"), true); return err }},
		{"mkdir", func() error { _, err := f.Mkdir("/b/empty/sub"); return err }},
		{"copy file", func() error { return f.Copy("/b/docs/a.txt", "/b/docs/b.txt", false) }},
		{"copy dir", func() error { return f.Copy("/b/docs", "/b/docs2", false) }},
		{"copy over", func() error { return f.Copy("/b/empty", "/b/docs2", true) }},
		{"rename", func() error { _, err := f.Rename("/b/docs", "renamed"); return err }},
		{"move into dir", func() error { return f.Move("/b/renamed/a.txt", "/b/empty/a.txt", false) }},
		{"move over", func() error { return f.Move("/b/renamed/b.txt", "/b/empty/a.txt", true) }},
		{"remove", func() error { return f.Remove("/b/docs2") }},
		{"move across buckets", func() error { return f.Move("/b/renamed", "/o/in", false) }},
		{"copy across buckets", func() error { return f.Copy("/o/in", "/b/back", false) }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		sameTrees(t, main, backup)
		sameTrees(t, d[2], d[3])
	}
	if got := tree(t, backup); got["empty/a.txt"] != "hello2" || got["back/"] != "" {
		t.Fatalf("unexpected final tree %v", got)
	}

	// Writes through OpenFile are replicated when reported via Changed.
	fh, loc, err := f.OpenFile("/b/direct.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	fh.WriteString("direct")
	fh.Close()
	f.Changed(loc)
	sameTrees(t, main, backup)
	os.Remove(loc.Real)
	f.Changed(loc)
	sameTrees(t, main, backup)
}

func TestMirrorKeepsMtime(t *testing.T) {
	d := dirs(t, "a", "b")
	f := New([]store.Mount{{Name: "m", Path: d[0], Mirrors: d[1:], Writable: true}}, false, nil)
	if _, err := f.WriteFile("/m/x", strings.NewReader("x"), false); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(filepath.Join(d[0], "x"))
	b, _ := os.Stat(filepath.Join(d[1], "x"))
	if !a.ModTime().Equal(b.ModTime()) {
		t.Fatalf("mtime differs: %v vs %v", a.ModTime(), b.ModTime())
	}
}

func TestActiveFallsBackToBackup(t *testing.T) {
	d := dirs(t, "backup", "third")
	missing := filepath.Join(filepath.Dir(d[0]), "offline")
	f := New([]store.Mount{{Name: "m", Path: missing, Mirrors: d, Writable: true}}, false, nil)
	r := f.Root("m")
	if r.Path != d[0] || !reflect.DeepEqual(r.Mirrors, d[1:]) {
		t.Fatalf("root %+v", r)
	}
	if _, err := f.WriteFile("/m/a", strings.NewReader("a"), false); err != nil {
		t.Fatal(err)
	}
	sameTrees(t, d[0], d[1])
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("offline directory must not be recreated")
	}
}

func TestFindConflicts(t *testing.T) {
	d := dirs(t, "a", "b", "c")
	put(t, filepath.Join(d[0], "same.txt"), "same")
	put(t, filepath.Join(d[1], "same.txt"), "same") // identical bytes, different mtime
	put(t, filepath.Join(d[0], "only-a.txt"), "a")
	put(t, filepath.Join(d[1], "sub", "only-b.txt"), "b")
	put(t, filepath.Join(d[2], "sub", "only-c.txt"), "c")
	if cs, err := FindConflicts(d); err != nil || len(cs) != 0 {
		t.Fatalf("no conflicts expected: %v %+v", err, cs)
	}

	put(t, filepath.Join(d[0], "sub", "x.txt"), "one")
	put(t, filepath.Join(d[2], "sub", "x.txt"), "two")
	put(t, filepath.Join(d[1], "kind"), "file")
	os.MkdirAll(filepath.Join(d[2], "kind"), 0o755)
	put(t, filepath.Join(d[0], "size.txt"), "short")
	put(t, filepath.Join(d[1], "size.txt"), "longer content")
	cs, err := FindConflicts(d)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range cs {
		got[c.Path] = c.Reason
		if len(c.Dirs) != 2 {
			t.Fatalf("conflict dirs %+v", c)
		}
	}
	want := map[string]string{"/kind": "同名的文件与目录", "/size.txt": "同名文件内容不同", "/sub/x.txt": "同名文件内容不同"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conflicts %v", got)
	}
}

func TestReconcile(t *testing.T) {
	d := dirs(t, "a", "b")
	put(t, filepath.Join(d[0], "x", "a.txt"), "a")
	put(t, filepath.Join(d[1], "y", "deep", "b.txt"), "b")
	put(t, filepath.Join(d[0], "both.txt"), "old")
	put(t, filepath.Join(d[1], "both.txt"), "new")
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(d[0], "both.txt"), old, old)
	put(t, filepath.Join(d[1], TempPrefix+"partial"), "tmp")
	os.MkdirAll(filepath.Join(d[1], "empty"), 0o755)

	missing := filepath.Join(filepath.Dir(d[0]), "offline")
	res, err := Reconcile(append(d, missing), nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Copied != 3 || len(res.Conflicts) != 0 {
		t.Fatalf("result %+v", res)
	}
	sameTreesExcept(t, d[0], d[1], TempPrefix+"partial")
	if got := tree(t, d[0]); got["both.txt"] != "new" || got["y/deep/b.txt"] != "b" || got["empty/"] != "" {
		t.Fatalf("merged %v", got)
	}
	// A second pass has nothing to do.
	if res, _ := Reconcile(d, nil); res.Copied != 0 {
		t.Fatalf("second pass copied %d", res.Copied)
	}
	// Nothing is deleted: a file removed from one side comes back.
	os.Remove(filepath.Join(d[1], "x", "a.txt"))
	if res, _ := Reconcile(d, nil); res.Copied != 1 {
		t.Fatalf("restore copied %d", res.Copied)
	}
	// File / directory clashes are reported and left alone.
	put(t, filepath.Join(d[0], "clash"), "f")
	os.MkdirAll(filepath.Join(d[1], "clash"), 0o755)
	res, _ = Reconcile(d, nil)
	if len(res.Conflicts) != 1 || res.Conflicts[0].Path != "/clash" {
		t.Fatalf("conflicts %+v", res.Conflicts)
	}
}

func sameTreesExcept(t *testing.T, a, b, skip string) {
	t.Helper()
	ta, tb := tree(t, a), tree(t, b)
	delete(ta, skip)
	delete(tb, skip)
	if !reflect.DeepEqual(ta, tb) {
		t.Fatalf("directories differ:\n%v\n%v", ta, tb)
	}
}
