# Docker 与镜像

## 运行

镜像已发布在 GitHub Container Registry（公开，无需登录即可拉取）：

```bash
docker pull ghcr.io/sakana-1314/mlist:latest

docker run -d --name mlist \
  -p 8080:8080 -p 9000:9000 -p 445:445 \
  -v mlist-data:/data \
  -v /srv/media:/media \
  ghcr.io/sakana-1314/mlist:latest
```

镜像名必须**全小写**。标签：`latest`、`main`、`sha-<短提交>`，版本标签 `1.2`、`1.2.3`。

自建镜像（构建上下文是**仓库根目录**）：

```bash
docker build -f server/Dockerfile -t mlist .
```

## 发布流程

[`.github/workflows/docker.yml`](https://github.com/Sakana-1314/MList/blob/main/.github/workflows/docker.yml)
构建并推送镜像到 ghcr.io：

- push 到 `main`：推送 `latest` / `main` / `sha-<短提交>`
- push 版本标签 `v1.2.3`：推送 `1.2.3` / `1.2` / `latest`
- Pull Request：**只构建不推送**
- 也支持手动触发

流程分两个任务：`build` 构建并推送 `linux/amd64` + `linux/arm64` 多架构镜像；
`verify` 单独构建 amd64 镜像并**实际运行**——检查 ffmpeg / smbd / smbpasswd / heif-convert / useradd
是否存在，启动容器探测 Web 与 OpenAPI 端点、确认 API 返回 401，并报告镜像体积。

推送使用仓库自带的 `GITHUB_TOKEN`，无需额外密钥。ghcr.io 要求镜像名全小写，工作流用
`${GITHUB_REPOSITORY,,}` 自动转换。首次推送后若包是私有的，需在
**Packages → 目标包 → Package settings** 中改为 Public（或让使用者用 PAT 登录 ghcr.io）。

部署时可用镜像标签 `org.opencontainers.image.revision` 核对部署的是哪个提交。

## 镜像体积

基础镜像基于 **Alpine**。已发布镜像的实测体积：

| 平台 | 未压缩 | 从 ghcr.io 拉取（压缩） |
| --- | --- | --- |
| linux/amd64 | 211 MiB | 80.0 MiB |
| linux/arm64 | — | 77.6 MiB |

这是权衡的结果：

- Debian 官方 `ffmpeg` **硬性依赖** SDL2 → Mesa → LLVM 整条图形链（三个包压缩后共约 42 MiB，
  无法剥离）：仅 apt 依赖层就约 165 MiB（压缩）。
- Alpine 的 `ffmpeg` 只依赖真正用到的编解码库，同口径约 66 MiB（压缩），镜像体积约为 Debian 方案的
  三分之一。
- 代价：Alpine 的 ffmpeg **不含 NVIDIA NVENC**。选 `h264_nvenc` 时会自动回退到 libx264；
  `h264_vaapi`（Intel / AMD）与软件编码可用。需要 NVENC 请改用 Debian 基础镜像或静态 ffmpeg。

构建还采取了：多阶段构建只把二进制与配置带入运行层；构建阶段固定在 `BUILDPLATFORM` 原生执行并
交叉编译 Go，多架构构建不为 arm64 走 QEMU；BuildKit 缓存挂载与 GHA 层缓存；`apk add --no-cache`；
Go 用 `-trimpath -ldflags "-s -w"`。CI 中设有 400 MiB 体积护栏。
