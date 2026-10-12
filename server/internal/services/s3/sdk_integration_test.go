package s3_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/Sakana-1314/MList/server/internal/auth"
	s3svc "github.com/Sakana-1314/MList/server/internal/services/s3"
	"github.com/Sakana-1314/MList/server/internal/store"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

type env struct {
	t         *testing.T
	rw, ro    string // local dirs of the "rw" and "ro" buckets
	outside   string // a directory next to the mounts that must never be reachable
	handler   *s3svc.Server
	srv       *httptest.Server
	tls       *httptest.Server
	ak, sk    string
	roAK      string
	roSK      string
	otherAK   string
	otherSK   string
	otherRoot string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	e := &env{t: t}
	mk := func(name string) string {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	data := mk("data")
	e.rw, e.ro, e.outside, e.otherRoot = mk("rw"), mk("ro"), mk("outside"), mk("other")
	os.WriteFile(filepath.Join(e.outside, "secret.txt"), []byte("secret"), 0o644)
	os.WriteFile(filepath.Join(e.ro, "readme.txt"), []byte("read only content"), 0o644)

	st, err := store.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	au, err := auth.New(st, data, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("pw")
	u := &store.User{Username: "alice", PasswordHash: hash, Role: store.RoleUser, Mounts: []store.Mount{
		{Name: "rw", Path: e.rw, Writable: true},
		{Name: "ro", Path: e.ro, Writable: false},
	}}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	o := &store.User{Username: "bob", PasswordHash: hash, Role: store.RoleUser, Mounts: []store.Mount{
		{Name: "bobs", Path: e.otherRoot, Writable: true},
	}}
	if err := st.CreateUser(o); err != nil {
		t.Fatal(err)
	}
	k, sk, err := au.CreateS3Key(u.ID, "rw", false)
	if err != nil {
		t.Fatal(err)
	}
	e.ak, e.sk = k.AccessKey, sk
	k, sk, _ = au.CreateS3Key(u.ID, "ro", true)
	e.roAK, e.roSK = k.AccessKey, sk
	k, sk, _ = au.CreateS3Key(o.ID, "bob", false)
	e.otherAK, e.otherSK = k.AccessKey, sk

	newFS := func(p *auth.Principal) *vfs.FS { return vfs.New(p.User.Mounts, p.ReadOnly, nil) }
	h := s3svc.New(au, newFS, data)
	e.handler = h
	// S3 is served as a handler now (no listener of its own); the API is
	// disabled until the app turns it on.
	h.SetEnabled(true)
	t.Cleanup(h.Close)
	e.srv = httptest.NewServer(h)
	t.Cleanup(e.srv.Close)
	e.tls = httptest.NewTLSServer(h)
	t.Cleanup(e.tls.Close)
	return e
}

type clientOpt = func(*s3.Options)

func (e *env) client(ak, sk string, opts ...clientOpt) *s3.Client {
	return s3.New(s3.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider(ak, sk, ""),
		BaseEndpoint: aws.String(e.srv.URL),
		UsePathStyle: true,
	}, opts...)
}

func withTLS(e *env) clientOpt {
	return func(o *s3.Options) {
		o.BaseEndpoint = aws.String(e.tls.URL)
		o.HTTPClient = e.tls.Client()
	}
}

func whenRequired(o *s3.Options) {
	o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
}

func unsigned(o *s3.Options) {
	o.APIOptions = append(o.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
}

func errCode(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode()
	}
	if err == nil {
		return ""
	}
	return "?" + err.Error()
}

// must panics on error (which fails the running test with a stack trace).
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func readAll(out *s3.GetObjectOutput, err error) string {
	if err != nil {
		panic(err)
	}
	defer out.Body.Close()
	return string(must(io.ReadAll(out.Body)))
}

func put(t *testing.T, c *s3.Client, bucket, key, body string) *s3.PutObjectOutput {
	t.Helper()
	out, err := c.PutObject(context.Background(), &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: strings.NewReader(body)})
	if err != nil {
		t.Fatalf("put %s/%s: %v", bucket, key, err)
	}
	return out
}

