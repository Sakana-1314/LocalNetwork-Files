# 挂载与服务

登录后打开「挂载」页面，会按当前用户与服务器地址生成可直接复制的命令。

## WebDAV

地址：`http://<服务器>:8080/dav/`，用登录密码或 API 令牌（只读令牌即只读挂载）。

Windows 资源管理器默认只允许 HTTPS 上的 Basic 认证，通过 HTTP 访问需先改注册表再重启 WebClient：

```bat
reg add HKLM\SYSTEM\CurrentControlSet\Services\WebClient\Parameters /v BasicAuthLevel /t REG_DWORD /d 2 /f
net stop webclient && net start webclient
net use Z: http://<服务器>:8080/dav/ /user:<用户名> *
```

Linux 用 davfs2 挂载：

```bash
sudo apt install davfs2
sudo mkdir -p /mnt/mlist
sudo mount -t davfs http://<服务器>:8080/dav/ /mnt/mlist -o uid=$(id -u),gid=$(id -g)
```

## SMB

由本程序托管独立的 Samba `smbd`（配置与密码库在 `<data_dir>/samba/`，不影响系统 Samba）。

- 需要 Linux + Samba，并以 root 运行。
- macOS 自带的 `smbd` 不是 Samba，不支持 Windows 客户端。
- 每个授权桶是一个共享（指向桶的主目录）。
- Samba 只能认证系统账户：可开启 `smb.create_system_users` 自动创建，
  或用 `smb.user_prefix` 避免与已有系统用户冲突。
- **开启 SMB 后用户需重新登录一次**，密码才会同步到 Samba。

```bash
# Windows
net use Z: \\<服务器>\<共享名> /user:<用户名> *
# macOS
open "smb://<用户名>@<服务器>/<共享名>"
# Linux
sudo mount -t cifs //<服务器>/<共享名> /mnt/<共享名> -o username=<用户名>,vers=3.0
```

## S3

端点 `http://<服务器>:9000`（端口可改），path-style。
**区域不需要设置，客户端填任意值都会被忽略。**

在「设置 → S3 密钥」创建密钥，**每个授权桶是一个 bucket**（建议只用小写字母、数字和 `-`）。

支持 ListBuckets、ListObjects(V1/V2)、Get/Head/Put/Copy/Delete Object、批量删除、分片上传。

```bash
aws --endpoint-url http://192.168.1.10:9000 s3 cp ./a.mp4 s3://movies/2024/a.mp4

# rclone：type = s3 / provider = Other / endpoint = http://192.168.1.10:9000 / force_path_style = true
rclone mount mlist:movies /mnt/movies --vfs-cache-mode writes
```

## REST API

见 [API 文档](/api)。认证用 `Authorization: Bearer <token>`；无法设置请求头的场景
（`<video>`、HLS）可用 `access_token=` 查询参数。缺少 `write` 范围的令牌只能只读操作。
虚拟路径 `/` 下一级是授权桶名。

```bash
TOKEN=ml_xxxxxxxx                  # 在「设置 → 访问令牌」中创建
H="Authorization: Bearer $TOKEN"
BASE=http://192.168.1.10:8080/api/v1

curl -H "$H" "$BASE/fs/list?path=/movies"                                      # 列目录
curl -H "$H" -G "$BASE/fs/search" --data-urlencode "q=avengers" -d kind=video  # 模糊搜索
curl -H "$H" -o a.mkv -G "$BASE/fs/raw" --data-urlencode "path=/movies/a.mkv"  # 下载（支持 Range）
curl -H "$H" -T notes.txt "$BASE/fs/file?path=/docs/notes.txt&overwrite=true"  # 上传
```
