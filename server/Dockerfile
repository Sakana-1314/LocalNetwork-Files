# syntax=docker/dockerfile:1
#
# LocalNetwork Files 容器镜像。构建上下文必须是仓库根目录：
#
#   docker build -f server/Dockerfile -t lnfiles .
#   docker run -d --name lnfiles \
#     -p 8080:8080 -p 9000:9000 -p 445:445 \
#     -v lnfiles-data:/data -v /path/to/media:/media \
#     lnfiles
#
# 然后在管理界面中把 /media 下的目录授权给用户。

ARG NODE_VERSION=22
ARG GO_VERSION=1.27

# ---------- web UI ----------
FROM node:${NODE_VERSION}-bookworm-slim AS web
RUN npm install -g pnpm@10
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
# vite writes the bundle to ../server/internal/webui/dist
RUN pnpm build

# ---------- server binary ----------
FROM golang:${GO_VERSION}-bookworm AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web /src/server/internal/webui/dist ./internal/webui/dist
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/lnfiles ./cmd/lnfiles

# ---------- runtime ----------
FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata ffmpeg samba \
 && rm -rf /var/lib/apt/lists/*
COPY --from=server /out/lnfiles /usr/local/bin/lnfiles
COPY server/deploy/config.docker.yaml /etc/lnfiles/config.yaml
VOLUME ["/data"]
# 8080: Web / REST API / WebDAV, 9000: S3, 445: SMB
EXPOSE 8080 9000 445
# Runs as root: smbd and the creation of SMB system accounts require it.
ENTRYPOINT ["/usr/local/bin/lnfiles"]
CMD ["-config", "/etc/lnfiles/config.yaml"]
