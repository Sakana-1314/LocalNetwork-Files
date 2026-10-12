#!/usr/bin/env bash
# 由 Go 注解生成 OpenAPI 规范（server/internal/api/openapi.json）。
#
# 规范**只能**这样生成，禁止手写：注解是唯一事实来源，CI 会用本脚本重新生成
# 并与仓库中的文件逐字节比对，不一致即判定有人手改。
#
# 用法（在 server/ 目录下）：
#     make docs
#     # 或
#     ./scripts/gen-openapi.sh [输出文件]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-$ROOT/internal/api/openapi.json}"
EMBED="$ROOT/internal/api/openapi.json"

# swag 版本锁定：生成物随上游大版本漂移会让 CI 的比对失败。
SWAG_PKG="github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc6"

# 输出到临时目录：swag 会把 docs.go 写到 -o 指定的目录，而注解源文件
# internal/api/docs.go 与该产物同名且就在同一目录，直接输出到 internal/api
# 会把它覆盖掉。
TMP="$(mktemp -d)"
PLACEHOLDER=0
SUCCESS=0
cleanup() {
  rm -rf "$TMP"
  # 生成失败时删掉占位文件：宁可让 //go:embed 编译失败，也不要留下一份空规范
  # 被当成正常产物提交或提供给 /api/openapi.json。
  if [[ "$PLACEHOLDER" == 1 && "$SUCCESS" == 0 ]]; then
    rm -f "$EMBED"
  fi
}
trap cleanup EXIT

# openapi.json 被 router.go 的 //go:embed 引用。若它不存在（首次生成，或刚被
# 清理），本包编译失败，swag 便拿不到包名，于是把本包类型静默命名为 `_.Xxx`
# 且不报任何错误——产出一份坏规范。先放占位文件让包能编译，随后覆盖它。
if [[ ! -f "$EMBED" ]]; then
  printf '{}\n' > "$EMBED"
  PLACEHOLDER=1
fi

# 必须在 internal/api 目录里执行：swag 按「注解所在文件的 import」解析
# 跨包类型（vfs.Entry、store.User 等），并以此决定 schema 名称；在 server/
# 根目录执行会得到 github_com_Sakana-1314_... 这样的长名称，还会把其它包
# 里的注解一起扫进来。
cd "$ROOT/internal/api"
env -u GOROOT -u GOFILE -u GOLINE -u GOOS -u GOARCH -u GOPACKAGE \
  go run "$SWAG_PKG" init \
  -g docs.go \
  --parseInternal --parseDependency --parseDepth 3 \
  --ot json --v3.1 \
  --output "$TMP" >/dev/null

# 兜底：包名解析失败会导致 `_.Xxx` 这种 schema 名，属生成失败，直接拦住。
if grep -q '"_\.[A-Za-z]' "$TMP/swagger.json"; then
  echo "生成失败：schema 名出现 '_.' 前缀，说明 swag 未能解析包名。" >&2
  exit 1
fi

cp "$TMP/swagger.json" "$OUT"

# 修正 swag 的已知缺陷并规范化格式，便于 CI 逐字节比对。
python3 "$ROOT/scripts/spec-normalize.py" "$OUT"

SUCCESS=1
echo "已生成 $OUT"
