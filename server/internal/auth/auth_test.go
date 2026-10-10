package auth

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

func init() {
	// Keep the suite fast: tests create many hashes.
	hashCost = bcrypt.MinCost
}

type env struct {
	st   *store.Store
	svc  *Service
	dir  string
	user *store.User
}

func newEnv(t *testing.T, ttl time.Duration) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc, err := New(st, dir, ttl)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{st: st, svc: svc, dir: dir}
	e.user = e.mkUser(t, "alice", "pw-alice")
	return e
}

func (e *env) mkUser(t *testing.T, name, pw string) *store.User {
	t.Helper()
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	u := &store.User{Username: name, PasswordHash: h, Role: store.RoleUser}
	if err := e.st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *env) disable(t *testing.T, u *store.User) {
	t.Helper()
	u, _ = e.st.GetUser(u.ID)
	u.Disabled = true
	if err := e.st.UpdateUser(u, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	if h == "s3cret" || !CheckPassword(h, "s3cret") || CheckPassword(h, "S3cret") || CheckPassword(h, "") {
		t.Fatal("hash/check")
	}
	h2, _ := HashPassword("s3cret")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
	if CheckPassword("not-a-hash", "x") {
		t.Fatal("garbage hash accepted")
	}
	// Unicode passwords.
	h, _ = HashPassword("密码🔑")
	if !CheckPassword(h, "密码🔑") {
		t.Fatal("unicode password")
	}
}

func TestSecretPersisted(t *testing.T) {
	e := newEnv(t, time.Hour)
	b, err := os.ReadFile(filepath.Join(e.dir, "secret.key"))
	if err != nil || len(b) != 32 {
		t.Fatalf("secret file: %v %d", err, len(b))
	}
	if fi, _ := os.Stat(filepath.Join(e.dir, "secret.key")); fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("secret readable by others: %v", fi.Mode())
	}
	svc2, err := New(e.st, e.dir, time.Hour)
	if err != nil || !bytes.Equal(svc2.secret, e.svc.secret) {
		t.Fatal("secret not reused")
	}
	// Sessions survive a restart.
	tok, _, _ := e.svc.IssueSession(e.user)
	if _, err := svc2.Resolve(tok); err != nil {
		t.Fatalf("session after restart: %v", err)
	}
}

func TestEnsureAdmin(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, _ := New(st, dir, time.Hour)
	created, err := svc.EnsureAdmin("admin", "pw")
	if err != nil || !created {
		t.Fatalf("first: %v %v", created, err)
	}
	created, err = svc.EnsureAdmin("admin2", "pw")
	if err != nil || created {
		t.Fatalf("second: %v %v", created, err)
	}
	u, err := svc.Authenticate("admin", "pw")
	if err != nil || !u.IsAdmin() {
		t.Fatalf("admin login: %v", err)
	}
}

func TestAuthenticate(t *testing.T) {
	e := newEnv(t, time.Hour)
	var mu sync.Mutex
	seen := map[string]string{}
	done := make(chan struct{}, 4)
	e.svc.OnPassword = func(u *store.User, pw string) {
		mu.Lock()
		seen[u.Username] = pw
		mu.Unlock()
		done <- struct{}{}
	}
	if _, err := e.svc.Authenticate("alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong pw: %v", err)
	}
	if _, err := e.svc.Authenticate("nobody", "pw"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	u, err := e.svc.Authenticate("alice", "pw-alice")
	if err != nil || u.ID != e.user.ID {
		t.Fatalf("login: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("OnPassword not called")
	}
	mu.Lock()
	if seen["alice"] != "pw-alice" {
		t.Fatal("OnPassword got wrong password")
	}
	mu.Unlock()
	e.disable(t, e.user)
	if _, err := e.svc.Authenticate("alice", "pw-alice"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	// Disabled state is not revealed without the right password.
	if _, err := e.svc.Authenticate("alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled + wrong pw: %v", err)
	}
}

func TestSessions(t *testing.T) {
	e := newEnv(t, time.Hour)
	tok, exp, err := e.svc.IssueSession(e.user)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(exp); d < 59*time.Minute || d > time.Hour+time.Second {
		t.Fatalf("expiry %v", d)
	}
	p, err := e.svc.Resolve(tok)
	if err != nil || p.User.ID != e.user.ID || p.Via != "session" || p.ReadOnly {
		t.Fatalf("resolve: %v %+v", err, p)
	}
	for _, bad := range []string{"", "garbage", tok + "x", strings.Replace(tok, ".", "..", 1), "a.b.c"} {
		if _, err := e.svc.Resolve(bad); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("Resolve(%q) = %v", bad, err)
		}
	}
	// Password change bumps TokenVersion and kills old sessions.
	if err := e.svc.SetPassword(e.user, "new-pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Resolve(tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old session after password change: %v", err)
	}
	if _, err := e.svc.Authenticate("alice", "pw-alice"); err == nil {
		t.Fatal("old password still valid")
	}
	if _, err := e.svc.Authenticate("alice", "new-pw"); err != nil {
		t.Fatal(err)
	}
	u, _ := e.st.GetUser(e.user.ID)
	tok2, _, _ := e.svc.IssueSession(u)
	if _, err := e.svc.Resolve(tok2); err != nil {
		t.Fatalf("new session: %v", err)
	}
	// Disabled users are rejected even with a valid session.
	e.disable(t, u)
	if _, err := e.svc.Resolve(tok2); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled session: %v", err)
	}
	// Deleted users.
	bob := e.mkUser(t, "bob", "pw")
	tb, _, _ := e.svc.IssueSession(bob)
	e.st.DeleteUser(bob.ID)
	if _, err := e.svc.Resolve(tb); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("deleted user session: %v", err)
	}
}

