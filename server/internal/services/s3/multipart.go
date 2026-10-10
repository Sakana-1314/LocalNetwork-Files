package s3

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Multipart uploads are staged under <data_dir>/s3-multipart/<uploadId>/ and
// assembled into the destination through the VFS on completion. Abandoned
// uploads are removed after uploadTTL.

const uploadTTL = 7 * 24 * time.Hour

type uploadMeta struct {
	UserID  uint      `json:"user_id"`
	Bucket  string    `json:"bucket"`
	Key     string    `json:"key"`
	Created time.Time `json:"created"`
}

func validUploadID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (s *Server) uploadPath(id string, parts ...string) string {
	return filepath.Join(append([]string{s.uploadDir, id}, parts...)...)
}

// loadUpload validates that the upload exists and belongs to the request's user/bucket/key.
func (s *Server) loadUpload(q *request) (string, *uploadMeta, error) {
	id := q.r.URL.Query().Get("uploadId")
	if !validUploadID(id) {
		return "", nil, errNoSuchUpload
	}
	b, err := os.ReadFile(s.uploadPath(id, "meta.json"))
	if err != nil {
		return "", nil, errNoSuchUpload
	}
	var m uploadMeta
	if json.Unmarshal(b, &m) != nil || m.UserID != q.p.User.ID || m.Bucket != q.bucket || m.Key != q.key {
		return "", nil, errNoSuchUpload
	}
	return id, &m, nil
}

func (s *Server) createUpload(q *request) {
	loc, err := q.fs.Locate(vpath(q.bucket, q.key))
	if err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	if loc.Root == nil || !loc.Root.Writable {
		writeErr(q.w, q.r, errAccessDenied)
		return
	}
	b := make([]byte, 16)
	rand.Read(b)
	id := hex.EncodeToString(b)
	if err := os.MkdirAll(s.uploadPath(id), 0o700); err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	meta, _ := json.Marshal(uploadMeta{UserID: q.p.User.ID, Bucket: q.bucket, Key: q.key, Created: time.Now()})
	if err := os.WriteFile(s.uploadPath(id, "meta.json"), meta, 0o600); err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	writeXML(q.w, struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		NS       string   `xml:"xmlns,attr"`
		Bucket   string
		Key      string
		UploadID string `xml:"UploadId"`
	}{NS: s3NS, Bucket: q.bucket, Key: q.key, UploadID: id})
}

func (s *Server) uploadPart(q *request) {
	id, _, err := s.loadUpload(q)
	if err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	n, err := strconv.Atoi(q.r.URL.Query().Get("partNumber"))
	if err != nil || n < 1 || n > 10000 {
		writeErr(q.w, q.r, errInvalidArgument)
		return
	}
	tmp, err := os.CreateTemp(s.uploadPath(id), "part-*.tmp")
	if err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	h := md5.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), body(q.r, q.sig))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		writeErr(q.w, q.r, err)
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if err := os.Rename(tmp.Name(), s.uploadPath(id, strconv.Itoa(n)+".part")); err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	os.WriteFile(s.uploadPath(id, strconv.Itoa(n)+".md5"), []byte(sum), 0o600)
	q.w.Header().Set("ETag", `"`+sum+`"`)
	q.w.WriteHeader(http.StatusOK)
}

