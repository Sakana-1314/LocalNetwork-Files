package s3

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/vfs"
)

const s3NS = "http://s3.amazonaws.com/doc/2006-03-01/"

func s3Time(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// etag derives a stable ETag from path, size and mtime. It deliberately uses the
// multipart form ("<hex>-1") so clients don't mistake it for the content MD5.
func etag(p string, size int64, mtime time.Time) string {
	sum := md5.Sum([]byte(fmt.Sprintf("%s|%d|%d", p, size, mtime.UnixNano())))
	return `"` + hex.EncodeToString(sum[:]) + `-1"`
}

// mtimeHeader is the user metadata rclone (and s3fs-style tools) use to carry
// the original modification time as "<unix seconds>.<nanoseconds>".
const mtimeHeader = "X-Amz-Meta-Mtime"

func formatMtime(t time.Time) string { return fmt.Sprintf("%d.%09d", t.Unix(), t.Nanosecond()) }

func parseMtime(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	sec, frac, _ := strings.Cut(v, ".")
	n, err := strconv.ParseInt(sec, 10, 64)
	if err != nil || n < 0 {
		return time.Time{}, false
	}
	if frac == "" && n > 1e12 { // milliseconds
		return time.UnixMilli(n), true
	}
	var ns int64
	if frac != "" {
		frac = (frac + "000000000")[:9]
		if ns, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return time.Time{}, false
		}
	}
	return time.Unix(n, ns), true
}

// applyMtime sets the file's modification time from client metadata, so tools
// that compare modification times (rclone sync) don't re-upload unchanged files.
func applyMtime(q *request, vp, v string) {
	t, ok := parseMtime(v)
	if !ok {
		return
	}
	if l, err := q.fs.Locate(vp); err == nil && l.Root != nil && l.Root.Writable && l.Rel != "" {
		os.Chtimes(l.Real, time.Now(), t)
	}
}

// pruneEmptyParents removes directories left empty by deleting key, up to
// (not including) the bucket root, matching S3 where prefixes vanish with
// their last object. os.Remove only succeeds on empty directories.
func pruneEmptyParents(q *request, key string) {
	dir := key
	for {
		i := strings.LastIndex(strings.TrimSuffix(dir, "/"), "/")
		if i < 0 {
			return
		}
		dir = dir[:i]
		l, err := q.fs.Locate(vpath(q.bucket, dir))
		if err != nil || l.Root == nil || l.Rel == "" || !l.Root.Writable {
			return
		}
		if os.Remove(l.Real) != nil {
			return
		}
		q.fs.Changed(l)
	}
}

// ---------- buckets ----------

func (s *Server) listBuckets(q *request) {
	type bucket struct {
		Name         string
		CreationDate string
	}
	res := struct {
		XMLName xml.Name `xml:"ListAllMyBucketsResult"`
		NS      string   `xml:"xmlns,attr"`
		Owner   struct{ ID, DisplayName string }
		Buckets []bucket `xml:"Buckets>Bucket"`
	}{NS: s3NS}
	res.Owner.ID, res.Owner.DisplayName = strconv.Itoa(int(q.p.User.ID)), q.p.User.Username
	for _, r := range q.fs.Roots() {
		created := time.Unix(0, 0)
		if fi, err := os.Stat(r.Path); err == nil {
			created = fi.ModTime()
		}
		res.Buckets = append(res.Buckets, bucket{Name: r.Name, CreationDate: s3Time(created)})
	}
	writeXML(q.w, res)
}

// ---------- listing ----------

type listEntry struct {
	key      string
	isPrefix bool
	size     int64
	mtime    time.Time
}

