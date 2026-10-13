package webdav_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/studio-b12/gowebdav"

	"github.com/Sakana-1314/MList/server/internal/auth"
	"github.com/Sakana-1314/MList/server/internal/services/webdav"
	"github.com/Sakana-1314/MList/server/internal/store"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

type env struct {
	rw, ro, outside string
	srv             *httptest.Server
	token, roToken  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(base, name)
		os.MkdirAll(p, 0o755)
		return p
	}
	e := &env{rw: mk("rw"), ro: mk("ro"), outside: mk("outside")}
	data := mk("data")
	os.WriteFile(filepath.Join(e.ro, "readme.txt"), []byte("read only content"), 0o644)
	os.WriteFile(filepath.Join(e.outside, "secret.txt"), []byte("secret"), 0o644)
	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	au, err := auth.New(st, data, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("secret-pw")
	mounts := func(rw, ro string) []store.Mount {
		return []store.Mount{{Name: "rw", Path: rw, Writable: true}, {Name: "只读", Path: ro}}
	}
	u := &store.User{Username: "alice", PasswordHash: hash, Role: store.RoleUser, Grants: grantMounts(t, st, mounts(e.rw, e.ro)...)}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	// bob is granted the same buckets (lock namespaces are per user and must not collide).
	b := &store.User{Username: "bob", PasswordHash: hash, Role: store.RoleUser, Grants: grantMounts(t, st, mounts(e.rw, e.ro)...)}
	if err := st.CreateUser(b); err != nil {
		t.Fatal(err)
	}
	_, e.token, _ = au.CreateToken(u.ID, "rw", []string{"read", "write"}, 0)
	_, e.roToken, _ = au.CreateToken(u.ID, "ro", []string{"read"}, 0)
	newFS := func(p *auth.Principal) *vfs.FS { return vfs.New(p.User.Mounts, p.ReadOnly, nil) }
	dav := webdav.New(auth.NewBasic(au), newFS, func() bool { return true })
	e.srv = httptest.NewServer(dav)
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) client(user, pw string) *gowebdav.Client {
	return gowebdav.NewClient(e.srv.URL+webdav.Prefix, user, pw)
}

func (e *env) do(t *testing.T, method, p string, body io.Reader, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+p, body)
	req.SetBasicAuth("alice", "secret-pw")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func statusOf(err error) int {
	var se gowebdav.StatusError
	if errors.As(err, &se) {
		return se.Status
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		var se gowebdav.StatusError
		if errors.As(pe.Err, &se) {
			return se.Status
		}
	}
	return 0
}