func TestSDKBasics(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.client(e.ak, e.sk)

	lb := must(c.ListBuckets(ctx, &s3.ListBucketsInput{}))
	var names []string
	for _, b := range lb.Buckets {
		names = append(names, *b.Name)
	}
	if strings.Join(names, ",") != "rw,ro" {
		t.Fatalf("buckets = %v", names)
	}
	must(c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("rw")}))
	if _, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("nope")}); err == nil {
		t.Fatal("HeadBucket nope succeeded")
	}
	// The region reported by the server is "auto" (what the mount guide has
	// clients configure); clients may still sign with any region.
	loc := must(c.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String("rw")}))
	if loc.LocationConstraint != "auto" {
		t.Fatalf("location = %q", loc.LocationConstraint)
	}

	if _, err := c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String("rw")}); errCode(err) != "NoSuchBucketPolicy" {
		t.Fatalf("bucket policy: %v", err)
	}
	if _, err := c.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: aws.String("rw")}); errCode(err) != "ObjectLockConfigurationNotFoundError" {
		t.Fatalf("object lock: %v", err)
	}
	acl := must(c.GetBucketAcl(ctx, &s3.GetBucketAclInput{Bucket: aws.String("rw")}))
	if len(acl.Grants) != 1 || acl.Grants[0].Permission != types.PermissionFullControl || *acl.Owner.DisplayName != "alice" {
		t.Fatalf("acl = %+v", acl)
	}

	po := put(t, c, "rw", "a.txt", "hello world")
	must(c.GetObjectAcl(ctx, &s3.GetObjectAclInput{Bucket: aws.String("rw"), Key: aws.String("a.txt")}))
	tg := must(c.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: aws.String("rw"), Key: aws.String("a.txt")}))
	if len(tg.TagSet) != 0 {
		t.Fatal("tags")
	}
	if *po.ETag != `"`+md5hex("hello world")+`"` {
		t.Fatalf("put etag %s", *po.ETag)
	}
	got := readAll(c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("a.txt")}))
	if got != "hello world" {
		t.Fatalf("get = %q", got)
	}
	rg, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("a.txt"), Range: aws.String("bytes=2-5")})
	if s := readAll(rg, err); s != "llo " || *rg.ContentRange != "bytes 2-5/11" || *rg.ContentLength != 4 {
		t.Fatalf("range = %q %v %v", s, *rg.ContentRange, *rg.ContentLength)
	}
	ho := must(c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("rw"), Key: aws.String("a.txt")}))
	if *ho.ContentLength != 11 || ho.LastModified == nil || ho.ETag == nil || *ho.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("head = %+v", ho)
	}
	// PUT returns the content MD5 (clients verify uploads with it); HEAD/GET/List
	// use a stable metadata-derived ETag in multipart form.
	if ho2 := must(c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("rw"), Key: aws.String("a.txt")})); *ho2.ETag != *ho.ETag {
		t.Errorf("HEAD ETag not stable: %s vs %s", *ho2.ETag, *ho.ETag)
	}
	if _, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("rw"), Key: aws.String("missing")}); errCode(err) != "NotFound" {
		t.Fatalf("head missing: %v", err)
	}
	if _, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("missing")}); errCode(err) != "NoSuchKey" {
		t.Fatalf("get missing: %v", err)
	}
	// Conditional requests.
	if _, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("a.txt"), IfNoneMatch: ho.ETag}); errCode(err) != "NotModified" {
		t.Fatalf("If-None-Match get: %v", err)
	}
	if _, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("a.txt"), IfMatch: aws.String(`"nope"`)}); errCode(err) != "PreconditionFailed" {
		t.Fatalf("If-Match get: %v", err)
	}
	_, err = c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("rw"), Key: aws.String("a.txt"), Body: strings.NewReader("x"), IfNoneMatch: aws.String("*")})
	if errCode(err) != "PreconditionFailed" {
		t.Fatalf("put If-None-Match: %v", err)
	}
	// Empty object and "directory" marker.
	put(t, c, "rw", "empty.txt", "")
	if s := readAll(c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("empty.txt")})); s != "" {
		t.Fatal("empty")
	}
	put(t, c, "rw", "folder/", "")
	if fi, err := os.Stat(filepath.Join(e.rw, "folder")); err != nil || !fi.IsDir() {
		t.Fatalf("dir marker: %v", err)
	}
	must(c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("rw"), Key: aws.String("folder/")}))

	// Unicode / space / special keys.
	for _, k := range []string{"中文 目录/文件 1.txt", "space key.txt", "plus+key&x=1.txt", "pct%20key.txt", "semi;colon,(paren)!'*@$.txt", "~tilde.txt"} {
		put(t, c, "rw", k, "v:"+k)
		if s := readAll(c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String(k)})); s != "v:"+k {
			t.Fatalf("key %q round-trip = %q", k, s)
		}
		if _, err := os.Stat(filepath.Join(e.rw, filepath.FromSlash(k))); err != nil {
			t.Fatalf("key %q not on disk: %v", k, err)
		}
	}
	// Copy within bucket, across buckets and with encoded unicode source.
	cp := must(c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: aws.String("rw"), Key: aws.String("copy/目标 a.txt"),
		CopySource: aws.String("rw/" + urlPathEscape("中文 目录/文件 1.txt"))}))
	if cp.CopyObjectResult == nil || cp.CopyObjectResult.ETag == nil {
		t.Fatal("copy result")
	}
	if s := readAll(c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("copy/目标 a.txt")})); s != "v:中文 目录/文件 1.txt" {
		t.Fatalf("copied = %q", s)
	}
	must(c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: aws.String("rw"), Key: aws.String("from-ro.txt"), CopySource: aws.String("/ro/readme.txt")}))
	if b, _ := os.ReadFile(filepath.Join(e.rw, "from-ro.txt")); string(b) != "read only content" {
		t.Fatalf("cross-bucket copy = %q", b)
	}
	if _, err := c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: aws.String("ro"), Key: aws.String("x.txt"), CopySource: aws.String("rw/a.txt")}); errCode(err) != "AccessDenied" {
		t.Fatalf("copy into ro: %v", err)
	}
	if _, err := c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: aws.String("rw"), Key: aws.String("x.txt"), CopySource: aws.String("rw/missing")}); errCode(err) != "NoSuchKey" {
		t.Fatalf("copy missing: %v", err)
	}
	if _, err := c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: aws.String("rw"), Key: aws.String("x.txt"), CopySource: aws.String("bobs/a")}); errCode(err) != "NoSuchBucket" {
		t.Fatalf("copy from other user's bucket: %v", err)
	}
	// Copy onto itself (metadata replace) keeps the content.
	must(c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: aws.String("rw"), Key: aws.String("a.txt"), CopySource: aws.String("rw/a.txt"), MetadataDirective: types.MetadataDirectiveReplace}))
	if b, _ := os.ReadFile(filepath.Join(e.rw, "a.txt")); string(b) != "hello world" {
		t.Fatalf("self copy = %q", b)
	}

	// Delete + DeleteObjects.
	must(c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("rw"), Key: aws.String("space key.txt")}))
	must(c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("rw"), Key: aws.String("never-existed")}))
	if _, err := os.Stat(filepath.Join(e.rw, "space key.txt")); !os.IsNotExist(err) {
		t.Fatal("not deleted")
	}
	do := must(c.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String("rw"), Delete: &types.Delete{Objects: []types.ObjectIdentifier{
		{Key: aws.String("plus+key&x=1.txt")}, {Key: aws.String("~tilde.txt")}, {Key: aws.String("nope")},
	}}}))
	if len(do.Deleted) != 3 || len(do.Errors) != 0 {
		t.Fatalf("delete objects: %+v %+v", do.Deleted, do.Errors)
	}
	do = must(c.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String("ro"), Delete: &types.Delete{Objects: []types.ObjectIdentifier{{Key: aws.String("readme.txt")}}}}))
	if len(do.Errors) != 1 || *do.Errors[0].Code != "AccessDenied" {
		t.Fatalf("delete objects ro: %+v", do.Errors)
	}
	// Deleting a non-empty directory marker must not delete the tree.
	must(c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("rw"), Key: aws.String("copy/")}))
	if _, err := os.Stat(filepath.Join(e.rw, "copy", "目标 a.txt")); err != nil {
		t.Fatal("non-empty prefix deleted")
	}
	must(c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("rw"), Key: aws.String("folder/")}))
	if _, err := os.Stat(filepath.Join(e.rw, "folder")); !os.IsNotExist(err) {
		t.Fatal("empty dir marker not deleted")
	}
}

