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

	"github.com/Sakana-1314/MList/server/internal/auth"
	"github.com/Sakana-1314/MList/server/internal/cache"
	"github.com/Sakana-1314/MList/server/internal/store"
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

// @Summary  系统与服务信息（用于挂载向导）
// @Tags     系统
// @Produce  json
// @Success  200 {object} SystemInfo "版本、主机名、地址与各协议服务的运行状态"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Security bearerAuth
// @Router   /system/info [get]
func (a *API) systemInfo(c *gin.Context) {
	host, _ := os.Hostname()
	addrs := lanAddresses()
	if addrs == nil {
		addrs = []string{}
	}
	c.JSON(http.StatusOK, SystemInfo{
		Version: a.Version, Hostname: host, Addresses: addrs, HTTPPort: a.httpPort(),
		FFmpeg: a.Media.HasFFmpeg(), Services: a.Services.Status(principal(c).User.ID),
	})
}

// ---------- users ----------

var validUsername = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,31}$`)

func (in *UserInput) validate(creating bool) error {
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

// @Summary  用户列表
// @Tags     管理
// @Produce  json
// @Success  200 {array} store.User "全部用户及其授权目录（仅管理员可见本地路径）"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Security bearerAuth
// @Router   /admin/users [get]
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

// @Summary  创建用户
// @Tags     管理
// @Accept   json
// @Produce  json
// @Param    body body UserInput true "用户信息与授权目录"
// @Success  201 {object} store.User "用户创建成功"
// @Failure  400 {object} ErrorResponse "用户名或密码无效"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Security bearerAuth
// @Router   /admin/users [post]
func (a *API) adminCreateUser(c *gin.Context) {
	var in UserInput
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

// @Summary  更新用户（password 为空表示不修改）
// @Tags     管理
// @Accept   json
// @Produce  json
// @Param    id path integer true "用户 ID"
// @Param    body body UserInput true "用户信息与授权目录（整表替换）"
// @Success  200 {object} store.User "用户已更新；角色或状态变化会导致该用户重新登录"
// @Failure  400 {object} ErrorResponse "用户名或密码无效"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Failure  404 {object} ErrorResponse "用户不存在"
// @Security bearerAuth
// @Router   /admin/users/{id} [put]
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
	var in UserInput
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

// @Summary  删除用户
// @Tags     管理
// @Param    id path integer true "用户 ID"
// @Success  204 "用户及其授权目录、访问令牌、S3 密钥均已删除，无响应体"
// @Failure  400 {object} ErrorResponse "不允许删除最后一个管理员或当前登录用户"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Failure  404 {object} ErrorResponse "用户不存在"
// @Security bearerAuth
// @Router   /admin/users/{id} [delete]
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
// @Summary  浏览服务器本地目录（用于选择授权目录）
// @Tags     管理
// @Produce  json
// @Param    path query string false "服务器本地绝对路径；省略时返回根目录 / 盘符列表"
// @Success  200 {object} BrowseResponse "该本地目录的子目录列表"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Security bearerAuth
// @Router   /admin/browse [get]
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
			c.JSON(http.StatusOK, BrowseResponse{Path: "", Parent: "", Dirs: drives})
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
	c.JSON(http.StatusOK, BrowseResponse{Path: p, Parent: parent, Dirs: dirs})
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// ---------- cache / search ----------

// @Summary  缓存统计
// @Tags     管理
// @Produce  json
// @Success  200 {object} CacheStats "预览图 / 转码缓存的占用、上限与保留时长，以及当前转码会话"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Security bearerAuth
// @Router   /admin/cache [get]
func (a *API) adminCacheStats(c *gin.Context) {
	stats, last := a.Cache.Stats()
	c.JSON(http.StatusOK, CacheStats{
		Dir: a.Cache.Dir(), Thumb: stats[cache.Thumb], Transcode: stats[cache.Transcode],
		Sessions: a.Media.Sessions(), LastCleanup: last,
	})
}

// @Summary  清理已过期的缓存（保留时长为 0 表示永不过期，不可清空）
// @Tags     管理
// @Accept   json
// @Produce  json
// @Param    body body CacheCleanRequest false "要清理的缓存桶，省略或 all 表示全部"
// @Success  200 {object} CacheCleanResponse "清理完成，返回删除的文件数与字节数"
// @Failure  400 {object} ErrorResponse "kind 无效"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Security bearerAuth
// @Router   /admin/cache/clean [post]
func (a *API) adminCacheClean(c *gin.Context) {
	var req CacheCleanRequest
	if c.Request.ContentLength > 0 && !bind(c, &req) {
		return
	}
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
	c.JSON(http.StatusOK, a.Cache.Cleanup(buckets))
}

// @Summary  搜索索引状态
// @Tags     管理
// @Produce  json
// @Success  200 {object} IndexStatus "索引的构建状态与统计"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Security bearerAuth
// @Router   /admin/search/status [get]
func (a *API) adminSearchStatus(c *gin.Context) { c.JSON(http.StatusOK, a.Search.Status()) }

// @Summary  重建搜索索引
// @Tags     管理
// @Produce  json
// @Success  202 {object} ReindexResponse "已开始后台重建"
// @Failure  403 {object} ErrorResponse "需要管理员权限"
// @Security bearerAuth
// @Router   /admin/search/reindex [post]
func (a *API) adminReindex(c *gin.Context) {
	a.Search.RebuildAsync()
	c.JSON(http.StatusAccepted, ReindexResponse{OK: true})
}
