package api

import (
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/Sakana-1314/MList/server/internal/vfs"
)

// @Summary     列出目录
// @Description 返回该目录下的文件与子目录；省略 path 时为虚拟根目录 /。
// @Description 每个条目带预览图摘要 thumb 与签名 thumb_sig，可直接用于获取预览图接口。
// @Tags     文件
// @Produce  json
// @Param    path query string false "目录的虚拟路径，省略时为根目录 /" default(/)
// @Success  200 {object} ListResponse "该目录下的条目列表"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  404 {object} ErrorResponse "目录不存在"
// @Security bearerAuth
// @Router   /fs/list [get]
func (a *API) fsList(c *gin.Context) {
	p := c.DefaultQuery("path", "/")
	entries, writable, err := a.fs(c).List(p)
	if err != nil {
		fail(c, err)
		return
	}
	a.signEntries(entries)
	c.JSON(http.StatusOK, ListResponse{Path: vfs.CleanPath(p), Writable: writable, Entries: entries})
}

// @Summary     获取文件信息
// @Description 返回单个文件或目录的条目信息，字段与列出目录的条目一致。
// @Tags     文件
// @Produce  json
// @Param    path query string true "文件或目录的虚拟路径"
// @Success  200 {object} vfs.Entry "该文件或目录的信息"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  404 {object} ErrorResponse "文件或目录不存在"
// @Security bearerAuth
// @Router   /fs/stat [get]
func (a *API) fsStat(c *gin.Context) {
	e, err := a.fs(c).Stat(c.Query("path"))
	if err != nil {
		fail(c, err)
		return
	}
	a.signEntry(e)
	c.JSON(http.StatusOK, e)
}

// activeTypes can run script when rendered by the browser; they are sandboxed.
var activeTypes = map[string]bool{
	"text/html": true, "image/svg+xml": true, "application/xhtml+xml": true, "text/xml": true, "application/xml": true,
}

// @Summary     读取原始文件
// @Description 以流的形式返回文件内容，支持 Range 请求，可用于断点续传与拖动进度。
// @Description 响应头带 Content-Type 与 Accept-Ranges；download 为 true 时以附件形式下载（Content-Disposition: attachment）。
// @Description 对可能被浏览器执行脚本的类型会附加 CSP sandbox。
// @Tags     文件
// @Param    path query string true "文件的虚拟路径"
// @Param    download query boolean false "为 true 时以附件形式下载（Content-Disposition: attachment）"
// @Success  200 {string} binary "完整文件内容；响应头带 Content-Type 与 Accept-Ranges"
// @Success  206 {string} binary "Range 请求命中的部分内容"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  404 {object} ErrorResponse "文件不存在"
// @Security bearerAuth
// @Router   /fs/raw [get]
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

// @Summary     读取原始文件并显示文件名
// @Description 与读取原始文件接口行为完全相同，只是 URL 末尾带上文件名，便于浏览器保存或下载时显示真实文件名。
// @Description 末尾的名称由服务端忽略，实际读取的路径仍取自 path 参数。
// @Tags     文件
// @Param    name path string true "文件名，仅用于让浏览器显示真实文件名，服务端忽略"
// @Param    path query string true "文件的虚拟路径（实际使用的路径）"
// @Param    download query boolean false "为 true 时以附件形式下载"
// @Success  200 {string} binary "完整文件内容"
// @Success  206 {string} binary "Range 请求命中的部分内容"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  404 {object} ErrorResponse "文件不存在"
// @Security bearerAuth
// @Router   /fs/raw/{name} [get]
func (a *API) fsRawNamed(c *gin.Context) { a.fsRaw(c) }

const maxTextSize = 4 << 20

// @Summary     读取文本文件
// @Description 按 UTF-8 返回文件文本，最大读取 4 MiB。
// @Description 超出部分会被截断，并通过 truncated 标记。
// @Tags     文件
// @Produce  json
// @Param    path query string true "文本文件的虚拟路径"
// @Success  200 {object} TextResponse "文本内容"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  404 {object} ErrorResponse "文件不存在"
// @Security bearerAuth
// @Router   /fs/text [get]
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
	c.JSON(http.StatusOK, TextResponse{Content: content, Truncated: truncated})
}

// @Summary     上传文件
// @Description 请求体为文件内容，自动创建不存在的父目录。
// @Description overwrite 为 false（默认）时，目标已存在则返回 409。
// @Tags     文件
// @Accept   application/octet-stream
// @Produce  json
// @Param    path query string true "目标文件的完整虚拟路径"
// @Param    overwrite query boolean false "目标已存在时是否覆盖；为 false 时返回 409" default(false)
// @Param    body body string true "文件内容"
// @Success  201 {object} vfs.Entry "上传成功，返回新文件的条目信息"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "目录只读或令牌无 write 权限"
// @Failure  409 {object} ErrorResponse "目标文件已存在且未允许覆盖"
// @Security bearerAuth
// @Router   /fs/file [put]
func (a *API) fsPut(c *gin.Context) {
	overwrite, _ := strconv.ParseBool(c.DefaultQuery("overwrite", "false"))
	e, err := a.fs(c).WriteFile(c.Query("path"), c.Request.Body, overwrite)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, e)
}