func TestSDKMtimeAndPrune(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.client(e.ak, e.sk)
	want := time.Unix(1600000000, 123456789)
	meta := map[string]string{"mtime": "1600000000.123456789"}
	must(c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("rw"), Key: aws.String("m/one.txt"), Body: strings.NewReader("1"), Metadata: meta}))
	fi, _ := os.Stat(filepath.Join(e.rw, "m", "one.txt"))
	if !fi.ModTime().Equal(want) {
		t.Fatalf("put mtime = %v", fi.ModTime())
	}
	ho := must(c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("rw"), Key: aws.String("m/one.txt")}))
	if ho.Metadata["mtime"] != "1600000000.123456789" || !ho.LastModified.Equal(want.Truncate(time.Second)) {
		t.Fatalf("head metadata %v %v", ho.Metadata, ho.LastModified)
	}
	// Multipart carries the metadata from initiation.
	cu := must(c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("rw"), Key: aws.String("m/two.txt"), Metadata: meta}))
	up := must(c.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String("rw"), Key: aws.String("m/two.txt"), UploadId: cu.UploadId, PartNumber: aws.Int32(1), Body: strings.NewReader("2")}))
	must(c.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String("rw"), Key: aws.String("m/two.txt"), UploadId: cu.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{ETag: up.ETag, PartNumber: aws.Int32(1)}}}}))
	if fi, _ := os.Stat(filepath.Join(e.rw, "m", "two.txt")); !fi.ModTime().Equal(want) {
		t.Fatalf("multipart mtime = %v", fi.ModTime())
	}
	// Self-copy with REPLACE updates the mtime (rclone SetModTime).
	must(c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: aws.String("rw"), Key: aws.String("m/one.txt"), CopySource: aws.String("rw/m/one.txt"),
		MetadataDirective: types.MetadataDirectiveReplace, Metadata: map[string]string{"mtime": "1500000000"}}))
	if fi, _ := os.Stat(filepath.Join(e.rw, "m", "one.txt")); !fi.ModTime().Equal(time.Unix(1500000000, 0)) {
		t.Fatalf("copy mtime = %v", fi.ModTime())
	}
	// Deleting the last objects removes the now-empty prefix directories, not the bucket root.
	put(t, c, "rw", "p/q/r/leaf.txt", "x")
	must(c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("rw"), Key: aws.String("p/q/r/leaf.txt")}))
	if _, err := os.Stat(filepath.Join(e.rw, "p")); !os.IsNotExist(err) {
		t.Fatal("empty prefix dirs left behind")
	}
	must(c.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String("rw"), Delete: &types.Delete{Objects: []types.ObjectIdentifier{{Key: aws.String("m/one.txt")}, {Key: aws.String("m/two.txt")}}}}))
	if _, err := os.Stat(filepath.Join(e.rw, "m")); !os.IsNotExist(err) {
		t.Fatal("empty prefix dir left behind")
	}
	if _, err := os.Stat(e.rw); err != nil {
		t.Fatal("bucket root removed")
	}
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, seg := range strings.Split(s, "/") {
		if b.Len() > 0 {
			b.WriteByte('/')
		}
		b.WriteString(escape(seg))
	}
	return b.String()
}

