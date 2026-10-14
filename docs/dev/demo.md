# 在线 Demo

本页说明 Demo 是怎么搭起来的，以及它的数据从哪来。

## 地址

| 页面 | 地址 |
| --- | --- |
| 演示界面（试用入口） | <https://sakana-1314.github.io/MList/app/> |
| 接口文档 | <https://sakana-1314.github.io/MList/api/> |
| Apifox 在线文档 | <https://mlist.apifox.cn> |
| 云端 Mock 基址 | `https://m1.apifoxmock.com/m1/8912512-8712920-default` |

## 构建方式

Demo 就是 `web/` 的前端，只是构建时换了几处参数（见[从源码构建](/dev/build)）：

```bash
cd web
WEB_BASE=/MList/app/ \
WEB_OUT_DIR=../docs/public/app \
VITE_API_BASE=https://m1.apifoxmock.com/m1/8912512-8712920-default \
VITE_HASH_ROUTER=1 \
  pnpm build:demo
```

产物放在 `docs/public/app/`，VitePress 构建时会原样复制到站点根，
于是 `docs/demo.md` 用 iframe 内嵌 `/MList/app/` 即可。

两处容易踩的点：

- **Mock 基址不加 `/api/v1`**：规范是以相对路径导入 Apifox 的（导入时不加 Base Path），
  接口就在 `/fs/list` 这类根路径下，再加 `/api/v1` 会全部 404。
- **要开 `VITE_HASH_ROUTER=1`**：GitHub Pages 没有服务端回退（只认**站点根**的
  `404.html`，放在 `app/` 下的那份不会被使用），History 路由下刷新 `/MList/app/files/xxx`
  会 404。hash 路由把路径放在 `#` 之后，任何路径都由同一个 `index.html` 处理。

这些步骤由
[`.github/workflows/pages.yml`](https://github.com/Sakana-1314/MList/blob/main/.github/workflows/pages.yml)
在 CI 中执行，Mock 基址在该工作流里以 `APIFOX_MOCK_BASE` 变量集中定义。

::: warning 首次部署需要先启用 Pages
`GITHUB_TOKEN` 无权创建 Pages 站点，所以工作流里不能加 `configure-pages` 的
`enablement: true`（加上会让所有 PR 构建直接失败）。首次需要手工开启一次，
把 Source 设为 **GitHub Actions**：

```bash
gh api -X POST repos/<owner>/<repo>/pages -f build_type=workflow
```
:::

## Mock 基址怎么来的

在 Apifox 项目的「云端 Mock」处可以看到地址，形如
`https://m1.apifoxmock.com/m1/{项目ID}-{分支ID}-{服务}`。若 Apifox 侧的分支或项目变化，
只需改 `pages.yml` 里的 `APIFOX_MOCK_BASE` 一处。

## 已知限制

- Mock 对二进制接口也只返回 JSON，因此**图片预览、视频转码、HLS 播放、下载在 Demo 中不可用**。
- 写操作（上传、新建、删除等）不会真正落盘。
- 数据由 Mock 即时生成，刷新后会变化。

## 相关

- 固定 Mock 取值的约定：[OpenAPI 与 Apifox](/dev/openapi#演示数据的取值)
- 文档站与 Pages 发布：[Docker 与镜像](/deploy/docker)
