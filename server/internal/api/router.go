// Package api is the REST API (Gin) consumed by the web UI and by OpenAPI
// clients using access tokens.
package api

import (
	_ "embed"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sakana-1314/MList/server/internal/auth"
	"github.com/Sakana-1314/MList/server/internal/cache"
	"github.com/Sakana-1314/MList/server/internal/config"
	"github.com/Sakana-1314/MList/server/internal/media"
	"github.com/Sakana-1314/MList/server/internal/search"
	"github.com/Sakana-1314/MList/server/internal/store"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

// openapiJSON is the OpenAPI document generated from the Go annotations by
// `make docs` (swaggo/swag v2). Do not edit it by hand: CI regenerates the spec
// and fails when the committed file differs from the annotations.
//
//go:embed openapi.json
var openapiJSONDoc []byte

// ServiceStatus 描述单个协议服务的状态，用于挂载向导与管理界面。
type ServiceStatus struct {
	Enabled bool              `json:"enabled"`           // 是否在 config.yaml 中启用
	Running bool              `json:"running"`           // 当前是否正在运行
	Port    int               `json:"port,omitempty"`    // 监听端口
	Path    string            `json:"path,omitempty"`    // 服务在 HTTP 端口上的路径前缀（WebDAV 为 /dav，S3 为 /s3）
	Error   string            `json:"error,omitempty"`   // 服务启动错误（仅管理员可见）
	Shares  map[string]string `json:"shares,omitempty"`  // 仅 SMB：当前用户的挂载名 → SMB 共享名
	Account string            `json:"account,omitempty"` // 仅 SMB：当前用户的 SMB 登录名
}

// Services is implemented by the application to expose protocol service state.
type Services interface {
	Status(userID uint) map[string]ServiceStatus
}

type Deps struct {
	Config   *config.Config
	Store    *store.Store
	Auth     *auth.Service
	Media    *media.Service
	Cache    *cache.Manager
	Search   *search.Indexer
	NewFS    func(p *auth.Principal) *vfs.FS
	Services Services
	Version  string
}

// maxThumbEntries bounds the digest -> file index used by /media/thumb. It is
// a cache: entries not in it simply return 404 and are refilled by the next
// directory listing.
const maxThumbEntries = 20000

type API struct {
	Deps
	limiter *loginLimiter
	thumbs  *thumbIndex
}

// Register mounts the API routes on r.
func Register(r *gin.Engine, d Deps) {
	a := &API{Deps: d, limiter: newLoginLimiter(), thumbs: newThumbIndex(maxThumbEntries)}
	// Every digest handed to a client is remembered here so /media/thumb can
	// resolve it later without a path in the URL.
	vfs.OnDigest = a.thumbs.Put

	spec := openapiJSON()
	r.GET("/api/openapi.json", func(c *gin.Context) { c.Data(http.StatusOK, "application/json", spec) })

	v1 := r.Group("/api/v1")
	v1.POST("/auth/login", a.login)
	v1.POST("/auth/logout", a.logout)
	// Previews are identified by content digest plus a non-expiring signature,
	// so they load in <img> without a session or an access_token in the URL.
	v1.GET("/media/thumb", a.mediaThumb)

	g := v1.Group("", a.authenticate)
	g.GET("/auth/me", a.me)
	g.PUT("/auth/password", a.requireWrite, a.changePassword)

	g.GET("/fs/list", a.fsList)
	g.GET("/fs/stat", a.fsStat)
	g.GET("/fs/raw", a.fsRaw)
	g.HEAD("/fs/raw", a.fsRaw)
	// Same as /fs/raw; the trailing name is ignored and only makes browsers show the real file name.
	g.GET("/fs/raw/:name", a.fsRawNamed)
	g.HEAD("/fs/raw/:name", a.fsRawNamed)
	g.GET("/fs/text", a.fsText)
	g.PUT("/fs/file", a.fsPut)
	g.POST("/fs/mkdir", a.fsMkdir)
	g.POST("/fs/rename", a.fsRename)
	g.POST("/fs/move", a.fsMove)
	g.POST("/fs/copy", a.fsCopy)
	g.POST("/fs/delete", a.fsDelete)
	g.GET("/fs/search", a.fsSearch)

	g.GET("/media/info", a.mediaInfo)
	g.POST("/media/transcode", a.mediaTranscode)
	g.GET("/media/hls/:id/index.m3u8", a.mediaHLSPlaylist)
	g.GET("/media/hls/:id/:file", a.mediaHLSSegment)
	g.DELETE("/media/hls/:id", a.mediaHLSStop)
	g.GET("/media/audio", a.mediaAudio)
	g.GET("/media/subtitle", a.mediaSubtitle)

	g.GET("/tokens", a.listTokens)
	g.POST("/tokens", a.requireWrite, a.createToken)
	g.GET("/tokens/:id/secret", a.requireWrite, a.revealToken)
	g.DELETE("/tokens/:id", a.requireWrite, a.deleteToken)
	g.GET("/s3keys", a.listS3Keys)
	g.POST("/s3keys", a.requireWrite, a.createS3Key)
	g.GET("/s3keys/:key/secret", a.requireWrite, a.revealS3Key)
	g.DELETE("/s3keys/:key", a.requireWrite, a.deleteS3Key)

	g.GET("/system/info", a.systemInfo)

	adm := g.Group("/admin", a.requireAdmin)
	adm.GET("/users", a.adminListUsers)
	adm.POST("/users", a.requireWrite, a.adminCreateUser)
	adm.PUT("/users/:id", a.requireWrite, a.adminUpdateUser)
	adm.DELETE("/users/:id", a.requireWrite, a.adminDeleteUser)
	adm.GET("/buckets", a.adminListBuckets)
	adm.POST("/buckets", a.requireWrite, a.adminCreateBucket)
	adm.PUT("/buckets/:id", a.requireWrite, a.adminUpdateBucket)
	adm.DELETE("/buckets/:id", a.requireWrite, a.adminDeleteBucket)
	adm.POST("/buckets/:id/sync", a.requireWrite, a.adminSyncBucket)
	adm.GET("/browse", a.adminBrowse)
	adm.GET("/cache", a.adminCacheStats)
	adm.POST("/cache/clean", a.requireWrite, a.adminCacheClean)
	adm.GET("/search/status", a.adminSearchStatus)
	adm.POST("/search/reindex", a.requireWrite, a.adminReindex)

}

func openapiJSON() []byte { return openapiJSONDoc }

// ---------- errors ----------

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(msg string) error { return &httpError{http.StatusBadRequest, msg} }

func statusOf(err error) int {
	var he *httpError
	switch {
	case errors.As(err, &he):
		return he.status
	case errors.Is(err, vfs.ErrNotFound), errors.Is(err, store.ErrNotFound), errors.Is(err, media.ErrNoThumb),
		errors.Is(err, media.ErrSessionNotFound):
		return http.StatusNotFound
	case errors.Is(err, vfs.ErrPermission), errors.Is(err, vfs.ErrReadOnly), errors.Is(err, auth.ErrDisabled):
		return http.StatusForbidden
	case errors.Is(err, vfs.ErrExists), errors.Is(err, store.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, vfs.ErrInvalid), errors.Is(err, store.ErrInvalid), errors.Is(err, vfs.ErrNotDir), errors.Is(err, vfs.ErrIsDir),
		errors.Is(err, media.ErrNoVideo):
		return http.StatusBadRequest
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, media.ErrNoFFmpeg):
		return http.StatusNotImplemented
	case errors.Is(err, media.ErrUnsupportedSubtitle):
		return http.StatusUnsupportedMediaType
	}
	return http.StatusInternalServerError
}