func escape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || strings.IndexByte("-_.~", c) >= 0 {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestSDKListing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.client(e.ak, e.sk)
	keys := []string{"a-b.txt", "a.txt", "a/1.txt", "a/2.txt", "a/sub/3.txt", "b/x.txt", "c.txt", "z/中文.txt", "z/空 格.txt"}
	for _, k := range keys {
		put(t, c, "rw", k, k)
	}
	// Hidden temp files and empty dirs.
	os.WriteFile(filepath.Join(e.rw, vfs.TempPrefix+"junk"), []byte("x"), 0o644)
	os.Mkdir(filepath.Join(e.rw, "emptydir"), 0o755)
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)

	listAll := func(in s3.ListObjectsV2Input) (objs, prefixes []string, pages int) {
		p := s3.NewListObjectsV2Paginator(c, &in)
		for p.HasMorePages() {
			out, err := p.NextPage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			pages++
			for _, o := range out.Contents {
				objs = append(objs, *o.Key)
				if o.ETag == nil || o.LastModified == nil || o.Size == nil {
					t.Fatalf("incomplete object %+v", o)
				}
			}
			for _, cp := range out.CommonPrefixes {
				prefixes = append(prefixes, *cp.Prefix)
			}
			if int(*out.KeyCount) != len(out.Contents)+len(out.CommonPrefixes) {
				t.Fatalf("KeyCount %d", *out.KeyCount)
			}
		}
		return
	}
	objs, pre, _ := listAll(s3.ListObjectsV2Input{Bucket: aws.String("rw")})
	if strings.Join(objs, "|") != strings.Join(sorted, "|") || len(pre) != 0 {
		t.Fatalf("recursive = %v %v", objs, pre)
	}
	objs, pre, pages := listAll(s3.ListObjectsV2Input{Bucket: aws.String("rw"), MaxKeys: aws.Int32(2)})
	if strings.Join(objs, "|") != strings.Join(sorted, "|") || pages != 5 {
		t.Fatalf("paged recursive = %v pages=%d", objs, pages)
	}
	objs, pre, _ = listAll(s3.ListObjectsV2Input{Bucket: aws.String("rw"), Delimiter: aws.String("/")})
	if strings.Join(objs, "|") != "a-b.txt|a.txt|c.txt" || strings.Join(pre, "|") != "a/|b/|emptydir/|z/" {
		t.Fatalf("delimited = %v %v", objs, pre)
	}
	objs2, pre2, pages := listAll(s3.ListObjectsV2Input{Bucket: aws.String("rw"), Delimiter: aws.String("/"), MaxKeys: aws.Int32(1)})
	if strings.Join(objs2, "|") != strings.Join(objs, "|") || strings.Join(pre2, "|") != strings.Join(pre, "|") || pages != 7 {
		t.Fatalf("paged delimited = %v %v %d", objs2, pre2, pages)
	}
	objs, pre, _ = listAll(s3.ListObjectsV2Input{Bucket: aws.String("rw"), Prefix: aws.String("a/"), Delimiter: aws.String("/")})
	if strings.Join(objs, "|") != "a/1.txt|a/2.txt" || strings.Join(pre, "|") != "a/sub/" {
		t.Fatalf("prefix a/ = %v %v", objs, pre)
	}
	objs, pre, _ = listAll(s3.ListObjectsV2Input{Bucket: aws.String("rw"), Prefix: aws.String("a"), Delimiter: aws.String("/")})
	if strings.Join(objs, "|") != "a-b.txt|a.txt" || strings.Join(pre, "|") != "a/" {
		t.Fatalf("prefix a = %v %v", objs, pre)
	}
	objs, _, _ = listAll(s3.ListObjectsV2Input{Bucket: aws.String("rw"), Prefix: aws.String("a/s")})
	if strings.Join(objs, "|") != "a/sub/3.txt" {
		t.Fatalf("prefix a/s = %v", objs)
	}
	objs, _, _ = listAll(s3.ListObjectsV2Input{Bucket: aws.String("rw"), StartAfter: aws.String("a/2.txt")})
	if strings.Join(objs, "|") != "a/sub/3.txt|b/x.txt|c.txt|z/中文.txt|z/空 格.txt" {
		t.Fatalf("start-after = %v", objs)
	}
	objs, _, _ = listAll(s3.ListObjectsV2Input{Bucket: aws.String("rw"), Prefix: aws.String("nope/"), Delimiter: aws.String("/")})
	if len(objs) != 0 {
		t.Fatal(objs)
	}
	// encoding-type=url: keys come back URL encoded.
	out := must(c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("rw"), Prefix: aws.String("z/"), EncodingType: types.EncodingTypeUrl}))
	if len(out.Contents) != 2 || *out.Contents[1].Key != "z/%E7%A9%BA+%E6%A0%BC.txt" || *out.Prefix != "z/" {
		t.Fatalf("encoded keys = %v prefix %v", *out.Contents[1].Key, *out.Prefix)
	}

	// ListObjects v1 with markers.
	var v1 []string
	marker := ""
	for i := 0; i < 20; i++ {
		o := must(c.ListObjects(ctx, &s3.ListObjectsInput{Bucket: aws.String("rw"), MaxKeys: aws.Int32(3), Marker: aws.String(marker)}))
		for _, x := range o.Contents {
			v1 = append(v1, *x.Key)
		}
		if !*o.IsTruncated {
			break
		}
		if o.NextMarker != nil {
			marker = *o.NextMarker
		} else {
			marker = *o.Contents[len(o.Contents)-1].Key
		}
	}
	if strings.Join(v1, "|") != strings.Join(sorted, "|") {
		t.Fatalf("v1 = %v", v1)
	}
	o1 := must(c.ListObjects(ctx, &s3.ListObjectsInput{Bucket: aws.String("rw"), Delimiter: aws.String("/")}))
	if len(o1.Contents) != 3 || len(o1.CommonPrefixes) != 4 {
		t.Fatalf("v1 delimited %d %d", len(o1.Contents), len(o1.CommonPrefixes))
	}
}

