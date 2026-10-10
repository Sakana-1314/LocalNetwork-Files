package smb

import (
	"os"
	osuser "os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/config"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

// fakeSmbd behaves like Samba's smbd closely enough for process management:
// --version/--help, logs its arguments, records SIGHUP and exits on demand.
const fakeSmbd = `#!/bin/sh
dir=$(dirname "$0")
case "$1" in
--version) echo "Version 4.99.0-fake"; exit 0;;
--help) echo "  --debug-stdout"; exit 0;;
esac
echo "$$ $*" >> "$dir/starts"
if [ -f "$dir/fail" ]; then echo "bind failed on port"; exit 1; fi
trap 'echo hup >> "$dir/hups"' HUP
trap 'exit 0' TERM
while [ ! -f "$dir/crash" ]; do sleep 0.05; done
echo "crashed: fatal error"
exit 3
`

const fakeSmbpasswd = `#!/bin/sh
dir=$(dirname "$0")
echo "$*" >> "$dir/smbpasswd.args"
cat >> "$dir/smbpasswd.stdin"
`

type env struct {
	t     *testing.T
	bin   string
	mu    sync.Mutex
	st    store.ServiceSettings
	users []store.User
	m     *Manager
}

func newEnv(t *testing.T) *env {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts required")
	}
	e := &env{t: t, bin: t.TempDir()}
	for name, body := range map[string]string{"smbd": fakeSmbd, "smbpasswd": fakeSmbpasswd} {
		if err := os.WriteFile(filepath.Join(e.bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.st = store.DefaultSettings().Services
	e.st.SMBEnabled = true
	e.st.SMBPort = 4455
	cfg := config.SMBConfig{Smbd: filepath.Join(e.bin, "smbd"), Smbpasswd: filepath.Join(e.bin, "smbpasswd")}
	e.m = newManager(cfg, t.TempDir(),
		func() store.ServiceSettings { e.mu.Lock(); defer e.mu.Unlock(); return e.st },
		func() ([]store.User, error) { e.mu.Lock(); defer e.mu.Unlock(); return e.users, nil })
	t.Cleanup(e.m.Close)
	return e
}

func (e *env) set(fn func(*store.ServiceSettings)) {
	e.mu.Lock()
	fn(&e.st)
	e.mu.Unlock()
}

func (e *env) lines(name string) []string {
	b, _ := os.ReadFile(filepath.Join(e.bin, name))
	s := strings.TrimSpace(string(b))
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (e *env) touch(name string) {
	if err := os.WriteFile(filepath.Join(e.bin, name), nil, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLifecycle(t *testing.T) {
	e := newEnv(t)
	e.users = []store.User{user(1, "alice", mount("Movies", "/srv/movies", true))}
	e.m.Apply()
	running, port, err := e.m.Status()
	if !running || port != 4455 || err != nil {
		t.Fatalf("status = %v %v %v", running, port, err)
	}
	eventually(t, "smbd start", func() bool { return len(e.lines("starts")) == 1 })
	if args := e.lines("starts")[0]; !strings.Contains(args, "--foreground") || !strings.Contains(args, "--configfile="+e.m.confPath()) {
		t.Errorf("smbd args = %q", args)
	}
	conf, err := os.ReadFile(e.m.confPath())
	if err != nil || !strings.Contains(string(conf), "[Movies]") {
		t.Fatalf("config: %v\n%s", err, conf)
	}
	if got := e.m.SharesFor(1); got["Movies"] != "Movies" {
		t.Errorf("SharesFor = %v", got)
	}

	// Same port: configuration is reloaded with SIGHUP, no restart.
	e.users = append(e.users, user(2, "bob", mount("Docs", "/srv/docs", false)))
	e.m.Apply()
	eventually(t, "SIGHUP", func() bool { return len(e.lines("hups")) >= 1 })
	conf, _ = os.ReadFile(e.m.confPath())
	if !strings.Contains(string(conf), "[Docs]") {
		t.Errorf("config not regenerated")
	}
	if n := len(e.lines("starts")); n != 1 {
		t.Errorf("smbd restarted on reload (%d starts)", n)
	}

	// Port change: restart.
	e.set(func(s *store.ServiceSettings) { s.SMBPort = 4456 })
	e.m.Apply()
	eventually(t, "restart", func() bool { return len(e.lines("starts")) == 2 })
	if _, port, err := e.m.Status(); port != 4456 || err != nil {
		t.Errorf("after restart port=%d err=%v", port, err)
	}

	// Disable: stopped without an error.
	e.set(func(s *store.ServiceSettings) { s.SMBEnabled = false })
	e.m.Apply()
	if running, _, err := e.m.Status(); running || err != nil {
		t.Errorf("after disable running=%v err=%v", running, err)
	}
}

func TestExitReported(t *testing.T) {
	e := newEnv(t)
	e.touch("fail")
	e.m.Apply()
	eventually(t, "exit", func() bool { r, _, _ := e.m.Status(); return !r })
	_, _, err := e.m.Status()
	if err == nil || !strings.Contains(err.Error(), "bind failed on port") {
		t.Fatalf("err = %v", err)
	}

	// After a crash the next Apply starts smbd again.
	os.Remove(filepath.Join(e.bin, "fail"))
	e.m.Apply()
	if r, _, err := e.m.Status(); !r || err != nil {
		t.Fatalf("restart: running=%v err=%v", r, err)
	}
	e.touch("crash")
	eventually(t, "crash", func() bool { r, _, _ := e.m.Status(); return !r })
	if _, _, err := e.m.Status(); err == nil || !strings.Contains(err.Error(), "crashed: fatal error") {
		t.Errorf("crash err = %v", err)
	}
}

func TestConcurrentApply(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				e.set(func(s *store.ServiceSettings) { s.SMBPort = 5000 + i })
			}
			e.m.Apply()
			e.m.Status()
			e.m.SharesFor(1)
		}(i)
	}
	wg.Wait()
	e.m.Close()
	// Every started smbd must have been stopped: no orphan keeps running.
	for _, l := range e.lines("starts") {
		pid, _ := strconv.Atoi(strings.Fields(l)[0])
		if p, err := os.FindProcess(pid); err == nil && p.Signal(syscall.Signal(0)) == nil {
			t.Errorf("smbd %d still running", pid)
		}
	}
	if r, _, _ := e.m.Status(); r {
		t.Errorf("still running after Close")
	}
}

func TestMissingSamba(t *testing.T) {
	e := newEnv(t)
	e.m.cfg.Smbd = filepath.Join(e.bin, "nope")
	e.m.Apply()
	r, _, err := e.m.Status()
	if r || err == nil || !strings.Contains(err.Error(), "Samba") {
		t.Fatalf("running=%v err=%v", r, err)
	}
	// Not Samba (e.g. macOS's smbd).
	notSamba := filepath.Join(e.bin, "fake")
	os.WriteFile(notSamba, []byte("#!/bin/sh\necho 'unrecognized option'\nexit 1\n"), 0o755)
	e.m.cfg.Smbd = notSamba
	e.m.Apply()
	if _, _, err := e.m.Status(); err == nil || !strings.Contains(err.Error(), "Samba") {
		t.Fatalf("err = %v", err)
	}
	e.set(func(s *store.ServiceSettings) { s.SMBEnabled = false })
	e.m.Apply()
	if _, _, err := e.m.Status(); err != nil {
		t.Errorf("disabled still reports %v", err)
	}
}

func TestSyncPassword(t *testing.T) {
	e := newEnv(t)
	cur, err := currentAccount()
	if err != nil {
		t.Skip(err)
	}
	u := &store.User{ID: 1, Username: cur}
	e.users = []store.User{*u}
	e.m.SyncPassword(u, "pw1") // config not generated yet -> skipped
	if len(e.lines("smbpasswd.args")) != 0 {
		t.Fatalf("synced before config existed")
	}
	e.m.Apply()
	e.m.SyncPassword(u, "pw1")
	e.m.SyncPassword(u, "pw1") // unchanged -> no second call
	e.m.SyncPassword(u, "bad\npw")
	args := e.lines("smbpasswd.args")
	if len(args) != 1 || args[0] != "-c "+e.m.confPath()+" -s -a "+cur {
		t.Fatalf("smbpasswd args = %q", args)
	}
	if in := e.lines("smbpasswd.stdin"); len(in) != 2 || in[0] != "pw1" || in[1] != "pw1" {
		t.Errorf("stdin = %q", in)
	}
	e.m.SyncPassword(u, "pw2")
	if len(e.lines("smbpasswd.args")) != 2 {
		t.Errorf("changed password not synced")
	}

	// Disabled service: nothing happens.
	e.set(func(s *store.ServiceSettings) { s.SMBEnabled = false })
	e.m.SyncPassword(u, "pw3")
	if len(e.lines("smbpasswd.args")) != 2 {
		t.Errorf("synced while disabled")
	}
}

func currentAccount() (string, error) {
	u, err := osuser.Current()
	if err != nil {
		return "", err
	}
	if !validAccount.MatchString(u.Username) {
		return "", os.ErrInvalid
	}
	return u.Username, nil
}

func TestRingBuffer(t *testing.T) {
	r := &ringBuffer{max: 8}
	if r.LastLine() != "" {
		t.Errorf("empty last line")
	}
	r.Write([]byte("hello\nworld 1234\n"))
	if r.String() != "ld 1234\n" || r.LastLine() != "ld 1234" {
		t.Errorf("ring = %q", r.String())
	}
}
