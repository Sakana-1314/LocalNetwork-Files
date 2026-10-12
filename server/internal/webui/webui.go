// Package webui serves the compiled web frontend (built from ../web into
// dist/ by `pnpm build`) as a single-page application.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

const placeholder = `<!doctype html><meta charset="utf-8"><title>MList</title>
<body style="font-family:sans-serif;padding:40px;color-scheme:light dark">
<h2>前端尚未构建</h2><p>请在 <code>web/</code> 目录执行 <code>pnpm install && pnpm build</code> 后重新编译服务端。</p>
<p>API 文档: <a href="/api/openapi.json">/api/openapi.json</a></p></body>`

// Handler serves static assets, falling back to index.html for client-side routes.
func Handler() http.Handler {
	sub, _ := fs.Sub(dist, "dist")
	files := http.FileServer(http.FS(sub))
	index, indexErr := fs.ReadFile(sub, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if fi, err := fs.Stat(sub, p); err == nil && !fi.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					// Vite emits content-hashed file names.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if indexErr != nil {
			w.Write([]byte(placeholder))
			return
		}
		w.Write(index)
	})
}
