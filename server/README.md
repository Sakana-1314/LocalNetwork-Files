# MList · 后端

Go（Gin）服务端，模块 `github.com/Sakana-1314/MList/server`。项目介绍、部署与使用说明见[仓库根目录 README](../README.md)。

## 命令

```bash
make build        # 编译到 bin/mlist（内嵌 internal/webui/dist 中已构建的前端）
make web          # 构建前端（../web，pnpm）
make all          # web + build
make run          # 编译并运行，存在 config.yaml 时使用它
make test         # go test -race ./...
make vet          # go vet ./...
make dist         # CGO_ENABLED=0 交叉编译 linux/amd64、linux/arm64、darwin/arm64、windows/amd64 → bin/dist/
```

`go run ./cmd/mlist -config config.yaml` 亦可；`-version` 打印版本（由 `-ldflags "-X main.version=..."` 注入）。

## 包结构

| 包 | 职责 |
| --- | --- |
| `cmd/mlist` | 入口：解析参数、加载配置、设置日志、处理 SIGINT/SIGTERM |
| `internal/config` | YAML 配置与默认值：监听、数据目录、服务开关与端口、转码、缓存、搜索（带 `Normalize` 校验），`MLIST_LISTEN` / `MLIST_DATA_DIR` 环境变量覆盖 |
| `internal/store` | GORM + SQLite（纯 Go 驱动）：用户、桶（Bucket，一个或多个互为备份的目录）、授权（Grant）、访问令牌、S3 密钥；`Subscribe` 发布 `EventUsersChanged` |
| `internal/auth` | bcrypt 密码、会话 JWT、`ml_` API 令牌（`read`/`write` 范围，与 S3 密钥一样 AES-GCM 加密存储、可再次读取）、WebDAV 用的 Basic 认证（带短期缓存）；`OnPassword` 回调把明文密码交给 SMB 同步 |
| `internal/vfs` | 每用户虚拟文件系统：`/<mount>/<rel>` → 磁盘路径；路径清理、符号链接约束、只读检查、原子上传（`.lnf-upload-*` 临时文件）；桶的多目录备份（`replica.go`：写入即复制到备份目录，`Reconcile` 只增不删地合并，`FindConflicts` 检测同名冲突）。**REST / WebDAV / S3 的唯一权限执行层** |
| `internal/search` | 内存文件名索引（按桶的主目录分根）、fzf 风格模糊匹配与打分、vfs 写入通知 + fsnotify 监听 + 定时重建 |
| `internal/media` | ffprobe 探测、图片 / 视频预览图、HLS 实时转码会话（按需分片、拖动重启、硬件编码回退）、音频转码流、字幕转 WebVTT；缓存以内容指纹（大小 + 首尾各 64 KiB 的 sha256）为 key，与文件路径无关 |
| `internal/cache` | 缓存目录布局（预览图 / 转码两个桶）、按 mtime 记录访问、TTL（0 = 永不过期）+ 容量上限（LRU）定时清理，只清理过期项；含一次性 key 布局迁移 |
| `internal/services/webdav` | `golang.org/x/net/webdav` 适配 vfs，挂在主 HTTP 端口的 `/dav` |
| `internal/services/s3` | S3 兼容接口（SigV4、path/virtual-host 风格、分片上传），独立端口，按配置启动 |
| `internal/services/smb` | 托管 Samba `smbd` 子进程：由用户授权桶生成 `smb.conf`（`conf.go` 纯函数）、SIGHUP 重载、端口变化重启、退出状态上报、`smbpasswd` 同步密码 |
| `internal/api` | Gin REST 接口（`/api/v1`）、认证中间件（Bearer / `access_token` / Cookie）、登录限流、内嵌由 Go 注解生成的 `openapi.json`（`/api/openapi.json`） |
| `internal/app` | 组装各子系统、订阅用户 / 桶变化刷新搜索与 Samba、HTTP 服务与优雅退出 |
| `internal/webui` | `go:embed` 前端产物（`dist/`，由 `web/` 的 `pnpm build` 生成），SPA 路由回退到 `index.html` |

## 开发说明

- **新增文件操作必须走 `vfs`**，不要在 API / 协议层直接拼接磁盘路径，否则会绕过权限检查。
- 新增 / 修改接口时，在 handler 上方补写中文注解（`@Summary`/`@Tags`/`@Param`/`@Success` 等），
  字段描述写在结构体字段的行尾注释里，然后运行 `make docs` 重新生成 `internal/api/openapi.json`。
  该文件是生成物，**禁止手写**：CI 会用 `scripts/check-openapi.sh` 重新生成并比对，不一致即失败。
  前端类型与 Apifox 在线文档都以它为准；配置仓库 Secret `APIFOX_TOKEN` 后由
  `.github/workflows/apifox-sync.yml` 自动同步到 Apifox，也可本地 `APIFOX_TOKEN=xxx ./server/scripts/sync-apifox.sh`。
- 新增配置项：加到 `config.Config` 的对应分组，在 `Normalize` 中给出默认值与范围，并在 `config.example.yaml` 中写明中文注释；
  配置只在启动时读取，改动需重启生效。
- 依赖 ffmpeg 的测试在找不到 ffmpeg 时应跳过；CI 会安装 ffmpeg。
- SMB 的配置生成可在任何平台单测（`go test ./internal/services/smb/`），进程管理测试使用一个伪造的 `smbd` 脚本；真正的 Samba 只能在 Linux 上运行（以 root）。
- 前端未构建时 `internal/webui/dist` 只有 `.gitkeep`，服务端会显示占位页面，API 不受影响。
- 构建产物 `bin/`、本地 `config.yaml`、`data/` 已在 `.gitignore` 中忽略。

## 部署文件

- `Dockerfile`：多阶段构建（node:alpine → golang:alpine 交叉编译 → alpine + ffmpeg + Samba），构建上下文为仓库根目录：`docker build -f server/Dockerfile .`；忽略规则在 `Dockerfile.dockerignore`。基础镜像选 Alpine 是为了避开 Debian 版 ffmpeg 带入的 Mesa/LLVM 依赖链（代价是 Alpine 的 ffmpeg 不含 NVENC）；由 `.github/workflows/docker.yml` 构建并推送到 ghcr.io。
- `deploy/config.docker.yaml`：镜像内置配置。
- `deploy/mlist.service`：systemd unit。
