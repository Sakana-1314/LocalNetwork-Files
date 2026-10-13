// Package vfs exposes a per-user virtual filesystem built from the user's
// authorized directories ("mounts"). Every protocol front-end (REST, WebDAV,
// S3) goes through it, so path confinement and read/write permissions are
// enforced in a single place.
//
// Virtual layout: "/" lists the mounts; "/<mount>/<rel...>" maps to
// <mount.Path>/<rel...> on the local disk.
package vfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Sakana-1314/MList/server/internal/store"
)

var (
	ErrNotFound   = errors.New("文件或目录不存在")
	ErrPermission = errors.New("没有权限")
	ErrReadOnly   = errors.New("该目录为只读")
	ErrExists     = errors.New("目标已存在")
	ErrInvalid    = errors.New("无效的路径或名称")
	ErrNotDir     = errors.New("不是目录")
	ErrIsDir      = errors.New("是一个目录")
)

// TempPrefix marks in-progress uploads; such files are hidden from listings.
const TempPrefix = ".lnf-upload-"

// FFmpegAvailable is set at startup; it controls whether media files can be
// thumbnailed at all.
var FFmpegAvailable bool

// Fingerprint computes a file's content digest. The app wires it to the media
// service; when nil, entries carry no digest and previews are unavailable.
var Fingerprint func(real string, size int64, mtime time.Time) (string, error)

// OnDigest, when set, is called with every digest produced for an entry. The
// API layer uses it to remember which file a digest belongs to, so previews can
// be requested by digest without putting paths in URLs.
var OnDigest func(digest, real string, size int64, mtime time.Time)

// Notifier is told about local paths changed through the VFS (e.g. the search index).
type Notifier interface {
	Changed(realPath string)
}

type Root struct {
	Name     string
	Path     string // active directory: absolute, symlinks resolved
	Writable bool
	// Mirrors are the bucket's other available directories; every change made
	// through the VFS is copied onto them.
	Mirrors []string
}

type FS struct {
	roots    []*Root
	byName   map[string]*Root
	readOnly bool
	notify   Notifier
}

// New builds the filesystem for a set of mounts. readOnly forces every mount read-only
// (used for tokens / keys without the write scope).
func New(mounts []store.Mount, readOnly bool, n Notifier) *FS {
	f := &FS{byName: map[string]*Root{}, readOnly: readOnly, notify: n}
	for _, m := range mounts {
		active, mirrors := m.Active()
		r := &Root{Name: m.Name, Path: cleanDir(active), Writable: m.Writable && !readOnly}
		for _, d := range mirrors {
			r.Mirrors = append(r.Mirrors, cleanDir(d))
		}
		f.roots = append(f.roots, r)
		f.byName[m.Name] = r
	}
	return f
}

func cleanDir(p string) string {
	p = filepath.Clean(p)
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		p = rp
	}
	return p
}

func (f *FS) Roots() []*Root         { return f.roots }
func (f *FS) ReadOnly() bool         { return f.readOnly }
func (f *FS) Root(name string) *Root { return f.byName[name] }

// Loc is a resolved virtual path.
type Loc struct {
	VPath string // cleaned virtual path, always starting with "/"
	Root  *Root  // nil for the virtual root "/"
	Rel   string // slash separated path inside the root, "" for the root itself
	Real  string // local path ("" for the virtual root)
}

func (l *Loc) IsVirtualRoot() bool { return l.Root == nil }
func (l *Loc) IsMountRoot() bool   { return l.Root != nil && l.Rel == "" }

// CleanPath normalizes a virtual path.
func CleanPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	return path.Clean("/" + p)
}

// Locate resolves a virtual path and verifies it stays inside its root, also
// across symlinks.
func (f *FS) Locate(vpath string) (*Loc, error) {
	if strings.ContainsRune(vpath, 0) {
		return nil, ErrInvalid
	}
	vp := CleanPath(vpath)
	if vp == "/" {
		return &Loc{VPath: "/"}, nil
	}
	parts := strings.SplitN(vp[1:], "/", 2)
	r := f.byName[parts[0]]
	if r == nil {
		return nil, ErrNotFound
	}
	rel := ""
	if len(parts) == 2 {
		rel = parts[1]
	}
	real := r.Path
	if rel != "" {
		real = filepath.Join(r.Path, filepath.FromSlash(rel))
	}
	if err := contained(r.Path, real); err != nil {
		return nil, err
	}
	return &Loc{VPath: vp, Root: r, Rel: rel, Real: real}, nil
}

