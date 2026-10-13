package vfs

// Buckets can be backed by several directories that are copies of each other.
// Reads and writes go to the active directory (Root.Path); every change made
// through the VFS is then replayed onto the other available directories
// (Root.Mirrors). Changes made outside the VFS (SMB, the local shell) or while
// a directory was offline are repaired by Reconcile, which merges directories
// additively: it never deletes, so a copy can only be lost by deleting it
// through MList itself.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// replicaMu orders VFS mutations (shared) against Reconcile steps (exclusive)
// so that a reconcile pass never copies back a file that is being deleted.
var replicaMu sync.RWMutex

// reconcileMu serializes whole Reconcile passes.
var reconcileMu sync.Mutex

func (f *FS) lockMirrors(roots ...*Root) func() {
	for _, r := range roots {
		if r != nil && len(r.Mirrors) > 0 {
			replicaMu.RLock()
			return replicaMu.RUnlock
		}
	}
	return func() {}
}

// mirror replays the state of rel (inside r) onto r's mirrors. With replace,
// whatever the mirror has at rel is discarded first (used when the operation
// overwrote the destination).
func (f *FS) mirror(r *Root, rel string, replace bool) {
	if r == nil {
		return
	}
	src := filepath.Join(r.Path, filepath.FromSlash(rel))
	for _, m := range r.Mirrors {
		dst := filepath.Join(m, filepath.FromSlash(rel))
		if err := mirrorOne(src, dst, m, replace); err != nil {
			slog.Warn("vfs: sync backup directory", "bucket", r.Name, "dir", m, "path", "/"+rel, "err", err)
		}
	}
}

func mirrorOne(src, dst, mroot string, replace bool) error {
	if dst == mroot || contained(mroot, dst) != nil {
		return ErrPermission
	}
	if replace {
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	return syncPath(src, dst)
}

// mirrorMove replays a rename inside one root onto its mirrors, falling back
// to copying when a mirror does not have the source.
func (f *FS) mirrorMove(r *Root, fromRel, toRel string, replace bool) {
	for _, m := range r.Mirrors {
		from := filepath.Join(m, filepath.FromSlash(fromRel))
		to := filepath.Join(m, filepath.FromSlash(toRel))
		if contained(m, from) != nil || contained(m, to) != nil {
			continue
		}
		if _, err := os.Lstat(from); err == nil {
			if replace {
				os.RemoveAll(to)
			}
			if err := os.MkdirAll(filepath.Dir(to), 0o755); err == nil {
				os.Rename(from, to)
			}
		}
	}
	f.mirror(r, fromRel, false)
	f.mirror(r, toRel, false)
}

// syncPath makes dst hold what src holds. A missing src removes dst. For
// directories the merge is additive: entries only present in dst are kept.
// Files whose size and modification time match are not copied again.
func syncPath(src, dst string) error {
	si, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return os.RemoveAll(dst)
	}
	if err != nil {
		return err
	}
	di, derr := os.Lstat(dst)
	switch {
	case si.IsDir():
		if derr == nil && !di.IsDir() {
			if err := os.RemoveAll(dst); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(dst, si.Mode().Perm()|0o700); err != nil {
			return err
		}
		des, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		var firstErr error
		for _, de := range des {
			if strings.HasPrefix(de.Name(), TempPrefix) {
				continue
			}
			if err := syncPath(filepath.Join(src, de.Name()), filepath.Join(dst, de.Name())); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	case si.Mode().IsRegular():
		if derr == nil {
			if sameFileMeta(si, di) {
				return nil
			}
			if di.IsDir() {
				if err := os.RemoveAll(dst); err != nil {
					return err
				}
			}
		}
		return copyAtomic(src, dst, si)
	}
	return nil // symlinks and special files are never replicated
}

func sameFileMeta(a, b fs.FileInfo) bool {
	return a.Mode().IsRegular() && b.Mode().IsRegular() && a.Size() == b.Size() &&
		a.ModTime().Truncate(time.Second).Equal(b.ModTime().Truncate(time.Second))
}

// copyAtomic copies src to dst through a temp file, keeping the modification
// time so that later comparisons see both copies as equal.
func copyAtomic(src, dst string, si fs.FileInfo) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, TempPrefix+"*")
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), si.Mode().Perm())
	}
	if err == nil {
		err = os.Chtimes(tmp.Name(), time.Now(), si.ModTime())
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// ---------- merging directories ----------

// Conflict 是合并目录时发现的同名冲突：同一相对路径在不同目录中内容不同。
type Conflict struct {
	Path   string   `json:"path"`   // 冲突条目在桶内的相对路径，以 / 开头
	Dirs   []string `json:"dirs"`   // 含有该条目的服务器目录
	Reason string   `json:"reason"` // 冲突原因
}

// SyncResult 是一次目录合并的结果。
type SyncResult struct {
	Copied    int        `json:"copied"`    // 复制的文件数
	Bytes     int64      `json:"bytes"`     // 复制的字节数
	Conflicts []Conflict `json:"conflicts"` // 无法自动合并的冲突（同名的文件与目录）
}

// MaxConflicts bounds how many conflicts a scan reports.
const MaxConflicts = 100

type holder struct {
	dir  string
	path string
	info fs.FileInfo
}

// entriesAt reads the entry named rel in every dir and groups them by name.
// Temp uploads, symlinks and special files are ignored.
func entriesAt(dirs []string, rel string) (map[string][]holder, []string, error) {
	byName := map[string][]holder{}
	for _, d := range dirs {
		base := filepath.Join(d, filepath.FromSlash(rel))
		des, err := os.ReadDir(base)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				continue
			}
			return nil, nil, err
		}
		for _, de := range des {
			name := de.Name()
			if strings.HasPrefix(name, TempPrefix) {
				continue
			}
			fi, err := de.Info()
			if err != nil || !(fi.IsDir() || fi.Mode().IsRegular()) {
				continue
			}
			byName[name] = append(byName[name], holder{dir: d, path: filepath.Join(base, name), info: fi})
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return byName, names, nil
}

func holderDirs(hs []holder) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.dir
	}
	return out
}

