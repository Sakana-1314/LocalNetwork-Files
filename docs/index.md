---
layout: home

hero:
  name: MList
  text: 局域网文件管理
  tagline: 把服务器上的目录按用户授权，通过 Web / WebDAV / S3 / SMB / REST API 访问
  actions:
    - theme: brand
      text: 快速开始
      link: /guide/quickstart
    - theme: alt
      text: 在线 Demo
      link: /demo
    - theme: alt
      text: 功能

features:
  - title: 文件管理
    details: 浏览（列表 / 网格）、新建、重命名、移动、复制、删除、下载；拖放与多文件上传队列。
  - title: 在线预览
    details: 图片、视频、音频、PDF、文本 / 代码（可编辑）；视频支持多音轨与内嵌 / 外挂字幕。
  - title: 实时转码串流
    details: 浏览器无法直接播放的视频（MKV、HEVC、AVI…）实时转 HLS，分片按需生成，可随意拖动进度。
  - title: 多协议访问
    details: WebDAV（/dav）、S3 兼容接口（每个授权桶一个 bucket）、SMB（托管 Samba），共用同一套权限检查。
  - title: 用户与权限
    details: 管理员按目录授权并设置只读 / 可读写；带 read / write 范围与有效期的 API 令牌。
  - title: 预览图与缓存治理
    details: 自动生成多尺寸缩略图；预览图与转码分片分别设保留时长与容量上限（LRU 淘汰）。
  - title: 模糊搜索
    details: 常驻内存的文件名索引，fzf 风格子序列匹配打分，只返回当前用户有权访问的文件。
  - title: 单文件部署
    details: Go + SQLite（纯 Go 驱动，无 CGO），前端构建后打包成单个可执行文件。
---
