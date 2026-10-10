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
	st.CreateToken(&AccessToken{UserID: u.ID, Name: "t", Hash: "h1", Scopes: []string{ScopeRead}})
	st.CreateToken(&AccessToken{UserID: other.ID, Name: "t", Hash: "h2", Scopes: []string{ScopeRead}})
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
	if _, err := st.GetTokenByHash("h1"); !errors.Is(err, ErrNotFound) {
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
	if _, err := st.GetTokenByHash("h2"); err != nil {
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
	tok := &AccessToken{UserID: u.ID, Name: "ci", Prefix: "lnf_abc", Hash: "hash", Scopes: []string{ScopeRead, ScopeWrite}, ExpiresAt: &exp}
	if err := st.CreateToken(tok); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateToken(&AccessToken{UserID: u.ID, Hash: "hash"}); err == nil {
		t.Fatal("duplicate hash accepted")
	}
	got, err := st.GetTokenByHash("hash")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Scopes, []string{"read", "write"}) || !got.HasScope(ScopeWrite) || got.HasScope("admin") ||
		got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) || got.LastUsedAt != nil {
		t.Fatalf("token %+v", got)
	}
	st.TouchToken(got.ID)
	got, _ = st.GetTokenByHash("hash")
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

	ro := &AccessToken{UserID: u.ID, Hash: "h2", Scopes: []string{ScopeRead}}
	st.CreateToken(ro)
	if got, _ := st.GetTokenByHash("h2"); got.HasScope(ScopeWrite) {
		t.Fatal("read-only token has write")
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

func TestSplitScopes(t *testing.T) {
	if got := splitScopes(" read , ,write,"); !reflect.DeepEqual(got, []string{"read", "write"}) {
		t.Fatalf("%v", got)
	}
	if got := splitScopes(""); got == nil || len(got) != 0 {
		t.Fatalf("empty: %#v", got)
	}
}

func TestSettingsNormalize(t *testing.T) {
	d := DefaultSettings()
	n := d
	n.Normalize()
	if !reflect.DeepEqual(n, d) {
		t.Fatal("defaults must be normalized already")
	}
	var z Settings
	z.Normalize()
	if z.Cache != d.Cache || z.Transcode != d.Transcode || z.Services.S3Port != 9000 || z.Services.S3Region != "us-east-1" ||
		z.Services.SMBWorkgroup != "WORKGROUP" || z.Search.ReindexIntervalMinutes != 0 {
		t.Fatalf("zero normalized: %+v", z)
	}
	s := d
	s.Cache.ThumbMaxMB = 1
	s.Transcode.Encoder = "rm -rf /"
	s.Transcode.Preset = "placebo"
	s.Transcode.MaxSessions = 100
	s.Transcode.SegmentSeconds = 1
	s.Search.ReindexIntervalMinutes = -5
	s.Services.S3Port = 70000
	s.Services.SMBPort = 0
	s.Normalize()
	if s.Cache.ThumbMaxMB != d.Cache.ThumbMaxMB || s.Transcode.Encoder != "libx264" || s.Transcode.Preset != "veryfast" ||
		s.Transcode.MaxSessions != 2 || s.Transcode.SegmentSeconds != 6 || s.Search.ReindexIntervalMinutes != 60 ||
		s.Services.S3Port != 9000 || s.Services.SMBPort != 445 {
		t.Fatalf("clamped: %+v", s)
	}
	s = d
	s.Transcode.Encoder = "h264_nvenc"
	s.Transcode.MaxSessions = 32
	s.Search.ReindexIntervalMinutes = 0
	s.Normalize()
	if s.Transcode.Encoder != "h264_nvenc" || s.Transcode.MaxSessions != 32 || s.Search.ReindexIntervalMinutes != 0 {
		t.Fatalf("valid values changed: %+v", s)
	}
}

func TestSettingsPersist(t *testing.T) {
	st, dir := openTest(t)
	if !reflect.DeepEqual(st.Settings(), DefaultSettings()) {
		t.Fatal("fresh store settings")
	}
	ev := &eventLog{}
	st.Subscribe(ev.add)
	s := st.Settings()
	s.Search.Watch = false
	s.Services.S3Port = 9100
	s.Transcode.Preset = "bogus"
	saved, err := st.SaveSettings(s)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Transcode.Preset != "veryfast" || st.Settings().Services.S3Port != 9100 {
		t.Fatalf("saved %+v", saved)
	}
	if got := ev.take(); !reflect.DeepEqual(got, []Event{EventSettingsChanged}) {
		t.Fatalf("events %v", got)
	}
	st.Close()
	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if !reflect.DeepEqual(st2.Settings(), saved) {
		t.Fatalf("reloaded %+v, want %+v", st2.Settings(), saved)
	}
	// Stored values out of range are normalized on load.
	st2.SetKV(settingsKey, `{"transcode":{"max_sessions":999},"services":{"s3_port":1234}}`)
	st2.Close()
	st3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st3.Close()
	got := st3.Settings()
	if got.Transcode.MaxSessions != 2 || got.Services.S3Port != 1234 || got.Cache != DefaultSettings().Cache {
		t.Fatalf("partial json: %+v", got)
	}
}

func TestSettingsCorruptJSON(t *testing.T) {
	st, dir := openTest(t)
	st.SetKV(settingsKey, "{not json")
	st.Close()
	if _, err := Open(dir); err == nil {
		t.Fatal("corrupt settings should fail to open")
	}
}

func TestKV(t *testing.T) {
	st, _ := openTest(t)
	if _, err := st.GetKV("k"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing kv")
	}
	st.SetKV("k", "v1")
	st.SetKV("k", "v2")
	if v, err := st.GetKV("k"); err != nil || v != "v2" {
		t.Fatalf("kv %q %v", v, err)
	}
}

func TestConcurrentSettingsAndSubscribe(t *testing.T) {
	st, _ := openTest(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s := st.Settings()
			s.Transcode.MaxSessions = i + 1
			if _, err := st.SaveSettings(s); err != nil {
				t.Error(err)
			}
		}(i)
		go func() {
			defer wg.Done()
			st.Subscribe(func(Event) {})
			_ = st.Settings()
		}()
	}
	wg.Wait()
	// In-memory and persisted settings agree after racing saves.
	v, _ := st.GetKV(settingsKey)
	mem := st.Settings()
	if !strings.Contains(v, `"max_sessions":`+itoa(mem.Transcode.MaxSessions)+`,`) {
		t.Fatalf("persisted %s vs memory %d", v, mem.Transcode.MaxSessions)
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