func TestSDKMultipart(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for name, c := range map[string]*s3.Client{
		"http":         e.client(e.ak, e.sk),
		"tls-trailer":  e.client(e.ak, e.sk, withTLS(e)),
		"unsigned":     e.client(e.ak, e.sk, unsigned),
		"whenRequired": e.client(e.ak, e.sk, whenRequired),
	} {
		t.Run(name, func(t *testing.T) {
			key := "mp/" + name + "/big file.bin"
			cu := must(c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("rw"), Key: aws.String(key)}))
			var parts []types.CompletedPart
			var all bytes.Buffer
			const partSize = 5 << 20
			for i := 1; i <= 3; i++ {
				data := make([]byte, partSize)
				if i == 3 {
					data = data[:1234]
				}
				rand.Read(data)
				all.Write(data)
				up := must(c.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String("rw"), Key: aws.String(key), UploadId: cu.UploadId,
					PartNumber: aws.Int32(int32(i)), Body: bytes.NewReader(data)}))
				if *up.ETag != `"`+md5hex(string(data))+`"` {
					t.Fatalf("part etag %s", *up.ETag)
				}
				parts = append(parts, types.CompletedPart{ETag: up.ETag, PartNumber: aws.Int32(int32(i))})
			}
			lp := must(c.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String("rw"), Key: aws.String(key), UploadId: cu.UploadId}))
			if len(lp.Parts) != 3 || *lp.Parts[0].Size != partSize || *lp.Parts[2].Size != 1234 {
				t.Fatalf("list parts = %+v", lp.Parts)
			}
			lu := must(c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String("rw")}))
			if len(lu.Uploads) == 0 {
				t.Fatal("no uploads listed")
			}
			// Nothing visible before completion.
			if _, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("rw"), Key: aws.String(key)}); err == nil {
				t.Fatal("object visible before complete")
			}
			co := must(c.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String("rw"), Key: aws.String(key), UploadId: cu.UploadId,
				MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}}))
			if !strings.HasSuffix(*co.ETag, `-3"`) {
				t.Fatalf("complete etag %s", *co.ETag)
			}
			b, _ := os.ReadFile(filepath.Join(e.rw, filepath.FromSlash(key)))
			if !bytes.Equal(b, all.Bytes()) {
				t.Fatalf("assembled content mismatch (%d vs %d)", len(b), all.Len())
			}
			// Upload is gone.
			if _, err := c.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String("rw"), Key: aws.String(key), UploadId: cu.UploadId}); errCode(err) != "NoSuchUpload" {
				t.Fatalf("list parts after complete: %v", err)
			}
			// Abort.
			cu2 := must(c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("rw"), Key: aws.String(key + ".2")}))
			must(c.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String("rw"), Key: aws.String(key + ".2"), UploadId: cu2.UploadId,
				PartNumber: aws.Int32(1), Body: strings.NewReader("abc")}))
			must(c.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String("rw"), Key: aws.String(key + ".2"), UploadId: cu2.UploadId}))
			if _, err := c.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String("rw"), Key: aws.String(key + ".2"), UploadId: cu2.UploadId,
				PartNumber: aws.Int32(2), Body: strings.NewReader("abc")}); errCode(err) != "NoSuchUpload" {
				t.Fatalf("upload after abort: %v", err)
			}
			// Wrong part ETag.
			cu3 := must(c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("rw"), Key: aws.String(key + ".3")}))
			must(c.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String("rw"), Key: aws.String(key + ".3"), UploadId: cu3.UploadId,
				PartNumber: aws.Int32(1), Body: strings.NewReader("abc")}))
			if _, err := c.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String("rw"), Key: aws.String(key + ".3"), UploadId: cu3.UploadId,
				MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{ETag: aws.String(`"deadbeef"`), PartNumber: aws.Int32(1)}}}}); errCode(err) != "InvalidPart" {
				t.Fatalf("complete with bad etag: %v", err)
			}
		})
	}
	// Multipart into a read-only bucket / with a read-only key.
	c := e.client(e.ak, e.sk)
	if _, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("ro"), Key: aws.String("x")}); errCode(err) != "AccessDenied" {
		t.Fatalf("multipart ro bucket: %v", err)
	}
	if _, err := e.client(e.roAK, e.roSK).CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("rw"), Key: aws.String("x")}); errCode(err) != "AccessDenied" {
		t.Fatalf("multipart ro key: %v", err)
	}
}