// publicMessage is the error text shown to clients. Unexpected (5xx) errors can
// carry local paths or other internals (e.g. *fs.PathError), so they are
// replaced by a generic message and only logged.
func publicMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "记录不存在"
	case errors.Is(err, store.ErrConflict):
		return "名称已被使用"
	}
	if st := statusOf(err); st >= 500 && st != http.StatusNotImplemented {
		return "服务器内部错误"
	}
	return err.Error()
}

func fail(c *gin.Context, err error) {
	st := statusOf(err)
	if st >= 500 {
		slog.Error("request failed", "method", c.Request.Method, "path", c.Request.URL.Path, "err", err)
	}
	c.AbortWithStatusJSON(st, ErrorResponse{Error: publicMessage(err)})
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		fail(c, badRequest("请求格式错误: "+err.Error()))
		return false
	}
	return true
}

// ---------- login rate limiting ----------

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{fails: map[string][]time.Time{}} }

const (
	loginWindow   = 10 * time.Minute
	loginMaxFails = 10
)

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) >= loginMaxFails
}

func (l *loginLimiter) recent(ip string) []time.Time {
	var out []time.Time
	for _, t := range l.fails[ip] {
		if time.Since(t) < loginWindow {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		delete(l.fails, ip)
	} else {
		l.fails[ip] = out
	}
	return out
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) > 1024 {
		// Drop stale entries so the map can't grow without bound.
		for k := range l.fails {
			l.recent(k)
		}
	}
	l.fails[ip] = append(l.recent(ip), time.Now())
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