// collect gathers the candidate entries for a listing. With delimiter "/" only
// one directory is read; otherwise the subtree below the prefix is walked,
// skipping directories whose keys all sort at or before marker.
func (s *Server) collect(q *request, prefix, delimiter, marker string) ([]listEntry, error) {
	dirPart := ""
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		dirPart = prefix[:i+1]
	}
	var out []listEntry
	if delimiter == "/" {
		entries, _, err := q.fs.List(vpath(q.bucket, dirPart))
		if err != nil {
			if errors.Is(err, vfs.ErrNotFound) || errors.Is(err, vfs.ErrNotDir) {
				return nil, nil
			}
			return nil, err
		}
		for _, e := range entries {
			k := dirPart + e.Name
			if !strings.HasPrefix(k, prefix) {
				continue
			}
			if e.IsDir {
				out = append(out, listEntry{key: k + "/", isPrefix: true})
			} else {
				out = append(out, listEntry{key: k, size: e.Size, mtime: e.ModTime})
			}
		}
		return out, nil
	}
	loc, err := q.fs.Locate(vpath(q.bucket, dirPart))
	if err != nil {
		return nil, nil
	}
	rootLoc, _ := q.fs.Locate("/" + q.bucket)
	filepath.WalkDir(loc.Real, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			if de != nil && de.IsDir() && p != loc.Real {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(de.Name(), vfs.TempPrefix) || de.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(rootLoc.Real, p)
		k := filepath.ToSlash(rel)
		if de.IsDir() {
			if p == loc.Real {
				return nil
			}
			// Every key below has the form "k/...": skip the subtree when it cannot
			// match the prefix or lies entirely at/before the marker.
			dk := k + "/"
			if !strings.HasPrefix(dk, prefix) && !strings.HasPrefix(prefix, dk) {
				return fs.SkipDir
			}
			if marker != "" && dk < marker && !strings.HasPrefix(marker, dk) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(k, prefix) {
			return nil
		}
		fi, err := de.Info()
		if err != nil {
			return nil
		}
		if delimiter != "" {
			if i := strings.Index(k[len(prefix):], delimiter); i >= 0 {
				out = append(out, listEntry{key: k[:len(prefix)+i+len(delimiter)], isPrefix: true})
				return nil
			}
		}
		out = append(out, listEntry{key: k, size: fi.Size(), mtime: fi.ModTime()})
		return nil
	})
	return out, nil
}

type xmlObject struct {
	Key          string
	LastModified string
	ETag         string
	Size         int64
	StorageClass string
}

type xmlPrefix struct{ Prefix string }

func (s *Server) listObjects(q *request) {
	qs := q.r.URL.Query()
	v2 := qs.Get("list-type") == "2"
	prefix, delimiter := qs.Get("prefix"), qs.Get("delimiter")
	maxKeys := 1000
	if v := qs.Get("max-keys"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeErr(q.w, q.r, errInvalidArgument)
			return
		}
		maxKeys = min(n, 1000)
	}
	marker := qs.Get("marker")
	if v2 {
		marker = qs.Get("start-after")
		if tok := qs.Get("continuation-token"); tok != "" {
			b, err := base64.RawURLEncoding.DecodeString(tok)
			if err != nil {
				writeErr(q.w, q.r, errInvalidArgument)
				return
			}
			marker = string(b)
		}
	}

	all, err := s.collect(q, prefix, delimiter, marker)
	if err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].key < all[j].key })
	// Deduplicate common prefixes produced by the recursive walk.
	dedup := all[:0]
	for i, e := range all {
		if i > 0 && e.isPrefix && all[i-1].isPrefix && all[i-1].key == e.key {
			continue
		}
		dedup = append(dedup, e)
	}
	all = dedup

	var contents []xmlObject
	var prefixes []xmlPrefix
	truncated := false
	last := ""
	count := 0
	for _, e := range all {
		if marker != "" && e.key <= marker {
			continue
		}
		if count >= maxKeys {
			truncated = maxKeys > 0
			break
		}
		count++
		last = e.key
		if e.isPrefix {
			prefixes = append(prefixes, xmlPrefix{Prefix: encodeKey(q, e.key)})
		} else {
			contents = append(contents, xmlObject{Key: encodeKey(q, e.key), LastModified: s3Time(e.mtime),
				ETag: etag(vpath(q.bucket, e.key), e.size, e.mtime), Size: e.size, StorageClass: "STANDARD"})
		}
	}
	encType := ""
	if qs.Get("encoding-type") == "url" {
		encType = "url"
	}
	if v2 {
		res := struct {
			XMLName               xml.Name `xml:"ListBucketResult"`
			NS                    string   `xml:"xmlns,attr"`
			Name                  string
			Prefix                string
			Delimiter             string `xml:",omitempty"`
			MaxKeys               int
			KeyCount              int
			IsTruncated           bool
			ContinuationToken     string `xml:",omitempty"`
			NextContinuationToken string `xml:",omitempty"`
			StartAfter            string `xml:",omitempty"`
			EncodingType          string `xml:",omitempty"`
			Contents              []xmlObject
			CommonPrefixes        []xmlPrefix
		}{NS: s3NS, Name: q.bucket, Prefix: encodeKey(q, prefix), Delimiter: encodeKey(q, delimiter), MaxKeys: maxKeys, KeyCount: count,
			IsTruncated: truncated, ContinuationToken: qs.Get("continuation-token"), StartAfter: encodeKey(q, qs.Get("start-after")),
			EncodingType: encType, Contents: contents, CommonPrefixes: prefixes}
		if truncated {
			res.NextContinuationToken = base64.RawURLEncoding.EncodeToString([]byte(last))
		}
		writeXML(q.w, res)
		return
	}
	res := struct {
		XMLName        xml.Name `xml:"ListBucketResult"`
		NS             string   `xml:"xmlns,attr"`
		Name           string
		Prefix         string
		Marker         string
		NextMarker     string `xml:",omitempty"`
		Delimiter      string `xml:",omitempty"`
		MaxKeys        int
		IsTruncated    bool
		EncodingType   string `xml:",omitempty"`
		Contents       []xmlObject
		CommonPrefixes []xmlPrefix
	}{NS: s3NS, Name: q.bucket, Prefix: encodeKey(q, prefix), Marker: encodeKey(q, marker), Delimiter: encodeKey(q, delimiter), MaxKeys: maxKeys,
		IsTruncated: truncated, EncodingType: encType, Contents: contents, CommonPrefixes: prefixes}
	if truncated {
		res.NextMarker = encodeKey(q, last)
	}
	writeXML(q.w, res)
}

