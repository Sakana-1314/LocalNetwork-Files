package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sakana-1314/MList/server/internal/auth"
	"github.com/Sakana-1314/MList/server/internal/store"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

// Folder sharing. A share stores the folder's virtual path in its owner's
// filesystem, not the files. Every public request rebuilds the owner's
// current filesystem read-only and resolves the path again, so the link stops
// working as soon as the owner loses access to the bucket, is disabled or
// deleted, or the folder is moved away; and it never grants more than the
// owner has, and never write access.

var (
	errShareGone    = &httpError{http.StatusNotFound, "分享不存在或已失效"}
	errShareExpired = &httpError{http.StatusGone, "分享已过期"}
)

func newShareToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// parseExpiry validates an expiry time given by a client: it must lie in the
// future. nil means the share never expires.
func parseExpiry(at *time.Time) (*time.Time, error) {
	if at == nil {
		return nil, nil
	}
	if !at.After(time.Now()) {
		return nil, badRequest("过期时间必须晚于当前时间")
	}
	t := at.UTC()
	return &t, nil
}

// shareFS opens the owner's filesystem read-only and resolves the shared
// folder in it. It fails when the share expired or the folder is no longer
// reachable with the owner's current permissions.
func (a *API) shareFS(sh *store.Share) (*vfs.FS, error) {
	if sh.Expired(time.Now()) {
		return nil, errShareExpired
	}
	owner, err := a.Store.GetUser(sh.UserID)
	if err != nil || owner.Disabled {
		return nil, errShareGone
	}
	fsys := a.NewFS(&auth.Principal{User: owner, ReadOnly: true})
	e, err := fsys.Stat(sh.Path)
	if err != nil || !e.IsDir {
		return nil, errShareGone
	}
	return fsys, nil
}

// shareStatus reports whether a share can currently be opened.
func (a *API) shareStatus(sh *store.Share) string {
	if _, err := a.shareFS(sh); err != nil {
		if errors.Is(err, errShareExpired) {
			return "expired"
		}
		return "unavailable"
	}
	return "active"
}

func shareID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, badRequest("id 无效"))
		return 0, false
	}
	return uint(id), true
}

func (a *API) shareView(sh *store.Share) ShareView {
	return ShareView{Share: *sh, Name: path.Base(sh.Path), Status: a.shareStatus(sh)}
}

// @Summary     创建分享
// @Description 把一个文件夹分享为公开链接（只允许分享文件夹，不能分享根目录）。
// @Description 分享的是文件夹路径：访问者只读，权限随分享者当前权限变化，分享者失去权限后链接随即失效。
// @Description expires_at 为空表示永不过期，否则必须晚于当前时间。
// @Tags     分享
// @Accept   json
// @Produce  json
// @Param    请求体 body CreateShareRequest true "文件夹路径与过期时间"
// @Success  201 {object} ShareView "分享已创建"
// @Failure  400 {object} ErrorResponse "不是文件夹、是根目录，或过期时间无效"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "该访问令牌没有 write 权限"
// @Failure  404 {object} ErrorResponse "文件夹不存在"
// @Security bearerAuth
// @Router   /shares [post]
func (a *API) createShare(c *gin.Context) {
	var req CreateShareRequest
	if !bind(c, &req) {
		return
	}
	vp := vfs.CleanPath(req.Path)
	if vp == "/" {
		fail(c, badRequest("不能分享根目录，请选择一个文件夹"))
		return
	}
	e, err := a.fs(c).Stat(vp)
	if err != nil {
		fail(c, err)
		return
	}
	if !e.IsDir {
		fail(c, badRequest("只能分享文件夹"))
		return
	}
	exp, err := parseExpiry(req.ExpiresAt)
	if err != nil {
		fail(c, err)
		return
	}
	tok, err := newShareToken()
	if err != nil {
		fail(c, err)
		return
	}
	sh := &store.Share{Token: tok, UserID: principal(c).User.ID, Path: vp, ExpiresAt: exp}
	if err := a.Store.CreateShare(sh); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, a.shareView(sh))
}

// @Summary     列出我的分享
// @Description 返回当前用户创建的全部分享（含已过期与已失效的），按创建时间倒序。
// @Description status 为 active（可访问）、expired（已过期）或 unavailable（分享者已无权限或文件夹已不存在）。
// @Tags     分享
// @Produce  json
// @Success  200 {array} ShareView "分享记录"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Security bearerAuth
// @Router   /shares [get]
func (a *API) listShares(c *gin.Context) {
	list, err := a.Store.ListShares(principal(c).User.ID)
	if err != nil {
		fail(c, err)
		return
	}
	out := make([]ShareView, 0, len(list))
	for i := range list {
		out = append(out, a.shareView(&list[i]))
	}
	c.JSON(http.StatusOK, out)
}

