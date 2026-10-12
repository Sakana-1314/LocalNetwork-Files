// Package s3 implements an S3-compatible endpoint over each user's virtual
// filesystem: every authorized directory is exposed as a bucket. Supported:
// ListBuckets, Head/GetBucketLocation/Versioning, ListObjects (v1/v2),
// Get/Head/Put/Copy/Delete object, DeleteObjects and multipart uploads.
// Authentication uses AWS SigV4 with the user's S3 access keys. The region is
// taken from each request's credential and may be any value.
package s3

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Sakana-1314/MList/server/internal/auth"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

// defaultRegion is reported by GetBucketLocation. The region in a request's
// SigV4 credential is whatever the client configured and is not validated;
// any value is accepted. "auto" matches what the mount guide tells clients to
// configure, so the value the server reports agrees with the UI.
const defaultRegion = "auto"

// Prefix is the path S3 is served under on the main HTTP port. Clients must
// point their endpoint at http://host:port/s3 and use path-style addressing.
const Prefix = "/s3"

// Server implements the S3-compatible API as an http.Handler mounted under
// Prefix on the main HTTP port. It owns no listener: the web server routes /s3
// here, which keeps S3 on the same port (and therefore behind the same
// reverse proxy, TLS certificate and firewall rule) as everything else.
type Server struct {
	auth      *auth.Service
	newFS     func(p *auth.Principal) *vfs.FS
	uploadDir string

	enabled bool

	stopOnce sync.Once
	stop     chan struct{}
}

func New(a *auth.Service, newFS func(p *auth.Principal) *vfs.FS, dataDir string) *Server {
	return &Server{auth: a, newFS: newFS, uploadDir: filepath.Join(dataDir, "s3-multipart"), stop: make(chan struct{})}
}

// SetEnabled turns the API on or off. Requests are rejected while disabled, so
// the route can stay mounted unconditionally.
func (s *Server) SetEnabled(enabled bool) {
	s.enabled = enabled
	if enabled {
		go s.cleanupUploads()
	}
}

func (s *Server) Enabled() bool { return s.enabled }

func (s *Server) Close() { s.stopOnce.Do(func() { close(s.stop) }) }

// ---------- errors ----------

type s3Error struct {
	Status  int
	Code    string
	Message string
}

func (e *s3Error) Error() string { return e.Code + ": " + e.Message }

var (
	errAccessDenied      = &s3Error{403, "AccessDenied", "Access Denied"}
	errNoSuchBucket      = &s3Error{404, "NoSuchBucket", "The specified bucket does not exist"}
	errNoSuchKey         = &s3Error{404, "NoSuchKey", "The specified key does not exist."}
	errNoSuchUpload      = &s3Error{404, "NoSuchUpload", "The specified multipart upload does not exist."}
	errSignature         = &s3Error{403, "SignatureDoesNotMatch", "The request signature we calculated does not match the signature you provided."}
	errInvalidKey        = &s3Error{403, "InvalidAccessKeyId", "The AWS Access Key Id you provided does not exist in our records."}
	errNotImplemented    = &s3Error{501, "NotImplemented", "A header or query you provided implies functionality that is not implemented."}
	errMalformedXML      = &s3Error{400, "MalformedXML", "The XML you provided was not well-formed."}
	errInvalidPart       = &s3Error{400, "InvalidPart", "One or more of the specified parts could not be found."}
	errBucketNotEmpty    = &s3Error{409, "BucketAlreadyOwnedByYou", "Buckets map to directories configured by the administrator."}
	errInvalidArgument   = &s3Error{400, "InvalidArgument", "Invalid argument."}
	errCreateBucket      = &s3Error{403, "AccessDenied", "Buckets map to directories configured by the administrator and cannot be created over S3."}
	errInvalidObjectName = &s3Error{400, "InvalidArgument", "Object keys must not contain \".\" or \"..\" path segments."}
)

func mapVFSErr(err error) *s3Error {
	var se *s3Error
	switch {
	case errors.As(err, &se):
		return se
	case errors.Is(err, vfs.ErrNotFound), errors.Is(err, vfs.ErrIsDir), errors.Is(err, vfs.ErrNotDir):
		return errNoSuchKey
	case errors.Is(err, vfs.ErrPermission), errors.Is(err, vfs.ErrReadOnly):
		return errAccessDenied
	case errors.Is(err, vfs.ErrInvalid):
		return &s3Error{400, "InvalidArgument", err.Error()}
	case errors.Is(err, errBadDigest):
		return &s3Error{400, "XAmzContentSHA256Mismatch", "The provided 'x-amz-content-sha256' header does not match what was computed."}
	}
	return &s3Error{500, "InternalError", err.Error()}
}

func requestID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return strings.ToUpper(hex.EncodeToString(b))
}

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	e := mapVFSErr(err)
	if e.Status >= 500 {
		slog.Warn("s3 error", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(e.Status)
	if r.Method == http.MethodHead {
		return
	}
	xml.NewEncoder(w).Encode(struct {
		XMLName   xml.Name `xml:"Error"`
		Code      string
		Message   string
		Resource  string
		RequestID string `xml:"RequestId"`
	}{Code: e.Code, Message: e.Message, Resource: r.URL.Path, RequestID: requestID()})
}

func writeXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml")
	w.Write([]byte(xml.Header))
	xml.NewEncoder(w).Encode(v)
}

