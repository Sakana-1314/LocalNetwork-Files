package s3

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AWS Signature Version 4 verification (header and presigned-URL forms).

const (
	algorithm       = "AWS4-HMAC-SHA256"
	unsignedPayload = "UNSIGNED-PAYLOAD"
	maxClockSkew    = 15 * time.Minute
	amzDateFormat   = "20060102T150405Z"
)

type sigParams struct {
	accessKey     string
	date          string // yyyymmdd
	region        string
	service       string
	signedHeaders []string
	signature     string
	amzDate       time.Time
	payloadHash   string
	presigned     bool
}

var errMissingAuth = errors.New("missing authentication")

func parseSigV4(r *http.Request) (*sigParams, error) {
	q := r.URL.Query()
	if q.Get("X-Amz-Algorithm") != "" {
		return parsePresigned(r, q)
	}
	h := r.Header.Get("Authorization")
	if h == "" {
		return nil, errMissingAuth
	}
	if !strings.HasPrefix(h, algorithm+" ") {
		return nil, fmt.Errorf("unsupported authorization scheme")
	}
	p := &sigParams{}
	for _, part := range strings.Split(h[len(algorithm)+1:], ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch k {
		case "Credential":
			if err := p.parseCredential(v); err != nil {
				return nil, err
			}
		case "SignedHeaders":
			p.signedHeaders = strings.Split(v, ";")
		case "Signature":
			p.signature = v
		}
	}
	if p.accessKey == "" || p.signature == "" || len(p.signedHeaders) == 0 {
		return nil, fmt.Errorf("malformed authorization header")
	}
	ds := r.Header.Get("X-Amz-Date")
	if ds == "" {
		ds = r.Header.Get("Date")
	}
	t, err := time.Parse(amzDateFormat, ds)
	if err != nil {
		if t, err = http.ParseTime(ds); err != nil {
			return nil, fmt.Errorf("invalid date")
		}
	}
	if d := time.Since(t); d > maxClockSkew || d < -maxClockSkew {
		return nil, errSkew
	}
	p.amzDate = t
	p.payloadHash = r.Header.Get("X-Amz-Content-Sha256")
	if p.payloadHash == "" {
		p.payloadHash = unsignedPayload
	}
	return p, nil
}

var (
	errSkew    = errors.New("request time too skewed")
	errExpired = errors.New("request has expired")
)

func parsePresigned(r *http.Request, q url.Values) (*sigParams, error) {
	if q.Get("X-Amz-Algorithm") != algorithm {
		return nil, fmt.Errorf("unsupported algorithm")
	}
	p := &sigParams{presigned: true, payloadHash: unsignedPayload}
	if err := p.parseCredential(q.Get("X-Amz-Credential")); err != nil {
		return nil, err
	}
	p.signedHeaders = strings.Split(q.Get("X-Amz-SignedHeaders"), ";")
	p.signature = q.Get("X-Amz-Signature")
	t, err := time.Parse(amzDateFormat, q.Get("X-Amz-Date"))
	if err != nil {
		return nil, fmt.Errorf("invalid date")
	}
	exp, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	if err != nil || exp < 0 || exp > 7*24*3600 {
		return nil, fmt.Errorf("invalid expires")
	}
	if time.Now().After(t.Add(time.Duration(exp) * time.Second)) {
		return nil, errExpired
	}
	if t.After(time.Now().Add(maxClockSkew)) {
		return nil, errSkew
	}
	p.amzDate = t
	if h := r.Header.Get("X-Amz-Content-Sha256"); h != "" {
		p.payloadHash = h
	}
	return p, nil
}

func (p *sigParams) parseCredential(v string) error {
	parts := strings.Split(v, "/")
	if len(parts) != 5 || parts[4] != "aws4_request" {
		return fmt.Errorf("malformed credential")
	}
	p.accessKey, p.date, p.region, p.service = parts[0], parts[1], parts[2], parts[3]
	return nil
}

func (p *sigParams) scope() string {
	return strings.Join([]string{p.date, p.region, p.service, "aws4_request"}, "/")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func signingKey(secret, date, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, "aws4_request")
}

// verify checks the request signature against secret.
func (p *sigParams) verify(r *http.Request, secret string) bool {
	canonical := canonicalRequest(r, p)
	sum := sha256.Sum256([]byte(canonical))
	sts := strings.Join([]string{algorithm, p.amzDate.UTC().Format(amzDateFormat), p.scope(), hex.EncodeToString(sum[:])}, "\n")
	sig := hex.EncodeToString(hmacSHA256(signingKey(secret, p.date, p.region, p.service), sts))
	return hmac.Equal([]byte(sig), []byte(p.signature))
}

