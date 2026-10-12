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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/config"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

// Manager owns the smbd child process and its generated configuration.
type Manager struct {
	cfg config.SMBConfig
	dir string
	// settings and listUsers are injected (replaceable in tests).
	settings  func() config.ServiceSettings
	listUsers func() ([]store.User, error)

	opMu sync.Mutex // serializes Apply/Close (process start/stop)
	// syncMu serializes SyncPassword (useradd/smbpasswd).
	syncMu sync.Mutex

	mu       sync.Mutex // guards the fields below
	cmd      *exec.Cmd
	done     chan struct{}
	port     int
	lastErr  error
	stopping *exec.Cmd // process being stopped on purpose (exit is not an error)
	output   *ringBuffer
	synced   map[string][32]byte
	shares   map[uint]map[string]string // user id -> mount name -> share name
	excluded map[string]string          // account -> reason it is not usable
}

// New builds the SMB manager. svc holds the service options from the
// configuration file; the getter keeps them replaceable in tests.
func New(cfg config.SMBConfig, svc config.ServiceSettings, dataDir string, st *store.Store) *Manager {
	return newManager(cfg, dataDir, func() config.ServiceSettings { return svc }, st.ListUsers)
}

func newManager(cfg config.SMBConfig, dataDir string, settings func() config.ServiceSettings, listUsers func() ([]store.User, error)) *Manager {
	return &Manager{cfg: cfg, dir: filepath.Join(dataDir, "samba"), settings: settings, listUsers: listUsers,
		synced: map[string][32]byte{}, shares: map[uint]map[string]string{}, excluded: map[string]string{},
		output: &ringBuffer{max: 4096}}
}

func (m *Manager) confPath() string { return filepath.Join(m.dir, "smb.conf") }

// Account returns the Samba account name for a user.
func (m *Manager) Account(username string) string {
	return accountName(m.cfg.UserPrefix, username)
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
		if runtime.GOOS == "windows" {
			return errors.New("Windows 不支持托管 Samba，请使用系统自带的文件共享或 WebDAV/S3")
		}
		return fmt.Errorf("未找到 Samba (%s)，请先安装 samba 软件包", m.cfg.Smbd)
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
	if _, err := exec.LookPath(m.cfg.Smbpasswd); err != nil {
		return fmt.Errorf("未找到 %s（Debian/Ubuntu 位于 samba-common-bin 软件包）", m.cfg.Smbpasswd)
	}
	return nil
}

// Apply regenerates the configuration and starts/reloads/stops smbd.
func (m *Manager) Apply() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	st := m.settings()
	if !st.SMBEnabled {
		m.stop()
		m.setErr(nil)
		return
	}
	if err := m.checkSamba(); err != nil {
		m.stop()
		m.setErr(err)
		return
	}
	if err := m.writeConfig(st); err != nil {
		slog.Error("smb: write config", "err", err)
		m.setErr(err)
		return
	}
	m.mu.Lock()
	cmd, port := m.cmd, m.port
	m.mu.Unlock()
	if cmd != nil && port == st.SMBPort {
		// smbd re-reads its configuration on SIGHUP.
		if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
			slog.Warn("smb: reload smbd", "err", err)
		}
		return
	}
	m.stop()
	m.start(st.SMBPort)
}

func (m *Manager) setErr(err error) {
	m.mu.Lock()
	m.lastErr = err
	m.mu.Unlock()
}

