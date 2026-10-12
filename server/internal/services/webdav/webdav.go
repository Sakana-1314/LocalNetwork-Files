// Package webdav serves each user's virtual filesystem over WebDAV, so it
// can be mounted as a network drive (Windows, macOS Finder, davfs2, rclone).
package webdav

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"

	"github.com/Sakana-1314/MList/server/internal/auth"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

const Prefix = "/dav"

// Server is an http.Handler serving WebDAV under Prefix.
type Server struct {
	auth    *auth.Basic
	newFS   func(p *auth.Principal) *vfs.FS
	enabled func() bool
	started time.Time

	mu    sync.Mutex
	locks map[uint]webdav.LockSystem // per user: lock paths are relative to each user's virtual root
}

func New(a *auth.Basic, newFS func(p *auth.Principal) *vfs.FS, enabled func() bool) *Server {
	return &Server{auth: a, newFS: newFS, enabled: enabled, started: time.Now(), locks: map[uint]webdav.LockSystem{}}
}

func (s *Server) lockSystem(uid uint) webdav.LockSystem {
	s.mu.Lock()
	defer s.mu.Unlock()
	ls := s.locks[uid]
	if ls == nil {
		ls = webdav.NewMemLS()
		s.locks[uid] = ls
	}
	return ls
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.enabled() {
		http.Error(w, "WebDAV service is disabled", http.StatusServiceUnavailable)
		return
	}
	p, err := s.auth.FromRequest(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="MList", charset="UTF-8"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	fsys := s.newFS(p)
	if code := precheck(fsys, r); code != 0 {
		http.Error(w, http.StatusText(code), code)
		return
	}
	switch r.Method {
	case "PROPFIND":
		// Depth: infinity (also the default when the header is missing) would walk
		// whole mounts; serve one level instead, as most servers do.
		if d := r.Header.Get("Depth"); d != "0" && d != "1" {
			r.Header.Set("Depth", "1")
		}
	case "MOVE":
		// RFC 4918 §10.6: a missing Overwrite header means "T" (x/net treats it as "F").
		if r.Header.Get("Overwrite") == "" {
			r.Header.Set("Overwrite", "T")
		}
	case "PUT":
		// Let the filesystem know whether the body arrived completely, so a
		// failed upload never replaces the existing file.
		body := &trackedBody{ReadCloser: r.Body, want: r.ContentLength}
		r.Body = body
		r = r.WithContext(context.WithValue(r.Context(), putKey{}, body))
	}
	h := &webdav.Handler{
		Prefix:     Prefix,
		FileSystem: &davFS{fs: fsys, rootTime: s.started},
		LockSystem: s.lockSystem(p.User.ID),
	}
	h.ServeHTTP(w, r)
}

func davPath(p string) string {
	return strings.TrimPrefix(p, Prefix)
}

// precheck rejects writes the filesystem would refuse anyway with the proper
// status (x/net/webdav maps most errors to 404/405/500).
func precheck(fsys *vfs.FS, r *http.Request) int {
	src := davPath(r.URL.Path)
	switch r.Method {
	case "PUT":
		if c := writeCheck(fsys, src, false); c != 0 {
			return c
		}
		if e, err := fsys.Stat(src); err == nil && e.IsDir {
			return http.StatusMethodNotAllowed
		}
	case "MKCOL", "PROPPATCH":
		return writeCheck(fsys, src, false)
	case "DELETE":
		return writeCheck(fsys, src, true)
	case "LOCK":
		// Locking a missing resource creates it.
		if _, err := fsys.Stat(src); err != nil {
			return writeCheck(fsys, src, false)
		}
	case "MOVE", "COPY":
		u, err := url.Parse(r.Header.Get("Destination"))
		if err != nil || u.Path == "" {
			return 0 // let the handler report the bad destination
		}
		if r.Method == "MOVE" {
			if c := writeCheck(fsys, src, true); c != 0 {
				return c
			}
		}
		return writeCheck(fsys, davPath(u.Path), true)
	}
	return 0
}

// writeCheck returns 403 when name cannot be written; structural operations
// (delete/move/copy target) are also refused on mount roots.
func writeCheck(fsys *vfs.FS, name string, structural bool) int {
	l, err := fsys.Locate(name)
	if err != nil {
		// Unknown top-level names would be created in the virtual root, which only lists mounts.
		if errors.Is(err, vfs.ErrPermission) || (errors.Is(err, vfs.ErrNotFound) && !strings.Contains(strings.Trim(vfs.CleanPath(name), "/"), "/")) {
			return http.StatusForbidden
		}
		return 0
	}
	if l.IsVirtualRoot() || (structural && l.IsMountRoot()) || !l.Root.Writable {
		return http.StatusForbidden
	}
	if strings.HasPrefix(path.Base(l.VPath), vfs.TempPrefix) {
		return http.StatusForbidden
	}
	return 0
}

type putKey struct{}

// trackedBody records whether the request body was read completely.
type trackedBody struct {
	io.ReadCloser
	want int64
	n    int64
	eof  bool
	err  error
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	switch {
	case err == io.EOF:
		b.eof = true
	case err != nil:
		b.err = err
	}
	return n, err
}

func (b *trackedBody) complete() bool {
	return b.eof && b.err == nil && (b.want < 0 || b.n == b.want)
}

// davFS adapts vfs.FS to webdav.FileSystem.
type davFS struct {
	fs       *vfs.FS
	rootTime time.Time
}

func toOSErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, vfs.ErrNotFound):
		return os.ErrNotExist
	case errors.Is(err, vfs.ErrPermission), errors.Is(err, vfs.ErrReadOnly):
		return os.ErrPermission
	case errors.Is(err, vfs.ErrExists):
		return os.ErrExist
	case errors.Is(err, vfs.ErrInvalid):
		return os.ErrInvalid
	}
	return err
}

