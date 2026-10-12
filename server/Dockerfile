# syntax=docker/dockerfile:1
#
# MList 容器镜像。构建上下文必须是仓库根目录：
#
#   docker build -f server/Dockerfile -t mlist .
#   docker run -d --name mlist \
#     -p 8080:8080 -p 9000:9000 -p 445:445 \
#     -v mlist-data:/data -v /path/to/media:/media \
#     mlist
#
# 然后在管理界面中把 /media 下的目录授权给用户。
#
# 体积：基础镜像选用 Alpine 而非 Debian。Debian 的官方 ffmpeg 会硬性依赖
# SDL2 → Mesa → LLVM 整条图形链（三个包压缩后约 42 MB，无法剥离）；
# Alpine 只依赖实际用到的编解码库，镜像体积约为 Debian 方案的三分之一。
# 代价：Alpine 的 ffmpeg 不含 NVIDIA NVENC 编码器；选择 h264_nvenc 时会自动
# 回退到 libx264（见 internal/media/hls.go）。

ARG NODE_VERSION=22
ARG GO_VERSION=1.26
ARG ALPINE_VERSION=3.22
ARG PNPM_VERSION=10

# ---------- web UI ----------
# 两个构建阶段都固定在 BUILDPLATFORM 上「原生」运行：产出与平台无关，
# 多架构构建时不会为 arm64 再跑一遍 QEMU 模拟。
FROM --platform=${BUILDPLATFORM} node:${NODE_VERSION}-alpine AS web
ARG PNPM_VERSION
# COREPACK_ENABLE_DOWNLOAD_PROMPT=0：容器内非交互，避免 corepack 等待确认。
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable && corepack prepare pnpm@${PNPM_VERSION} --activate && pnpm --version
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile
COPY web/ ./
# vite 把产物写到 ../server/internal/webui/dist
RUN pnpm build

# ---------- server binary ----------
FROM --platform=${BUILDPLATFORM} golang:${GO_VERSION}-alpine AS server
WORKDIR /src/server
# go.mod 是纯 Go 依赖（SQLite 驱动无 CGO），可以交叉编译成静态二进制。
COPY server/go.mod server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY server/ ./
COPY --from=web /src/server/internal/webui/dist ./internal/webui/dist
ARG TARGETOS TARGETARCH
ARG VERSION=docker
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/mlist ./cmd/mlist

# ---------- runtime ----------
FROM alpine:${ALPINE_VERSION}

# 运行期依赖，按用途分组：
#   ffmpeg              预览图、HLS 实时转码、音频流、字幕转换
#   samba-server        smbd（SMB 共享；无此包则 SMB 功能不可用）
#   samba-common-tools  smbpasswd（同步用户密码）
#   libheif-tools       heif-convert，ffmpeg 无法解码的 HEIC/HEIF 预览图
#   shadow              useradd（smb.create_system_users 自动建账户）
#   tzdata              容器时区（TZ 环境变量）
#   ca-certificates     HTTPS（外部 S3 端点等）
# 不含 samba-client：应用只启动 smbd / 调用 smbpasswd，不需要客户端工具。
# --no-cache 让 apk 不保留索引，装完立即释放。
RUN apk add --no-cache \
      ffmpeg \
      samba-server \
      samba-common-tools \
      libheif-tools \
      shadow \
      tzdata \
      ca-certificates \
 && rm -rf /var/cache/apk/*

COPY --from=server /out/mlist /usr/local/bin/mlist
COPY server/deploy/config.docker.yaml /etc/mlist/config.yaml

# Samba 需要 /var/lib/samba 等可写目录；数据与状态都放在 /data 下。
VOLUME ["/data"]
# 8080: Web / REST API / WebDAV, 9000: S3, 445: SMB
EXPOSE 8080 9000 445
# 以 root 运行：smbd 监听 445 端口，且创建 SMB 系统账户需要 root。
ENTRYPOINT ["/usr/local/bin/mlist"]
CMD ["-config", "/etc/mlist/config.yaml"]
