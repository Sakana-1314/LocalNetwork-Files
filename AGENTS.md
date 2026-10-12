# AGENTS.md

MList（局域网文件管理）：把服务器上的目录按用户授权，通过 Web / WebDAV / S3 / SMB / REST API 访问。
事实标准以代码与各目录内文档为准，本文件不重复实现细节与历史决策。

## 唯一约定

**禁止未经明确授权修改 AGENTS.md。** 未获得用户明确授权，任何智能体不得增删改本文件。

## 目录

- `server/` - 后端（Go + Gin + SQLite），含 `internal/api/openapi.yaml` 规范与 `internal/webui/` 内嵌前端产物
- `server/scripts/` - 后端相关脚本（如 Apifox 文档同步）
- `web/` - 前端（Vue 3 + TypeScript + Naive UI）
- `.github/workflows/` - CI/CD 工作流
- `notes/` - 本地资料

根目录只放 `AGENTS.md`、`README.md`、`LICENSE` 与各子目录；文档与脚本放进所属目录内部。

## notes

`notes/` 用于存放本地资料：机密文件、测试服务器、生产服务器相关信息。

- 不得进入 git，必须在 `.gitignore` 中忽略（`notes/`）。
- 只留本地，不复用、不外传、不复制进仓库其它目录。

## 协作与提交

- 作者与提交者统一为 `Sakana <admin@yangrucheng.top>`，SSH 私钥签名（`user.signingkey=/root/.ssh/private.key`），改写历史后重新签名。
- 必须走 PR：`gh pr create` → 等 CI 门禁 → `gh pr checks` 全绿后 `gh pr merge --squash` 并删除 head 分支；禁止本地 merge 后直推 `main`（会把 PR 关成 CLOSED 而非 MERGED）。
- 分支前缀 `feat/`、`fix/`、`refactor/`、`docs/`、`chore/`；提交信息中文，格式 `type(scope): 描述`。
- 一次 PR 只做一件事：改动按功能拆分，不把无关重构混进同一个提交。
- 门禁未绿不得合并：`gofmt -l .`、`go vet ./...`、`go test -race ./...`（server）与 `pnpm typecheck`、`pnpm build`（web）由 CI 执行；
  本地 `pnpm` 可能被依赖供应链策略拦截，此时直接调用 `web/node_modules/.bin/` 下的二进制，最终仍以 CI 结果为准。
- 工作流 `name` 用中文（专有名词除外）；严禁硬编码 IP、密钥与内部域名，一律 `${{ secrets.* }}`。
- 部署主机、容器名、路径与凭据只写进本地 `notes/`，不得出现在仓库任何文件中。

## 部署

- 镜像：`.github/workflows/docker.yml` 在 push 到 `main` 或打 `v*` 标签时构建并推送镜像（多架构 amd64/arm64）；
  镜像标签 `org.opencontainers.image.revision` 携带提交号，可用于核对部署的是哪个提交。
- 升级流程：合并到 `main` → 等镜像构建成功 → 在目标主机更新容器 → 校验健康检查与版本。
- 目标主机、容器名、端口、路径与凭据一律记录在本地 `notes/`，仓库内只写通用流程。
