# 配置

完整说明见 [`server/config.example.yaml`](https://github.com/Sakana-1314/MList/blob/main/server/config.example.yaml)。

**所有配置都在配置文件里，修改后重启进程生效**（没有「系统设置」表单）。
升级到本版本时，原先保存在数据库里的运行时选项不再生效，请把需要保留的值写进 `config.yaml`，
否则回落到默认值。

| 配置 | 默认值 | 说明 |
| --- | --- | --- |
| `listen` | `:8080` | Web / API / WebDAV 监听地址（`MLIST_LISTEN`） |
| `data_dir` | `./data` | 数据库、密钥、Samba 状态（`MLIST_DATA_DIR`） |
| `cache_dir` | `<data_dir>/cache` | 预览图与转码分片 |
| `ffmpeg` / `ffprobe` | `ffmpeg` / `ffprobe` | 可执行文件路径 |
| `session_ttl` | `168h` | 网页登录有效期 |
| `admin.username` / `admin.password` | `admin` / `admin123` | 初始管理员，仅在没有任何用户时创建 |
| `smb.*` | | smbd / smbpasswd 路径、自动创建系统账户、账户名前缀、force user |
| `services.*` | 见示例 | WebDAV / S3 / SMB 开关、S3 / SMB 端口、SMB 工作组 |
| `transcode.*` | 见示例 | 编码器、编码预设、最大并发转码数、分片时长 |
| `cache.*` | 见示例 | 预览图 / 转码缓存的保留时长（0 = 永不过期）与容量上限、清理间隔 |
| `search.*` | 见示例 | 定时重建索引间隔、文件系统监听 |
| `log_level` | `info` | debug / info / warn / error |

## 缓存

- `cache.thumb_ttl_hours` / `cache.transcode_ttl_hours`：在最后一次访问后保留多久，
  `0` = 永不过期（只在超出容量上限时按 LRU 淘汰）。
- `cache.thumb_max_mb` / `cache.transcode_max_mb`：容量上限。
- `cache.cleanup_interval_minutes`：后台清理间隔。

「清理过期」只删除已过期项，不会因为清理而丢掉仍可复用的缓存。

## 转码

- `transcode.encoder`：`libx264` / `h264_videotoolbox` / `h264_nvenc` / `h264_qsv` / `h264_vaapi`。
  用户选择 H.265 输出时使用同一硬件的 HEVC 编码器（软件编码为 libx265）。
- `transcode.preset`：越快画质越低（`ultrafast` … `medium`）。
- `transcode.max_sessions`：同时运行的 ffmpeg 进程上限。
- `transcode.segment_seconds`：HLS 分片时长（2–20 秒）。

## 搜索

- `search.reindex_interval_minutes`：定时全量重建间隔（`0` 关闭）。
- `search.watch`：是否用文件系统事件增量更新。