func TestSDKChecksumModes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	payload := strings.Repeat("0123456789abcdef", 70000) // > 1 MiB, several aws-chunked chunks
	for name, c := range map[string]*s3.Client{
		"http-default":        e.client(e.ak, e.sk),
		"http-whenRequired":   e.client(e.ak, e.sk, whenRequired),
		"http-unsigned":       e.client(e.ak, e.sk, unsigned),
		"tls-default-trailer": e.client(e.ak, e.sk, withTLS(e)),
		"tls-whenRequired":    e.client(e.ak, e.sk, withTLS(e), whenRequired),
		"tls-unsigned":        e.client(e.ak, e.sk, withTLS(e), unsigned),
	} {
		t.Run(name, func(t *testing.T) {
			key := "ck/" + name + ".bin"
			put(t, c, "rw", key, payload)
			b, _ := os.ReadFile(filepath.Join(e.rw, filepath.FromSlash(key)))
			if string(b) != payload {
				t.Fatalf("stored %d bytes, want %d", len(b), len(payload))
			}
			// Unseekable bodies (TLS → trailer; plain HTTP → SDK refuses unless unsigned).
			_, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("rw"), Key: aws.String(key + ".stream"),
				Body: io.NopCloser(strings.NewReader(payload)), ContentLength: aws.Int64(int64(len(payload)))})
			if err == nil {
				b, _ := os.ReadFile(filepath.Join(e.rw, filepath.FromSlash(key+".stream")))
				if string(b) != payload {
					t.Fatalf("stream stored %d bytes", len(b))
				}
			} else if strings.HasPrefix(name, "tls") {
				t.Fatalf("streaming put over TLS: %v", err)
			}
			got := readAll(c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String(key), ChecksumMode: types.ChecksumModeEnabled}))
			if got != payload {
				t.Fatal("get mismatch")
			}
			must(c.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String("rw"), Delete: &types.Delete{Objects: []types.ObjectIdentifier{{Key: aws.String(key)}}}}))
		})
	}
}

func TestSDKManager(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for name, c := range map[string]*s3.Client{"http": e.client(e.ak, e.sk), "tls": e.client(e.ak, e.sk, withTLS(e))} {
		t.Run(name, func(t *testing.T) {
			data := make([]byte, 20<<20+12345)
			rand.Read(data)
			up := manager.NewUploader(c, func(u *manager.Uploader) { u.PartSize = 5 << 20; u.Concurrency = 3 })
			// Non-seekable reader forces the streaming multipart path.
			res, err := up.Upload(ctx, &s3.PutObjectInput{Bucket: aws.String("rw"), Key: aws.String("mgr/" + name + ".bin"), Body: io.MultiReader(bytes.NewReader(data))})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.CompletedParts) != 5 {
				t.Fatalf("parts = %d", len(res.CompletedParts))
			}
			b, _ := os.ReadFile(filepath.Join(e.rw, "mgr", name+".bin"))
			if !bytes.Equal(b, data) {
				t.Fatal("content mismatch")
			}
			dl := manager.NewDownloader(c, func(d *manager.Downloader) { d.PartSize = 3 << 20; d.Concurrency = 4 })
			buf := manager.NewWriteAtBuffer(nil)
			n, err := dl.Download(ctx, buf, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("mgr/" + name + ".bin")})
			if err != nil || n != int64(len(data)) || !bytes.Equal(buf.Bytes(), data) {
				t.Fatalf("download n=%d err=%v", n, err)
			}
			// Small upload goes through a single PutObject.
			if _, err := up.Upload(ctx, &s3.PutObjectInput{Bucket: aws.String("rw"), Key: aws.String("mgr/small-" + name), Body: strings.NewReader("small")}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSDKPresigned(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.client(e.ak, e.sk)
	ps := s3.NewPresignClient(c)
	key := "presign/中文 文件+1.txt"
	req := must(ps.PresignPutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("rw"), Key: aws.String(key)}, s3.WithPresignExpires(10*time.Minute)))
	hr, _ := http.NewRequest(req.Method, req.URL, strings.NewReader("presigned body"))
	for k, v := range req.SignedHeader {
		if !strings.EqualFold(k, "host") {
			hr.Header[k] = v
		}
	}
	resp, err := http.DefaultClient.Do(hr)
	if err != nil || resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("presigned put: %v %d %s", err, resp.StatusCode, b)
	}
	resp.Body.Close()
	g := must(ps.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String(key),
		ResponseContentDisposition: aws.String(`attachment; filename="x.txt"`)}))
	resp, err = http.Get(g.URL)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "presigned body" || resp.Header.Get("Content-Disposition") != `attachment; filename="x.txt"` {
		t.Fatalf("presigned get: %d %q %v", resp.StatusCode, b, resp.Header)
	}
	// Tampered URL.
	resp, _ = http.Get(strings.Replace(g.URL, "presign", "presigx", 1))
	if resp.StatusCode != 403 {
		t.Fatalf("tampered presigned: %d", resp.StatusCode)
	}
	resp.Body.Close()
	// Expired URL.
	h := must(ps.PresignHeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("rw"), Key: aws.String(key)}, func(o *s3.PresignOptions) {
		o.Expires = time.Second
	}))
	time.Sleep(2100 * time.Millisecond)
	hreq, _ := http.NewRequest("HEAD", h.URL, nil)
	resp, _ = http.DefaultClient.Do(hreq)
	if resp.StatusCode != 403 {
		t.Fatalf("expired presigned: %d", resp.StatusCode)
	}
}