// ---------- routing ----------

type request struct {
	w      http.ResponseWriter
	r      *http.Request
	sig    *sigParams
	p      *auth.Principal
	fs     *vfs.FS
	bucket string
	key    string
}

// ServeHTTP handles a request whose path still includes Prefix. The prefix is
// removed *before* verifying the signature: a client configured with the
// endpoint http://host:port/s3 signs the full path ("/s3/bucket/key"), so
// verification must see that same path. splitBucketKey then works on the
// prefix-free path, exactly as it did when S3 had its own listener.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.enabled {
		writeErr(w, r, &s3Error{403, "AccessDenied", "The S3 API is disabled"})
		return
	}
	full := r.URL.Path
	w.Header().Set("Server", "MList")
	w.Header().Set("x-amz-request-id", requestID())
	sig, err := parseSigV4(r)
	if err != nil {
		switch {
		case errors.Is(err, errMissingAuth):
			writeErr(w, r, errAccessDenied)
		case errors.Is(err, errSkew):
			writeErr(w, r, &s3Error{403, "RequestTimeTooSkewed", "The difference between the request time and the server's time is too large."})
		case errors.Is(err, errExpired):
			writeErr(w, r, &s3Error{403, "AccessDenied", "Request has expired"})
		default:
			writeErr(w, r, &s3Error{400, "AuthorizationHeaderMalformed", err.Error()})
		}
		return
	}
	key, user, secret, err := s.auth.LookupS3Key(sig.accessKey)
	if err != nil {
		writeErr(w, r, errInvalidKey)
		return
	}
	p := &auth.Principal{User: user, ReadOnly: key.ReadOnly, Via: "s3"}

	// Signature first, against the path the client actually signed. A client
	// whose endpoint is http://host:port/s3 signs "/s3/bucket/key", so the
	// prefix must still be present here; stripping it beforehand would make
	// every signature mismatch.
	var ok bool
	if sig.v2 {
		ok = sig.verifyV2(r, secret, v2BucketPrefix(r, s.newFS(p)))
	} else {
		ok = sig.verify(r, secret)
	}
	if !ok {
		writeErr(w, r, errSignature)
		return
	}

	// Routing, on the other hand, works on the prefix-free path so that the
	// bucket/key parsing is identical to a server listening on a dedicated port.
	stripped := *r
	u := *r.URL
	u.Path = strings.TrimPrefix(full, Prefix)
	stripped.URL = &u
	req := &request{w: w, r: &stripped, sig: sig, p: p, fs: s.newFS(p)}
	req.bucket, req.key, _ = splitBucketKey(&stripped, req.fs)
	if !validKey(req.key) {
		writeErr(w, r, errInvalidObjectName)
		return
	}
	s.route(req)
}

// v2BucketPrefix is the "/bucket" part Signature V2 includes for virtual-hosted
// requests; path-style requests sign the path alone.
func v2BucketPrefix(r *http.Request, fsys *vfs.FS) string {
	if bucket, _, vhost := splitBucketKey(r, fsys); vhost {
		return "/" + bucket
	}
	return ""
}

// splitBucketKey supports path-style (/bucket/key) and virtual-hosted style
// (<bucket>.<endpoint host>). The latter is assumed only when the first host
// label names one of the user's buckets and the rest still looks like a full
// endpoint host ("nas.lan", "localhost"), so that reaching the server as e.g.
// "media.local" keeps path-style working for a bucket named "media".
func splitBucketKey(r *http.Request, fsys *vfs.FS) (bucket, key string, vhost bool) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if net.ParseIP(host) == nil {
		label, rest, ok := strings.Cut(strings.TrimSuffix(host, "."), ".")
		if ok && fsys.Root(label) != nil && (strings.Contains(rest, ".") || strings.EqualFold(rest, "localhost")) {
			return label, strings.TrimPrefix(r.URL.Path, "/"), true
		}
	}
	bucket, key, _ = strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	return bucket, key, false
}

