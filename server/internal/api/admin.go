package api

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/auth"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/cache"
	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

// ---------- system ----------

func (a *API) httpPort() int {
	_, p, err := net.SplitHostPort(a.Config.Listen)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return n
}

func lanAddresses() []string {
	var out []string
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	sort.Slice(out, func(i, j int) bool { return lanRank(out[i]) < lanRank(out[j]) })
	return out
}

// lanRank prefers typical home-network ranges.
func lanRank(ip string) int {
	switch {
	case strings.HasPrefix(ip, "192.168."):
		return 0
	case strings.HasPrefix(ip, "10."):
		return 1
	case strings.HasPrefix(ip, "172."):
		return 2
	}
	return 3
}

func (a *API) systemInfo(c *gin.Context) {
	host, _ := os.Hostname()
	addrs := lanAddresses()
	if addrs == nil {
		addrs = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"version":   a.Version,
		"hostname":  host,
		"addresses": addrs,
		"http_port": a.httpPort(),
		"ffmpeg":    a.Media.HasFFmpeg(),
		"services":  a.Services.Status(principal(c).User.ID),
	})
}

// ---------- users ----------

var validUsername = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}$`)

type userInput struct {
	Username string        `json:"username"`
	Password string        `json:"password"`
	Role     string        `json:"role"`
	Disabled bool          `json:"disabled"`
	Mounts   []store.Mount `json:"mounts"`
}

func (in *userInput) validate(creating bool) error {
	in.Username = strings.TrimSpace(in.Username)
	if !validUsername.MatchString(in.Username) {
		return badRequest("用户名只能包含字母、数字、下划线、点和横线，最长 32 位")
	}
	if creating || in.Password != "" {
		if len(in.Password) < 6 {
			return badRequest("密码至少 6 位")
		}
	}
	if in.Role == "" {
		in.Role = store.RoleUser
	}
	if in.Role != store.RoleUser && in.Role != store.RoleAdmin {
		return badRequest("角色无效")
	}
	if in.Mounts == nil {
		in.Mounts = []store.Mount{}
	}
	if err := store.ValidateMounts(in.Mounts); err != nil {
		return badRequest(err.Error())
	}
	for _, m := range in.Mounts {
		fi, err := os.Stat(m.Path)
		if err != nil || !fi.IsDir() {
			return badRequest("目录不存在: " + m.Path)
		}
	}
	return nil
}

// usernameTaken reports whether another user already has name, ignoring case:
// Samba accounts are lower-cased and Basic auth compares case-insensitively,
// so "Admin" and "admin" must not coexist.
func (a *API) usernameTaken(name string, exclude uint) (bool, error) {
	users, err := a.Store.ListUsers()
	if err != nil {
		return false, err
	}
	for _, u := range users {
		if u.ID != exclude && strings.EqualFold(u.Username, name) {
			return true, nil
		}
	}
	return false, nil
}

func (a *API) checkUsername(c *gin.Context, name string, exclude uint) bool {
	taken, err := a.usernameTaken(name, exclude)
	if err == nil && taken {
		err = store.ErrConflict
	}
	if err != nil {
		fail(c, err)
		return false
	}
	return true
}

func (a *API) adminListUsers(c *gin.Context) {
	users, err := a.Store.ListUsers()
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]store.User, 0, len(users))
	for i := range users {
		out = append(out, userView(&users[i], true))
	}
	c.JSON(http.StatusOK, out)
}

func (a *API) adminCreateUser(c *gin.Context) {
	var in userInput
	if !bind(c, &in) {
		return
	}
	if err := in.validate(true); err != nil {
		fail(c, err)
		return
	}
	if !a.checkUsername(c, in.Username, 0) {
		return
	}
	h, err := auth.HashPassword(in.Password)
	if err != nil {
		fail(c, err)
		return
	}
	u := &store.User{Username: in.Username, PasswordHash: h, Role: in.Role, Disabled: in.Disabled, Mounts: in.Mounts}
	if err := a.Store.CreateUser(u); err != nil {
		fail(c, err)
		return
	}
	if a.Auth.OnPassword != nil {
		go a.Auth.OnPassword(u, in.Password)
	}
	c.JSON(http.StatusCreated, userView(u, true))
}

func (a *API) countActiveAdmins(exclude uint) int {
	users, _ := a.Store.ListUsers()
	n := 0
	for _, u := range users {
		if u.ID != exclude && u.IsAdmin() && !u.Disabled {
			n++
		}
	}
	return n
}

func (a *API) adminUpdateUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, badRequest("id 无效"))
		return
	}
	u, err := a.Store.GetUser(uint(id))
	if err != nil {
		fail(c, err)
		return
	}
	var in userInput
	if !bind(c, &in) {
		return
	}
	if err := in.validate(false); err != nil {
		fail(c, err)
		return
	}
	if !a.checkUsername(c, in.Username, u.ID) {
		return
	}
	if u.IsAdmin() && (in.Role != store.RoleAdmin || in.Disabled) && a.countActiveAdmins(u.ID) == 0 {
		fail(c, badRequest("至少需要保留一个启用的管理员"))
		return
	}
	if in.Disabled != u.Disabled || in.Role != u.Role {
		u.TokenVersion++ // force re-login
	}
	u.Username, u.Role, u.Disabled = in.Username, in.Role, in.Disabled
	if in.Password != "" {
		if err := a.Auth.SetPassword(u, in.Password); err != nil {
			fail(c, err)
			return
		}
	}
	if err := a.Store.UpdateUser(u, in.Mounts); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, userView(u, true))
}

func (a *API) adminDeleteUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, badRequest("id 无效"))
		return
	}
	if uint(id) == principal(c).User.ID {
		fail(c, badRequest("不能删除当前登录的账户"))
		return
	}
	if err := a.Store.DeleteUser(uint(id)); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// adminBrowse lists local directories so admins can pick mount paths.
func (a *API) adminBrowse(c *gin.Context) {
	p := c.Query("path")
	if p == "" {
		if runtime.GOOS == "windows" {
			var drives []string
			for d := 'A'; d <= 'Z'; d++ {
				root := string(d) + `:\`
				if _, err := os.Stat(root); err == nil {
					drives = append(drives, root)
				}
			}
			c.JSON(http.StatusOK, gin.H{"path": "", "parent": "", "dirs": drives})
			return
		}
		p = "/"
	}
	if !filepath.IsAbs(p) {
		fail(c, badRequest("请输入绝对路径"))
		return
	}
	p = filepath.Clean(p)
	des, err := os.ReadDir(p)
	if err != nil {
		fail(c, badRequest("无法读取目录: "+err.Error()))
		return
	}
	dirs := []string{}
	for _, de := range des {
		full := filepath.Join(p, de.Name())
		if de.IsDir() || (de.Type()&os.ModeSymlink != 0 && isDir(full)) {
			dirs = append(dirs, full)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i]) < strings.ToLower(dirs[j]) })
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	c.JSON(http.StatusOK, gin.H{"path": p, "parent": parent, "dirs": dirs})
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// ---------- settings / cache / search ----------

func (a *API) adminGetSettings(c *gin.Context) { c.JSON(http.StatusOK, a.Store.Settings()) }

func (a *API) adminPutSettings(c *gin.Context) {
	st := a.Store.Settings()
	if !bind(c, &st) {
		return
	}
	saved, err := a.Store.SaveSettings(st)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, saved)
}

func (a *API) adminCacheStats(c *gin.Context) {
	stats, last := a.Cache.Stats()
	c.JSON(http.StatusOK, gin.H{
		"dir":          a.Cache.Dir(),
		"thumb":        stats[cache.Thumb],
		"transcode":    stats[cache.Transcode],
		"sessions":     a.Media.Sessions(),
		"last_cleanup": last,
	})
}

func (a *API) adminCacheClean(c *gin.Context) {
	var req struct {
		Kind        string `json:"kind"`
		ExpiredOnly *bool  `json:"expired_only"`
	}
	if c.Request.ContentLength > 0 && !bind(c, &req) {
		return
	}
	expiredOnly := req.ExpiredOnly == nil || *req.ExpiredOnly
	var buckets []cache.Bucket
	switch req.Kind {
	case "thumb":
		buckets = []cache.Bucket{cache.Thumb}
	case "transcode":
		buckets = []cache.Bucket{cache.Transcode}
	case "", "all":
	default:
		fail(c, badRequest("kind 无效"))
		return
	}
	c.JSON(http.StatusOK, a.Cache.Cleanup(buckets, expiredOnly))
}

func (a *API) adminSearchStatus(c *gin.Context) { c.JSON(http.StatusOK, a.Search.Status()) }

func (a *API) adminReindex(c *gin.Context) {
	a.Search.RebuildAsync()
	c.JSON(http.StatusAccepted, gin.H{"ok": true})
}