func TestSessionExpiredAndForged(t *testing.T) {
	e := newEnv(t, -time.Minute)
	tok, _, err := e.svc.IssueSession(e.user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Resolve(tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired: %v", err)
	}
	now := time.Now()
	sign := func(m jwt.SigningMethod, key any, c jwt.Claims) string {
		s, err := jwt.NewWithClaims(m, c).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	good := sessionClaims{UID: e.user.ID, Ver: 0, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}}
	// Sanity: correctly signed by us works.
	if _, err := e.svc.Resolve(sign(jwt.SigningMethodHS256, e.svc.secret, good)); err != nil {
		t.Fatalf("own token: %v", err)
	}
	forged := []string{
		sign(jwt.SigningMethodHS256, []byte("another-secret-another-secret-xx"), good),
		sign(jwt.SigningMethodHS512, e.svc.secret, good),
		sign(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, good),
		// No expiry.
		sign(jwt.SigningMethodHS256, e.svc.secret, sessionClaims{UID: e.user.ID}),
		// Stale version.
		sign(jwt.SigningMethodHS256, e.svc.secret, sessionClaims{UID: e.user.ID, Ver: 5, RegisteredClaims: good.RegisteredClaims}),
		// Unknown user.
		sign(jwt.SigningMethodHS256, e.svc.secret, sessionClaims{UID: 999, RegisteredClaims: good.RegisteredClaims}),
		// UID 0.
		sign(jwt.SigningMethodHS256, e.svc.secret, sessionClaims{UID: 0, RegisteredClaims: good.RegisteredClaims}),
	}
	for i, f := range forged {
		if _, err := e.svc.Resolve(f); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("forged #%d accepted: %v", i, err)
		}
	}
}