func kinds(hs []holder) (dirs, files int) {
	for _, h := range hs {
		if h.info.IsDir() {
			dirs++
		} else {
			files++
		}
	}
	return
}

// FindConflicts reports entries that exist in more than one of dirs with
// different content: a file in one and a directory in another, or two files
// whose content differs. Identical files are not conflicts. At most
// MaxConflicts are returned.
func FindConflicts(dirs []string) ([]Conflict, error) {
	var out []Conflict
	var visit func(rel string) error
	visit = func(rel string) error {
		byName, names, err := entriesAt(dirs, rel)
		if err != nil {
			return err
		}
		for _, name := range names {
			if len(out) >= MaxConflicts {
				return nil
			}
			hs := byName[name]
			r := path.Join(rel, name)
			nd, nf := kinds(hs)
			switch {
			case nd > 0 && nf > 0:
				out = append(out, Conflict{Path: "/" + r, Dirs: holderDirs(hs), Reason: "同名的文件与目录"})
			case nd > 0:
				if err := visit(r); err != nil {
					return err
				}
			case nf > 1:
				same, err := sameContent(hs)
				if err != nil {
					return err
				}
				if !same {
					out = append(out, Conflict{Path: "/" + r, Dirs: holderDirs(hs), Reason: "同名文件内容不同"})
				}
			}
		}
		return nil
	}
	return out, visit("")
}

// sameContent reports whether all files hold identical bytes. Equal size and
// modification time are not trusted here: on coarse-grained filesystems two
// different files can share both, and merging must never pick one silently.
func sameContent(hs []holder) (bool, error) {
	first := hs[0]
	for _, h := range hs[1:] {
		if h.info.Size() != first.info.Size() {
			return false, nil
		}
		eq, err := equalFiles(first.path, h.path)
		if err != nil || !eq {
			return false, err
		}
	}
	return true, nil
}

