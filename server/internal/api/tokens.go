package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/store"
)

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

func (a *API) createToken(c *gin.Context) {
	var req struct {
		Name          string   `json:"name" binding:"required"`
		Scopes        []string `json:"scopes"`
		ExpiresInDays int      `json:"expires_in_days"`
	}
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
	c.JSON(http.StatusCreated, struct {
		*store.AccessToken
		Token string `json:"token"`
	}{t, plain})
}

// revealToken returns the plaintext of one of the caller's own tokens.
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
	c.JSON(http.StatusOK, gin.H{"token": secret})
}

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

func (a *API) createS3Key(c *gin.Context) {
	var req struct {
		Name     string `json:"name"`
		ReadOnly bool   `json:"read_only"`
	}
	if c.Request.ContentLength > 0 && !bind(c, &req) {
		return
	}
	k, secret, err := a.Auth.CreateS3Key(principal(c).User.ID, req.Name, req.ReadOnly)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, struct {
		*store.S3Key
		SecretKey string `json:"secret_key"`
	}{k, secret})
}

// revealS3Key returns the secret of one of the caller's own S3 keys.
func (a *API) revealS3Key(c *gin.Context) {
	secret, err := a.Auth.S3KeySecret(principal(c).User.ID, c.Param("key"))
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"secret_key": secret})
}

func (a *API) deleteS3Key(c *gin.Context) {
	if err := a.Store.DeleteS3Key(principal(c).User.ID, c.Param("key")); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