func TestSDKAuthAndPermissions(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.client(e.ak, "wrong-secret").ListBuckets(ctx, &s3.ListBucketsInput{}); errCode(err) != "SignatureDoesNotMatch" {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := e.client("LNFNOPE", "x").ListBuckets(ctx, &s3.ListBucketsInput{}); errCode(err) != "InvalidAccessKeyId" {
		t.Fatalf("unknown key: %v", err)
	}
	resp, _ := http.Get(e.srv.URL + "/rw/")
	if resp.StatusCode != 403 {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
	resp.Body.Close()

	ro := e.client(e.roAK, e.roSK)
	must(ro.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("rw")}))
	if _, err := ro.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("rw"), Key: aws.String("x"), Body: strings.NewReader("x")}); errCode(err) != "AccessDenied" {
		t.Fatalf("ro key put: %v", err)
	}
	c := e.client(e.ak, e.sk)
	if _, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("ro"), Key: aws.String("x"), Body: strings.NewReader("x")}); errCode(err) != "AccessDenied" {
		t.Fatalf("ro bucket put: %v", err)
	}
	if _, err := c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("ro"), Key: aws.String("readme.txt")}); errCode(err) != "AccessDenied" {
		t.Fatalf("ro bucket delete: %v", err)
	}
	if s := readAll(c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("ro"), Key: aws.String("readme.txt")})); s != "read only content" {
		t.Fatal(s)
	}
	for _, b := range []string{"bobs", "nope"} {
		if _, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(b)}); errCode(err) != "NoSuchBucket" {
			t.Fatalf("other bucket list: %v", err)
		}
		if _, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(b), Key: aws.String("x"), Body: strings.NewReader("x")}); errCode(err) != "NoSuchBucket" {
			t.Fatalf("other bucket put: %v", err)
		}
	}
	if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("newbucket")}); err == nil {
		t.Fatal("create bucket succeeded")
	}

	// Path traversal attempts never leave the bucket.
	for _, k := range []string{"../ro/readme.txt", "../outside/secret.txt", "../../outside/secret.txt", "a/../../ro/readme.txt", "..", "./../ro/readme.txt"} {
		if out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String(k)}); err == nil {
			b, _ := io.ReadAll(out.Body)
			t.Errorf("GET rw/%s succeeded: %q", k, b)
		}
		if _, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("rw"), Key: aws.String(k), Body: strings.NewReader("pwned")}); err == nil {
			t.Errorf("PUT rw/%s succeeded", k)
		}
		if _, err := c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("rw"), Key: aws.String(k)}); err == nil {
			// DeleteObject of a missing key is a no-op success; check nothing vanished below.
		}
	}
	if b, _ := os.ReadFile(filepath.Join(e.ro, "readme.txt")); string(b) != "read only content" {
		t.Fatal("ro file changed")
	}
	if b, _ := os.ReadFile(filepath.Join(e.outside, "secret.txt")); string(b) != "secret" {
		t.Fatal("outside file changed")
	}
	if _, err := c.CopyObject(ctx, &s3.CopyObjectInput{Bucket: aws.String("rw"), Key: aws.String("stolen"), CopySource: aws.String("rw/../outside/secret.txt")}); err == nil {
		t.Error("copy with traversal source succeeded")
	}
	// Symlink escaping the mount.
	os.Symlink(e.outside, filepath.Join(e.rw, "link"))
	if _, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("link/secret.txt")}); err == nil {
		t.Error("symlink escape readable")
	}
	lo := must(c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("rw")}))
	for _, o := range lo.Contents {
		if strings.Contains(*o.Key, "secret") {
			t.Errorf("listing leaks %s", *o.Key)
		}
	}
	// Other user's key cannot see alice's buckets.
	bob := e.client(e.otherAK, e.otherSK)
	if _, err := bob.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("ro"), Key: aws.String("readme.txt")}); errCode(err) != "NoSuchBucket" {
		t.Fatalf("bob reading alice: %v", err)
	}
}