func TestWebDAVClient(t *testing.T) {
	e := newEnv(t)
	c := e.client("alice", "secret-pw")
	if err := c.Connect(); err != nil {
		t.Fatal(err)
	}
	// Root lists the mounts.
	fis, err := c.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, fi := range fis {
		names = append(names, fi.Name())
		if !fi.IsDir() {
			t.Fatalf("%s not a dir", fi.Name())
		}
	}
	if strings.Join(names, ",") != "rw,只读" {
		t.Fatalf("root = %v", names)
	}
	// Depth 0 on root and on "/dav" without slash.
	for _, p := range []string{"/dav/", "/dav"} {
		resp, body := e.do(t, "PROPFIND", p, nil, map[string]string{"Depth": "0"})
		if resp.StatusCode != 207 || strings.Contains(body, "<D:href>/dav/rw/") || strings.Contains(body, "0001") {
			t.Fatalf("PROPFIND depth 0 %s: %d %s", p, resp.StatusCode, body)
		}
	}
	// Missing / infinite depth is served as depth 1.
	if err := c.MkdirAll("/rw/deep/er/est", 0); err != nil {
		t.Fatal(err)
	}
	resp, body := e.do(t, "PROPFIND", "/dav/rw/", nil, map[string]string{"Depth": "infinity"})
	if resp.StatusCode != 207 || strings.Contains(body, "/dav/rw/deep/er") || !strings.Contains(body, "/dav/rw/deep/") {
		t.Fatalf("infinity: %d %s", resp.StatusCode, body)
	}

	// Mkcol semantics.
	if err := c.Mkdir("/rw/dir", 0); err != nil {
		t.Fatal(err)
	}
	resp, _ = e.do(t, "MKCOL", "/dav/rw/dir", nil, nil)
	if resp.StatusCode != 405 {
		t.Fatalf("MKCOL existing: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, "MKCOL", "/dav/rw/missing/child", nil, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("MKCOL missing parent: %d", resp.StatusCode)
	}

	// Put / get / range / stat.
	data := bytes.Repeat([]byte("0123456789"), 100000)
	if err := c.Write("/rw/dir/中文 文件.bin", data, 0); err != nil {
		t.Fatal(err)
	}
	got, err := c.Read("/rw/dir/中文 文件.bin")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back: %v %d", err, len(got))
	}
	rs, err := c.ReadStreamRange("/rw/dir/中文 文件.bin", 5, 7)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := io.ReadAll(rs)
	rs.Close()
	if string(rb) != "5678901" {
		t.Fatalf("range = %q", rb)
	}
	fi, err := c.Stat("/rw/dir/中文 文件.bin")
	if err != nil || fi.Size() != int64(len(data)) || fi.ModTime().IsZero() {
		t.Fatalf("stat: %v %+v", err, fi)
	}
	if fi.(*gowebdav.File).ETag() == "" {
		t.Fatal("no etag in PROPFIND")
	}
	resp, _ = e.do(t, "HEAD", "/dav/rw/dir/中文 文件.bin", nil, nil)
	if resp.Header.Get("ETag") == "" || resp.Header.Get("Last-Modified") == "" || resp.ContentLength != int64(len(data)) {
		t.Fatalf("HEAD headers %v", resp.Header)
	}
	resp, _ = e.do(t, "GET", "/dav/rw/dir/中文 文件.bin", nil, map[string]string{"If-None-Match": resp.Header.Get("ETag")})
	if resp.StatusCode != 304 {
		t.Fatalf("If-None-Match: %d", resp.StatusCode)
	}
	// Streamed PUT (chunked, no Content-Length) like Finder does.
	if err := c.WriteStream("/rw/dir/stream.txt", io.MultiReader(strings.NewReader("streamed")), 0); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.rw, "dir", "stream.txt")); string(b) != "streamed" {
		t.Fatalf("stream put = %q", b)
	}
	// PUT into a missing directory is a conflict.
	// PUT into a missing directory is a conflict (gowebdav would retry with MKCOL, so use a raw request).
	if resp, _ := e.do(t, "PUT", "/dav/rw/nodir/x.txt", strings.NewReader("x"), nil); resp.StatusCode != 409 {
		t.Fatalf("PUT missing parent: %d", resp.StatusCode)
	}
	// PUT onto a collection.
	resp, _ = e.do(t, "PUT", "/dav/rw/dir", strings.NewReader("x"), nil)
	if resp.StatusCode != 405 {
		t.Fatalf("PUT on dir: %d", resp.StatusCode)
	}

	// Copy / move with Overwrite.
	c.Write("/rw/a.txt", []byte("A"), 0)
	c.Write("/rw/b.txt", []byte("B"), 0)
	if err := c.Copy("/rw/a.txt", "/rw/b.txt", false); statusOf(err) != 412 {
		t.Fatalf("copy no-overwrite: %v", err)
	}
	if err := c.Copy("/rw/a.txt", "/rw/b.txt", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.rw, "b.txt")); string(b) != "A" {
		t.Fatalf("copy overwrite = %q", b)
	}
	if err := c.Copy("/rw/dir", "/rw/dir-copy", false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.rw, "dir-copy", "stream.txt")); string(b) != "streamed" {
		t.Fatal("dir copy")
	}
	c.Write("/rw/c.txt", []byte("C"), 0)
	if err := c.Rename("/rw/c.txt", "/rw/b.txt", false); statusOf(err) != 412 {
		t.Fatalf("move no-overwrite: %v", err)
	}
	if err := c.Rename("/rw/c.txt", "/rw/b.txt", true); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.rw, "b.txt")); string(b) != "C" {
		t.Fatal("move overwrite")
	}
	// MOVE without Overwrite header defaults to T (RFC 4918).
	c.Write("/rw/d.txt", []byte("D"), 0)
	resp, _ = e.do(t, "MOVE", "/dav/rw/d.txt", nil, map[string]string{"Destination": e.srv.URL + "/dav/rw/b.txt"})
	if resp.StatusCode != 204 {
		t.Fatalf("MOVE default overwrite: %d", resp.StatusCode)
	}
	// Destination given as a path only and percent-encoded unicode.
	resp, _ = e.do(t, "MOVE", "/dav/rw/b.txt", nil, map[string]string{"Destination": "/dav/rw/%E6%96%B0.txt"})
	if resp.StatusCode != 201 {
		t.Fatalf("MOVE encoded dest: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.rw, "新.txt")); err != nil {
		t.Fatal(err)
	}
	// Cross-mount move into read-only is forbidden; copy out of read-only works.
	if err := c.Copy("/只读/readme.txt", "/rw/readme-copy.txt", false); err != nil {
		t.Fatal(err)
	}
	if err := c.Rename("/rw/新.txt", "/只读/新.txt", false); statusOf(err) != 403 {
		t.Fatalf("move into ro: %v", err)
	}

	// Delete.
	if err := c.RemoveAll("/rw/dir-copy"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.rw, "dir-copy")); !os.IsNotExist(err) {
		t.Fatal("not deleted")
	}
	resp, _ = e.do(t, "DELETE", "/dav/rw/nope", nil, nil)
	if resp.StatusCode != 404 {
		t.Fatalf("DELETE missing: %d", resp.StatusCode)
	}
	// Mount roots and the virtual root are protected.
	for _, m := range []string{"DELETE", "MKCOL"} {
		resp, _ = e.do(t, m, "/dav/rw", nil, nil)
		if resp.StatusCode != 403 && resp.StatusCode != 405 {
			t.Fatalf("%s mount root: %d", m, resp.StatusCode)
		}
	}
	resp, _ = e.do(t, "MOVE", "/dav/rw", nil, map[string]string{"Destination": "/dav/rw2"})
	if resp.StatusCode != 403 {
		t.Fatalf("MOVE mount root: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, "PUT", "/dav/x.txt", strings.NewReader("x"), nil)
	if resp.StatusCode != 403 {
		t.Fatalf("PUT in virtual root: %d", resp.StatusCode)
	}
}

func TestWebDAVReadOnlyAndAuth(t *testing.T) {
	e := newEnv(t)
	c := e.client("alice", "secret-pw")
	if b, err := c.Read("/只读/readme.txt"); err != nil || string(b) != "read only content" {
		t.Fatalf("read ro: %v", err)
	}
	if err := c.Write("/只读/new.txt", []byte("x"), 0); statusOf(err) != 403 {
		t.Fatalf("write ro mount: %v", err)
	}
	if err := c.Write("/只读/readme.txt", []byte("x"), 0); statusOf(err) != 403 {
		t.Fatalf("overwrite ro mount: %v", err)
	}
	if err := c.Mkdir("/只读/d", 0); statusOf(err) != 403 {
		t.Fatalf("mkcol ro mount: %v", err)
	}
	if err := c.Remove("/只读/readme.txt"); statusOf(err) != 403 {
		t.Fatalf("delete ro mount: %v", err)
	}
	if err := c.Rename("/只读/readme.txt", "/只读/r2.txt", false); statusOf(err) != 403 {
		t.Fatalf("move ro mount: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.ro, "readme.txt")); string(b) != "read only content" {
		t.Fatal("ro modified")
	}

	// Token auth (any username or the right one), read-only token.
	tc := e.client("alice", e.token)
	if err := tc.Write("/rw/tok.txt", []byte("t"), 0); err != nil {
		t.Fatal(err)
	}
	if err := e.client("", e.token).Connect(); err != nil {
		t.Fatalf("token with empty user: %v", err)
	}
	if err := e.client("bob", e.token).Connect(); err == nil {
		t.Fatal("token with wrong user accepted")
	}
	rc := e.client("alice", e.roToken)
	if _, err := rc.ReadDir("/rw"); err != nil {
		t.Fatal(err)
	}
	if err := rc.Write("/rw/tok2.txt", []byte("t"), 0); statusOf(err) != 403 {
		t.Fatalf("ro token write: %v", err)
	}
	// Wrong password / no auth.
	if err := e.client("alice", "wrong").Connect(); err == nil {
		t.Fatal("wrong password accepted")
	}
	resp, err := http.DefaultClient.Do(must(http.NewRequest("PROPFIND", e.srv.URL+"/dav/", nil)))
	if err != nil || resp.StatusCode != 401 || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("anonymous: %v %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// Traversal and symlink escapes.
	for _, p := range []string{"/dav/rw/../只读/readme.txt", "/dav/rw/%2e%2e/%2e%2e/outside/secret.txt", "/dav/../outside/secret.txt"} {
		resp, body := e.do(t, "GET", p, nil, nil)
		if strings.Contains(body, "secret") {
			t.Fatalf("GET %s leaked: %d %q", p, resp.StatusCode, body)
		}
	}
	os.Symlink(e.outside, filepath.Join(e.rw, "escape"))
	resp, body := e.do(t, "GET", "/dav/rw/escape/secret.txt", nil, nil)
	if resp.StatusCode == 200 || strings.Contains(body, "secret") {
		t.Fatalf("symlink escape: %d %q", resp.StatusCode, body)
	}
	_, body = e.do(t, "PROPFIND", "/dav/rw/", nil, map[string]string{"Depth": "1"})
	if strings.Contains(body, "escape") {
		t.Fatalf("escaping symlink listed: %s", body)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestWebDAVLocks(t *testing.T) {
	e := newEnv(t)
	lockBody := `<?xml version="1.0" encoding="utf-8"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner>me</D:owner></D:lockinfo>`
	resp, body := e.do(t, "LOCK", "/dav/rw/locked.txt", strings.NewReader(lockBody), map[string]string{"Timeout": "Second-60"})
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		t.Fatalf("LOCK: %d %s", resp.StatusCode, body)
	}
	token := resp.Header.Get("Lock-Token")
	if token == "" {
		t.Fatal("no lock token")
	}
	// Write without the token fails, with it succeeds.
	resp, _ = e.do(t, "PUT", "/dav/rw/locked.txt", strings.NewReader("x"), nil)
	if resp.StatusCode != 423 {
		t.Fatalf("PUT without token: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, "PUT", "/dav/rw/locked.txt", strings.NewReader("y"), map[string]string{"If": "(" + token + ")"})
	if resp.StatusCode != 201 && resp.StatusCode != 204 {
		t.Fatalf("PUT with token: %d", resp.StatusCode)
	}
	// Another user of the same bucket is unaffected.
	req, _ := http.NewRequest("PUT", e.srv.URL+"/dav/rw/locked.txt", strings.NewReader("bob"))
	req.SetBasicAuth("bob", "secret-pw")
	r2, err := http.DefaultClient.Do(req)
	if err != nil || (r2.StatusCode != 201 && r2.StatusCode != 204) {
		t.Fatalf("bob PUT blocked by alice's lock: %v %d", err, r2.StatusCode)
	}
	r2.Body.Close()
	resp, _ = e.do(t, "UNLOCK", "/dav/rw/locked.txt", nil, map[string]string{"Lock-Token": token})
	if resp.StatusCode != 204 {
		t.Fatalf("UNLOCK: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, "PUT", "/dav/rw/locked.txt", strings.NewReader("z"), nil)
	if resp.StatusCode != 201 && resp.StatusCode != 204 {
		t.Fatalf("PUT after unlock: %d", resp.StatusCode)
	}
	// LOCK of an existing file on a read-only mount is allowed (Office, Finder), creating one is not.
	resp, _ = e.do(t, "LOCK", "/dav/只读/readme.txt", strings.NewReader(lockBody), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("LOCK ro existing: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, "LOCK", "/dav/只读/new.txt", strings.NewReader(lockBody), nil)
	if resp.StatusCode != 403 {
		t.Fatalf("LOCK ro new: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, "OPTIONS", "/dav/", nil, nil)
	if !strings.Contains(resp.Header.Get("DAV"), "2") {
		t.Fatalf("DAV header %q", resp.Header.Get("DAV"))
	}
}

func TestWebDAVProppatch(t *testing.T) {
	e := newEnv(t)
	os.WriteFile(filepath.Join(e.rw, "w.txt"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(e.rw, "wdir"), 0o755)
	body := `<?xml version="1.0" encoding="utf-8" ?><D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:"><D:set><D:prop>` +
		`<Z:Win32CreationTime>Mon, 14 Sep 2020 12:26:40 GMT</Z:Win32CreationTime><Z:Win32LastModifiedTime>Mon, 14 Sep 2020 12:26:40 GMT</Z:Win32LastModifiedTime>` +
		`<Z:Win32FileAttributes>00000020</Z:Win32FileAttributes></D:prop></D:set></D:propertyupdate>`
	for _, p := range []string{"/dav/rw/w.txt", "/dav/rw/wdir"} {
		resp, out := e.do(t, "PROPPATCH", p, strings.NewReader(body), nil)
		if resp.StatusCode != 207 || !strings.Contains(out, "200 OK") || strings.Contains(out, "403") {
			t.Fatalf("PROPPATCH %s: %d %s", p, resp.StatusCode, out)
		}
	}
	want := time.Date(2020, 9, 14, 12, 26, 40, 0, time.UTC)
	for _, n := range []string{"w.txt", "wdir"} {
		if fi, _ := os.Stat(filepath.Join(e.rw, n)); !fi.ModTime().Equal(want) {
			t.Fatalf("%s mtime = %v", n, fi.ModTime())
		}
	}
	q := `<?xml version="1.0"?><D:propfind xmlns:D="DAV:"><D:prop><D:quota-available-bytes/><D:quota-used-bytes/></D:prop></D:propfind>`
	resp, out := e.do(t, "PROPFIND", "/dav/rw/", strings.NewReader(q), map[string]string{"Depth": "0"})
	if resp.StatusCode != 207 || !strings.Contains(out, "quota-available-bytes") || strings.Contains(out, "404") {
		t.Fatalf("quota props: %s", out)
	}
	resp, _ = e.do(t, "PROPPATCH", "/dav/只读/readme.txt", strings.NewReader(body), nil)
	if resp.StatusCode != 403 {
		t.Fatalf("PROPPATCH ro: %d", resp.StatusCode)
	}
}

// An interrupted PUT must not replace the existing file nor leave temp files visible.
func TestWebDAVInterruptedPut(t *testing.T) {
	e := newEnv(t)
	os.WriteFile(filepath.Join(e.rw, "keep.txt"), []byte("original"), 0o644)
	conn, err := net.Dial("tcp", e.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	req := "PUT /dav/rw/keep.txt HTTP/1.1\r\nHost: x\r\nAuthorization: Basic YWxpY2U6c2VjcmV0LXB3\r\nContent-Length: 1000000\r\n\r\npartial data"
	conn.Write([]byte(req))
	time.Sleep(300 * time.Millisecond)
	conn.Close()
	time.Sleep(300 * time.Millisecond)
	if b, _ := os.ReadFile(filepath.Join(e.rw, "keep.txt")); string(b) != "original" {
		t.Fatalf("file clobbered by interrupted PUT: %q", b)
	}
	des, _ := os.ReadDir(e.rw)
	for _, de := range des {
		if strings.HasPrefix(de.Name(), vfs.TempPrefix) {
			t.Fatalf("temp file left behind: %s", de.Name())
		}
	}
	// Temp files that do exist (upload in progress) are hidden and not addressable for writes.
	os.WriteFile(filepath.Join(e.rw, vfs.TempPrefix+"inprogress"), []byte("x"), 0o644)
	_, body := e.do(t, "PROPFIND", "/dav/rw/", nil, map[string]string{"Depth": "1"})
	if strings.Contains(body, vfs.TempPrefix) {
		t.Fatal("temp file listed")
	}
	resp, _ := e.do(t, "PUT", "/dav/rw/"+vfs.TempPrefix+"evil", strings.NewReader("x"), nil)
	if resp.StatusCode != 403 {
		t.Fatalf("PUT temp name: %d", resp.StatusCode)
	}
}

func TestWebDAVLargeDir(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	e := newEnv(t)
	for i := 0; i < 5000; i++ {
		os.WriteFile(filepath.Join(e.rw, fmt.Sprintf("f%05d.txt", i)), []byte("x"), 0o644)
	}
	start := time.Now()
	fis, err := e.client("alice", "secret-pw").ReadDir("/rw")
	if err != nil || len(fis) != 5000 {
		t.Fatalf("readdir: %v %d", err, len(fis))
	}
	t.Logf("PROPFIND depth 1 with 5000 entries: %v", time.Since(start))
}

// grantMounts creates a bucket for every mount (reusing one with the same
// name) and returns the matching grants.
func grantMounts(t testing.TB, st *store.Store, mounts ...store.Mount) []store.Grant {
	t.Helper()
	bs, err := st.ListBuckets()
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Grant
	for _, m := range mounts {
		var id uint
		for _, b := range bs {
			if b.Name == m.Name {
				id = b.ID
			}
		}
		if id == 0 {
			b := &store.Bucket{Name: m.Name, Paths: m.Dirs()}
			if err := st.CreateBucket(b); err != nil {
				t.Fatal(err)
			}
			bs = append(bs, *b)
			id = b.ID
		}
		out = append(out, store.Grant{BucketID: id, Writable: m.Writable})
	}
	return out
}