func (s *Server) route(q *request) {
	r := q.r
	qs := r.URL.Query()
	if q.bucket == "" {
		if r.Method == http.MethodGet {
			s.listBuckets(q)
			return
		}
		writeErr(q.w, r, errNotImplemented)
		return
	}
	root := q.fs.Root(q.bucket)
	if root == nil {
		if q.key == "" && r.Method == http.MethodPut {
			writeErr(q.w, r, errCreateBucket)
			return
		}
		writeErr(q.w, r, errNoSuchBucket)
		return
	}
	if q.key == "" {
		switch {
		case r.Method == http.MethodHead:
			q.w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && qs.Has("location"):
			writeXML(q.w, struct {
				XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ LocationConstraint"`
				Value   string   `xml:",chardata"`
			}{Value: defaultRegion})
		case r.Method == http.MethodGet && qs.Has("versioning"):
			writeXML(q.w, struct {
				XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ VersioningConfiguration"`
			}{})
		case r.Method == http.MethodGet && qs.Has("uploads"):
			s.listUploads(q)
		case r.Method == http.MethodGet && qs.Has("acl"):
			s.writeACL(q)
		case r.Method == http.MethodGet && bucketConfigMissing(qs) != nil:
			writeErr(q.w, r, bucketConfigMissing(qs))
		case r.Method == http.MethodGet:
			s.listObjects(q)
		case r.Method == http.MethodPost && qs.Has("delete"):
			s.deleteObjects(q)
		case r.Method == http.MethodPut:
			writeErr(q.w, r, errBucketNotEmpty)
		default:
			writeErr(q.w, r, errAccessDenied)
		}
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if qs.Has("uploadId") {
			s.listParts(q)
			return
		}
		if qs.Has("acl") || qs.Has("tagging") {
			if _, err := q.fs.Stat(vpath(q.bucket, q.key)); err != nil {
				writeErr(q.w, r, errNoSuchKey)
			} else if qs.Has("acl") {
				s.writeACL(q)
			} else {
				writeXML(q.w, struct {
					XMLName xml.Name `xml:"Tagging"`
					NS      string   `xml:"xmlns,attr"`
					TagSet  struct{}
				}{NS: s3NS})
			}
			return
		}
		s.getObject(q)
	case http.MethodPut:
		switch {
		case qs.Has("uploadId") && qs.Has("partNumber"):
			if r.Header.Get("X-Amz-Copy-Source") != "" {
				writeErr(q.w, r, errNotImplemented)
				return
			}
			s.uploadPart(q)
		case qs.Has("tagging") || qs.Has("acl"):
			q.w.WriteHeader(http.StatusOK)
		case r.Header.Get("X-Amz-Copy-Source") != "":
			s.copyObject(q)
		default:
			s.putObject(q)
		}
	case http.MethodPost:
		switch {
		case qs.Has("uploads"):
			s.createUpload(q)
		case qs.Has("uploadId"):
			s.completeUpload(q)
		default:
			writeErr(q.w, r, errNotImplemented)
		}
	case http.MethodDelete:
		if qs.Has("uploadId") {
			s.abortUpload(q)
			return
		}
		s.deleteObject(q)
	default:
		writeErr(q.w, r, errNotImplemented)
	}
}

// validKey rejects keys with "." or ".." segments: they cannot be stored
// as-is on a filesystem and would otherwise resolve outside the bucket's
// directory (e.g. "../other-bucket/file").
func validKey(k string) bool {
	if k == "" {
		return true
	}
	for _, seg := range strings.Split(strings.ReplaceAll(k, "\\", "/"), "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// bucketConfigMissing returns the "not configured" error S3 uses for optional
// bucket configuration (clients such as mc probe these), or nil.
func bucketConfigMissing(qs url.Values) *s3Error {
	for _, c := range []struct{ param, code string }{
		{"policy", "NoSuchBucketPolicy"},
		{"cors", "NoSuchCORSConfiguration"},
		{"lifecycle", "NoSuchLifecycleConfiguration"},
		{"tagging", "NoSuchTagSet"},
		{"encryption", "ServerSideEncryptionConfigurationNotFoundError"},
		{"object-lock", "ObjectLockConfigurationNotFoundError"},
		{"website", "NoSuchWebsiteConfiguration"},
		{"replication", "ReplicationConfigurationNotFoundError"},
	} {
		if qs.Has(c.param) {
			return &s3Error{404, c.code, "The " + c.param + " configuration does not exist"}
		}
	}
	return nil
}

// writeACL reports the owner as having FULL_CONTROL (ACLs are not supported;
// permissions come from the mount and key configuration).
func (s *Server) writeACL(q *request) {
	type grantee struct {
		XMLNSXSI    string `xml:"xmlns:xsi,attr"`
		Type        string `xml:"xsi:type,attr"`
		ID          string
		DisplayName string
	}
	type grant struct {
		Grantee    grantee
		Permission string
	}
	id := strconv.Itoa(int(q.p.User.ID))
	writeXML(q.w, struct {
		XMLName xml.Name `xml:"AccessControlPolicy"`
		NS      string   `xml:"xmlns,attr"`
		Owner   struct{ ID, DisplayName string }
		Grants  []grant `xml:"AccessControlList>Grant"`
	}{NS: s3NS, Owner: struct{ ID, DisplayName string }{id, q.p.User.Username},
		Grants: []grant{{Grantee: grantee{XMLNSXSI: "http://www.w3.org/2001/XMLSchema-instance", Type: "CanonicalUser", ID: id, DisplayName: q.p.User.Username}, Permission: "FULL_CONTROL"}}})
}

// vpath maps bucket/key to a virtual path.
func vpath(bucket, key string) string { return "/" + bucket + "/" + key }

// encodeKey applies encoding-type=url the way S3 does: form encoding
// (space as "+"), keeping "/" readable.
func encodeKey(q *request, k string) string {
	if k != "" && q.r.URL.Query().Get("encoding-type") == "url" {
		return strings.ReplaceAll(url.QueryEscape(k), "%2F", "/")
	}
	return k
}