func TestAPITokens(t *testing.T) {
	e := newEnv(t, time.Hour)
	at, plain, err := e.svc.CreateToken(e.user.ID, "  ci  ", []string{"write", "admin", "write"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, TokenPrefix) || len(plain) != len(TokenPrefix)+40 || at.Name != "ci" ||
		!strings.HasPrefix(plain, at.Prefix) || at.Hash == plain || at.ExpiresAt != nil {
		t.Fatalf("token %+v %q", at, plain)
	}
	if strings.Join(at.Scopes, ",") != "read,write" {
		t.Fatalf("scopes %v", at.Scopes)
	}
	p, err := e.svc.Resolve(plain)
	if err != nil || p.ReadOnly || p.Via != "token" || p.User.ID != e.user.ID {
		t.Fatalf("rw token: %v %+v", err, p)
	}
	stored, _ := e.st.GetTokenByHash(hashToken(plain))
	if stored.LastUsedAt == nil {
		t.Error("last used not recorded")
	}

	_, ro, _ := e.svc.CreateToken(e.user.ID, "ro", nil, 30)
	p, err = e.svc.Resolve(ro)
	if err != nil || !p.ReadOnly {
		t.Fatalf("read-only token: %v %+v", err, p)
	}
	_, ro2, _ := e.svc.CreateToken(e.user.ID, "ro2", []string{"read"}, 0)
	if p, _ := e.svc.Resolve(ro2); !p.ReadOnly {
		t.Fatal("read scope only must be read-only")
	}
	if ro == plain || ro2 == ro {
		t.Fatal("tokens must be unique")
	}

	if _, err := e.svc.Resolve(TokenPrefix + "unknown"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown token: %v", err)
	}
	if _, err := e.svc.Resolve(plain[:len(plain)-1]); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("truncated token: %v", err)
	}

	// Expired token.
	exp, expPlain, _ := e.svc.CreateToken(e.user.ID, "old", []string{"write"}, 1)
	past := time.Now().Add(-time.Minute)
	exp.ExpiresAt = &past
	e.st.DeleteToken(e.user.ID, exp.ID)
	exp.ID = 0
	e.st.CreateToken(exp)
	if _, err := e.svc.Resolve(expPlain); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired token: %v", err)
	}
	// Revoked token.
	e.st.DeleteToken(e.user.ID, at.ID)
	if _, err := e.svc.Resolve(plain); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked token: %v", err)
	}
	// Tokens survive password changes (they are independent credentials) but
	// not a disabled account.
	e.svc.SetPassword(e.user, "changed")
	if _, err := e.svc.Resolve(ro); err != nil {
		t.Fatalf("token after password change: %v", err)
	}
	e.disable(t, e.user)
	if _, err := e.svc.Resolve(ro); !errors.Is(err, ErrDisabled) {
		t.Fatalf("token of disabled user: %v", err)
	}
}

func TestS3Keys(t *testing.T) {
	e := newEnv(t, time.Hour)
	k, secret, err := e.svc.CreateS3Key(e.user.ID, " backup ", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.AccessKey, "LNF") || len(k.AccessKey) != 20 || len(secret) != 40 || k.Name != "backup" || !k.ReadOnly {
		t.Fatalf("key %+v", k)
	}
	if strings.Contains(k.SecretEnc, secret) {
		t.Fatal("secret stored in plaintext")
	}
	k2, u, sec, err := e.svc.LookupS3Key(k.AccessKey)
	if err != nil || sec != secret || u.ID != e.user.ID || !k2.ReadOnly {
		t.Fatalf("lookup: %v", err)
	}
	if _, _, _, err := e.svc.LookupS3Key("LNFNOPE"); err == nil {
		t.Fatal("unknown key")
	}
	// Round trip and tamper detection.
	enc1, _ := e.svc.encrypt("hello")
	enc2, _ := e.svc.encrypt("hello")
	if enc1 == enc2 {
		t.Fatal("nonce reuse")
	}
	if p, err := e.svc.decrypt(enc1); err != nil || p != "hello" {
		t.Fatalf("decrypt: %q %v", p, err)
	}
	raw := []byte(enc1)
	raw[len(raw)/2] ^= 1
	if _, err := e.svc.decrypt(string(raw)); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
	if _, err := e.svc.decrypt("!!"); err == nil {
		t.Fatal("bad base64 accepted")
	}
	if _, err := e.svc.decrypt(""); err == nil {
		t.Fatal("empty accepted")
	}
	other := &Service{store: e.st, secret: []byte("different-secret-different-secre")}
	if _, err := other.decrypt(enc1); err == nil {
		t.Fatal("decrypted with a different secret")
	}
	e.disable(t, e.user)
	if _, _, _, err := e.svc.LookupS3Key(k.AccessKey); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled user key: %v", err)
	}
}

func TestRandomString(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s := randomString(20)
		if len(s) != 20 || strings.Trim(s, alphabet) != "" || seen[s] {
			t.Fatalf("random %q", s)
		}
		seen[s] = true
	}
}