// ---------- objects ----------

func (s *Server) getObject(q *request) {
	vp := vpath(q.bucket, q.key)
	if strings.HasSuffix(q.key, "/") {
		e, err := q.fs.Stat(vp)
		if err != nil || !e.IsDir {
			writeErr(q.w, q.r, errNoSuchKey)
			return
		}
		q.w.Header().Set("Content-Type", "application/x-directory")
		q.w.Header().Set("Last-Modified", e.ModTime.UTC().Format(http.TimeFormat))
		q.w.Header().Set("ETag", etag(vp, 0, e.ModTime))
		q.w.Header().Set("Content-Length", "0")
		q.w.WriteHeader(http.StatusOK)
		return
	}
	f, fi, err := q.fs.Open(vp)
	if err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	defer f.Close()
	h := q.w.Header()
	h.Set("ETag", etag(vp, fi.Size(), fi.ModTime()))
	h.Set("Content-Type", vfs.MimeOf(fi.Name(), false))
	h.Set("Accept-Ranges", "bytes")
	h.Set(mtimeHeader, formatMtime(fi.ModTime()))
	if v := q.r.URL.Query().Get("response-content-type"); v != "" {
		h.Set("Content-Type", v)
	}
	if v := q.r.URL.Query().Get("response-content-disposition"); v != "" {
		h.Set("Content-Disposition", v)
	}
	http.ServeContent(q.w, q.r, "", fi.ModTime(), f)
}

func (s *Server) putObject(q *request) {
	vp := vpath(q.bucket, q.key)
	if strings.HasSuffix(q.key, "/") {
		// Directory marker.
		io.Copy(io.Discard, body(q.r, q.sig))
		if _, err := q.fs.Mkdir(vp); err != nil && !errors.Is(err, vfs.ErrExists) {
			writeErr(q.w, q.r, err)
			return
		}
		q.w.Header().Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
		q.w.WriteHeader(http.StatusOK)
		return
	}
	overwrite := q.r.Header.Get("If-None-Match") != "*"
	h := md5.New()
	_, err := q.fs.WriteFile(vp, io.TeeReader(body(q.r, q.sig), h), overwrite)
	if err != nil {
		if errors.Is(err, vfs.ErrExists) {
			writeErr(q.w, q.r, &s3Error{412, "PreconditionFailed", "At least one of the pre-conditions you specified did not hold"})
			return
		}
		writeErr(q.w, q.r, err)
		return
	}
	applyMtime(q, vp, q.r.Header.Get(mtimeHeader))
	q.w.Header().Set("ETag", `"`+hex.EncodeToString(h.Sum(nil))+`"`)
	q.w.WriteHeader(http.StatusOK)
}

