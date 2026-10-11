# LocalNetwork Files · 后端

Go（Gin）服务端，模块 `github.com/Sakana-1314/LocalNetwork-Files/server`。项目介绍、部署与使用说明见[仓库根目录 README](../README.md)。

## 命令

```bash
make build        # 编译到 bin/lnfiles（内嵌 internal/webui/dist 中已构建的前端）
make web          # 构建前端（../web，pnpm）
make all          # web + build
make run          # 编译并运行，存在 config.yaml 时使用它
make test         # go test -race ./...
make vet          # go vet ./...
make dist         # CGO_ENABLED=0 交叉编译 linux/amd64、linux/arm64、darwin/arm64、windows/amd64 → bin/dist/
```

`go run ./cmd/lnfiles -config config.yaml` 亦可；`-version` 打印版本（由 `-ldflags "-X main.version=..."` 注入）。

## 包结构

| 包 | 职责 |
| --- | --- |
| `cmd/lnfiles` | 入口：解析参数、加载配置、设置日志、处理 SIGINT/SIGTERM |
| `internal/config` | 静态 YAML 配置与默认值，`LNF_LISTEN` / `LNF_DATA_DIR` 环境变量覆盖 |
| `internal/store` | GORM + SQLite（纯 Go 驱动）：用户、授权目录（Mount）、访问令牌、S3 密钥、KV；运行时设置 `Settings`（带 `Normalize` 校验）；`Subscribe` 发布 `EventUsersChanged` / `EventSettingsChanged` |
| `internal/auth` | bcrypt 密码、会话 JWT、`lnf_` API 令牌（只存哈希，`read`/`write` 范围）、S3 密钥（AES-GCM 加密存储）、WebDAV 用的 Basic 认证（带短期缓存）；`OnPassword` 回调把明文密码交给 SMB 同步 |
| `internal/vfs` | 每用户虚拟文件系统：`/<mount>/<rel>` → 磁盘路径；路径清理、符号链接约束、只读检查、原子上传（`.lnf-upload-*` 临时文件）。**REST / WebDAV / S3 的唯一权限执行层** |
| `internal/search` | 内存文件名索引（按顶层授权目录分根）、fzf 风格模糊匹配与打分、vfs 写入通知 + fsnotify 监听 + 定时重建 |
| `internal/media` | ffprobe 探测、图片 / 视频预览图、HLS 实时转码会话（按需分片、拖动重启、硬件编码回退）、音频转码流、字幕转 WebVTT |
| `internal/cache` | 缓存目录布局（预览图 / 转码两个桶）、按 mtime 记录访问、TTL + 容量上限（LRU）定时清理 |
| `internal/services/webdav` | `golang.org/x/net/webdav` 适配 vfs，挂在主 HTTP 端口的 `/dav` |
| `internal/services/s3` | S3 兼容接口（SigV4、path/virtual-host 风格、分片上传），独立端口，按设置启动 / 重启 / 停止 |
| `internal/services/smb` | 托管 Samba `smbd` 子进程：由用户授权目录生成 `smb.conf`（`conf.go` 纯函数）、SIGHUP 重载、端口变化重启、退出状态上报、`smbpasswd` 同步密码 |
| `internal/api` | Gin REST 接口（`/api/v1`）、认证中间件（Bearer / `access_token` / Cookie）、登录限流、内嵌 `openapi.yaml`（`/api/openapi.json`） |
| `internal/app` | 组装各子系统、订阅 store 事件刷新服务、HTTP 服务与优雅退出 |
| `internal/webui` | `go:embed` 前端产物（`dist/`，由 `web/` 的 `pnpm build` 生成），SPA 路由回退到 `index.html` |

## 开发说明

- **新增文件操作必须走 `vfs`**，不要在 API / 协议层直接拼接磁盘路径，否则会绕过权限检查。
- 新增 / 修改接口时同步更新 `internal/api/openapi.yaml`，前端类型与 Apifox 在线文档都以它为准；
  改完用 `APIFOX_TOKEN=xxx ./server/scripts/sync-apifox.sh` 推送（「API 文档」页面内嵌 Apifox）。
- 新增运行时选项：加到 `store.Settings` 并在 `Normalize` 中给出默认值与范围；需要立即生效的在 `app` 的 `EventSettingsChanged` 处理中应用。
- 静态配置项：加到 `config.Config` 并在 `config.example.yaml` 中写明（中文注释）。
- 依赖 ffmpeg 的测试在找不到 ffmpeg 时应跳过；CI 会安装 ffmpeg。
- SMB 的配置生成可在任何平台单测（`go test ./internal/services/smb/`），进程管理测试使用一个伪造的 `smbd` 脚本；真正的 Samba 只能在 Linux 上运行（以 root）。
- 前端未构建时 `internal/webui/dist` 只有 `.gitkeep`，服务端会显示占位页面，API 不受影响。
- 构建产物 `bin/`、本地 `config.yaml`、`data/` 已在 `.gitignore` 中忽略。

## 部署文件

- `Dockerfile`：多阶段构建（node:alpine → golang:alpine 交叉编译 → alpine + ffmpeg + Samba），构建上下文为仓库根目录：`docker build -f server/Dockerfile .`；忽略规则在 `Dockerfile.dockerignore`。基础镜像选 Alpine 是为了避开 Debian 版 ffmpeg 带入的 Mesa/LLVM 依赖链（代价是 Alpine 的 ffmpeg 不含 NVENC）；由 `.github/workflows/docker.yml` 构建并推送到 ghcr.io。
- `deploy/config.docker.yaml`：镜像内置配置。
- `deploy/lnfiles.service`：systemd unit。
