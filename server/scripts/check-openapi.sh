#!/usr/bin/env bash
# 校验 server/internal/api/openapi.json 确实由 Go 注解生成且与注解一致。
#
# 做法：把规范重新生成到临时文件，与仓库中的文件逐字节比对。任何人手写、
# 手改规范（包括只改一个描述）都会让本脚本失败。
#
# 用法（在 server/ 目录下）：
#     ./scripts/check-openapi.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMMITTED="$ROOT/internal/api/openapi.json"

if [[ ! -f "$COMMITTED" ]]; then
  echo "缺少 $COMMITTED；请运行 make docs 生成规范。" >&2
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

"$ROOT/scripts/gen-openapi.sh" "$TMP/openapi.json" >/dev/null

if ! diff -u "$COMMITTED" "$TMP/openapi.json" > "$TMP/diff.txt"; then
  cat "$TMP/diff.txt" >&2
  cat >&2 <<'EOF'

错误：internal/api/openapi.json 与 Go 注解生成的规范不一致。

规范只能由 Go 注解生成，禁止手写或手工修改。请：
  1. 把改动写进 handler 的注解（@Summary/@Param/@Success 等）或结构体字段注释；
  2. 运行 `make docs` 重新生成；
  3. 提交重新生成的 openapi.json。
EOF
  exit 1
fi

echo "openapi.json 由 Go 注解生成，且与注解一致。"
