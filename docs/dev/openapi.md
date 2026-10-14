# OpenAPI 与 Apifox

## 规范由 Go 注解生成

规范在 `GET /api/openapi.json`，由 Go 注解生成，生成物是
`server/internal/api/openapi.json`。

**禁止手写**：改动 handler 注解或结构体字段注释后运行 `cd server && make docs` 重新生成；
CI 会重新生成并与仓库中的文件逐字节比对，不一致即失败。

## 同步到 Apifox

界面「API 文档」以 iframe 内嵌 Apifox 在线文档 <https://mlist.apifox.cn>。同步方式：

```bash
APIFOX_TOKEN=afxp_xxxxxxxx ./server/scripts/sync-apifox.sh   # 项目 ID 默认 8912512，可用 APIFOX_PROJECT_ID 覆盖
```

令牌是 Apifox 的**系统级访问令牌**，只经环境变量传入，不要写进仓库。配置好仓库 Secret
`APIFOX_TOKEN` 后，
[`.github/workflows/apifox-sync.yml`](https://github.com/Sakana-1314/MList/blob/main/.github/workflows/apifox-sync.yml)
会在 `main` 上的规范变更时**自动同步**（也可手动触发并指定项目 ID）；未配置令牌时该工作流只告警并
跳过，接口文档不会更新。

规范里的 `servers` 是相对路径 `/api/v1`，在 Apifox 的「环境」里配置 Base URL
（如 `http://192.168.1.10:8080`）后再调试。

::: warning 同步会覆盖 Apifox 侧的手工改动
同步脚本使用「覆盖已有接口」并删除规范外的资源，因此在 Apifox 网页上手工添加的接口、
Mock 规则与响应示例会在下次同步时被清掉。文档与 Mock 的唯一来源是本仓库的 Go 注解。
:::

## 演示数据的取值

演示站需要 Mock 返回稳定且自洽的数据，做法是在 Go 注解上固定关键字段
（Apifox 的属性级 Mock 字段，对应规范里的 `x-apifox-mock` 扩展）：

- `ServiceStatus.enabled` / `running`：固定 `true`，否则「挂载与服务」的卡片随机显示「未启用」。
- `ServiceStatus.error`：固定为空串，否则随机字符串会让卡片显示「异常」。
- `ServiceStatus.path` / `port`：固定为空串与 `0`，让前端回退到各协议正确的默认值
  （`/dav`、`/s3`、`http_port`、`445`）——端口与路径因协议而异，一个字段无法同时正确。
- `User.role`：固定 `admin`，否则演示里看不到管理菜单。
- `ListResponse.path` / `writable`、`SystemInfo` 的 `version` / `hostname` / `http_port` / `ffmpeg`。

`Entry` 的 `name` / `path` / `kind` 等**有意不固定**：列目录需要多样化的条目，
固定后一屏会出现多条完全相同的文件。

::: danger `x-apifox-mock` 只能固定标量值
Apifox 的「Mock 字段」是**纯文本输入框**：在里面写 JSON 对象（想一次固定整个对象），
接口返回的会是那个**字符串**而不是对象，前端读不到字段。
例如给 `ServicesStatus.webdav` 写 `{"enabled":true,...}`，实测返回
`"services": {"webdav": "{\"enabled\":true,...}"}`，卡片直接不显示状态。

更麻烦的是，**导入一份不含该配置的规范并不会清掉 Apifox 侧已存在的值**，
必须手工在 Apifox 界面里删掉。所以只固定标量，并在前端对两种形态做兼容
（见 `web/src/api/normalize.ts` 的 `normServiceStatus`）。
:::

详见[在线 Demo](/dev/demo)。

详见[在线 Demo](/dev/demo)。