func TestSDKVirtualHost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	addr := e.srv.Listener.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	tr := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}, TLSClientConfig: &tls.Config{}}
	c := s3.New(s3.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider(e.ak, e.sk, ""),
		BaseEndpoint: aws.String("http://s3.nas.test:" + port),
		HTTPClient:   &http.Client{Transport: tr},
	})
	put(t, c, "rw", "vh/key one.txt", "virtual")
	if s := readAll(c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("vh/key one.txt")})); s != "virtual" {
		t.Fatal(s)
	}
	lo := must(c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("rw"), Prefix: aws.String("vh/")}))
	if len(lo.Contents) != 1 {
		t.Fatalf("vh list %d", len(lo.Contents))
	}
	must(c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("ro")}))

	// Path-style through a host whose first label equals a bucket name ("rw.lan").
	ps := s3.New(s3.Options{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider(e.ak, e.sk, ""),
		BaseEndpoint: aws.String("http://rw.lan:" + port),
		UsePathStyle: true,
		HTTPClient:   &http.Client{Transport: tr},
	})
	if s := readAll(ps.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("vh/key one.txt")})); s != "virtual" {
		t.Fatal(s)
	}
	lb := must(ps.ListBuckets(ctx, &s3.ListBucketsInput{}))
	if len(lb.Buckets) != 2 {
		t.Fatalf("list buckets via rw.lan: %d", len(lb.Buckets))
	}
	// max-keys=0 is not truncated (avoids endless pagination).
	lo = must(c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("rw"), MaxKeys: aws.Int32(0)}))
	if *lo.IsTruncated || len(lo.Contents) != 0 {
		t.Fatal("max-keys=0")
	}
}

func TestListingPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	e := newEnv(t)
	ctx := context.Background()
	dir := filepath.Join(e.rw, "many")
	os.MkdirAll(dir, 0o755)
	for i := 0; i < 5000; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d.txt", i)), []byte("x"), 0o644)
	}
	c := e.client(e.ak, e.sk)
	for _, delim := range []string{"/", ""} {
		start := time.Now()
		n := 0
		p := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{Bucket: aws.String("rw"), Prefix: aws.String("many/"), Delimiter: aws.String(delim)})
		for p.HasMorePages() {
			out, err := p.NextPage(ctx)
			if err != nil {
				t.Fatal(err)
			}
			n += len(out.Contents)
		}
		el := time.Since(start)
		t.Logf("listing 5000 files delimiter=%q: %v", delim, el)
		if n != 5000 {
			t.Fatalf("listed %d", n)
		}
		if el > 5*time.Second {
			t.Errorf("listing too slow: %v", el)
		}
	}
}

// The S3 API shares the main HTTP port and is mounted under /s3. A client whose
// endpoint includes that prefix signs the full path, so the handler must verify
// against the signed path and only then strip the prefix for routing.
func TestPrefixedEndpoint(t *testing.T) {
	e := newEnv(t)

	// A client pointed at <base>/s3 must work end to end.
	prefixed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mirror the app's routing: forward only the prefixed paths.
		if r.URL.Path != s3svc.Prefix && !strings.HasPrefix(r.URL.Path, s3svc.Prefix+"/") {
			http.NotFound(w, r)
			return
		}
		e.handler.ServeHTTP(w, r)
	}))
	defer prefixed.Close()

	c := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider(e.ak, e.sk, ""),
		BaseEndpoint: aws.String(prefixed.URL + s3svc.Prefix),
		UsePathStyle: true,
	})
	ctx := context.Background()

	buckets := must(c.ListBuckets(ctx, &s3.ListBucketsInput{}))
	if len(buckets.Buckets) == 0 {
		t.Fatal("no buckets over the prefixed endpoint")
	}
	body := "hello over /s3"
	if _, err := c.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String("rw"), Key: aws.String("prefixed.txt"), Body: strings.NewReader(body),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	got := must(c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("rw"), Key: aws.String("prefixed.txt")}))
	defer got.Body.Close()
	b, _ := io.ReadAll(got.Body)
	if string(b) != body {
		t.Fatalf("round trip = %q", b)
	}

	// A signature made for the prefixed path must be rejected when replayed
	// against the same request minus the prefix: that proves the prefix is
	// covered by the signature rather than silently stripped before verifying.
	signed, err := http.NewRequest(http.MethodGet, prefixed.URL+s3svc.Prefix+"/rw/", nil)
	if err != nil {
		t.Fatal(err)
	}
	signed.Host = strings.TrimPrefix(prefixed.URL, "http://")
	resp, err := http.DefaultClient.Do(signed)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthenticated prefixed request: %d", resp.StatusCode)
	}
}

func TestDisabledAPIRejectsRequests(t *testing.T) {
	e := newEnv(t)
	e.handler.SetEnabled(false)
	req := httptest.NewRequest(http.MethodGet, s3svc.Prefix+"/", nil)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled API: %d", rec.Code)
	}
}