func (s *Server) completeUpload(q *request) {
	id, _, err := s.loadUpload(q)
	if err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	var req struct {
		Parts []struct {
			PartNumber int
			ETag       string
		} `xml:"Part"`
	}
	if err := xml.NewDecoder(io.LimitReader(body(q.r, q.sig), 4<<20)).Decode(&req); err != nil || len(req.Parts) == 0 {
		writeErr(q.w, q.r, errMalformedXML)
		return
	}
	sort.SliceStable(req.Parts, func(i, j int) bool { return req.Parts[i].PartNumber < req.Parts[j].PartNumber })
	var readers []io.Reader
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	all := md5.New()
	for _, p := range req.Parts {
		sumHex, err := os.ReadFile(s.uploadPath(id, strconv.Itoa(p.PartNumber)+".md5"))
		if err != nil || (p.ETag != "" && strings.Trim(p.ETag, `"`) != string(sumHex)) {
			writeErr(q.w, q.r, errInvalidPart)
			return
		}
		raw, _ := hex.DecodeString(string(sumHex))
		all.Write(raw)
		f, err := os.Open(s.uploadPath(id, strconv.Itoa(p.PartNumber)+".part"))
		if err != nil {
			writeErr(q.w, q.r, errInvalidPart)
			return
		}
		files = append(files, f)
		readers = append(readers, f)
	}
	vp := vpath(q.bucket, q.key)
	if _, err := q.fs.WriteFile(vp, io.MultiReader(readers...), true); err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	for _, f := range files {
		f.Close()
	}
	files = nil
	os.RemoveAll(s.uploadPath(id))
	writeXML(q.w, struct {
		XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
		NS       string   `xml:"xmlns,attr"`
		Location string
		Bucket   string
		Key      string
		ETag     string
	}{NS: s3NS, Location: vp, Bucket: q.bucket, Key: q.key,
		ETag: `"` + hex.EncodeToString(all.Sum(nil)) + "-" + strconv.Itoa(len(req.Parts)) + `"`})
}

func (s *Server) abortUpload(q *request) {
	id, _, err := s.loadUpload(q)
	if err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	os.RemoveAll(s.uploadPath(id))
	q.w.WriteHeader(http.StatusNoContent)
}

type xmlPart struct {
	PartNumber   int
	LastModified string
	ETag         string
	Size         int64
}

func (s *Server) listParts(q *request) {
	id, _, err := s.loadUpload(q)
	if err != nil {
		writeErr(q.w, q.r, err)
		return
	}
	des, _ := os.ReadDir(s.uploadPath(id))
	var parts []xmlPart
	for _, de := range des {
		name := de.Name()
		if !strings.HasSuffix(name, ".part") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(name, ".part"))
		if err != nil {
			continue
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		sum, _ := os.ReadFile(s.uploadPath(id, strconv.Itoa(n)+".md5"))
		parts = append(parts, xmlPart{PartNumber: n, LastModified: s3Time(fi.ModTime()), ETag: `"` + string(sum) + `"`, Size: fi.Size()})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
	writeXML(q.w, struct {
		XMLName     xml.Name `xml:"ListPartsResult"`
		NS          string   `xml:"xmlns,attr"`
		Bucket      string
		Key         string
		UploadID    string `xml:"UploadId"`
		IsTruncated bool
		Parts       []xmlPart `xml:"Part"`
	}{NS: s3NS, Bucket: q.bucket, Key: q.key, UploadID: id, Parts: parts})
}

func (s *Server) listUploads(q *request) {
	type upload struct {
		Key       string
		UploadID  string `xml:"UploadId"`
		Initiated string
	}
	var ups []upload
	des, _ := os.ReadDir(s.uploadDir)
	for _, de := range des {
		if !validUploadID(de.Name()) {
			continue
		}
		b, err := os.ReadFile(s.uploadPath(de.Name(), "meta.json"))
		var m uploadMeta
		if err != nil || json.Unmarshal(b, &m) != nil || m.UserID != q.p.User.ID || m.Bucket != q.bucket {
			continue
		}
		if p := q.r.URL.Query().Get("prefix"); p != "" && !strings.HasPrefix(m.Key, p) {
			continue
		}
		ups = append(ups, upload{Key: m.Key, UploadID: de.Name(), Initiated: s3Time(m.Created)})
	}
	writeXML(q.w, struct {
		XMLName     xml.Name `xml:"ListMultipartUploadsResult"`
		NS          string   `xml:"xmlns,attr"`
		Bucket      string
		IsTruncated bool
		Uploads     []upload `xml:"Upload"`
	}{NS: s3NS, Bucket: q.bucket, Uploads: ups})
}

func (s *Server) cleanupUploads() {
	des, _ := os.ReadDir(s.uploadDir)
	for _, de := range des {
		if fi, err := de.Info(); err == nil && time.Since(fi.ModTime()) > uploadTTL {
			os.RemoveAll(filepath.Join(s.uploadDir, de.Name()))
		}
	}
}
