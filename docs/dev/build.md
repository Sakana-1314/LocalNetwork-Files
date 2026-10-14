# 从源码构建

需要 Go（版本见 `server/go.mod`）、Node.js 20.19+ / 22.12+ 与 pnpm 10（构建 Vite 8 前端）。

ffmpeg / ffprobe 与 libheif / libvips 为可选项（缺失时对应功能自动禁用），Samba 仅在 Linux 上用于 SMB。

```bash
cd server
make all                              # = make web（pnpm install && pnpm build）+ make build
cp config.example.yaml config.yaml
make run                              # 或 ./bin/mlist -config config.yaml
```

浏览器打开 `http://<服务器IP>:8080`。

其它目标：

| 命令 | 作用 |
| --- | --- |
| `make test` | `go test -race ./...` |
| `make vet` | `go vet ./...` |
| `make docs` | 由 Go 注解重新生成 `internal/api/openapi.json` |
| `make dist` | 交叉编译 linux/amd64、linux/arm64、darwin/arm64、windows/amd64 |
| `make clean` | 删除 `bin/` |

版本号取自 `git describe`，可用 `make build VERSION=1.2.3` 指定。

## 开发模式

```bash
# 终端 1：后端
cd server && go run ./cmd/mlist

# 终端 2：前端（热更新，/api 与 /dav 代理到 127.0.0.1:8080）
cd web && pnpm install && pnpm dev
```

未构建前端时后端仍可运行（显示占位页面，API 不受影响）。

## 前端构建期环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MLIST_BACKEND` | `http://127.0.0.1:8080` | 仅开发服务器：`/api`、`/dav` 的代理目标 |
| `WEB_BASE` | `/` | Vite `base`，部署到子路径时设置 |
| `WEB_OUT_DIR` | `../server/internal/webui/dist` | 产物目录（相对路径按 `web/` 解析） |
| `VITE_API_BASE` | `/api/v1` | API 前缀 |
| `VITE_APIFOX_DOC_URL` | `https://mlist.apifox.cn/` | 「API 文档」页内嵌的在线文档地址 |

## 仓库门禁

CI 执行 `gofmt -l .`、`go vet ./...`、`go test -race ./...`（server）与 `pnpm typecheck`、
`pnpm build`（web），另外会重新生成 OpenAPI 规范并与仓库中的文件比对，以及构建文档站。

本地 `pnpm` 可能被依赖供应链策略拦截，此时直接调用 `web/node_modules/.bin/` 下的二进制，
最终仍以 CI 结果为准。