func (d *davFS) Mkdir(_ context.Context, name string, _ os.FileMode) error {
	l, err := d.fs.Locate(name)
	if err != nil {
		return toOSErr(err)
	}
	if l.Root != nil && l.Rel != "" {
		// WebDAV MKCOL requires the parent to exist.
		if fi, err := os.Stat(filepath.Dir(l.Real)); err != nil || !fi.IsDir() {
			return os.ErrNotExist
		}
	}
	_, err = d.fs.Mkdir(name)
	return toOSErr(err)
}

func (d *davFS) RemoveAll(_ context.Context, name string) error {
	return toOSErr(d.fs.Remove(name))
}

func (d *davFS) Rename(_ context.Context, oldName, newName string) error {
	return toOSErr(d.fs.Move(oldName, newName, false))
}

func (d *davFS) Stat(_ context.Context, name string) (os.FileInfo, error) {
	l, err := d.fs.Locate(name)
	if err != nil {
		return nil, toOSErr(err)
	}
	if l.IsVirtualRoot() {
		return virtualDirInfo{name: "/", mtime: d.rootTime}, nil
	}
	fi, err := os.Stat(l.Real)
	if err != nil {
		return nil, toOSErr(err)
	}
	if l.IsMountRoot() {
		return renamedInfo{fi, l.Root.Name}, nil
	}
	return fi, nil
}

func (d *davFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	l, err := d.fs.Locate(name)
	if err != nil {
		return nil, toOSErr(err)
	}
	if l.IsVirtualRoot() {
		if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 {
			return nil, os.ErrPermission
		}
		return &rootDir{fs: d.fs, mtime: d.rootTime}, nil
	}
	if tb, ok := ctx.Value(putKey{}).(*trackedBody); ok && flag&os.O_CREATE != 0 && flag&os.O_TRUNC != 0 {
		return d.createAtomic(name, tb)
	}
	if flag&os.O_RDWR != 0 && flag&(os.O_CREATE|os.O_TRUNC|os.O_APPEND) == 0 && !l.IsVirtualRoot() {
		// PROPPATCH opens its target O_RDWR, which fails on directories; write
		// permission was already checked by precheck.
		if fi, err := os.Stat(l.Real); err == nil && fi.IsDir() {
			flag = os.O_RDONLY
		}
	}
	f, loc, err := d.fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, toOSErr(err)
	}
	return &file{File: f, fs: d.fs, loc: loc}, nil
}

// createAtomic serves PUT: data goes to a hidden temp file in the target
// directory that replaces the destination only when the upload completed.
func (d *davFS) createAtomic(name string, body *trackedBody) (webdav.File, error) {
	l, err := d.fs.Locate(name)
	if err != nil {
		return nil, toOSErr(err)
	}
	if l.IsVirtualRoot() || l.IsMountRoot() || !l.Root.Writable {
		return nil, os.ErrPermission
	}
	tmp, err := os.CreateTemp(filepath.Dir(l.Real), vfs.TempPrefix+"*")
	if err != nil {
		return nil, err // missing parent → ErrNotExist → 409 Conflict
	}
	return &file{File: tmp, fs: d.fs, loc: l, body: body}, nil
}

// file wraps os.File: hides temp files in listings, reports writes to the index.
type file struct {
	*os.File
	fs      *vfs.FS
	loc     *vfs.Loc
	written bool
	body    *trackedBody // set for atomic PUT; File is then a temp file
}

func (f *file) Write(p []byte) (int, error) {
	f.written = true
	return f.File.Write(p)
}

func (f *file) Close() error {
	err := f.File.Close()
	if f.body != nil {
		tmp := f.File.Name()
		if err != nil || !f.body.complete() {
			os.Remove(tmp)
			if err == nil {
				err = errors.New("upload incomplete")
			}
			return err
		}
		os.Chmod(tmp, 0o644)
		if err = os.Rename(tmp, f.loc.Real); err != nil {
			os.Remove(tmp)
			return err
		}
		f.fs.Changed(f.loc)
		return nil
	}
	if f.written {
		f.fs.Changed(f.loc)
	}
	return err
}