// @Summary     新建目录
// @Description 创建目录，并自动创建不存在的父目录。
// @Tags     文件
// @Accept   json
// @Produce  json
// @Param    body body MkdirRequest true "新目录路径"
// @Success  201 {object} vfs.Entry "目录创建成功，返回新目录的条目信息"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "目录只读或令牌无 write 权限"
// @Security bearerAuth
// @Router   /fs/mkdir [post]
func (a *API) fsMkdir(c *gin.Context) {
	var req MkdirRequest
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

// @Summary     重命名
// @Description 在同一目录内重命名文件或目录，name 为不含路径的新名称。
// @Tags     文件
// @Accept   json
// @Produce  json
// @Param    body body RenameRequest true "原路径与新名称"
// @Success  200 {object} vfs.Entry "重命名成功，返回更新后的条目信息"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "目录只读或令牌无 write 权限"
// @Security bearerAuth
// @Router   /fs/rename [post]
func (a *API) fsRename(c *gin.Context) {
	var req RenameRequest
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
		if statusOf(err) >= 500 {
			slog.Error("batch operation failed", "path", p, "err", err)
		}
		b.Failed = append(b.Failed, batchFailure{p, publicMessage(err)})
	} else {
		b.Succeeded = append(b.Succeeded, p)
	}
}

func (a *API) transfer(c *gin.Context, op func(fsys *vfs.FS, src, dst string, overwrite bool) error) {
	var req TransferRequest
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

// @Summary     移动文件
// @Description 把多个条目移动到目标目录，可以跨授权目录。
// @Description overwrite 为 true 时覆盖同名目标；逐个条目返回成功与失败，部分失败不影响其余条目。
// @Tags     文件
// @Accept   json
// @Produce  json
// @Param    body body TransferRequest true "源路径、目标目录与是否覆盖"
// @Success  200 {object} BatchResult "每个条目的处理结果（成功与失败分别列出）"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "目录只读或令牌无 write 权限"
// @Security bearerAuth
// @Router   /fs/move [post]
func (a *API) fsMove(c *gin.Context) {
	a.transfer(c, func(fsys *vfs.FS, src, dst string, ow bool) error { return fsys.Move(src, dst, ow) })
}

// @Summary     复制文件
// @Description 把多个条目复制到目标目录。
// @Description overwrite 为 true 时覆盖同名目标；逐个条目返回成功与失败。
// @Tags     文件
// @Accept   json
// @Produce  json
// @Param    body body TransferRequest true "源路径、目标目录与是否覆盖"
// @Success  200 {object} BatchResult "每个条目的处理结果（成功与失败分别列出）"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "目录只读或令牌无 write 权限"
// @Security bearerAuth
// @Router   /fs/copy [post]
func (a *API) fsCopy(c *gin.Context) {
	a.transfer(c, func(fsys *vfs.FS, src, dst string, ow bool) error { return fsys.Copy(src, dst, ow) })
}

// @Summary     删除文件
// @Description 删除给定的条目，目录会被递归删除。
// @Description 逐个条目返回成功与失败。
// @Tags     文件
// @Accept   json
// @Produce  json
// @Param    body body DeleteRequest true "要删除的路径列表"
// @Success  200 {object} BatchResult "每个条目的处理结果（成功与失败分别列出）"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Failure  403 {object} ErrorResponse "目录只读或令牌无 write 权限"
// @Security bearerAuth
// @Router   /fs/delete [post]
func (a *API) fsDelete(c *gin.Context) {
	var req DeleteRequest
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

// @Summary     搜索文件名
// @Description 按子序列模糊匹配文件名，支持中文。
// @Description 可用 path 限定搜索范围、kind 限定类型；结果按相关度排序，并带匹配位置与预览图摘要。
// @Tags     文件
// @Produce  json
// @Param    q query string true "搜索关键字（支持中文；按子序列模糊匹配）"
// @Param    path query string false "仅在该目录（含子目录）下搜索，省略时为全部授权目录" default(/)
// @Param    kind query string false "只返回该类型的条目，省略时不限类型"
// @Param    limit query integer false "最多返回的结果条数，上限 500" default(100)
// @Success  200 {object} SearchResponse "按相关度排序的搜索结果"
// @Failure  401 {object} ErrorResponse "未登录或会话已失效"
// @Security bearerAuth
// @Router   /fs/search [get]
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
	resp := a.Search.Search(a.fs(c), c.DefaultQuery("path", "/"), q, kind, limit)
	for i := range resp.Results {
		a.signEntry(&resp.Results[i].Entry)
	}
	c.JSON(http.StatusOK, resp)
}
