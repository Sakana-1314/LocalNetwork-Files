package store

import (
	"errors"
	"os"
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

func TestValidateBucket(t *testing.T) {
	ok := &Bucket{ID: 7, Name: "  Photos ", Paths: []string{abs("data", "photos", "..", "pics") + string(filepath.Separator), abs("backup", "pics")}}
	if err := ValidateBucket(ok); err != nil {
		t.Fatal(err)
	}
	if ok.Name != "Photos" || ok.Paths[0] != abs("data", "pics") || ok.Paths[1] != abs("backup", "pics") {
		t.Fatalf("normalized: %+v", ok)
	}
	bad := map[string]*Bucket{
		"empty":      {Name: " ", Paths: []string{abs("a")}},
		"dot":        {Name: ".", Paths: []string{abs("a")}},
		"dotdot":     {Name: "..", Paths: []string{abs("a")}},
		"slash":      {Name: "a/b", Paths: []string{abs("a")}},
		"backslash":  {Name: `a\b`, Paths: []string{abs("a")}},
		"nul":        {Name: "a\x00b", Paths: []string{abs("a")}},
		"too long":   {Name: strings.Repeat("长", 129), Paths: []string{abs("a")}},
		"no paths":   {Name: "a"},
		"relative":   {Name: "a", Paths: []string{"relative/dir"}},
		"empty path": {Name: "a", Paths: []string{""}},
		"nul path":   {Name: "a", Paths: []string{abs("a\x00b")}},
		"dup path":   {Name: "a", Paths: []string{abs("a"), abs("a", ".")}},
		"nested":     {Name: "a", Paths: []string{abs("a"), abs("a", "b")}},
		"parent":     {Name: "a", Paths: []string{abs("a", "b"), abs("a")}},
	}
	for name, b := range bad {
		if err := ValidateBucket(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid, got %v", name, err)
		}
	}
}

// bucket creates a bucket for tests.
func bucket(t *testing.T, st *Store, name string, paths ...string) *Bucket {
	t.Helper()
	b := &Bucket{Name: name, Paths: paths}
	if err := st.CreateBucket(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBucketCRUD(t *testing.T) {
	st, _ := openTest(t)
	ev := &eventLog{}
	st.Subscribe(ev.add)

	b := bucket(t, st, "照片", abs("p1"), abs("p2"))
	if b.ID == 0 {
		t.Fatal("no id")
	}
	if got := ev.take(); !reflect.DeepEqual(got, []Event{EventUsersChanged}) {
		t.Fatalf("events %v", got)
	}
	if err := st.CreateBucket(&Bucket{Name: "照片", Paths: []string{abs("x")}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := st.CreateBucket(&Bucket{Name: "music", Paths: []string{abs("m")}}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateBucket(&Bucket{Name: "MUSIC", Paths: []string{abs("m2")}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	ev.take()

	g, err := st.GetBucket(b.ID)
	if err != nil || g.Name != "照片" || !reflect.DeepEqual(g.Paths, []string{abs("p1"), abs("p2")}) {
		t.Fatalf("get %v %+v", err, g)
	}
	g.Name, g.Paths = "图片", []string{abs("p2"), abs("p3")}
	if err := st.UpdateBucket(g); err != nil {
		t.Fatal(err)
	}
	if g, _ = st.GetBucket(b.ID); g.Name != "图片" || !reflect.DeepEqual(g.Paths, []string{abs("p2"), abs("p3")}) {
		t.Fatalf("after update %+v", g)
	}
	g.Name = "Music"
	if err := st.UpdateBucket(g); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename conflict: %v", err)
	}
	if err := st.UpdateBucket(&Bucket{ID: 999, Name: "zz", Paths: []string{abs("z")}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	bs, err := st.ListBuckets()
	if err != nil || len(bs) != 2 || bs[0].Name != "图片" {
		t.Fatalf("list %v %+v", err, bs)
	}

	u := &User{Username: "u", PasswordHash: "h", Grants: []Grant{{BucketID: b.ID, Writable: true}}}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.BucketUsers(); n[b.ID] != 1 {
		t.Fatalf("bucket users %v", n)
	}
	ev.take()
	if err := st.DeleteBucket(b.ID); err != nil {
		t.Fatal(err)
	}
	if got := ev.take(); !reflect.DeepEqual(got, []Event{EventUsersChanged}) {
		t.Fatalf("events %v", got)
	}
	if g, _ := st.GetUser(u.ID); len(g.Grants) != 0 || len(g.Mounts) != 0 {
		t.Fatalf("grants not cascaded: %+v", g)
	}
	if err := st.DeleteBucket(b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestUserCRUD(t *testing.T) {
	st, _ := openTest(t)
	ba := bucket(t, st, "a", abs("data", "a"))
	bb := bucket(t, st, "b", abs("data", "b"), abs("backup", "b"))
	ev := &eventLog{}
	st.Subscribe(ev.add)

	if n, _ := st.CountUsers(); n != 0 {
		t.Fatal("fresh store has users")
	}
	u := &User{Username: "alice", PasswordHash: "h", Role: RoleAdmin, Grants: []Grant{
		{BucketID: bb.ID, Writable: true}, {BucketID: ba.ID}}}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 || !u.IsAdmin() || len(u.Grants) != 2 || u.Grants[0].Name != "b" {
		t.Fatalf("created %+v", u)
	}
	if got := ev.take(); !reflect.DeepEqual(got, []Event{EventUsersChanged}) {
		t.Fatalf("events %v", got)
	}
	if err := st.CreateUser(&User{Username: "alice", PasswordHash: "h"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate username: %v", err)
	}
	if err := st.CreateUser(&User{Username: "bad", PasswordHash: "h", Grants: []Grant{{BucketID: 999}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown bucket accepted: %v", err)
	}
	if err := st.CreateUser(&User{Username: "bad", PasswordHash: "h", Grants: []Grant{{BucketID: ba.ID}, {BucketID: ba.ID}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate grant accepted: %v", err)
	}
	if len(ev.take()) != 0 {
		t.Fatal("failed creates must not publish")
	}

	g, err := st.GetUser(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []Mount{
		{BucketID: bb.ID, Name: "b", Path: abs("data", "b"), Mirrors: []string{abs("backup", "b")}, Writable: true},
		{BucketID: ba.ID, Name: "a", Path: abs("data", "a")},
	}
	if g.Username != "alice" || g.Role != RoleAdmin || !reflect.DeepEqual(g.Mounts, want) {
		t.Fatalf("get %+v", g)
	}
	if g2, err := st.GetUserByName("alice"); err != nil || g2.ID != u.ID || len(g2.Mounts) != 2 {
		t.Fatalf("by name: %v %+v", err, g2)
	}
	if _, err := st.GetUserByName("nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing by name: %v", err)
	}
	if _, err := st.GetUser(9999); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing user found")
	}

	// Scalar update keeps grants; replacing grants works.
	g.Disabled, g.TokenVersion = true, 3
	if err := st.UpdateUser(g, nil); err != nil {
		t.Fatal(err)
	}
	if g, _ = st.GetUser(u.ID); !g.Disabled || g.TokenVersion != 3 || len(g.Grants) != 2 {
		t.Fatalf("after scalar update %+v", g)
	}
	if err := st.UpdateUser(g, []Grant{{BucketID: ba.ID, Writable: true}}); err != nil {
		t.Fatal(err)
	}
	if g, _ = st.GetUser(u.ID); len(g.Grants) != 1 || g.Grants[0].Name != "a" || !g.Mounts[0].Writable {
		t.Fatalf("after grant replace %+v", g)
	}
	if err := st.UpdateUser(g, []Grant{{BucketID: 999}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown bucket on update: %v", err)
	}
	if g, _ = st.GetUser(u.ID); len(g.Grants) != 1 {
		t.Fatal("failed update changed grants")
	}
	if err := st.UpdateUser(g, []Grant{}); err != nil {
		t.Fatal(err)
	}
	if g, _ = st.GetUser(u.ID); len(g.Grants) != 0 {
		t.Fatal("grants not cleared")
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
	b := bucket(t, st, "m", abs("m"))
	u := &User{Username: "u", PasswordHash: "h", Grants: []Grant{{BucketID: b.ID}}}
	other := &User{Username: "o", PasswordHash: "h", Grants: []Grant{{BucketID: b.ID}}}
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
	st.db.Model(&Grant{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatal("grants not cascaded")
	}
	// Other user's data and the bucket are untouched.
	if _, err := st.GetToken(other.ID, 2); err != nil {
		t.Fatal("other token deleted")
	}
	if _, err := st.GetS3Key("AK2"); err != nil {
		t.Fatal("other key deleted")
	}
	if o, _ := st.GetUser(other.ID); len(o.Grants) != 1 {
		t.Fatal("other grants deleted")
	}
	if _, err := st.GetBucket(b.ID); err != nil {
		t.Fatal("bucket deleted with its user")
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
	tmp := t.TempDir()
	p1, p2, p3, backup := filepath.Join(tmp, "p1"), filepath.Join(tmp, "p2"), filepath.Join(tmp, "p3"), filepath.Join(tmp, "b")
	for _, d := range []string{p2, p3, backup} {
		os.MkdirAll(d, 0o755)
	}
	x := bucket(t, st, "x", p1, backup) // p1 is offline: the backup serves
	y := bucket(t, st, "y", p2)
	z := bucket(t, st, "z", p3)
	bucket(t, st, "unused", abs("unused"))
	st.CreateUser(&User{Username: "a", PasswordHash: "h", Grants: []Grant{{BucketID: x.ID}, {BucketID: y.ID}}})
	st.CreateUser(&User{Username: "b", PasswordHash: "h", Grants: []Grant{{BucketID: x.ID}}})
	st.CreateUser(&User{Username: "c", PasswordHash: "h", Disabled: true, Grants: []Grant{{BucketID: z.ID}}})
	ps, err := st.AllMountPaths()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ps, []string{backup, p2}) {
		t.Fatalf("paths %v", ps)
	}
}

func TestMigrateLegacyMounts(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &User{Username: "a", PasswordHash: "h"}
	b := &User{Username: "b", PasswordHash: "h"}
	st.CreateUser(a)
	st.CreateUser(b)
	exec := func(q string, args ...any) {
		t.Helper()
		if err := st.db.Exec(q, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE mounts (id integer PRIMARY KEY AUTOINCREMENT, user_id integer NOT NULL, name text NOT NULL, path text NOT NULL, writable numeric NOT NULL DEFAULT false)`)
	exec(`INSERT INTO mounts (user_id, name, path, writable) VALUES (?, '电影', ?, 1), (?, '照片', ?, 0), (?, '电影', ?, 0), (?, '影视', ?, 0)`,
		a.ID, abs("movies"), a.ID, abs("photos"), b.ID, abs("other-movies"), b.ID, abs("movies"))
	st.Close()

	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.db.Migrator().HasTable("mounts") {
		t.Fatal("legacy table kept")
	}
	bs, _ := st.ListBuckets()
	got := map[string][]string{}
	for _, x := range bs {
		got[x.Name] = x.Paths
	}
	want := map[string][]string{"电影": {abs("movies")}, "照片": {abs("photos")}, "电影-2": {abs("other-movies")}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buckets %v", got)
	}
	ga, _ := st.GetUser(a.ID)
	gb, _ := st.GetUser(b.ID)
	if len(ga.Mounts) != 2 || ga.Mounts[0].Name != "电影" || !ga.Mounts[0].Writable || ga.Mounts[1].Writable {
		t.Fatalf("a mounts %+v", ga.Mounts)
	}
	if len(gb.Mounts) != 2 || gb.Mounts[0].Name != "电影-2" || gb.Mounts[1].Name != "电影" || gb.Mounts[1].Path != abs("movies") {
		t.Fatalf("b mounts %+v", gb.Mounts)
	}
}

func TestTokensAndKeys(t *testing.T) {
	st, _ := openTest(t)
	u := &User{Username: "u", PasswordHash: "h"}
	st.CreateUser(u)
	exp := time.Now().Add(time.Hour).Round(time.Second)
	tok := &AccessToken{UserID: u.ID, Name: "ci", Prefix: "ml_abc", TokenEnc: "enc", Scopes: []string{ScopeRead, ScopeWrite}, ExpiresAt: &exp}
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
	byPrefix, err := st.GetTokensByPrefix("ml_abc")
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
		u.ID, "old", "ml_abc", "read", "deadbeef").Error; err != nil {
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
	if err := st2.CreateToken(&AccessToken{UserID: u.ID, Name: "new", Prefix: "ml_xyz", TokenEnc: "enc"}); err != nil {
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

// Upgrading from the pre-rename layout must keep the data: the database file is
// renamed in place, WAL sidecars included, and running twice is harmless.
func TestLegacyDatabaseFileIsRenamed(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Username: "keeper", PasswordHash: "h"}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Re-create the pre-rename layout.
	if err := os.Rename(filepath.Join(dir, dbFile), filepath.Join(dir, legacyDBFile)); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(dir, dbFile+suffix)); err == nil {
			if err := os.Rename(filepath.Join(dir, dbFile+suffix), filepath.Join(dir, legacyDBFile+suffix)); err != nil {
				t.Fatal(err)
			}
		}
	}

	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, err := os.Stat(filepath.Join(dir, legacyDBFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy database file still present: %v", err)
	}
	got, err := st2.GetUserByName("keeper")
	if err != nil || got.Username != "keeper" {
		t.Fatalf("user lost across the rename: %v", err)
	}
}

func TestDatabaseRenameIsIdempotentAndKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	// A database already at the new name wins; a stray legacy file is left alone
	// rather than overwriting it.
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(&User{Username: "new", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}
	st.Close()

	if err := os.WriteFile(filepath.Join(dir, legacyDBFile), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if _, err := st2.GetUserByName("new"); err != nil {
		t.Fatalf("existing database was replaced: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, legacyDBFile)); err != nil || string(b) != "not a database" {
		t.Fatalf("legacy file was touched: %q %v", b, err)
	}
	if err := migrateDBFile(dir); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
}
