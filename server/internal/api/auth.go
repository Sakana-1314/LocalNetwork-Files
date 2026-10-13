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
		c.AbortWithStatusJSON(http.StatusForbidden, ErrorResponse{Error: "需要管理员权限"})
		return
	}
	c.Next()
}

// requireWrite rejects tokens without the write scope on mutating, non-file endpoints
// (file endpoints are protected by the read-only VFS).
func (a *API) requireWrite(c *gin.Context) {
	if principal(c).ReadOnly {
		c.AbortWithStatusJSON(http.StatusForbidden, ErrorResponse{Error: "该访问令牌没有 write 权限"})
		return
	}
	c.Next()
}

// userView is the JSON form of a user. Grants only carry bucket names; local
// paths are only exposed through the admin bucket endpoints.
func userView(u *store.User) store.User {
	out := *u
	out.Grants = append([]store.Grant{}, u.Grants...)
	return out
}

func (a *API) setSessionCookie(c *gin.Context, tok string, exp time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: c.Request.TLS != nil,
	})
}

// @Summary     登录
// @Description 校验用户名与密码，成功后返回会话令牌并写入会话 Cookie。
// @Description 该接口不需要鉴权。同一 IP 连续失败会触发限流（10 分钟）。
// @Tags     认证
// @Accept   json
// @Produce  json
// @Param    body body LoginRequest true "登录凭据"
// @Success  200 {object} LoginResponse "登录成功，返回会话令牌与用户信息（同时写入会话 Cookie）"
// @Failure  401 {object} ErrorResponse "用户名或密码错误"
// @Failure  429 {object} ErrorResponse "登录失败次数过多，请 10 分钟后再试"
// @Router   /auth/login [post]
func (a *API) login(c *gin.Context) {
	var req LoginRequest
	if !bind(c, &req) {
		return
	}
	ip := c.ClientIP()
	if a.limiter.blocked(ip) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, ErrorResponse{Error: "登录失败次数过多，请 10 分钟后再试"})
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
	c.JSON(http.StatusOK, LoginResponse{Token: tok, ExpiresAt: exp, User: userView(u)})
}

// @Summary     退出登录
// @Description 清除浏览器会话 Cookie。接口不需要鉴权，会话过期后调用同样返回 204。
// @Tags    认证
// @Success 204 "已退出登录，无响应体"
// @Router  /auth/logout [post]
func (a *API) logout(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	c.Status(http.StatusNoContent)
}

// @Summary     获取当前用户
// @Description 返回当前登录用户及其授权目录。普通用户只能看到自己的信息。
// @Tags     认证
// @Produce  json
// @Success  200 {object} store.User "当前登录用户及其授权目录"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Security bearerAuth
// @Router   /auth/me [get]
func (a *API) me(c *gin.Context) {
	u := principal(c).User
	c.JSON(http.StatusOK, userView(u))
}

// 修改密码会使该用户此前的所有会话失效，因此会签发并写回新的会话令牌。
//
// @Summary     修改密码
// @Description 需要提供原密码，新密码至少 6 位。
// @Description 修改成功后该用户此前签发的所有会话失效，返回新的会话令牌并写入 Cookie，客户端应替换本地保存的令牌。
// @Tags     认证
// @Accept   json
// @Produce  json
// @Param    body body PasswordRequest true "原密码与新密码"
// @Success  200 {object} PasswordResponse "修改成功。旧会话全部失效，返回新的会话令牌（同时写入 Cookie），客户端应替换本地保存的令牌。"
// @Failure  400 {object} ErrorResponse "原密码错误或新密码不足 6 位"
// @Security bearerAuth
// @Router   /auth/password [put]
func (a *API) changePassword(c *gin.Context) {
	var req PasswordRequest
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
	c.JSON(http.StatusOK, PasswordResponse{Token: tok, ExpiresAt: exp})
}
