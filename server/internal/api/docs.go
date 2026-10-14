// Package api is the REST API (Gin) consumed by the web UI and by OpenAPI
// clients using access tokens.
//
// 本文件只放 OpenAPI 的全局信息（general info），**不要**在此文件里写任何
// operation 注解（路由、摘要、参数等）。规范由注解生成，禁止手写：
//
//	go generate ./internal/api     # 或 make docs
//
// 两个已实测的 swag 行为，改本文件时必须遵守：
//
//  1. 全局信息必须独立成文件。若与 operation 注解同处一个文件，swag 会把
//     某个 operation 的鉴权注解误当作全局 security 提升，导致公开接口
//     （登录、退出、预览图）在规范里被标成需要鉴权。
//  2. 分组注解必须写在鉴权定义注解之前。swag v2 的鉴权定义块会吞掉紧随
//     其后的分组注解，使分组描述整段丢失（产物里连顶层 tags 都没有）。
//  3. 注释正文里不要出现以 @ 开头的注解名。swag 会把它们当成真注解解析，
//     例如正文里写一句「@securityDefinitions 要放在 @tag 之前」就会被误读成
//     鉴权定义本身，从而报错或吞掉后面的注解。
//
// @title       局域网文件管理 API
// @version     1.0.0
// @description 局域网文件管理系统 HTTP API。
//
// @description **认证方式**（任选其一）：
// @description - `Authorization: Bearer <token>`，token 可以是登录得到的会话令牌，或在「账号设置 → 访问令牌」中创建的 API 访问令牌（`ml_` 前缀）。
// @description - 查询参数 `access_token=<token>`（用于 `<img>` / `<video>` 等无法设置请求头的场景）。
// @description - 浏览器登录后自动携带的会话 Cookie。
// @description
// @description **路径**：所有文件路径均为虚拟路径。`/` 为根，下一级是当前用户被授权的桶（桶名），例如 `/电影/2024/a.mkv`。
// @description
// @description **预览图**：`GET /media/thumb` 只接收内容摘要与签名，不接收文件路径，且不要求登录，因此可直接放进 `<img>` 标签。
// @description
// @description **错误**：非 2xx 响应体为 `{"error": "message"}`。
//
// @servers.url /api/v1
//
// @externalDocs.description 项目文档
// @externalDocs.url         https://github.com/Sakana-1314/MList
//
// @tag.name        认证
// @tag.description 登录、退出与当前用户信息
// @tag.name        文件
// @tag.description 目录浏览、上传下载、重命名、移动复制、删除与搜索
// @tag.name        媒体
// @tag.description 预览图、媒体信息、HLS 实时转码、音频转码与字幕转换
// @tag.name        分享
// @tag.description 文件夹分享链接：管理自己的分享，以及无需登录的只读访问
// @tag.name        访问凭据
// @tag.description 访问令牌与 S3 访问密钥（仅当前用户自己的凭据）
// @tag.name        系统
// @tag.description 系统与服务状态（用于挂载向导）
// @tag.name        管理
// @tag.description 用户与授权桶、桶与目录、缓存与搜索索引，需要管理员权限
// @securityDefinitions.apikey bearerAuth
// @in                         header
// @name                       Authorization
// @description                会话令牌或 API 访问令牌，形如 `Authorization: Bearer <token>`
package api

//go:generate ../../scripts/gen-openapi.sh
