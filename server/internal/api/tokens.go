package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Sakana-1314/MList/server/internal/store"
)

// @Summary  我的访问令牌
// @Tags     访问凭据
// @Produce  json
// @Success  200 {array} store.AccessToken "当前用户的访问令牌列表（不含令牌明文）"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Security bearerAuth
// @Router   /tokens [get]
func (a *API) listTokens(c *gin.Context) {
	ts, err := a.Store.ListTokens(principal(c).User.ID)
	if err != nil {
		fail(c, err)
		return
	}
	if ts == nil {
		ts = []store.AccessToken{}
	}
	c.JSON(http.StatusOK, ts)
}

// @Summary  创建访问令牌（明文可随时通过 /tokens/{id}/secret 再次读取）
// @Tags     访问凭据
// @Accept   json
// @Produce  json
// @Param    body body CreateTokenRequest true "令牌名称、权限范围与有效期"
// @Success  201 {object} TokenWithSecret "令牌创建成功，返回令牌信息与明文"
// @Failure  400 {object} ErrorResponse "名称或有效期无效"
// @Failure  403 {object} ErrorResponse "该访问令牌没有 write 权限"
// @Security bearerAuth
// @Router   /tokens [post]
func (a *API) createToken(c *gin.Context) {
	var req CreateTokenRequest
	if !bind(c, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Name) > 128 {
		fail(c, badRequest("名称无效"))
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 {
		fail(c, badRequest("有效期无效"))
		return
	}
	t, plain, err := a.Auth.CreateToken(principal(c).User.ID, req.Name, req.Scopes, req.ExpiresInDays)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, TokenWithSecret{AccessToken: *t, Token: plain})
}

// revealToken returns the plaintext of one of the caller's own tokens.
// @Summary  读取访问令牌明文（仅令牌所属用户）
// @Tags     访问凭据
// @Produce  json
// @Param    id path integer true "令牌 ID"
// @Success  200 {object} TokenSecret "令牌明文。响应带 Cache-Control: no-store，请勿长期缓存。"
// @Failure  403 {object} ErrorResponse "该访问令牌没有 write 权限"
// @Failure  404 {object} ErrorResponse "令牌不存在或不属于当前用户"
// @Security bearerAuth
// @Router   /tokens/{id}/secret [get]
func (a *API) revealToken(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, badRequest("id 无效"))
		return
	}
	secret, err := a.Auth.TokenSecret(principal(c).User.ID, uint(id))
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, TokenSecret{Token: secret})
}

// @Summary  吊销访问令牌
// @Tags     访问凭据
// @Param    id path integer true "令牌 ID"
// @Success  204 "令牌已吊销，无响应体"
// @Failure  403 {object} ErrorResponse "该访问令牌没有 write 权限"
// @Failure  404 {object} ErrorResponse "令牌不存在或不属于当前用户"
// @Security bearerAuth
// @Router   /tokens/{id} [delete]
func (a *API) deleteToken(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		fail(c, badRequest("id 无效"))
		return
	}
	if err := a.Store.DeleteToken(principal(c).User.ID, uint(id)); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// @Summary  我的 S3 访问密钥
// @Tags     访问凭据
// @Produce  json
// @Success  200 {array} store.S3Key "当前用户的 S3 密钥列表（不含 Secret）"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Security bearerAuth
// @Router   /s3keys [get]
func (a *API) listS3Keys(c *gin.Context) {
	ks, err := a.Store.ListS3Keys(principal(c).User.ID)
	if err != nil {
		fail(c, err)
		return
	}
	if ks == nil {
		ks = []store.S3Key{}
	}
	c.JSON(http.StatusOK, ks)
}

// @Summary  创建 S3 密钥（Secret 可随时通过 /s3keys/{access_key}/secret 再次读取）
// @Tags     访问凭据
// @Accept   json
// @Produce  json
// @Param    body body CreateS3KeyRequest true "备注名称与是否只读"
// @Success  201 {object} S3KeyWithSecret "密钥创建成功，返回 Access Key 与 Secret"
// @Failure  403 {object} ErrorResponse "该访问令牌没有 write 权限"
// @Security bearerAuth
// @Router   /s3keys [post]
func (a *API) createS3Key(c *gin.Context) {
	var req CreateS3KeyRequest
	if c.Request.ContentLength > 0 && !bind(c, &req) {
		return
	}
	k, secret, err := a.Auth.CreateS3Key(principal(c).User.ID, req.Name, req.ReadOnly)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, S3KeyWithSecret{S3Key: *k, SecretKey: secret})
}

// revealS3Key returns the secret of one of the caller's own S3 keys.
// @Summary  读取 S3 密钥的 Secret（仅密钥所属用户）
// @Tags     访问凭据
// @Produce  json
// @Param    access_key path string true "S3 Access Key（访问密钥 ID）"
// @Success  200 {object} S3KeySecret "S3 Secret Key。响应带 Cache-Control: no-store，请勿长期缓存。"
// @Failure  403 {object} ErrorResponse "该访问令牌没有 write 权限"
// @Failure  404 {object} ErrorResponse "密钥不存在或不属于当前用户"
// @Security bearerAuth
// @Router   /s3keys/{access_key}/secret [get]
func (a *API) revealS3Key(c *gin.Context) {
	secret, err := a.Auth.S3KeySecret(principal(c).User.ID, c.Param("key"))
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, S3KeySecret{SecretKey: secret})
}

// @Summary  删除 S3 密钥
// @Tags     访问凭据
// @Param    access_key path string true "S3 Access Key（访问密钥 ID）"
// @Success  204 "密钥已删除，无响应体"
// @Failure  403 {object} ErrorResponse "该访问令牌没有 write 权限"
// @Failure  404 {object} ErrorResponse "密钥不存在或不属于当前用户"
// @Security bearerAuth
// @Router   /s3keys/{access_key} [delete]
func (a *API) deleteS3Key(c *gin.Context) {
	if err := a.Store.DeleteS3Key(principal(c).User.ID, c.Param("key")); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
