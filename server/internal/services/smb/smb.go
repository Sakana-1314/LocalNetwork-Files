// Package smb provides SMB shares by managing a Samba (smbd) child process.
//
// The pure-Go SMB server implementations available cannot enforce per-user
// share permissions, so the server generates a dedicated smb.conf from the
// users' authorized directories (one share per directory, with read/write
// lists) and keeps Samba's password database in sync. Samba needs plaintext
// passwords to derive its NT hashes, so a user's SMB password is set the next
// time they log in (or when their password changes) after SMB is enabled.
package smb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/config"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

type Manager struct {
	cfg   config.SMBConfig
	dir   string
	store *store.Store

	mu       sync.Mutex
	cmd      *exec.Cmd
	done     chan struct{}
	port     int
	lastErr  error
	output   *ringBuffer
	synced   map[string][32]byte
	shares   map[uint]map[string]string // user id -> mount name -> share name
	stopping bool
}

func New(cfg config.SMBConfig, dataDir string, st *store.Store) *Manager {
	return &Manager{cfg: cfg, dir: filepath.Join(dataDir, "samba"), store: st, synced: map[string][32]byte{},
		shares: map[uint]map[string]string{}, output: &ringBuffer{max: 4096}}
}

func (m *Manager) confPath() string { return filepath.Join(m.dir, "smb.conf") }

// Account returns the Samba account name for a user.
func (m *Manager) Account(username string) string {
	return strings.ToLower(m.cfg.UserPrefix + username)
}

// Status reports the smbd process state.
func (m *Manager) Status() (running bool, port int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil, m.port, m.lastErr
}

// SharesFor maps a user's mount names to SMB share names.
func (m *Manager) SharesFor(userID uint) map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for k, v := range m.shares[userID] {
		out[k] = v
	}
	return out
}

func (m *Manager) checkSamba() error {
	if _, err := exec.LookPath(m.cfg.Smbd); err != nil {
		return fmt.Errorf("未找到 Samba (smbd)，请先安装 samba 软件包")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.cfg.Smbd, "--version").CombinedOutput()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "Version") {
		if runtime.GOOS == "darwin" {
			return errors.New("macOS 自带的 smbd 不是 Samba，无法托管；请在 Linux 上运行，或安装 Samba 并在配置文件中指定 smb.smbd 路径")
		}
		return fmt.Errorf("%s 不是可用的 Samba smbd", m.cfg.Smbd)
	}
	return nil
}

// Apply regenerates the configuration and starts/reloads/stops smbd.
func (m *Manager) Apply() {
	st := m.store.Settings().Services
	m.mu.Lock()
	defer m.mu.Unlock()
	if !st.SMBEnabled {
		m.stopLocked()
		m.lastErr = nil
		return
	}
	if err := m.checkSamba(); err != nil {
		m.stopLocked()
		m.lastErr = err
		return
	}
	if err := m.writeConfig(st); err != nil {
		m.lastErr = err
		slog.Error("smb: write config", "err", err)
		return
	}
	if m.cmd != nil && m.port == st.SMBPort {
		// smbd re-reads its configuration on SIGHUP.
		m.cmd.Process.Signal(syscall.SIGHUP)
		return
	}
	m.stopLocked()
	m.startLocked(st.SMBPort)
}

func (m *Manager) startLocked(port int) {
	args := []string{"--foreground", "--no-process-group", "--debug-stdout", "--configfile=" + m.confPath()}
	if out, _ := exec.Command(m.cfg.Smbd, "--help").CombinedOutput(); !bytes.Contains(out, []byte("debug-stdout")) {
		args = []string{"-F", "--no-process-group", "-S", "--configfile=" + m.confPath()}
	}
	cmd := exec.Command(m.cfg.Smbd, args...)
	m.output.Reset()
	cmd.Stdout, cmd.Stderr = m.output, m.output
	if err := cmd.Start(); err != nil {
		m.lastErr = err
		return
	}
	m.cmd, m.port, m.lastErr = cmd, port, nil
	done := make(chan struct{})
	m.done = done
	slog.Info("smb: smbd started", "pid", cmd.Process.Pid, "port", port)
	go func() {
		err := cmd.Wait()
		close(done)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.cmd == cmd {
			m.cmd = nil
			if !m.stopping {
				m.lastErr = fmt.Errorf("smbd 已退出 (%v): %s", err, m.output.LastLine())
				slog.Error("smb: smbd exited", "err", err, "output", m.output.String())
			}
		}
	}()
}

func (m *Manager) stopLocked() {
	if m.cmd == nil {
		return
	}
	m.stopping = true
	cmd, done := m.cmd, m.done
	cmd.Process.Signal(syscall.SIGTERM)
	m.mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-done
	}
	m.mu.Lock()
	m.cmd = nil
	m.stopping = false
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
}

var unsafeShareChars = regexp.MustCompile(`[\[\]\x00-\x1f\\/:*?"<>|;=,+]`)

type share struct {
	name  string
	path  string
	read  []string
	write []string
}

