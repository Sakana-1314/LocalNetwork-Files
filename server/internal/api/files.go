package api

import (
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/Sakana-1314/LocalNetwork-Files/server/internal/vfs"
)

func (a *API) fsList(c *gin.Context) {
	p := c.DefaultQuery("path", "/")
	entries, writable, err := a.fs(c).List(p)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": vfs.CleanPath(p), "writable": writable, "entries": entries})
}

func (a *API) fsStat(c *gin.Context) {
	e, err := a.fs(c).Stat(c.Query("path"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, e)
}

// activeTypes can run script when rendered by the browser; they are sandboxed.
var activeTypes = map[string]bool{
	"text/html": true, "image/svg+xml": true, "application/xhtml+xml": true, "text/xml": true, "application/xml": true,
}

func (a *API) fsRaw(c *gin.Context) {
	f, fi, err := a.fs(c).Open(c.Query("path"))
	if err != nil {
		fail(c, err)
		return
	}
	defer f.Close()
	ct := vfs.MimeOf(fi.Name(), false)
	h := c.Writer.Header()
	h.Set("Content-Type", ct)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-cache")
	disposition := "inline"
	if d := c.Query("download"); d == "1" || d == "true" {
		disposition = "attachment"
	}
	h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": fi.Name()}))
	base, _, _ := strings.Cut(ct, ";")
	if activeTypes[base] {
		h.Set("Content-Security-Policy", "sandbox")
	}
	http.ServeContent(c.Writer, c.Request, "", fi.ModTime(), f)
}

const maxTextSize = 4 << 20

func (a *API) fsText(c *gin.Context) {
	f, fi, err := a.fs(c).Open(c.Query("path"))
	if err != nil {
		fail(c, err)
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxTextSize))
	if err != nil {
		fail(c, err)
		return
	}
	truncated := fi.Size() > maxTextSize
	if truncated {
		// Don't cut a multi-byte character in half.
		for i := 0; i < 3 && len(b) > 0 && !utf8.Valid(b); i++ {
			b = b[:len(b)-1]
		}
	}
	content := string(b)
	if !utf8.Valid(b) {
		if dec, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b); err == nil {
			content = string(dec)
		}
	}
	c.JSON(http.StatusOK, gin.H{"content": content, "truncated": truncated})
}

func (a *API) fsPut(c *gin.Context) {
	overwrite, _ := strconv.ParseBool(c.DefaultQuery("overwrite", "false"))
	e, err := a.fs(c).WriteFile(c.Query("path"), c.Request.Body, overwrite)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, e)
}

func (a *API) fsMkdir(c *gin.Context) {
	var req struct {
		Path string `json:"path" binding:"required"`
	}
	if !bind(c, &req) {
		return
	}
	e, err := a.fs(c).Mkdir(req.Path)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, e)
}

func (a *API) fsRename(c *gin.Context) {
	var req struct {
		Path string `json:"path" binding:"required"`
		Name string `json:"name" binding:"required"`
	}
	if !bind(c, &req) {
		return
	}
	e, err := a.fs(c).Rename(req.Path, strings.TrimSpace(req.Name))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, e)
}

type batchFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type batchResult struct {
	Succeeded []string       `json:"succeeded"`
	Failed    []batchFailure `json:"failed"`
}

func (b *batchResult) add(p string, err error) {
	if err != nil {
		b.Failed = append(b.Failed, batchFailure{p, err.Error()})
	} else {
		b.Succeeded = append(b.Succeeded, p)
	}
}

func (a *API) transfer(c *gin.Context, op func(fsys *vfs.FS, src, dst string, overwrite bool) error) {
	var req struct {
		Sources   []string `json:"sources" binding:"required"`
		DestDir   string   `json:"dest_dir" binding:"required"`
		Overwrite bool     `json:"overwrite"`
	}
	if !bind(c, &req) {
		return
	}
	fsys := a.fs(c)
	res := batchResult{Succeeded: []string{}, Failed: []batchFailure{}}
	for _, src := range req.Sources {
		src = vfs.CleanPath(src)
		res.add(src, op(fsys, src, path.Join(vfs.CleanPath(req.DestDir), path.Base(src)), req.Overwrite))
	}
	c.JSON(http.StatusOK, res)
}

func (a *API) fsMove(c *gin.Context) {
	a.transfer(c, func(fsys *vfs.FS, src, dst string, ow bool) error { return fsys.Move(src, dst, ow) })
}

func (a *API) fsCopy(c *gin.Context) {
	a.transfer(c, func(fsys *vfs.FS, src, dst string, ow bool) error { return fsys.Copy(src, dst, ow) })
}

func (a *API) fsDelete(c *gin.Context) {
	var req struct {
		Paths []string `json:"paths" binding:"required"`
	}
	if !bind(c, &req) {
		return
	}
	fsys := a.fs(c)
	res := batchResult{Succeeded: []string{}, Failed: []batchFailure{}}
	for _, p := range req.Paths {
		res.add(p, fsys.Remove(p))
	}
	c.JSON(http.StatusOK, res)
}

func (a *API) fsSearch(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		fail(c, badRequest("缺少搜索关键字"))
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	kind := c.Query("kind")
	if kind == "all" {
		kind = ""
	}
	c.JSON(http.StatusOK, a.Search.Search(a.fs(c), c.DefaultQuery("path", "/"), q, kind, limit))
}
