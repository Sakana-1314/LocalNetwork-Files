# 快速开始

推荐用 Docker 运行。镜像已发布在 GitHub Container Registry（公开，无需登录即可拉取），
支持 linux/amd64 与 linux/arm64：

```bash
docker pull ghcr.io/sakana-1314/mlist:latest

docker run -d --name mlist \
  -p 8080:8080 -p 9000:9000 -p 445:445 \
  -v mlist-data:/data \
  -v /srv/media:/media \
  ghcr.io/sakana-1314/mlist:latest
```

镜像名必须**全小写**。可用标签：`latest`、`main`、`sha-<短提交>`，以及版本标签 `1.2`、`1.2.3`。

端口与数据：

- `8080` Web / REST API / WebDAV，`9000` S3，`445` SMB。
- 数据保存在 `/data`（数据库、密钥、Samba 状态），**请务必持久化并纳入备份**。
- 内置配置 `/etc/mlist/config.yaml`，可挂载自己的覆盖：
  `-v ./config.yaml:/etc/mlist/config.yaml:ro`。

容器默认以 root 运行（`smbd` 监听 445、自动创建 SMB 系统账户都需要 root）；不用 SMB 时可用普通用户运行。

## 首次使用

1. 浏览器打开 `http://<服务器IP>:8080`，用默认账户 `admin` / `admin123` 登录。
2. **立即在右上角「账号设置」修改密码**。
3. 在「系统管理」创建桶（对应服务器目录，可指定多个目录互为备份）。
4. 在「用户管理」创建用户并授权桶——**管理员自己也需要授权桶才能看到文件**。

## 下一步

- [功能](#功能) 了解各项能力
- [挂载与服务](/guide/mount) 把 WebDAV / S3 / SMB 接到本机
- [配置](/guide/config) 调整监听、转码、缓存与搜索
- [在线 Demo](/demo) 不用安装，直接试用界面