func canonicalRequest(r *http.Request, p *sigParams) string {
	var b strings.Builder
	b.WriteString(r.Method)
	b.WriteByte('\n')
	b.WriteString(awsEncode(r.URL.Path, false))
	b.WriteByte('\n')

	// Canonical query string.
	q := r.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		if p.presigned && k == "X-Amz-Signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			pairs = append(pairs, awsEncode(k, true)+"="+awsEncode(v, true))
		}
	}
	b.WriteString(strings.Join(pairs, "&"))
	b.WriteByte('\n')

	for _, h := range p.signedHeaders {
		var v string
		switch h {
		case "host":
			v = r.Host
		case "content-length":
			v = r.Header.Get("Content-Length")
			if v == "" && r.ContentLength >= 0 {
				v = strconv.FormatInt(r.ContentLength, 10)
			}
		default:
			v = strings.Join(r.Header.Values(h), ",")
		}
		b.WriteString(h)
		b.WriteByte(':')
		b.WriteString(collapseSpaces(strings.TrimSpace(v)))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(strings.Join(p.signedHeaders, ";"))
	b.WriteByte('\n')
	b.WriteString(p.payloadHash)
	return b.String()
}

func collapseSpaces(s string) string {
	if !strings.Contains(s, "  ") {
		return s
	}
	return strings.Join(strings.Fields(s), " ")
}

// awsEncode percent-encodes everything except unreserved characters (and "/" unless encodeSlash).
func awsEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' || (c == '/' && !encodeSlash) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ---------- body handling ----------

// isStreaming reports whether the body uses aws-chunked encoding.
func isStreaming(r *http.Request, payloadHash string) bool {
	return strings.HasPrefix(payloadHash, "STREAMING-") ||
		strings.Contains(r.Header.Get("Content-Encoding"), "aws-chunked")
}

// chunkedReader decodes aws-chunked bodies ("<hex>[;chunk-signature=..]\r\n<data>\r\n"...),
// including the unsigned/trailer variants. Chunk signatures are not verified;
// the seed signature over the headers is.
type chunkedReader struct {
	r      *bufio.Reader
	remain int64
	done   bool
	err    error
}

func newChunkedReader(r io.Reader) *chunkedReader {
	return &chunkedReader{r: bufio.NewReaderSize(r, 64<<10)}
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	for c.remain == 0 {
		if c.done {
			return 0, io.EOF
		}
		line, err := c.r.ReadString('\n')
		if err != nil {
			c.err = fmt.Errorf("aws-chunked: %w", err)
			return 0, c.err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue // CRLF after chunk data
		}
		sizeStr, _, _ := strings.Cut(line, ";")
		size, err := strconv.ParseInt(strings.TrimSpace(sizeStr), 16, 64)
		if err != nil || size < 0 {
			c.err = fmt.Errorf("aws-chunked: bad chunk size %q", line)
			return 0, c.err
		}
		if size == 0 {
			c.done = true
			// Drain trailers (e.g. x-amz-checksum-crc32) until the final empty line or EOF.
			for {
				l, err := c.r.ReadString('\n')
				if err != nil || strings.TrimRight(l, "\r\n") == "" {
					break
				}
			}
			return 0, io.EOF
		}
		c.remain = size
	}
	if int64(len(p)) > c.remain {
		p = p[:c.remain]
	}
	n, err := c.r.Read(p)
	c.remain -= int64(n)
	if err == io.EOF && c.remain > 0 {
		err = io.ErrUnexpectedEOF
	}
	if err != nil && err != io.EOF {
		c.err = err
	}
	if err == io.EOF {
		err = nil
	}
	return n, err
}

// hashCheckReader verifies the SHA-256 of a body against the signed payload hash at EOF.
type hashCheckReader struct {
	r    io.Reader
	h    hash.Hash
	want string
}

var errBadDigest = errors.New("payload SHA256 mismatch")

func (h *hashCheckReader) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	h.h.Write(p[:n])
	if err == io.EOF && hex.EncodeToString(h.h.Sum(nil)) != h.want {
		return n, errBadDigest
	}
	return n, err
}

// body returns the decoded, integrity-checked request body.
func body(r *http.Request, p *sigParams) io.Reader {
	if isStreaming(r, p.payloadHash) {
		return newChunkedReader(r.Body)
	}
	if len(p.payloadHash) == 64 {
		if _, err := hex.DecodeString(p.payloadHash); err == nil {
			return &hashCheckReader{r: r.Body, h: sha256.New(), want: strings.ToLower(p.payloadHash)}
		}
	}
	return r.Body
}
