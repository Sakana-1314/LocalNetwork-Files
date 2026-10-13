# MList · Web 前端

Vite + Vue 3（`<script setup lang="ts">`）+ TypeScript（strict）+ Vue Router + Pinia + Naive UI。
接口契约见 `../server/internal/api/openapi.json`（由 Go 注解生成，Base URL `/api/v1`）。
「API 文档」页以 iframe 内嵌 Apifox 在线文档（默认 `https://mlist.apifox.cn/`，可用 `VITE_APIFOX_DOC_URL` 覆盖）；
规范变更后由 `.github/workflows/apifox-sync.yml` 自动同步（本地可用 `../server/scripts/sync-apifox.sh`）。

## 命令

```bash
pnpm install
pnpm dev         # 开发服务器，/api 与 /dav 代理到 http://127.0.0.1:8080（可用 MLIST_BACKEND 覆盖）
pnpm typecheck   # vue-tsc 类型检查（应用 + vite.config.ts）
pnpm build       # 类型检查 + 构建到 ../server/internal/webui/dist（构建后自动重建 .gitkeep）
```

构建产物由后端通过 `embed` 提供；生产环境需要后端对非 `/api`、`/dav` 的路径回退到 `index.html`（History 路由）。

## 构建期环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MLIST_BACKEND` | `http://127.0.0.1:8080` | 仅开发服务器：`/api`、`/dav` 的代理目标 |
| `WEB_BASE` | `/` | Vite `base`，部署到子路径时设置（如 `/MList/demo/`） |
| `WEB_OUT_DIR` | `../server/internal/webui/dist` | 产物目录（相对路径按 `web/` 解析） |
| `VITE_API_BASE` | `/api/v1` | API 前缀；指向 Apifox 云端 Mock 时填完整前缀 |
| `VITE_APIFOX_DOC_URL` | `https://mlist.apifox.cn/` | 「API 文档」页内嵌的在线文档地址 |

`pnpm build:demo` 与 `pnpm build` 相同，供演示站构建调用；示例：

```bash
WEB_BASE=/MList/demo/ WEB_OUT_DIR=../docs/public/demo \
  VITE_API_BASE=https://m1.apifoxmock.com/m1/<项目>-<版本>-<服务>/api/v1 \
  pnpm build:demo
```

## 目录结构

```
src/
  api/          类型化 API 客户端：http.ts（fetch 封装、401 处理、withToken）、token.ts、types.ts（对应 openapi schemas）
                以及按 tag 划分的 auth / fs / media / tokens / system / admin 模块
  stores/       Pinia：auth（会话）、prefs（主题/视图偏好，localStorage）、uploads（上传队列，XHR 进度、并发 3）
  router/       路由与守卫（未登录跳转 /login?redirect=…，管理员路由校验，页面标题）
  layouts/      MainLayout：可折叠侧栏（窄屏为抽屉）、短搜索入口（点击进搜索页）、主题切换、用户菜单（账号设置 / 退出）
  views/        Files / Search / Mount / Settings(+tabs: 外观 / 访问令牌 / S3 密钥) / ApiDocs(内嵌 Apifox) /
                admin(Users, System) / Login / NotFound
  components/
    common/     图标、高亮、复制块、输入弹窗、账号设置（含修改密码）、FeedbackBridge（在 setup 外使用 message/dialog）
    files/      列表/网格视图、面包屑、文件夹选择器、上传队列面板、排序
    preview/    预览浮层（← → Esc）及 图片/视频(HLS)/音频/文本(可编辑)/PDF/其他 预览器
    admin/      服务器目录选择器
    apidocs/    OpenAPI 渲染（$ref/allOf 解析为字段树）
  composables/  useIsMobile、useCopy、useDebounceFn、usePolling、feedback
  utils/        格式化（大小/日期/时长）、虚拟路径、文件类型图标、剪贴板（兼容非 HTTPS）、拖放读取、下载、极简 Markdown、OpenAPI 解析
```