func (f *file) Stat() (fs.FileInfo, error) {
	fi, err := f.File.Stat()
	if err == nil && f.loc.IsMountRoot() {
		return renamedInfo{fi, f.loc.Root.Name}, nil
	}
	return fi, err
}

func (f *file) Readdir(count int) ([]fs.FileInfo, error) {
	fis, err := f.File.Readdir(count)
	out := fis[:0]
	for _, fi := range fis {
		if strings.HasPrefix(fi.Name(), vfs.TempPrefix) {
			continue
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			// Locate rejects links escaping the mount; others are shown as their target.
			if _, lerr := f.fs.Locate(path.Join(f.loc.VPath, fi.Name())); lerr != nil {
				continue
			}
			target, serr := os.Stat(filepath.Join(f.loc.Real, fi.Name()))
			if serr != nil {
				continue
			}
			fi = renamedInfo{target, fi.Name()}
		}
		out = append(out, fi)
	}
	return out, err
}

// DeadProps implements webdav.DeadPropsHolder. No dead properties are stored;
// directories report RFC 4331 quota properties (free space shown by Finder,
// Windows Explorer and `rclone about`).
func (f *file) DeadProps() (map[xml.Name]webdav.Property, error) {
	if f.body != nil {
		return nil, nil
	}
	fi, err := f.File.Stat()
	if err != nil || !fi.IsDir() {
		return nil, nil
	}
	avail, used, ok := diskUsage(f.File.Name())
	if !ok {
		return nil, nil
	}
	return quotaProps(avail, used), nil
}

func quotaProps(avail, used uint64) map[xml.Name]webdav.Property {
	a := xml.Name{Space: "DAV:", Local: "quota-available-bytes"}
	u := xml.Name{Space: "DAV:", Local: "quota-used-bytes"}
	return map[xml.Name]webdav.Property{
		a: {XMLName: a, InnerXML: []byte(strconv.FormatUint(avail, 10))},
		u: {XMLName: u, InnerXML: []byte(strconv.FormatUint(used, 10))},
	}
}

// Patch accepts PROPPATCH so clients that set properties after an upload
// (Windows Explorer sets Win32*Time, Finder/others set custom properties) don't
// report errors. Win32LastModifiedTime is applied to the file; other
// properties are acknowledged but not stored.
func (f *file) Patch(patches []webdav.Proppatch) ([]webdav.Propstat, error) {
	ps := webdav.Propstat{Status: http.StatusOK}
	for _, p := range patches {
		for _, prop := range p.Props {
			if !p.Remove && prop.XMLName.Space == win32NS && prop.XMLName.Local == "Win32LastModifiedTime" {
				if t, err := http.ParseTime(strings.TrimSpace(string(prop.InnerXML))); err == nil {
					os.Chtimes(f.loc.Real, time.Now(), t)
				}
			}
			ps.Props = append(ps.Props, webdav.Property{XMLName: prop.XMLName})
		}
	}
	return []webdav.Propstat{ps}, nil
}

const win32NS = "urn:schemas-microsoft-com:"

// rootDir is the virtual "/" listing the user's mounts.
type rootDir struct {
	fs    *vfs.FS
	mtime time.Time
	read  bool
}

func (r *rootDir) Close() error                   { return nil }
func (r *rootDir) Read([]byte) (int, error)       { return 0, os.ErrInvalid }
func (r *rootDir) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (r *rootDir) Seek(int64, int) (int64, error) { return 0, nil }
func (r *rootDir) Stat() (fs.FileInfo, error)     { return virtualDirInfo{name: "/", mtime: r.mtime}, nil }
func (r *rootDir) Readdir(count int) ([]fs.FileInfo, error) {
	if r.read {
		return nil, nil
	}
	r.read = true
	var out []fs.FileInfo
	for _, root := range r.fs.Roots() {
		if fi, err := os.Stat(root.Path); err == nil {
			out = append(out, renamedInfo{fi, root.Name})
		}
	}
	return out, nil
}

// DeadProps reports the combined free space of the user's mounts.
func (r *rootDir) DeadProps() (map[xml.Name]webdav.Property, error) {
	var paths []string
	for _, root := range r.fs.Roots() {
		paths = append(paths, root.Path)
	}
	if avail, used, ok := diskUsage(paths...); ok {
		return quotaProps(avail, used), nil
	}
	return nil, nil
}

func (r *rootDir) Patch([]webdav.Proppatch) ([]webdav.Propstat, error) {
	return nil, os.ErrPermission
}

type renamedInfo struct {
	fs.FileInfo
	name string
}

func (r renamedInfo) Name() string { return r.name }

type virtualDirInfo struct {
	name  string
	mtime time.Time
}

func (v virtualDirInfo) Name() string       { return v.name }
func (v virtualDirInfo) Size() int64        { return 0 }
func (v virtualDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (v virtualDirInfo) ModTime() time.Time { return v.mtime }
func (v virtualDirInfo) IsDir() bool        { return true }
func (v virtualDirInfo) Sys() any           { return nil }
