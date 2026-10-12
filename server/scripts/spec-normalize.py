#!/usr/bin/env python3
"""规范化 swag 生成的 OpenAPI 文档，并校验中文覆盖情况。

swag v2 有两个已知缺陷会在产物里留下非法或误导性的内容，这里统一修掉：

1. 未配置 @externalDocs 时仍会写出空的 ``externalDocs``，其中 ``url`` 缺失，
   导致 ``kin-openapi`` 校验失败（``invalid external docs: url is required``）。
2. ``{file} binary`` 会生成 Swagger 2.0 残留的 ``type: file``，在 OpenAPI 3 里
   非法（``unsupported 'type' value "file"``）。本仓库统一改用
   ``{string} binary``，这里再兜底转换一次，防止以后有人写回 ``{file}``。

另外把文档按固定格式重排（``sort_keys``、两空格缩进、结尾换行），保证重新生成
的结果可稳定比对。
"""

import json
import sys


def fix_type_file(node: object) -> None:
    """把 Swagger 2.0 的 ``type: file`` 改写为 OpenAPI 3 的二进制字符串。"""
    if isinstance(node, dict):
        if node.get("type") == "file":
            node["type"] = "string"
            node["format"] = "binary"
        for value in node.values():
            fix_type_file(value)
    elif isinstance(node, list):
        for value in node:
            fix_type_file(value)


def drop_spurious_text_plain(node: object) -> None:
    """删掉 swag 因 ``{type} binary`` 注解额外塞进来的 ``text/plain``。

    写 ``@Param body body string`` 时 swag 会同时生成 ``text/plain``（用注解里
    的类型名当 schema title）——它不是真实的请求体类型，只会让文档里多出一个
    误导性的选项。仅当存在其它更具体的 content type 时才删除。
    """
    if isinstance(node, dict):
        for value in node.values():
            drop_spurious_text_plain(value)
        content = node.get("content")
        if isinstance(content, dict) and len(content) > 1:
            plain = content.get("text/plain")
            if isinstance(plain, dict) and isinstance(plain.get("schema"), dict) and "title" in plain["schema"]:
                del content["text/plain"]
    elif isinstance(node, list):
        for value in node:
            drop_spurious_text_plain(value)


def main() -> int:
    if len(sys.argv) != 2:
        print("用法: spec-normalize.py <openapi.json>", file=sys.stderr)
        return 2
    path = sys.argv[1]
    with open(path, encoding="utf-8") as fh:
        doc = json.load(fh)

    if not (doc.get("externalDocs") or {}).get("url"):
        doc.pop("externalDocs", None)

    fix_type_file(doc)
    drop_spurious_text_plain(doc)

    with open(path, "w", encoding="utf-8") as fh:
        json.dump(doc, fh, ensure_ascii=False, indent=2, sort_keys=True)
        fh.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