// @Summary     修改分享有效期
// @Description 修改自己某个分享的过期时间；expires_at 为空表示永不过期，否则必须晚于当前时间。
// @Description 已过期的分享延长有效期后即可重新访问，链接不变。
// @Tags     分享
// @Accept   json
// @Produce  json
// @Param    id path integer true "分享 ID"
// @Param    请求体 body UpdateShareRequest true "新的过期时间"
// @Success  200 {object} ShareView "已更新"
// @Failure  400 {object} ErrorResponse "过期时间无效"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "该访问令牌没有 write 权限"
// @Failure  404 {object} ErrorResponse "分享不存在"
// @Security bearerAuth
// @Router   /shares/{id} [put]
func (a *API) updateShare(c *gin.Context) {
	id, ok := shareID(c)
	if !ok {
		return
	}
	var req UpdateShareRequest
	if !bind(c, &req) {
		return
	}
	exp, err := parseExpiry(req.ExpiresAt)
	if err != nil {
		fail(c, err)
		return
	}
	uid := principal(c).User.ID
	if err := a.Store.SetShareExpiry(uid, id, exp); err != nil {
		fail(c, err)
		return
	}
	sh, err := a.Store.GetShare(uid, id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, a.shareView(sh))
}

// @Summary     取消分享
// @Description 删除自己的一个分享，链接立即失效。文件不受影响。
// @Tags     分享
// @Param    id path integer true "分享 ID"
// @Success  204 "已删除，无响应体"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "该访问令牌没有 write 权限"
// @Failure  404 {object} ErrorResponse "分享不存在"
// @Security bearerAuth
// @Router   /shares/{id} [delete]
func (a *API) deleteShare(c *gin.Context) {
	id, ok := shareID(c)
	if !ok {
		return
	}
	if err := a.Store.DeleteShare(principal(c).User.ID, id); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ---------- public access ----------

// openShare resolves the token of a public request. path, when given, is a
// path inside the shared folder ("/" = the folder itself); it is cleaned
// before being joined, so it cannot climb out of the folder.
func (a *API) openShare(c *gin.Context) (*store.Share, *vfs.FS, bool) {
	sh, err := a.Store.GetShareByToken(c.Param("token"))
	if err != nil {
		fail(c, errShareGone)
		return nil, nil, false
	}
	fsys, err := a.shareFS(sh)
	if err != nil {
		fail(c, err)
		return nil, nil, false
	}
	return sh, fsys, true
}

func inShare(sh *store.Share, rel string) string {
	return path.Join(sh.Path, vfs.CleanPath(rel))
}

// insideShare reports whether vpath, with symlinks resolved, stays inside the
// shared folder. The VFS only confines symlinks to the bucket; a link inside
// the shared folder must not expose the rest of the owner's bucket.
func insideShare(fsys *vfs.FS, sh *store.Share, vpath string) bool {
	base, err := fsys.Locate(sh.Path)
	if err != nil {
		return false
	}
	l, err := fsys.Locate(vpath)
	if err != nil {
		return false
	}
	br, err := filepath.EvalSymlinks(base.Real)
	if err != nil {
		return false
	}
	lr, err := filepath.EvalSymlinks(l.Real)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(br, lr)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// sharedPath maps a client path to the owner's filesystem, failing with 404
// when it leaves the shared folder.
func sharedPath(c *gin.Context, fsys *vfs.FS, sh *store.Share, rel string) (string, bool) {
	vp := inShare(sh, rel)
	if !insideShare(fsys, sh, vp) {
		fail(c, vfs.ErrNotFound)
		return "", false
	}
	return vp, true
}

// relEntry rewrites an entry of the owner's filesystem to a path relative to
// the shared folder, so links never reveal the owner's bucket layout.
func relEntry(sh *store.Share, e vfs.Entry) vfs.Entry {
	e.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(e.Path, sh.Path), "/")
	e.Writable = false
	return e
}

// @Summary     查看分享
// @Description 公开接口，无需登录。返回分享的文件夹名称、分享者与过期时间。
// @Description 分享不存在、分享者已无权限或文件夹已不存在时返回 404，已过期返回 410。
// @Tags     分享
// @Produce  json
// @Param    token path string true "分享令牌"
// @Success  200 {object} PublicShareInfo "分享信息"
// @Failure  404 {object} ErrorResponse "分享不存在或已失效"
// @Failure  410 {object} ErrorResponse "分享已过期"
// @Router   /share/{token} [get]
func (a *API) publicShareInfo(c *gin.Context) {
	sh, _, ok := a.openShare(c)
	if !ok {
		return
	}
	owner := ""
	if u, err := a.Store.GetUser(sh.UserID); err == nil {
		owner = u.Username
	}
	c.JSON(http.StatusOK, PublicShareInfo{Name: path.Base(sh.Path), Owner: owner, ExpiresAt: sh.ExpiresAt})
}

// @Summary     列出分享内容
// @Description 公开接口，无需登录。列出分享文件夹内某个子目录的内容，path 相对于分享的文件夹（/ 为其本身）。
// @Description 返回的条目路径同样相对于分享的文件夹，且全部只读。
// @Tags     分享
// @Produce  json
// @Param    token path string true "分享令牌"
// @Param    path query string false "相对于分享文件夹的路径，省略为 /"
// @Success  200 {object} ListResponse "目录内容"
// @Failure  404 {object} ErrorResponse "分享或目录不存在"
// @Failure  410 {object} ErrorResponse "分享已过期"
// @Router   /share/{token}/list [get]
func (a *API) publicShareList(c *gin.Context) {
	sh, fsys, ok := a.openShare(c)
	if !ok {
		return
	}
	rel := vfs.CleanPath(c.DefaultQuery("path", "/"))
	vp, ok := sharedPath(c, fsys, sh, rel)
	if !ok {
		return
	}
	all, _, err := fsys.List(vp)
	if err != nil {
		fail(c, err)
		return
	}
	entries := make([]vfs.Entry, 0, len(all))
	for _, e := range all {
		if insideShare(fsys, sh, e.Path) {
			entries = append(entries, relEntry(sh, e))
		}
	}
	a.signEntries(entries)
	c.JSON(http.StatusOK, ListResponse{Path: rel, Writable: false, Entries: entries})
}

// @Summary     读取分享中的文件
// @Description 公开接口，无需登录。以流的形式返回分享文件夹内的文件，支持 Range；download 为 true 时作为附件下载。
// @Tags     分享
// @Param    token path string true "分享令牌"
// @Param    path query string true "相对于分享文件夹的文件路径"
// @Param    download query boolean false "为 true 时以附件形式下载"
// @Success  200 {string} binary "完整文件内容"
// @Success  206 {string} binary "Range 请求命中的部分内容"
// @Failure  404 {object} ErrorResponse "分享或文件不存在"
// @Failure  410 {object} ErrorResponse "分享已过期"
// @Router   /share/{token}/raw [get]
func (a *API) publicShareRaw(c *gin.Context) {
	sh, fsys, ok := a.openShare(c)
	if !ok {
		return
	}
	if vp, ok := sharedPath(c, fsys, sh, c.Query("path")); ok {
		serveRaw(c, fsys, vp)
	}
}

// @Summary     按文件名读取分享文件
// @Description 与读取分享中的文件接口相同，URL 末尾带上文件名，便于浏览器下载时显示真实文件名；末尾名称由服务端忽略。
// @Tags     分享
// @Param    token path string true "分享令牌"
// @Param    name path string true "文件名，仅用于显示，服务端忽略"
// @Param    path query string true "相对于分享文件夹的文件路径"
// @Param    download query boolean false "为 true 时以附件形式下载"
// @Success  200 {string} binary "完整文件内容"
// @Success  206 {string} binary "Range 请求命中的部分内容"
// @Failure  404 {object} ErrorResponse "分享或文件不存在"
// @Failure  410 {object} ErrorResponse "分享已过期"
// @Router   /share/{token}/raw/{name} [get]
func (a *API) publicShareRawNamed(c *gin.Context) { a.publicShareRaw(c) }

// @Summary     读取分享中的文本
// @Description 公开接口，无需登录。按 UTF-8 返回分享文件夹内文本文件的内容，最大 4 MiB，超出部分截断。
// @Tags     分享
// @Produce  json
// @Param    token path string true "分享令牌"
// @Param    path query string true "相对于分享文件夹的文件路径"
// @Success  200 {object} TextResponse "文本内容"
// @Failure  404 {object} ErrorResponse "分享或文件不存在"
// @Failure  410 {object} ErrorResponse "分享已过期"
// @Router   /share/{token}/text [get]
func (a *API) publicShareText(c *gin.Context) {
	sh, fsys, ok := a.openShare(c)
	if !ok {
		return
	}
	if vp, ok := sharedPath(c, fsys, sh, c.Query("path")); ok {
		serveText(c, fsys, vp)
	}
}
