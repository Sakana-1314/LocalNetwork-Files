// Package webdav serves each user's virtual filesystem over WebDAV, so it
// can be mounted as a network drive (Windows, macOS Finder, davfs2, rclone).
package webdav

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/auth"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/vfs"
)

const Prefix = "/dav"

// Server is an http.Handler serving WebDAV under Prefix.
type Server struct {
	auth    *auth.Basic
	newFS   func(p *auth.Principal) *vfs.FS
	enabled func() bool
	locks   webdav.LockSystem
}

func New(a *auth.Basic, newFS func(p *auth.Principal) *vfs.FS, enabled func() bool) *Server {
	return &Server{auth: a, newFS: newFS, enabled: enabled, locks: webdav.NewMemLS()}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.enabled() {
		http.Error(w, "WebDAV service is disabled", http.StatusServiceUnavailable)
		return
	}
	p, err := s.auth.FromRequest(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="LocalNetwork Files"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	h := &webdav.Handler{
		Prefix:     Prefix,
		FileSystem: &davFS{fs: s.newFS(p)},
		LockSystem: s.locks,
	}
	h.ServeHTTP(w, r)
}

// davFS adapts vfs.FS to webdav.FileSystem.
type davFS struct{ fs *vfs.FS }

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
		return virtualDirInfo{name: "/"}, nil
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

func (d *davFS) OpenFile(_ context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	l, err := d.fs.Locate(name)
	if err != nil {
		return nil, toOSErr(err)
	}
	if l.IsVirtualRoot() {
		if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE) != 0 {
			return nil, os.ErrPermission
		}
		return &rootDir{fs: d.fs}, nil
	}
	f, loc, err := d.fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, toOSErr(err)
	}
	return &file{File: f, fs: d.fs, loc: loc}, nil
}

// file wraps os.File: hides temp files in listings, reports writes to the index.
type file struct {
	*os.File
	fs      *vfs.FS
	loc     *vfs.Loc
	written bool
}

func (f *file) Write(p []byte) (int, error) {
	f.written = true
	return f.File.Write(p)
}

func (f *file) Close() error {
	err := f.File.Close()
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

// rootDir is the virtual "/" listing the user's mounts.
type rootDir struct {
	fs   *vfs.FS
	read bool
}

func (r *rootDir) Close() error                   { return nil }
func (r *rootDir) Read([]byte) (int, error)       { return 0, os.ErrInvalid }
func (r *rootDir) Write([]byte) (int, error)      { return 0, os.ErrPermission }
func (r *rootDir) Seek(int64, int) (int64, error) { return 0, nil }
func (r *rootDir) Stat() (fs.FileInfo, error)     { return virtualDirInfo{name: "/"}, nil }
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

type renamedInfo struct {
	fs.FileInfo
	name string
}

func (r renamedInfo) Name() string { return r.name }

type virtualDirInfo struct{ name string }

func (v virtualDirInfo) Name() string       { return v.name }
func (v virtualDirInfo) Size() int64        { return 0 }
func (v virtualDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (v virtualDirInfo) ModTime() time.Time { return time.Time{} }
func (v virtualDirInfo) IsDir() bool        { return true }
func (v virtualDirInfo) Sys() any           { return nil }