func (m *Manager) writeConfig(st store.ServiceSettings) error {
	users, err := m.store.ListUsers()
	if err != nil {
		return err
	}
	// Group mounts by (name, path); disambiguate names that map to different paths.
	type key struct{ name, path string }
	shares := map[key]*share{}
	pathsByName := map[string]map[string]bool{}
	for _, u := range users {
		if u.Disabled {
			continue
		}
		for _, mt := range u.Mounts {
			n := unsafeShareChars.ReplaceAllString(mt.Name, "_")
			if pathsByName[strings.ToLower(n)] == nil {
				pathsByName[strings.ToLower(n)] = map[string]bool{}
			}
			pathsByName[strings.ToLower(n)][mt.Path] = true
		}
	}
	userShares := map[uint]map[string]string{}
	for _, u := range users {
		if u.Disabled {
			continue
		}
		acct := m.Account(u.Username)
		userShares[u.ID] = map[string]string{}
		for _, mt := range u.Mounts {
			n := unsafeShareChars.ReplaceAllString(mt.Name, "_")
			if len(pathsByName[strings.ToLower(n)]) > 1 {
				n = n + "-" + u.Username
			}
			k := key{n, mt.Path}
			sh := shares[k]
			if sh == nil {
				sh = &share{name: n, path: mt.Path}
				shares[k] = sh
			}
			if mt.Writable {
				sh.write = append(sh.write, acct)
			} else {
				sh.read = append(sh.read, acct)
			}
			userShares[u.ID][mt.Name] = n
		}
	}
	m.shares = userShares

	for _, d := range []string{"private", "lock", "state", "cache", "run", "ncalrpc"} {
		if err := os.MkdirAll(filepath.Join(m.dir, d), 0o700); err != nil {
			return err
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `# Generated by LocalNetwork Files. Do not edit; changes are overwritten.
[global]
  workgroup = %s
  server string = LocalNetwork Files
  netbios name = LNFILES
  smb ports = %d
  security = user
  map to guest = never
  restrict anonymous = 2
  server min protocol = SMB2_10
  passdb backend = tdbsam:%s
  private dir = %s
  lock directory = %s
  state directory = %s
  cache directory = %s
  pid directory = %s
  ncalrpc dir = %s
  log level = 1
  load printers = no
  printing = bsd
  printcap name = /dev/null
  disable spoolss = yes
  disable netbios = yes
  unix extensions = no
  wide links = no
  follow symlinks = yes
  vfs objects = catia fruit streams_xattr
  fruit:metadata = stream
  fruit:posix_rename = yes
  veto files = /.lnf-upload-*/
`, st.SMBWorkgroup, st.SMBPort, filepath.Join(m.dir, "private", "passdb.tdb"),
		filepath.Join(m.dir, "private"), filepath.Join(m.dir, "lock"), filepath.Join(m.dir, "state"),
		filepath.Join(m.dir, "cache"), filepath.Join(m.dir, "run"), filepath.Join(m.dir, "ncalrpc"))

	list := make([]*share, 0, len(shares))
	for _, sh := range shares {
		list = append(list, sh)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
	for _, sh := range list {
		all := append(append([]string{}, sh.read...), sh.write...)
		fmt.Fprintf(&b, "\n[%s]\n  path = %s\n  browseable = yes\n  read only = yes\n  valid users = %s\n",
			sh.name, sh.path, strings.Join(all, " "))
		if len(sh.write) > 0 {
			fmt.Fprintf(&b, "  write list = %s\n", strings.Join(sh.write, " "))
		}
		if m.cfg.ForceUser != "" {
			fmt.Fprintf(&b, "  force user = %s\n", m.cfg.ForceUser)
		}
		b.WriteString("  create mask = 0644\n  directory mask = 0755\n")
	}
	return os.WriteFile(m.confPath(), []byte(b.String()), 0o600)
}

var validAccount = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`)

// SyncPassword stores the user's password in Samba's database (when SMB is enabled).
func (m *Manager) SyncPassword(u *store.User, password string) {
	if !m.store.Settings().Services.SMBEnabled {
		return
	}
	acct := m.Account(u.Username)
	sum := sha256.Sum256([]byte(password))
	m.mu.Lock()
	if m.synced[acct] == sum {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	if !validAccount.MatchString(acct) {
		slog.Warn("smb: username not usable as a system account", "account", acct)
		return
	}
	if err := m.ensureSystemUser(acct); err != nil {
		slog.Warn("smb: ensure system user", "account", acct, "err", err)
		return
	}
	cmd := exec.Command(m.cfg.Smbpasswd, "-c", m.confPath(), "-s", "-a", acct)
	cmd.Stdin = strings.NewReader(password + "\n" + password + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		slog.Warn("smb: smbpasswd failed", "account", acct, "err", err, "output", strings.TrimSpace(string(out)))
		return
	}
	m.mu.Lock()
	m.synced[acct] = sum
	m.mu.Unlock()
	slog.Info("smb: password synced", "account", acct)
}

func (m *Manager) ensureSystemUser(acct string) error {
	if err := exec.Command("id", "-u", acct).Run(); err == nil {
		return nil
	}
	if !m.cfg.CreateSystemUsers {
		return fmt.Errorf("系统用户 %s 不存在（可在配置中开启 smb.create_system_users）", acct)
	}
	shell := "/usr/sbin/nologin"
	if _, err := os.Stat(shell); err != nil {
		shell = "/sbin/nologin"
	}
	if out, err := exec.Command("useradd", "-M", "-s", shell, acct).CombinedOutput(); err != nil {
		return fmt.Errorf("useradd: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ringBuffer keeps the tail of the smbd output for error reporting.
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) Reset() {
	r.mu.Lock()
	r.buf = nil
	r.mu.Unlock()
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

func (r *ringBuffer) LastLine() string {
	lines := strings.Split(strings.TrimSpace(r.String()), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
