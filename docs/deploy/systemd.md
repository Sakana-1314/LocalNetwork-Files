# systemd 与安全

## systemd 部署

```bash
sudo install -Dm755 server/bin/mlist /usr/local/bin/mlist
sudo install -Dm640 server/config.example.yaml /etc/mlist/config.yaml
sudo install -Dm644 server/deploy/mlist.service /etc/systemd/system/mlist.service
sudo systemctl daemon-reload && sudo systemctl enable --now mlist   # 数据在 /var/lib/mlist
```

## 安全

- 设计用于**可信局域网**，服务本身只提供 HTTP。跨网络访问请放在 HTTPS 反向代理（Caddy、Nginx）
  之后或使用 VPN；WebDAV / S3 / SMB 的凭据在 HTTP 上可被嗅探。
- 首次登录后务必修改默认密码；登录接口对同一 IP 有失败次数限制。
- 用户只能访问被授权的目录，路径穿越与指向外部的符号链接会被拒绝；只读授权对所有协议生效。
- API 令牌与 S3 密钥可随时吊销。
- 用户密码除 bcrypt 哈希外，还会用 `data_dir` 下的 `secret.key` 加密保存一份可逆副本，
  供管理员在用户管理中查看并转述给用户；登录校验始终只使用 bcrypt 哈希。
  请把 `data_dir` 视为机密目录。
- `data_dir` 含数据库与签名密钥，请限制访问权限并纳入备份。