// start launches smbd. Caller holds opMu.
func (m *Manager) start(port int) {
	args := []string{"--foreground", "--no-process-group", "--debug-stdout", "--configfile=" + m.confPath()}
	if out, _ := exec.Command(m.cfg.Smbd, "--help").CombinedOutput(); !bytes.Contains(out, []byte("debug-stdout")) {
		// Samba < 4.15 spells "log to stdout" as -S.
		args = []string{"-F", "--no-process-group", "-S", "--configfile=" + m.confPath()}
	}
	cmd := exec.Command(m.cfg.Smbd, args...)
	cmd.SysProcAttr = sysProcAttr()
	m.output.Reset()
	cmd.Stdout, cmd.Stderr = m.output, m.output
	if err := cmd.Start(); err != nil {
		m.setErr(fmt.Errorf("启动 smbd 失败: %w", err))
		return
	}
	done := make(chan struct{})
	m.mu.Lock()
	m.cmd, m.done, m.port, m.lastErr = cmd, done, port, nil
	m.mu.Unlock()
	slog.Info("smb: smbd started", "pid", cmd.Process.Pid, "port", port)
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		if m.cmd == cmd {
			m.cmd = nil
		}
		if m.stopping != cmd {
			m.lastErr = fmt.Errorf("smbd 已退出 (%v): %s", err, m.output.LastLine())
			slog.Error("smb: smbd exited", "err", err, "output", m.output.String())
		}
		m.mu.Unlock()
		close(done)
	}()
}

// stop terminates smbd (SIGTERM, then SIGKILL after 5s). Caller holds opMu.
func (m *Manager) stop() {
	m.mu.Lock()
	cmd, done := m.cmd, m.done
	if cmd == nil {
		m.mu.Unlock()
		return
	}
	m.stopping = cmd
	m.mu.Unlock()
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		<-done
	}
	m.mu.Lock()
	m.stopping = nil
	m.mu.Unlock()
	slog.Info("smb: smbd stopped")
}

// Close stops smbd.
func (m *Manager) Close() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.stop()
}

func (m *Manager) writeConfig(st config.ServiceSettings) error {
	users, err := m.listUsers()
	if err != nil {
		return err
	}
	res := buildConfig(users, confParams{Dir: m.dir, Port: st.SMBPort, Workgroup: st.SMBWorkgroup,
		UserPrefix: m.cfg.UserPrefix, ForceUser: m.cfg.ForceUser})
	for acct, why := range res.Excluded {
		slog.Warn("smb: account excluded", "account", acct, "reason", why)
	}
	for _, s := range res.Skipped {
		slog.Warn("smb: mount skipped", "mount", s)
	}
	for _, d := range []string{"private", "lock", "state", "cache", "run", "ncalrpc"} {
		if err := os.MkdirAll(filepath.Join(m.dir, d), 0o700); err != nil {
			return err
		}
	}
	tmp := m.confPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(res.Conf), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.confPath()); err != nil {
		return err
	}
	m.mu.Lock()
	m.shares, m.excluded = res.Shares, res.Excluded
	m.mu.Unlock()
	return nil
}

// SyncPassword stores the user's password in Samba's database (when SMB is enabled).
func (m *Manager) SyncPassword(u *store.User, password string) {
	if !m.settings().SMBEnabled {
		return
	}
	acct := m.Account(u.Username)
	sum := sha256.Sum256([]byte(password))
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	m.mu.Lock()
	done := m.synced[acct] == sum
	why, excluded := m.excluded[acct]
	m.mu.Unlock()
	switch {
	case done:
		return
	case excluded:
		slog.Warn("smb: password not synced", "account", acct, "reason", why)
		return
	case !validAccount.MatchString(acct):
		slog.Warn("smb: username not usable as a system account", "account", acct)
		return
	case strings.ContainsAny(password, "\r\n"):
		slog.Warn("smb: password contains a line break, not synced", "account", acct)
		return
	}
	if _, err := os.Stat(m.confPath()); err != nil {
		slog.Warn("smb: config not generated yet, password not synced", "account", acct)
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
	if out, err := exec.Command("id", "-u", acct).Output(); err == nil {
		// When accounts are managed automatically, never adopt an existing
		// system account (daemon, www-data, ...): its files and privileges
		// would become reachable over SMB.
		if uid, _ := strconv.Atoi(strings.TrimSpace(string(out))); m.cfg.CreateSystemUsers && uid < 1000 {
			return fmt.Errorf("%s 是已存在的系统账户 (uid %d)，请设置 smb.user_prefix 避免冲突", acct, uid)
		}
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