func equalFiles(a, b string) (bool, error) {
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	ba, bb := make([]byte, 256<<10), make([]byte, 256<<10)
	for {
		na, ea := io.ReadFull(fa, ba)
		nb, eb := io.ReadFull(fb, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false, nil
		}
		if ea == io.EOF || ea == io.ErrUnexpectedEOF {
			return eb == io.EOF || eb == io.ErrUnexpectedEOF, nil
		}
		if ea != nil {
			return false, ea
		}
		if eb != nil {
			return false, eb
		}
	}
}

// Reconcile merges dirs so that each holds every file and directory found in
// any of them. When copies of a file differ, the most recently modified one
// wins. Nothing is ever deleted; entries that are a file in one directory and
// a directory in another are reported as conflicts and left alone. Missing
// dirs are skipped. n, when not nil, is told about every file written.
func Reconcile(dirs []string, n Notifier) (SyncResult, error) {
	reconcileMu.Lock()
	defer reconcileMu.Unlock()
	res := SyncResult{Conflicts: []Conflict{}}
	var avail []string
	for _, d := range dirs {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			avail = append(avail, d)
		}
	}
	if len(avail) < 2 {
		return res, nil
	}
	var firstErr error
	note := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	var visit func(rel string) error
	visit = func(rel string) error {
		byName, names, err := entriesAt(avail, rel)
		if err != nil {
			return err
		}
		for _, name := range names {
			hs := byName[name]
			r := path.Join(rel, name)
			nd, nf := kinds(hs)
			switch {
			case nd > 0 && nf > 0:
				if len(res.Conflicts) < MaxConflicts {
					res.Conflicts = append(res.Conflicts, Conflict{Path: "/" + r, Dirs: holderDirs(hs), Reason: "同名的文件与目录"})
				}
			case nd > 0:
				if nd < len(avail) {
					note(withLock(func() error {
						for _, d := range avail {
							p := filepath.Join(d, filepath.FromSlash(r))
							if _, err := os.Lstat(p); err == nil {
								continue
							}
							if err := os.MkdirAll(p, 0o755); err != nil {
								return err
							}
							if n != nil {
								n.Changed(p)
							}
						}
						return nil
					}))
				}
				if err := visit(r); err != nil {
					return err
				}
			default:
				note(reconcileFile(avail, r, &res, n))
			}
		}
		return nil
	}
	if err := visit(""); err != nil {
		return res, err
	}
	return res, firstErr
}

func withLock(fn func() error) error {
	replicaMu.Lock()
	defer replicaMu.Unlock()
	return fn()
}

// reconcileFile copies the newest version of rel to every dir that lacks it or
// holds a different version. The state is re-read under the lock so that a
// concurrent delete through the VFS is not undone.
func reconcileFile(dirs []string, rel string, res *SyncResult, n Notifier) error {
	return withLock(func() error {
		var win *holder
		infos := make([]fs.FileInfo, len(dirs))
		for i, d := range dirs {
			p := filepath.Join(d, filepath.FromSlash(rel))
			fi, err := os.Lstat(p)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			infos[i] = fi
			if win == nil || fi.ModTime().After(win.info.ModTime()) {
				win = &holder{dir: d, path: p, info: fi}
			}
		}
		if win == nil {
			return nil
		}
		var firstErr error
		for i, d := range dirs {
			if d == win.dir || (infos[i] != nil && sameFileMeta(infos[i], win.info)) {
				continue
			}
			dst := filepath.Join(d, filepath.FromSlash(rel))
			if contained(d, dst) != nil {
				continue
			}
			if _, err := os.Lstat(dst); err == nil && infos[i] == nil {
				continue // a directory or symlink appeared meanwhile
			}
			if err := copyAtomic(win.path, dst, win.info); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", dst, err)
				}
				continue
			}
			res.Copied++
			res.Bytes += win.info.Size()
			if n != nil {
				n.Changed(dst)
			}
		}
		return firstErr
	})
}
