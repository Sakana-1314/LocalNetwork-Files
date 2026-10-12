package smb

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Sakana-1314/MList/server/internal/store"
)

func params() confParams {
	return confParams{Dir: "/var/lib/lnf/samba", Port: 445, Workgroup: "WORKGROUP"}
}

// section returns the body of share [name] (without the header), or "".
func section(conf, name string) string {
	start := strings.Index(conf, "\n["+name+"]\n")
	if start < 0 {
		return ""
	}
	body := conf[start+len(name)+3:]
	if end := strings.Index(body, "\n["); end >= 0 {
		body = body[:end]
	}
	return body
}

func sectionNames(conf string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^\[([^\]\n]*)\]$`).FindAllStringSubmatch(conf, -1) {
		out = append(out, m[1])
	}
	return out
}

func directive(body, key string) string {
	for _, l := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func user(id uint, name string, mounts ...store.Mount) store.User {
	return store.User{ID: id, Username: name, Mounts: mounts}
}

func mount(name, path string, writable bool) store.Mount {
	return store.Mount{Name: name, Path: path, Writable: writable}
}

func TestGroupingAndLists(t *testing.T) {
	users := []store.User{
		user(2, "bob", mount("Movies", "/srv/movies", false), mount("Docs", "/srv/docs", true)),
		user(1, "Alice", mount("Movies", "/srv/movies", true)),
		user(3, "carol", mount("movies", "/srv/movies", false)), // same share, different case
	}
	res := buildConfig(users, params())
	if got := sectionNames(res.Conf); strings.Join(got, ",") != "global,Docs,Movies" {
		t.Fatalf("sections = %v\n%s", got, res.Conf)
	}
	mv := section(res.Conf, "Movies")
	if v := directive(mv, "path"); v != "/srv/movies" {
		t.Errorf("path = %q", v)
	}
	if v := directive(mv, "valid users"); v != "bob carol alice" {
		t.Errorf("valid users = %q", v)
	}
	if v := directive(mv, "write list"); v != "alice" {
		t.Errorf("write list = %q", v)
	}
	if v := directive(mv, "read only"); v != "yes" {
		t.Errorf("read only = %q", v)
	}
	docs := section(res.Conf, "Docs")
	if v := directive(docs, "write list"); v != "bob" {
		t.Errorf("docs write list = %q", v)
	}
	if res.Shares[3]["movies"] != "Movies" || res.Shares[1]["Movies"] != "Movies" || res.Shares[2]["Docs"] != "Docs" {
		t.Errorf("shares = %v", res.Shares)
	}
	// Read-only shares must not get a write list.
	ro := buildConfig([]store.User{user(1, "dan", mount("Pics", "/p", false))}, params())
	if strings.Contains(section(ro.Conf, "Pics"), "write list") {
		t.Errorf("read-only share has a write list:\n%s", ro.Conf)
	}
}

func TestDisabledUsersExcluded(t *testing.T) {
	off := user(2, "eve", mount("Secret", "/srv/secret", true), mount("Movies", "/srv/movies", true))
	off.Disabled = true
	res := buildConfig([]store.User{user(1, "alice", mount("Movies", "/srv/movies", false)), off}, params())
	if strings.Contains(res.Conf, " eve") || strings.Contains(res.Conf, "Secret") {
		t.Fatalf("disabled user leaked into config:\n%s", res.Conf)
	}
	if directive(section(res.Conf, "Movies"), "write list") != "" {
		t.Errorf("disabled user's write access kept")
	}
	if _, ok := res.Shares[2]; ok {
		t.Errorf("disabled user has shares")
	}
}

func TestSanitizeNames(t *testing.T) {
	users := []store.User{user(1, "alice",
		mount("a[b]:c*?", "/x/1", false),
		mount("evil]\n[global]\n  path = /", "/x/2", true),
		mount("100%U", "/x/3", false),
		mount("global", "/x/4", false),
		mount("IPC$", "/x/5", false),
		mount("中文 目录", "/x/6", false),
	)}
	res := buildConfig(users, params())
	names := sectionNames(res.Conf)
	if names[0] != "global" {
		t.Fatalf("first section %q", names[0])
	}
	for _, n := range names[1:] {
		if strings.EqualFold(n, "global") || strings.ContainsAny(n, "[]\n:*?%") {
			t.Errorf("unsafe share name %q", n)
		}
	}
	if strings.Count(res.Conf, "[global]") != 1 {
		t.Errorf("config injection:\n%s", res.Conf)
	}
	want := map[string]string{
		"a[b]:c*?": "a_b__c__", "100%U": "100_U", "global": "global_", "IPC$": "IPC$_", "中文 目录": "中文 目录",
	}
	for mt, sh := range want {
		if got := res.Shares[1][mt]; got != sh {
			t.Errorf("share for %q = %q, want %q", mt, got, sh)
		}
	}
	if directive(section(res.Conf, "中文 目录"), "path") != "/x/6" {
		t.Errorf("unicode share missing:\n%s", res.Conf)
	}
	long := buildConfig([]store.User{user(1, "a", mount(strings.Repeat("长", 200), "/l", false))}, params())
	if n := len([]rune(long.Shares[1][strings.Repeat("长", 200)])); n != maxShareName {
		t.Errorf("long name length %d", n)
	}
}

func TestPaths(t *testing.T) {
	users := []store.User{user(1, "alice",
		mount("Spaces", "/srv/my movies/2024 新", false),
		mount("NL", "/srv/a\n[x]\n  path = /etc", true),
		mount("CR", "/srv/a\rb", true),
		mount("Pct", "/srv/%U", true),
		mount("Rel", "srv/rel", true),
		mount("Trail", "/srv/trail ", true),
		mount("Clean", "/srv/x/../y/", false),
	)}
	res := buildConfig(users, params())
	if v := directive(section(res.Conf, "Spaces"), "path"); v != "/srv/my movies/2024 新" {
		t.Errorf("path with spaces = %q", v)
	}
	if v := directive(section(res.Conf, "Clean"), "path"); v != "/srv/y" {
		t.Errorf("cleaned path = %q", v)
	}
	for _, n := range []string{"NL", "CR", "Pct", "Rel", "Trail"} {
		if section(res.Conf, n) != "" {
			t.Errorf("unsafe mount %s included", n)
		}
		if _, ok := res.Shares[1][n]; ok {
			t.Errorf("unsafe mount %s reported as share", n)
		}
	}
	if len(res.Skipped) != 5 || strings.Contains(res.Conf, "/etc") {
		t.Errorf("skipped = %v\n%s", res.Skipped, res.Conf)
	}
}

func TestForceUserAndGlobals(t *testing.T) {
	p := params()
	p.ForceUser = "mlist\n[evil]"
	p.Workgroup = "HOME\nsecurity = share"
	p.Port = 4455
	res := buildConfig([]store.User{user(1, "alice", mount("A", "/a", true))}, p)
	if v := directive(section(res.Conf, "A"), "force user"); v != "mlist[evil]" {
		t.Errorf("force user = %q", v)
	}
	if strings.Count(res.Conf, "\n  security =") != 1 || !strings.Contains(res.Conf, "workgroup = HOMEsecurity = share") {
		t.Errorf("workgroup not sanitized:\n%s", res.Conf)
	}
	if !strings.Contains(res.Conf, "smb ports = 4455\n") {
		t.Errorf("port missing")
	}
	if !strings.Contains(res.Conf, "passdb backend = tdbsam:/var/lib/lnf/samba/private/passdb.tdb") {
		t.Errorf("passdb not in data dir:\n%s", res.Conf)
	}
	none := buildConfig([]store.User{user(1, "alice", mount("A", "/a", true))}, params())
	if strings.Contains(none.Conf, "force user") {
		t.Errorf("force user set without config")
	}
	empty := buildConfig(nil, confParams{Dir: "/d", Port: 445})
	if !strings.Contains(empty.Conf, "workgroup = WORKGROUP") || len(sectionNames(empty.Conf)) != 1 {
		t.Errorf("empty config:\n%s", empty.Conf)
	}
}

func TestNameCollisions(t *testing.T) {
	users := []store.User{
		user(1, "alice", mount("Movies", "/a/movies", true), mount("Movies-bob", "/z", false)),
		user(2, "bob", mount("movies", "/b/movies", true)),
		user(3, "carol", mount("Movies", "/a/movies", false)),
		user(4, "dave", mount("Photos", "/p1", false)),
		user(5, "erin", mount("Photos", "/p1", true)),
	}
	res := buildConfig(users, params())
	got := map[string]string{
		"alice": res.Shares[1]["Movies"], "alice2": res.Shares[1]["Movies-bob"], "bob": res.Shares[2]["movies"],
		"carol": res.Shares[3]["Movies"], "dave": res.Shares[4]["Photos"], "erin": res.Shares[5]["Photos"],
	}
	want := map[string]string{
		"alice": "Movies-alice", "alice2": "Movies-bob", "bob": "movies-bob-2",
		"carol": "Movies-carol", "dave": "Photos", "erin": "Photos",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: share %q, want %q", k, got[k], v)
		}
	}
	// Every section name is unique case-insensitively and maps to a single path.
	seen := map[string]bool{}
	for _, n := range sectionNames(res.Conf) {
		if seen[strings.ToLower(n)] {
			t.Errorf("duplicate section %q\n%s", n, res.Conf)
		}
		seen[strings.ToLower(n)] = true
	}
	if directive(section(res.Conf, "movies-bob-2"), "path") != "/b/movies" ||
		directive(section(res.Conf, "Movies-bob"), "path") != "/z" {
		t.Errorf("collision paths wrong:\n%s", res.Conf)
	}
	ph := section(res.Conf, "Photos")
	if directive(ph, "valid users") != "dave erin" || directive(ph, "write list") != "erin" {
		t.Errorf("shared share lists wrong:\n%s", ph)
	}
}

func TestAccounts(t *testing.T) {
	users := []store.User{
		user(1, "Bob", mount("A", "/a", true)),
		user(2, "bob", mount("B", "/b", true)),
		user(3, "9lives", mount("C", "/c", true)),
		user(4, "ok.user", mount("D", "/d", false)),
		user(5, "root", mount("E", "/e", true)),
	}
	res := buildConfig(users, params())
	if _, ok := res.Excluded["bob"]; !ok {
		t.Errorf("case-colliding accounts not excluded: %v", res.Excluded)
	}
	if _, ok := res.Excluded["9lives"]; !ok {
		t.Errorf("invalid account not excluded: %v", res.Excluded)
	}
	if _, ok := res.Excluded["root"]; !ok {
		t.Errorf("root not excluded: %v", res.Excluded)
	}
	if names := sectionNames(res.Conf); strings.Join(names, ",") != "global,D" {
		t.Errorf("sections = %v", names)
	}
	// A prefix can make an otherwise invalid username usable.
	p := params()
	p.UserPrefix = "ml_"
	res = buildConfig(users[2:3], p)
	if directive(section(res.Conf, "C"), "valid users") != "ml_9lives" {
		t.Errorf("prefixed account missing:\n%s", res.Conf)
	}
}

func TestDeterministic(t *testing.T) {
	users := []store.User{
		user(2, "bob", mount("X", "/2", true)),
		user(1, "alice", mount("X", "/1", true), mount("Y", "/y", false)),
	}
	a := buildConfig(users, params()).Conf
	b := buildConfig([]store.User{users[1], users[0]}, params()).Conf
	if a != b {
		t.Errorf("output depends on input order")
	}
}
