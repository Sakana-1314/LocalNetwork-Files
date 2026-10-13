#!/usr/bin/env bash
#
# 把 server/internal/api/openapi.json 同步到 Apifox 项目。
#
# 该文件是 Go 注解的生成物（make docs），不要手改。
#
#   APIFOX_TOKEN=afxp_xxx ./server/scripts/sync-apifox.sh
#   APIFOX_TOKEN=afxp_xxx APIFOX_PROJECT_ID=8912512 ./server/scripts/sync-apifox.sh
#
# 依赖：curl、python3（仅用标准库）。令牌只经环境变量传入，不写入仓库。
#
set -euo pipefail

# 脚本位于 server/scripts/，仓库根目录是上两级。
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SPEC="${SPEC:-$ROOT/server/internal/api/openapi.json}"

: "${APIFOX_TOKEN:?请先设置 APIFOX_TOKEN 环境变量（Apifox 系统级访问令牌）}"
PROJECT_ID="${APIFOX_PROJECT_ID:-8912512}"
API_BASE="${APIFOX_API_BASE:-https://api.apifox.com}"
API_VERSION="${APIFOX_API_VERSION:-2024-03-28}"

if [[ ! -f "$SPEC" ]]; then
  echo "找不到 OpenAPI 规范文件：$SPEC" >&2
  exit 1
fi

command -v curl >/dev/null || { echo "缺少 curl" >&2; exit 1; }
command -v python3 >/dev/null || { echo "缺少 python3" >&2; exit 1; }

BODY="$(mktemp)"
RESP="$(mktemp)"
trap 'rm -f "$BODY" "$RESP"' EXIT

# input 支持字符串形式的 OpenAPI 数据（JSON/YAML），这里直接提交 YAML 原文。
SPEC_PATH="$SPEC" python3 - "$BODY" <<'PY'
import json, os, sys
with open(os.environ["SPEC_PATH"], encoding="utf-8") as f:
    spec = f.read()
payload = {
    "input": spec,
    "options": {
        "endpointOverwriteBehavior": "OVERWRITE_EXISTING",
        "schemaOverwriteBehavior": "OVERWRITE_EXISTING",
        # 开启「更新接口目录」：规范里的中文 tags 是分组的唯一来源，
        # 早期导入留下的英文目录（auth/files/media/tokens/system/admin）
        # 必须跟着改过来，否则文档侧栏一直是英文。
        "updateFolderOfChangedEndpoint": True,
        "prependBasePath": False,
        # 开启「删除规范外资源」：文档与仓库里的规范保持完全一致，
        # 清掉先前导入残留的英文模型与已删除接口。这意味着手工在 Apifox
        # 里添加、规范中不存在的接口也会被删除——文档的唯一来源是本仓库。
        "deleteUnmatchedResources": True,
    },
}
with open(sys.argv[1], "w", encoding="utf-8") as f:
    json.dump(payload, f, ensure_ascii=False)
PY

echo "==> 导入 $SPEC 到 Apifox 项目 $PROJECT_ID"
STATUS="$(curl -sS -o "$RESP" -w '%{http_code}' -X POST \
  -H "Authorization: Bearer $APIFOX_TOKEN" \
  -H "X-Apifox-Api-Version: $API_VERSION" \
  -H "Content-Type: application/json" \
  --data-binary "@$BODY" \
  "$API_BASE/v1/projects/$PROJECT_ID/import-openapi")"

if [[ "$STATUS" != "200" && "$STATUS" != "201" ]]; then
  echo "导入失败（HTTP $STATUS）：" >&2
  cat "$RESP" >&2
  echo >&2
  exit 1
fi

if [[ ! -s "$RESP" ]]; then
  echo "==> 导入成功（HTTP $STATUS，未返回统计信息）"
else
  python3 - "$RESP" <<'PY'
import json, sys

raw = open(sys.argv[1], encoding="utf-8").read()
try:
    d = json.loads(raw)
except json.JSONDecodeError:
    print("导入成功，但响应不是 JSON：" + raw[:400], file=sys.stderr)
    raise SystemExit(0)

if isinstance(d, dict) and d.get("success") is False:
    print("导入失败：" + json.dumps(d, ensure_ascii=False), file=sys.stderr)
    raise SystemExit(1)

counters = ((d.get("data") if isinstance(d, dict) else None) or {}).get("counters") or {}
if not counters:
    print("==> 导入成功，响应：" + json.dumps(d, ensure_ascii=False)[:400])
    raise SystemExit(0)

keys = [
    "endpointCreated", "endpointUpdated", "endpointIgnored", "endpointFailed",
    "schemaCreated", "schemaUpdated", "schemaIgnored", "schemaFailed",
]
print("==> 导入完成：" + "，".join(f"{k}={counters.get(k, 0)}" for k in keys))
for e in ((d.get("data") or {}).get("errors") or []):
    print(f"  ! {e.get('code')}: {e.get('message')}", file=sys.stderr)
if counters.get("endpointFailed") or counters.get("schemaFailed"):
    raise SystemExit(1)
PY
fi

cat <<'EOF'

提示：
  * 文档地址 https://mlist.apifox.cn 在 Apifox 后台「发布」后才会更新。
  * 规范里的 servers 是相对路径 /api/v1，在 Apifox 的「环境」里配置 Base URL
    （例如 http://192.168.1.10:8080）后再调试。
EOF