// contained checks that real (or its nearest existing ancestor) resolves inside root.
// Dangling symlinks are followed lexically so that creating a file through a
// link whose target does not exist yet cannot escape the root either.
func contained(root, real string) error {
	return containedDepth(root, real, 0)
}

const maxLinkDepth = 40

func containedDepth(root, real string, depth int) error {
	if depth > maxLinkDepth || !within(root, real) {
		return ErrPermission
	}
	p := real
	for {
		rp, err := filepath.EvalSymlinks(p)
		if err == nil {
			if !within(root, rp) {
				return ErrPermission
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			if errors.Is(err, syscall.ENOTDIR) {
				return ErrNotDir
			}
			// Symlink loops and similar oddities are never served.
			return ErrPermission
		}
		if fi, lerr := os.Lstat(p); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
			// p exists but is a dangling symlink: check where it would lead.
			target, rerr := os.Readlink(p)
			if rerr != nil {
				return ErrPermission
			}
			if !filepath.IsAbs(target) {
				dir, derr := filepath.EvalSymlinks(filepath.Dir(p))
				if derr != nil {
					return ErrPermission
				}
				target = filepath.Join(dir, target)
			}
			if err := containedDepth(root, target, depth+1); err != nil {
				return ErrPermission
			}
			return nil
		}
		parent := filepath.Dir(p)
		if parent == p || !within(root, parent) {
			return nil
		}
		p = parent
	}
}

