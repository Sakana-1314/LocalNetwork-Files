package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Sakana-1314/MList/server/internal/auth"
	"github.com/Sakana-1314/MList/server/internal/store"
	"github.com/Sakana-1314/MList/server/internal/vfs"
)

const sessionCookie = "ml_session"

const ctxPrincipal = "principal"

// authenticate resolves the bearer token, access_token query parameter or session cookie.
func (a *API) authenticate(c *gin.Context) {
	tok := ""
	if h := c.GetHeader("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimSpace(h[7:])
	} else if q := c.Query("access_token"); q != "" {
		tok = q
	} else if ck, err := c.Cookie(sessionCookie); err == nil {
		tok = ck
	}
	p, err := a.Auth.Resolve(tok)
	if err != nil {
		fail(c, err)
		return
	}
	c.Set(ctxPrincipal, p)
	c.Next()
}

func principal(c *gin.Context) *auth.Principal { return c.MustGet(ctxPrincipal).(*auth.Principal) }

func (a *API) fs(c *gin.Context) *vfs.FS { return a.NewFS(principal(c)) }

func (a *API) requireAdmin(c *gin.Context) {
	if !principal(c).User.IsAdmin() {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "需要管理员权限"})
		return
	}
	c.Next()
}

// requireWrite rejects tokens without the write scope on mutating, non-file endpoints
// (file endpoints are protected by the read-only VFS).
func (a *API) requireWrite(c *gin.Context) {
	if principal(c).ReadOnly {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "该访问令牌没有 write 权限"})
		return
	}
	c.Next()
}

// userView hides local paths from non-admin viewers.
func userView(u *store.User, showPaths bool) store.User {
	out := *u
	out.Mounts = make([]store.Mount, len(u.Mounts))
	copy(out.Mounts, u.Mounts)
	if !showPaths {
		for i := range out.Mounts {
			out.Mounts[i].Path = ""
		}
	}
	if out.Mounts == nil {
		out.Mounts = []store.Mount{}
	}
	return out
}

func (a *API) setSessionCookie(c *gin.Context, tok string, exp time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: c.Request.TLS != nil,
	})
}

func (a *API) login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if !bind(c, &req) {
		return
	}
	ip := c.ClientIP()
	if a.limiter.blocked(ip) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "登录失败次数过多，请 10 分钟后再试"})
		return
	}
	u, err := a.Auth.Authenticate(strings.TrimSpace(req.Username), req.Password)
	if err != nil {
		a.limiter.fail(ip)
		fail(c, err)
		return
	}
	a.limiter.reset(ip)
	tok, exp, err := a.Auth.IssueSession(u)
	if err != nil {
		fail(c, err)
		return
	}
	a.setSessionCookie(c, tok, exp)
	c.JSON(http.StatusOK, gin.H{"token": tok, "expires_at": exp, "user": userView(u, u.IsAdmin())})
}

func (a *API) logout(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	c.Status(http.StatusNoContent)
}

func (a *API) me(c *gin.Context) {
	u := principal(c).User
	c.JSON(http.StatusOK, userView(u, u.IsAdmin()))
}

// changePassword updates the password. Existing sessions are invalidated, so a
// fresh session token is returned (and set as cookie).
func (a *API) changePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password" binding:"required"`
	}
	if !bind(c, &req) {
		return
	}
	if len(req.NewPassword) < 6 {
		fail(c, badRequest("新密码至少 6 位"))
		return
	}
	u := principal(c).User
	if !auth.CheckPassword(u.PasswordHash, req.OldPassword) {
		fail(c, badRequest("原密码错误"))
		return
	}
	if err := a.Auth.SetPassword(u, req.NewPassword); err != nil {
		fail(c, err)
		return
	}
	tok, exp, err := a.Auth.IssueSession(u)
	if err != nil {
		fail(c, err)
		return
	}
	a.setSessionCookie(c, tok, exp)
	c.JSON(http.StatusOK, gin.H{"token": tok, "expires_at": exp})
}
