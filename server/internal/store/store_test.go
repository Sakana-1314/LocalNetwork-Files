package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func abs(parts ...string) string {
	return filepath.Join(append([]string{string(filepath.Separator)}, parts...)...)
}

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, dir
}

type eventLog struct {
	mu sync.Mutex
	ev []Event
}

func (l *eventLog) add(e Event) { l.mu.Lock(); l.ev = append(l.ev, e); l.mu.Unlock() }
func (l *eventLog) take() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.ev
	l.ev = nil
	return out
}

func TestValidateMounts(t *testing.T) {
	ok := []Mount{{ID: 7, Name: "  Photos ", Path: abs("data", "photos", "..", "pics") + string(filepath.Separator)}, {Name: "照片", Path: abs("x")}}
	if err := ValidateMounts(ok); err != nil {
		t.Fatal(err)
	}
	if ok[0].Name != "Photos" || ok[0].Path != abs("data", "pics") || ok[0].ID != 0 {
		t.Fatalf("normalized: %+v", ok[0])
	}
	bad := map[string][]Mount{
		"empty":        {{Name: " ", Path: abs("a")}},
		"dot":          {{Name: ".", Path: abs("a")}},
		"dotdot":       {{Name: "..", Path: abs("a")}},
		"slash":        {{Name: "a/b", Path: abs("a")}},
		"backslash":    {{Name: `a\b`, Path: abs("a")}},
		"nul":          {{Name: "a\x00b", Path: abs("a")}},
		"too long":     {{Name: strings.Repeat("长", 129), Path: abs("a")}},
		"relative":     {{Name: "a", Path: "relative/dir"}},
		"empty path":   {{Name: "a", Path: ""}},
		"nul path":     {{Name: "a", Path: abs("a\x00b")}},
		"dup":          {{Name: "a", Path: abs("a")}, {Name: "a", Path: abs("b")}},
		"dup case":     {{Name: "Music", Path: abs("a")}, {Name: "music", Path: abs("b")}},
		"dup trimmed":  {{Name: "x", Path: abs("a")}, {Name: " x ", Path: abs("b")}},
		"dup unicode":  {{Name: "Ärger", Path: abs("a")}, {Name: "ärger", Path: abs("b")}},
		"second wrong": {{Name: "ok", Path: abs("a")}, {Name: "", Path: abs("b")}},
	}
	for name, ms := range bad {
		if err := ValidateMounts(ms); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if err := ValidateMounts(nil); err != nil {
		t.Fatal(err)
	}
}

func TestUserCRUD(t *testing.T) {
	st, _ := openTest(t)
	ev := &eventLog{}
	st.Subscribe(ev.add)

	if n, _ := st.CountUsers(); n != 0 {
		t.Fatal("fresh store has users")
	}
	u := &User{Username: "alice", PasswordHash: "h", Role: RoleAdmin, Mounts: []Mount{
		{Name: "b", Path: abs("data", "b"), Writable: true}, {Name: "a", Path: abs("data", "a")}}}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 || !u.IsAdmin() {
		t.Fatalf("created %+v", u)
	}
	if got := ev.take(); !reflect.DeepEqual(got, []Event{EventUsersChanged}) {
		t.Fatalf("events %v", got)
	}
	if err := st.CreateUser(&User{Username: "alice", PasswordHash: "h"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username: %v", err)
	}
	if err := st.CreateUser(&User{Username: "bad", PasswordHash: "h", Mounts: []Mount{{Name: "x", Path: "rel"}}}); err == nil {
		t.Fatal("invalid mounts accepted")
	}
	if len(ev.take()) != 0 {
		t.Fatal("failed creates must not publish")
	}

	g, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Username != "alice" || g.Role != RoleAdmin || len(g.Mounts) != 2 || g.Mounts[0].Name != "b" || !g.Mounts[0].Writable {
		t.Fatalf("get %+v", g)
	}
	if g2, err := st.GetUserByName("alice"); err != nil || g2.ID != u.ID || len(g2.Mounts) != 2 {
		t.Fatalf("by name: %v %+v", err, g2)
	}
	if _, err := st.GetUserByName("nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing by name: %v", err)
	}
	if _, err := st.GetUser(9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing by id: %v", err)
	}

	// Scalar update without touching mounts.
	g.Disabled = true
	g.TokenVersion = 3
	if err := st.UpdateUser(g, nil); err != nil {
		t.Fatal(err)
	}
	g, _ = st.GetUser(u.ID)
	if !g.Disabled || g.TokenVersion != 3 || len(g.Mounts) != 2 {
		t.Fatalf("after scalar update %+v", g)
	}
	// Disabled is a bool: setting it back to false must persist (zero value).
	g.Disabled = false
	st.UpdateUser(g, nil)
	if g, _ = st.GetUser(u.ID); g.Disabled {
		t.Fatal("disabled=false not persisted")
	}
	// Replace mounts.
	if err := st.UpdateUser(g, []Mount{{ID: 99, Name: "c", Path: abs("c")}}); err != nil {
		t.Fatal(err)
	}
	g, _ = st.GetUser(u.ID)
	if len(g.Mounts) != 1 || g.Mounts[0].Name != "c" || g.Mounts[0].ID == 99 {
		t.Fatalf("after mount replace %+v", g.Mounts)
	}
	// Invalid mounts leave everything unchanged.
	if err := st.UpdateUser(g, []Mount{{Name: "x", Path: abs("x")}, {Name: "X", Path: abs("y")}}); err == nil {
		t.Fatal("dup mounts accepted on update")
	}
	if g, _ = st.GetUser(u.ID); len(g.Mounts) != 1 {
		t.Fatal("failed update changed mounts")
	}
	// Empty (non-nil) mounts removes all.
	if err := st.UpdateUser(g, []Mount{}); err != nil {
		t.Fatal(err)
	}
	if g, _ = st.GetUser(u.ID); len(g.Mounts) != 0 {
		t.Fatal("mounts not cleared")
	}
	// Rename conflict.
	bob := &User{Username: "bob", PasswordHash: "h"}
	st.CreateUser(bob)
	bob.Username = "alice"
	if err := st.UpdateUser(bob, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename conflict: %v", err)
	}
	ev.take()

	users, err := st.ListUsers()
	if err != nil || len(users) != 2 || users[0].Username != "alice" || users[1].Username != "bob" {
		t.Fatalf("list %v %+v", err, users)
	}
	if n, _ := st.CountUsers(); n != 2 {
		t.Fatal("count")
	}
}

func TestDeleteUserCascades(t *testing.T) {
	st, _ := openTest(t)
	ev := &eventLog{}
	u := &User{Username: "u", PasswordHash: "h", Mounts: []Mount{{Name: "m", Path: abs("m")}}}
	other := &User{Username: "o", PasswordHash: "h", Mounts: []Mount{{Name: "m", Path: abs("o")}}}
	st.CreateUser(u)
	st.CreateUser(other)
	st.CreateToken(&AccessToken{UserID: u.ID, Name: "t", TokenEnc: "e1", Scopes: []string{ScopeRead}})
	st.CreateToken(&AccessToken{UserID: other.ID, Name: "t", TokenEnc: "e2", Scopes: []string{ScopeRead}})
	st.CreateS3Key(&S3Key{AccessKey: "AK1", UserID: u.ID, SecretEnc: "x"})
	st.CreateS3Key(&S3Key{AccessKey: "AK2", UserID: other.ID, SecretEnc: "x"})
	st.Subscribe(ev.add)

	if err := st.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if got := ev.take(); !reflect.DeepEqual(got, []Event{EventUsersChanged}) {
		t.Fatalf("events %v", got)
	}
	if _, err := st.GetUser(u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("user still there")
	}
	if _, err := st.GetToken(u.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("token not cascaded")
	}
	if _, err := st.GetS3Key("AK1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("s3 key not cascaded")
	}
	var n int64
	st.db.Model(&Mount{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatal("mounts not cascaded")
	}
	// Other user's data untouched.
	if _, err := st.GetToken(other.ID, 2); err != nil {
		t.Fatal("other token deleted")
	}
	if _, err := st.GetS3Key("AK2"); err != nil {
		t.Fatal("other key deleted")
	}
	if o, _ := st.GetUser(other.ID); len(o.Mounts) != 1 {
		t.Fatal("other mounts deleted")
	}
	if err := st.DeleteUser(u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if len(ev.take()) != 0 {
		t.Fatal("failed delete published")
	}
}

func TestAllMountPaths(t *testing.T) {
	st, _ := openTest(t)
	st.CreateUser(&User{Username: "a", PasswordHash: "h", Mounts: []Mount{{Name: "x", Path: abs("p1")}, {Name: "y", Path: abs("p2")}}})
	st.CreateUser(&User{Username: "b", PasswordHash: "h", Mounts: []Mount{{Name: "x", Path: abs("p1")}}})
	st.CreateUser(&User{Username: "c", PasswordHash: "h", Disabled: true, Mounts: []Mount{{Name: "x", Path: abs("p3")}}})
	ps, err := st.AllMountPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("paths %v", ps)
	}
	for _, p := range ps {
		if p == abs("p3") {
			t.Fatal("disabled user's mount included")
		}
	}
}

func TestTokensAndKeys(t *testing.T) {
	st, _ := openTest(t)
	u := &User{Username: "u", PasswordHash: "h"}
	st.CreateUser(u)
	exp := time.Now().Add(time.Hour).Round(time.Second)
	tok := &AccessToken{UserID: u.ID, Name: "ci", Prefix: "lnf_abc", TokenEnc: "enc", Scopes: []string{ScopeRead, ScopeWrite}, ExpiresAt: &exp}
	if err := st.CreateToken(tok); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetToken(u.ID, tok.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TokenEnc != "enc" {
		t.Fatalf("encrypted token not stored: %+v", got)
	}
	// Lookup by the clear-text prefix, which is what authentication uses.
	byPrefix, err := st.GetTokensByPrefix("lnf_abc")
	if err != nil || len(byPrefix) != 1 || byPrefix[0].ID != tok.ID {
		t.Fatalf("prefix lookup %v %+v", err, byPrefix)
	}
	if !reflect.DeepEqual(got.Scopes, []string{"read", "write"}) || !got.HasScope(ScopeWrite) || got.HasScope("admin") ||
		got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) || got.LastUsedAt != nil {
		t.Fatalf("token %+v", got)
	}
	st.TouchToken(got.ID)
	got, _ = st.GetToken(u.ID, tok.ID)
	if got.LastUsedAt == nil {
		t.Fatal("touch")
	}
	ts, _ := st.ListTokens(u.ID)
	if len(ts) != 1 || len(ts[0].Scopes) != 2 {
		t.Fatalf("list %+v", ts)
	}
	if err := st.DeleteToken(u.ID+1, got.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete other user's token: %v", err)
	}
	if err := st.DeleteToken(u.ID, got.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteToken(u.ID, got.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("double delete")
	}

	ro := &AccessToken{UserID: u.ID, TokenEnc: "e2", Scopes: []string{ScopeRead}}
	st.CreateToken(ro)
	if got, _ := st.GetToken(u.ID, ro.ID); got.HasScope(ScopeWrite) {
		t.Fatal("read-only token has write")
	}
	// A token cannot be read through another user's id.
	if _, err := st.GetToken(u.ID+1, ro.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner-scoped lookup: %v", err)
	}

	if err := st.CreateS3Key(&S3Key{AccessKey: "AK", UserID: u.ID, Name: "k", SecretEnc: "enc", ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	k, err := st.GetS3Key("AK")
	if err != nil || k.SecretEnc != "enc" || !k.ReadOnly || k.UserID != u.ID {
		t.Fatalf("key %v %+v", err, k)
	}
	if ks, _ := st.ListS3Keys(u.ID); len(ks) != 1 {
		t.Fatal("list keys")
	}
	if err := st.DeleteS3Key(u.ID+1, "AK"); !errors.Is(err, ErrNotFound) {
		t.Fatal("delete other's key")
	}
	if err := st.DeleteS3Key(u.ID, "AK"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetS3Key("AK"); !errors.Is(err, ErrNotFound) {
		t.Fatal("key still there")
	}
}

// Tokens created before the switch to encrypted storage cannot be recovered:
// their hashed rows and the column itself must disappear on open.
func TestLegacyHashedTokensAreDropped(t *testing.T) {
	st, dir := openTest(t)
	u := &User{Username: "u", PasswordHash: "h"}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	// Simulate the old schema: an extra hash column holding a hashed token,
	// plus the unique index the old version created on it. SQLite refuses to
	// drop a column while an index still references it, and if the column
	// survives as NOT NULL every later token INSERT fails with a 500.
	if err := st.db.Exec("ALTER TABLE access_tokens ADD COLUMN hash TEXT NOT NULL DEFAULT ''").Error; err != nil {
		t.Fatal(err)
	}
	if err := st.db.Exec("CREATE UNIQUE INDEX idx_access_tokens_hash ON access_tokens(hash)").Error; err != nil {
		t.Fatal(err)
	}
	if err := st.db.Exec("INSERT INTO access_tokens (user_id, name, prefix, scopes, hash) VALUES (?,?,?,?,?)",
		u.ID, "old", "lnf_abc", "read", "deadbeef").Error; err != nil {
		t.Fatal(err)
	}
	st.Close()

	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if n := len(mustList(t, st2, u.ID)); n != 0 {
		t.Fatalf("legacy token survived: %d", n)
	}
	if st2.db.Migrator().HasColumn(&AccessToken{}, "hash") {
		t.Fatal("hash column not dropped")
	}
	// A token created afterwards works normally.
	if err := st2.CreateToken(&AccessToken{UserID: u.ID, Name: "new", Prefix: "lnf_xyz", TokenEnc: "enc"}); err != nil {
		t.Fatal(err)
	}
	if n := len(mustList(t, st2, u.ID)); n != 1 {
		t.Fatalf("new token missing: %d", n)
	}
}

func mustList(t *testing.T, st *Store, userID uint) []AccessToken {
	t.Helper()
	ts, err := st.ListTokens(userID)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func TestSplitScopes(t *testing.T) {
	if got := splitScopes(" read , ,write,"); !reflect.DeepEqual(got, []string{"read", "write"}) {
		t.Fatalf("%v", got)
	}
	if got := splitScopes(""); got == nil || len(got) != 0 {
		t.Fatalf("empty: %#v", got)
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