// resolved returns p with symlinks in its longest existing prefix resolved.
func resolved(p string) string {
	rest := ""
	cur := p
	for {
		if rp, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return rp
			}
			return filepath.Join(rp, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// validRel checks every component of a path that is about to be created.
func validRel(rel string) error {
	for _, part := range strings.Split(rel, "/") {
		if err := validName(part); err != nil {
			return err
		}
	}
	return nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (f *FS) writableLoc(l *Loc) error {
	if l.Root == nil {
		return ErrPermission
	}
	if !l.Root.Writable {
		return ErrReadOnly
	}
	return nil
}

func (f *FS) changed(real string) {
	if f.notify != nil && real != "" {
		f.notify.Changed(real)
	}
}

// ---------- reading ----------

type Entry struct {
	Name     string    `json:"name"`                                                      // 文件或目录名
	Path     string    `json:"path"`                                                      // 虚拟路径
	IsDir    bool      `json:"is_dir"`                                                    // 是否为目录
	Size     int64     `json:"size"`                                                      // 文件大小（字节）；目录为 0
	ModTime  time.Time `json:"mod_time"`                                                  // 最后修改时间
	Mime     string    `json:"mime"`                                                      // 由扩展名推断的 MIME 类型
	Kind     string    `json:"kind" enums:"dir,image,video,audio,text,pdf,archive,other"` // 文件类型，用于选择预览方式
	Writable bool      `json:"writable"`                                                  // 是否可写（目录权限与令牌范围共同决定）
	// Thumb 是预览源文件的内容摘要（不可预览时为空串）。它不是路径，
	// 因此列目录不会暴露文件在服务器上的位置。
	Thumb string `json:"thumb"`
	// ThumbSig 是服务端对 Thumb 的签名，供 /media/thumb 校验。
	ThumbSig string `json:"thumb_sig"`
}

func NewEntry(vpath, real string, fi fs.FileInfo, writable bool) Entry {
	name := path.Base(vpath)
	isDir := fi.IsDir()
	e := Entry{
		Name: name, Path: vpath, IsDir: isDir, ModTime: fi.ModTime(),
		Mime: MimeOf(name, isDir), Kind: KindOf(name, isDir), Writable: writable,
	}
	if !isDir {
		e.Size = fi.Size()
		e.Thumb = thumbDigest(real, fi, name)
	}
	return e
}

// thumbDigest fingerprints only files that can actually be previewed, so that
// listing a directory of documents or archives costs no extra reads.
func thumbDigest(real string, fi fs.FileInfo, name string) string {
	return DigestFor(real, name, fi.Size(), fi.ModTime())
}

// DigestFor returns the preview digest for a file, or "" when it cannot be
// previewed. It takes primitives so callers that only have index metadata
// (the search index) can use it without a FileInfo.
func DigestFor(real, name string, size int64, mtime time.Time) string {
	if Fingerprint == nil || real == "" || !Thumbable(name, false, FFmpegAvailable) {
		return ""
	}
	fp, err := Fingerprint(real, size, mtime)
	if err != nil {
		return ""
	}
	if OnDigest != nil {
		OnDigest(fp, real, size, mtime)
	}
	return fp
}

func (f *FS) rootEntry(r *Root) Entry {
	e := Entry{Name: r.Name, Path: "/" + r.Name, IsDir: true, Mime: "inode/directory", Kind: KindDir, Writable: r.Writable}
	if fi, err := os.Stat(r.Path); err == nil {
		e.ModTime = fi.ModTime()
	}
	return e
}

// Stat returns information about a path.
func (f *FS) Stat(vpath string) (*Entry, error) {
	l, err := f.Locate(vpath)
	if err != nil {
		return nil, err
	}
	if l.IsVirtualRoot() {
		return &Entry{Name: "", Path: "/", IsDir: true, Mime: "inode/directory", Kind: KindDir}, nil
	}
	fi, err := os.Stat(l.Real)
	if err != nil {
		return nil, mapErr(err)
	}
	if l.IsMountRoot() {
		e := f.rootEntry(l.Root)
		return &e, nil
	}
	e := NewEntry(l.VPath, l.Real, fi, l.Root.Writable)
	return &e, nil
}

// List returns the entries of a directory and whether it is writable.
func (f *FS) List(vpath string) ([]Entry, bool, error) {
	l, err := f.Locate(vpath)
	if err != nil {
		return nil, false, err
	}
	if l.IsVirtualRoot() {
		out := make([]Entry, 0, len(f.roots))
		for _, r := range f.roots {
			out = append(out, f.rootEntry(r))
		}
		return out, false, nil
	}
	des, err := os.ReadDir(l.Real)
	if err != nil {
		return nil, false, mapErr(err)
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		name := de.Name()
		if strings.HasPrefix(name, TempPrefix) {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			// Only follow symlinks that stay inside the root.
			target := filepath.Join(l.Real, name)
			if contained(l.Root.Path, target) != nil {
				continue
			}
			if fi, err = os.Stat(target); err != nil {
				continue
			}
		}
		out = append(out, NewEntry(path.Join(l.VPath, name), filepath.Join(l.Real, name), fi, l.Root.Writable))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, l.Root.Writable, nil
}

// LocalFile resolves a readable regular file to its local path.
func (f *FS) LocalFile(vpath string) (*Loc, fs.FileInfo, error) {
	l, err := f.Locate(vpath)
	if err != nil {
		return nil, nil, err
	}
	if l.IsVirtualRoot() {
		return nil, nil, ErrIsDir
	}
	fi, err := os.Stat(l.Real)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	if fi.IsDir() {
		return nil, nil, ErrIsDir
	}
	return l, fi, nil
}

// Open opens a file for reading.
func (f *FS) Open(vpath string) (*os.File, fs.FileInfo, error) {
	l, fi, err := f.LocalFile(vpath)
	if err != nil {
		return nil, nil, err
	}
	fh, err := os.Open(l.Real)
	if err != nil {
		return nil, nil, mapErr(err)
	}
	return fh, fi, nil
}

// ---------- writing ----------

// OpenFile opens a local file with os flags, enforcing write permissions when needed.
func (f *FS) OpenFile(vpath string, flag int, perm os.FileMode) (*os.File, *Loc, error) {
	l, err := f.Locate(vpath)
	if err != nil {
		return nil, nil, err
	}
	if l.IsVirtualRoot() {
		return nil, l, ErrIsDir
	}
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0 {
		if err := f.writableLoc(l); err != nil {
			return nil, l, err
		}
	}
	fh, err := os.OpenFile(l.Real, flag, perm)
	if err != nil {
		return nil, l, mapErr(err)
	}
	return fh, l, nil
}

// WriteFile atomically writes r to vpath, creating parent directories.
func (f *FS) WriteFile(vpath string, r io.Reader, overwrite bool) (*Entry, error) {
	l, err := f.Locate(vpath)
	if err != nil {
		return nil, err
	}
	if l.IsVirtualRoot() || l.IsMountRoot() {
		return nil, ErrInvalid
	}
	if err := f.writableLoc(l); err != nil {
		return nil, err
	}
	if err := validRel(l.Rel); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(l.Real); err == nil {
		if fi.IsDir() {
			return nil, ErrIsDir
		}
		if !overwrite {
			return nil, ErrExists
		}
	}
	dir := filepath.Dir(l.Real)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, mapErr(err)
	}
	tmp, err := os.CreateTemp(dir, TempPrefix+"*")
	if err != nil {
		return nil, mapErr(err)
	}
	_, err = io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	os.Chmod(tmp.Name(), 0o644)
	unlock := f.lockMirrors(l.Root)
	if err := publish(tmp.Name(), l.Real, overwrite); err != nil {
		unlock()
		os.Remove(tmp.Name())
		return nil, err
	}
	f.mirror(l.Root, l.Rel, false)
	unlock()
	f.changed(l.Real)
	return f.Stat(l.VPath)
}

// publish moves the finished temp file into place. Without overwrite a hard
// link is used so that an entry created concurrently is never clobbered
// (link fails atomically with EEXIST); filesystems without hard links fall
// back to check-then-rename.
func publish(tmp, dst string, overwrite bool) error {
	if overwrite {
		return mapErr(os.Rename(tmp, dst))
	}
	err := os.Link(tmp, dst)
	if err == nil {
		os.Remove(tmp)
		return nil
	}
	if errors.Is(err, fs.ErrExist) {
		return ErrExists
	}
	if _, err := os.Lstat(dst); err == nil {
		return ErrExists
	}
	return mapErr(os.Rename(tmp, dst))
}

// Mkdir creates a directory (and missing parents).
func (f *FS) Mkdir(vpath string) (*Entry, error) {
	l, err := f.Locate(vpath)
	if err != nil {
		return nil, err
	}
	if l.IsVirtualRoot() || l.IsMountRoot() {
		return nil, ErrExists
	}
	if err := f.writableLoc(l); err != nil {
		return nil, err
	}
	if err := validRel(l.Rel); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(l.Real); err == nil {
		return nil, ErrExists
	}
	unlock := f.lockMirrors(l.Root)
	if err := os.MkdirAll(l.Real, 0o755); err != nil {
		unlock()
		return nil, mapErr(err)
	}
	f.mirror(l.Root, l.Rel, false)
	unlock()
	f.changed(l.Real)
	return f.Stat(l.VPath)
}

// Rename renames an entry inside its directory.
func (f *FS) Rename(vpath, newName string) (*Entry, error) {
	if err := validName(newName); err != nil {
		return nil, err
	}
	l, err := f.Locate(vpath)
	if err != nil {
		return nil, err
	}
	dst := path.Join(path.Dir(l.VPath), newName)
	if err := f.moveOne(l, dst, false); err != nil {
		return nil, err
	}
	return f.Stat(dst)
}

// Move moves src to the exact destination virtual path.
func (f *FS) Move(src, dst string, overwrite bool) error {
	l, err := f.Locate(src)
	if err != nil {
		return err
	}
	return f.moveOne(l, dst, overwrite)
}

func (f *FS) moveOne(l *Loc, dstPath string, overwrite bool) error {
	if l.IsVirtualRoot() || l.IsMountRoot() {
		return ErrPermission
	}
	if err := f.writableLoc(l); err != nil {
		return err
	}
	d, err := f.Locate(dstPath)
	if err != nil {
		return err
	}
	if d.IsVirtualRoot() || d.IsMountRoot() {
		return ErrInvalid
	}
	if err := f.writableLoc(d); err != nil {
		return err
	}
	if d.Real == l.Real {
		return nil
	}
	if _, err := os.Lstat(l.Real); err != nil {
		return mapErr(err)
	}
	if err := validRel(d.Rel); err != nil {
		return err
	}
	srcR, dstR := resolved(l.Real), resolved(d.Real)
	if within(l.Real, d.Real) || (srcR != dstR && within(srcR, dstR)) {
		return fmt.Errorf("%w: 不能移动到自身的子目录中", ErrInvalid)
	}
	if within(d.Real, l.Real) || within(dstR, srcR) {
		// Overwriting an ancestor of the source would delete the source itself.
		return fmt.Errorf("%w: 不能覆盖包含源的目录", ErrInvalid)
	}
	defer f.lockMirrors(l.Root, d.Root)()
	replaced := false
	if fi, err := os.Lstat(d.Real); err == nil {
		// Case-only renames on case-insensitive filesystems refer to the same file.
		sameFile := false
		if si, err2 := os.Lstat(l.Real); err2 == nil {
			sameFile = os.SameFile(fi, si)
		}
		if !sameFile {
			if !overwrite {
				return ErrExists
			}
			if err := os.RemoveAll(d.Real); err != nil {
				return mapErr(err)
			}
			replaced = true
		}
	}
	if err := os.MkdirAll(filepath.Dir(d.Real), 0o755); err != nil {
		return mapErr(err)
	}
	if err := os.Rename(l.Real, d.Real); err != nil {
		if !isCrossDevice(err) {
			return mapErr(err)
		}
		if err := copyTree(l.Real, d.Real); err != nil {
			os.RemoveAll(d.Real)
			return err
		}
		if err := os.RemoveAll(l.Real); err != nil {
			return mapErr(err)
		}
	}
	if l.Root == d.Root {
		f.mirrorMove(l.Root, l.Rel, d.Rel, replaced)
	} else {
		f.mirror(l.Root, l.Rel, false)
		f.mirror(d.Root, d.Rel, replaced)
	}
	f.changed(l.Real)
	f.changed(d.Real)
	return nil
}

// Copy copies src (file or directory tree) to the exact destination path.
func (f *FS) Copy(src, dst string, overwrite bool) error {
	l, err := f.Locate(src)
	if err != nil {
		return err
	}
	if l.IsVirtualRoot() {
		return ErrInvalid
	}
	d, err := f.Locate(dst)
	if err != nil {
		return err
	}
	if d.IsVirtualRoot() || d.IsMountRoot() {
		return ErrInvalid
	}
	if err := f.writableLoc(d); err != nil {
		return err
	}
	if err := validRel(d.Rel); err != nil {
		return err
	}
	// Copy what the source resolves to (Locate guarantees it stays inside its root).
	srcReal, err := filepath.EvalSymlinks(l.Real)
	if err != nil {
		return mapErr(err)
	}
	dstR := resolved(d.Real)
	if d.Real == l.Real || within(l.Real, d.Real) || within(srcReal, dstR) {
		return fmt.Errorf("%w: 不能复制到自身或其子目录中", ErrInvalid)
	}
	if within(d.Real, l.Real) || within(dstR, srcReal) {
		return fmt.Errorf("%w: 不能覆盖包含源的目录", ErrInvalid)
	}
	defer f.lockMirrors(d.Root)()
	replaced := false
	if _, err := os.Lstat(d.Real); err == nil {
		if !overwrite {
			return ErrExists
		}
		if err := os.RemoveAll(d.Real); err != nil {
			return mapErr(err)
		}
		replaced = true
	}
	if err := os.MkdirAll(filepath.Dir(d.Real), 0o755); err != nil {
		return mapErr(err)
	}
	if err := copyTree(srcReal, d.Real); err != nil {
		os.RemoveAll(d.Real)
		if replaced {
			f.mirror(d.Root, d.Rel, false)
		}
		return err
	}
	f.mirror(d.Root, d.Rel, replaced)
	f.changed(d.Real)
	return nil
}

// Remove deletes a file or directory tree.
func (f *FS) Remove(vpath string) error {
	l, err := f.Locate(vpath)
	if err != nil {
		return err
	}
	if l.IsVirtualRoot() || l.IsMountRoot() {
		return ErrPermission
	}
	if err := f.writableLoc(l); err != nil {
		return err
	}
	if _, err := os.Lstat(l.Real); err != nil {
		return mapErr(err)
	}
	defer f.lockMirrors(l.Root)()
	if err := os.RemoveAll(l.Real); err != nil {
		f.mirror(l.Root, l.Rel, false)
		return mapErr(err)
	}
	f.mirror(l.Root, l.Rel, false)
	f.changed(l.Real)
	return nil
}

// Changed lets callers that write through OpenFile report modifications.
func (f *FS) Changed(l *Loc) {
	if l != nil {
		if l.Root != nil && l.Rel != "" {
			unlock := f.lockMirrors(l.Root)
			f.mirror(l.Root, l.Rel, false)
			unlock()
		}
		f.changed(l.Real)
	}
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return mapErr(err)
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := de.Info()
		if err != nil {
			return mapErr(err)
		}
		switch {
		case de.IsDir():
			return mapErr(os.MkdirAll(target, info.Mode().Perm()|0o700))
		case info.Mode()&fs.ModeSymlink != 0:
			return nil // never copy symlinks; they might point outside the root
		case info.Mode().IsRegular():
			return copyFile(p, target, info)
		}
		return nil
	})
}

func copyFile(src, dst string, info fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return mapErr(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return mapErr(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, time.Now(), info.ModTime())
}

func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || len(name) > 255 ||
		strings.HasPrefix(name, TempPrefix) {
		return ErrInvalid
	}
	return nil
}

func isCrossDevice(err error) bool {
	var le *os.LinkError
	return errors.As(err, &le) && errors.Is(le.Err, syscall.EXDEV)
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrPermission):
		return ErrPermission
	case errors.Is(err, fs.ErrExist):
		return ErrExists
	case errors.Is(err, syscall.ENOTDIR):
		return ErrNotDir
	case errors.Is(err, syscall.EISDIR):
		return ErrIsDir
	}
	return err
}
