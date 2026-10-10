package s3

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Legacy AWS Signature Version 2 (header "AWS key:sig" and presigned
// ?AWSAccessKeyId=&Expires=&Signature=), still produced by e.g. `aws s3 presign`
// (AWS CLI v1), s3cmd --signature-v2 and older SDKs.

// v2SubResources are the query parameters included in the string to sign.
var v2SubResources = map[string]bool{
	"acl": true, "cors": true, "delete": true, "encryption": true, "lifecycle": true, "location": true,
	"logging": true, "notification": true, "object-lock": true, "partNumber": true, "policy": true,
	"requestPayment": true, "restore": true, "tagging": true, "torrent": true, "uploadId": true,
	"uploads": true, "versionId": true, "versioning": true, "versions": true, "website": true,
	"response-cache-control": true, "response-content-disposition": true, "response-content-encoding": true,
	"response-content-language": true, "response-content-type": true, "response-expires": true,
}

func parseSigV2Header(r *http.Request, h string) (*sigParams, error) {
	ak, sig, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(h, "AWS ")), ":")
	if !ok || ak == "" || sig == "" {
		return nil, fmt.Errorf("malformed authorization header")
	}
	ds := r.Header.Get("X-Amz-Date")
	if ds == "" {
		ds = r.Header.Get("Date")
	}
	t, err := http.ParseTime(ds)
	if err != nil {
		if t, err = time.Parse(amzDateFormat, ds); err != nil {
			return nil, fmt.Errorf("invalid date")
		}
	}
	if d := time.Since(t); d > maxClockSkew || d < -maxClockSkew {
		return nil, errSkew
	}
	return &sigParams{v2: true, accessKey: ak, signature: sig, amzDate: t, payloadHash: unsignedPayload}, nil
}

func parsePresignedV2(q url.Values) (*sigParams, error) {
	exp, err := strconv.ParseInt(q.Get("Expires"), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid expires")
	}
	if time.Now().Unix() > exp {
		return nil, errExpired
	}
	return &sigParams{v2: true, presigned: true, accessKey: q.Get("AWSAccessKeyId"), signature: q.Get("Signature"),
		v2Expires: q.Get("Expires"), payloadHash: unsignedPayload}, nil
}

// verifyV2 checks a SigV2 signature; bucketPrefix is "/<bucket>" for
// virtual-hosted requests, whose canonical resource includes the bucket.
func (p *sigParams) verifyV2(r *http.Request, secret, bucketPrefix string) bool {
	var b strings.Builder
	b.WriteString(r.Method + "\n")
	b.WriteString(r.Header.Get("Content-Md5") + "\n")
	b.WriteString(r.Header.Get("Content-Type") + "\n")
	switch {
	case p.presigned:
		b.WriteString(p.v2Expires)
	case r.Header.Get("X-Amz-Date") == "":
		b.WriteString(r.Header.Get("Date"))
	}
	b.WriteByte('\n')
	var amz []string
	for k := range r.Header {
		if lk := strings.ToLower(k); strings.HasPrefix(lk, "x-amz-") {
			amz = append(amz, lk)
		}
	}
	sort.Strings(amz)
	for _, k := range amz {
		vals := r.Header.Values(k)
		for i := range vals {
			vals[i] = strings.TrimSpace(vals[i])
		}
		b.WriteString(k + ":" + strings.Join(vals, ",") + "\n")
	}
	b.WriteString(bucketPrefix + r.URL.EscapedPath())
	q := r.URL.Query()
	var subs []string
	for k := range q {
		if v2SubResources[k] {
			subs = append(subs, k)
		}
	}
	sort.Strings(subs)
	for i, k := range subs {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(k)
		if v := q.Get(k); v != "" {
			b.WriteString("=" + v)
		}
	}
	m := hmac.New(sha1.New, []byte(secret))
	m.Write([]byte(b.String()))
	want := base64.StdEncoding.EncodeToString(m.Sum(nil))
	return hmac.Equal([]byte(want), []byte(p.signature))
}