func parseCopySource(v string) (string, string, error) {
	v, _, _ = strings.Cut(v, "?") // drop versionId
	dec, err := url.PathUnescape(v)
	if err != nil {
		return "", "", err
	}
	dec = strings.TrimPrefix(dec, "/")
	b, k, ok := strings.Cut(dec, "/")
	if !ok || b == "" || k == "" || !validKey(k) {
		return "", "", errors.New("invalid copy source")
	}
	return b, k, nil
}

func (s *Server) copyObject(q *request) {
	sb, sk, err := parseCopySource(q.r.Header.Get("X-Amz-Copy-Source"))
	if err != nil {
		writeErr(q.w, q.r, errInvalidArgument)
		return
	}
	if q.fs.Root(sb) == nil {
		writeErr(q.w, q.r, errNoSuchBucket)
		return
	}
	src, dst := vpath(sb, sk), vpath(q.bucket, q.key)
	if src != dst {
		if _, _, err := q.fs.LocalFile(src); err != nil {
			writeErr(q.w, q.r, err)
			return
		}
		if err := q.fs.Copy(src, dst, true); err != nil {
			writeErr(q.w, q.r, err)
			return
		}
	}
	if strings.EqualFold(q.r.Header.Get("X-Amz-Metadata-Directive"), "REPLACE") {
		// rclone sets modification times with a self-copy that replaces metadata.
		applyMtime(q, dst, q.r.Header.Get(mtimeHeader))
	}
	e, err := q.fs.Stat(dst)
	if err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	writeXML(q.w, struct {
		XMLName      xml.Name `xml:"CopyObjectResult"`
		NS           string   `xml:"xmlns,attr"`
		LastModified string
		ETag         string
	}{NS: s3NS, LastModified: s3Time(e.ModTime), ETag: etag(dst, e.Size, e.ModTime)})
}

// removeKey deletes an object; missing keys are not an error (S3 semantics).
func (s *Server) removeKey(q *request, key string) error {
	vp := vpath(q.bucket, key)
	e, err := q.fs.Stat(vp)
	if err != nil {
		if errors.Is(err, vfs.ErrNotFound) {
			return nil
		}
		return err
	}
	if e.IsDir {
		// Only "dir/" markers delete directories, and only empty ones.
		if !strings.HasSuffix(key, "/") {
			return nil
		}
		if entries, _, err := q.fs.List(vp); err != nil || len(entries) > 0 {
			return err
		}
	}
	if err := q.fs.Remove(vp); err != nil {
		return err
	}
	pruneEmptyParents(q, key)
	return nil
}

func (s *Server) deleteObject(q *request) {
	if err := s.removeKey(q, q.key); err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	q.w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteObjects(q *request) {
	var req struct {
		Quiet   bool
		Objects []struct{ Key string } `xml:"Object"`
	}
	if err := xml.NewDecoder(io.LimitReader(body(q.r, q.sig), 4<<20)).Decode(&req); err != nil {
		writeErr(q.w, q.r, errMalformedXML)
		return
	}
	type deleted struct{ Key string }
	type delErr struct{ Key, Code, Message string }
	res := struct {
		XMLName xml.Name  `xml:"DeleteResult"`
		NS      string    `xml:"xmlns,attr"`
		Deleted []deleted `xml:"Deleted"`
		Error   []delErr  `xml:"Error"`
	}{NS: s3NS}
	for _, o := range req.Objects {
		if err := s.removeKey(q, o.Key); err != nil {
			e := mapVFSErr(err)
			res.Error = append(res.Error, delErr{Key: o.Key, Code: e.Code, Message: e.Message})
		} else if !req.Quiet {
			res.Deleted = append(res.Deleted, deleted{Key: o.Key})
		}
	}
	writeXML(q.w, res)
}