func TestBasicCheck(t *testing.T) {
	e := newEnv(t, time.Hour)
	b := NewBasic(e.svc)
	p, err := b.Check("alice", "pw-alice")
	if err != nil || p.Via != "password" || p.User.ID != e.user.ID || p.ReadOnly {
		t.Fatalf("password: %v %+v", err, p)
	}
	// Cached path.
	if p, err := b.Check("alice", "pw-alice"); err != nil || p.User.ID != e.user.ID {
		t.Fatalf("cached: %v", err)
	}
	if _, err := b.Check("alice", "wrong"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong: %v", err)
	}
	// Usernames are unique case-insensitively, so login ignores case.
	if p, err := b.Check("ALICE", "pw-alice"); err != nil || p.User.ID != e.user.ID {
		t.Fatalf("case-insensitive username: %v", err)
	}
	if _, err := b.Check("", ""); err == nil {
		t.Fatal("empty credentials")
	}

	// Token as password: any or matching username, read-only scope honored.
	_, tok, _ := e.svc.CreateToken(e.user.ID, "dav", []string{"read"}, 0)
	for _, user := range []string{"", "alice", "Alice"} {
		p, err := b.Check(user, tok)
		if err != nil || p.Via != "token" || !p.ReadOnly {
			t.Fatalf("token with user %q: %v %+v", user, err, p)
		}
	}
	if _, err := b.Check("bob", tok); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("token with another username: %v", err)
	}
	if _, err := b.Check("alice", TokenPrefix+"bogus"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bogus token: %v", err)
	}

	// Password change invalidates the cache immediately.
	u, _ := e.st.GetUser(e.user.ID)
	if err := e.svc.SetPassword(u, "pw2"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Check("alice", "pw-alice"); err == nil {
		t.Fatal("old password accepted from cache")
	}
	if _, err := b.Check("alice", "pw2"); err != nil {
		t.Fatal(err)
	}
	// Disabling invalidates the cache too.
	e.disable(t, u)
	if _, err := b.Check("alice", "pw2"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled cached: %v", err)
	}
	if _, err := b.Check("", tok); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled token: %v", err)
	}
}

func TestBasicCacheExpiry(t *testing.T) {
	e := newEnv(t, time.Hour)
	b := NewBasic(e.svc)
	if _, err := b.Check("alice", "pw-alice"); err != nil {
		t.Fatal(err)
	}
	// Expire the entry and change the hash behind the cache's back: the
	// expired entry must not be used.
	b.mu.Lock()
	for k, v := range b.cache {
		v.expires = time.Now().Add(-time.Second)
		b.cache[k] = v
	}
	b.mu.Unlock()
	u, _ := e.st.GetUser(e.user.ID)
	u.PasswordHash, _ = HashPassword("other")
	e.st.UpdateUser(u, nil)
	if _, err := b.Check("alice", "pw-alice"); err == nil {
		t.Fatal("expired cache entry used")
	}
	// Cache stays bounded.
	b.mu.Lock()
	for i := 0; i < 5000; i++ {
		b.cache[[32]byte{byte(i), byte(i >> 8), 1}] = basicEntry{expires: time.Now().Add(time.Hour)}
	}
	b.mu.Unlock()
	if _, err := b.Check("alice", "other"); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	n := len(b.cache)
	b.mu.Unlock()
	if n > basicCacheMax+1 {
		t.Fatalf("cache grew to %d", n)
	}
}

func TestBasicFromRequest(t *testing.T) {
	e := newEnv(t, time.Hour)
	b := NewBasic(e.svc)
	r := httptest.NewRequest("GET", "/", nil)
	if _, err := b.FromRequest(r); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("no auth: %v", err)
	}
	r.SetBasicAuth("alice", "pw-alice")
	if p, err := b.FromRequest(r); err != nil || p.User.Username != "alice" {
		t.Fatalf("basic: %v", err)
	}
	tok, _, _ := e.svc.IssueSession(e.user)
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	if p, err := b.FromRequest(r); err != nil || p.Via != "session" {
		t.Fatalf("bearer: %v", err)
	}
	r.Header.Set("Authorization", "Bearer ")
	if _, err := b.FromRequest(r); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("empty bearer: %v", err)
	}
}

func TestBasicConcurrent(t *testing.T) {
	e := newEnv(t, time.Hour)
	b := NewBasic(e.svc)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pw := "pw-alice"
			if i%3 == 0 {
				pw = "wrong"
			}
			for j := 0; j < 5; j++ {
				b.Check("alice", pw)
			}
		}(i)
	}
	wg.Wait()
}

func TestBasicCacheRename(t *testing.T) {
	e := newEnv(t, time.Hour)
	b := NewBasic(e.svc)
	if _, err := b.Check("alice", "pw-alice"); err != nil {
		t.Fatal(err)
	}
	u, _ := e.st.GetUser(e.user.ID)
	u.Username = "alice2"
	if err := e.st.UpdateUser(u, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Check("alice", "pw-alice"); err == nil {
		t.Fatal("old username accepted from cache after rename")
	}
	if p, err := b.Check("alice2", "pw-alice"); err != nil || p.User.Username != "alice2" {
		t.Fatalf("new name: %v", err)
	}
}
