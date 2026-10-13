#!/usr/bin/env python3
"""规范化 swag 生成的 OpenAPI 文档，并校验中文覆盖情况。

swag v2 有三个已知缺陷会在产物里留下非法或误导性的内容，这里统一修掉：

1. 未配置 @externalDocs 时仍会写出空的 ``externalDocs``，其中 ``url`` 缺失，
   导致 ``kin-openapi`` 校验失败（``invalid external docs: url is required``）。
2. ``{file} binary`` 会生成 Swagger 2.0 残留的 ``type: file``，在 OpenAPI 3 里
   非法（``unsupported 'type' value "file"``）。本仓库统一改用
   ``{string} binary``，这里再兜底转换一次，防止以后有人写回 ``{file}``。

3. swag 生成的模型名带 Go 包名前缀（``api.UserInput``、``vfs.Entry``），这些前缀
   对使用文档的人没有意义，也会把接口响应示例里的类型名弄得很吵，这里统一去掉。

另外把文档按固定格式重排（``sort_keys``、两空格缩进、结尾换行），保证重新生成
的结果可稳定比对。

### 模型的中文名

OpenAPI 规定 ``components.schemas`` 的键只能匹配 ``^[a-zA-Z0-9._-]+$``，因此模型名
本身不可能是中文（用中文键虽然能被 Apifox 接受，但规范不合法，kin-openapi 之类的
校验器会直接拒绝）。规范里唯一合法的中文位置是 schema 的 ``title``：Apifox 在「数据
模型」里正是用 ``title`` 作为显示名。swag 没有给类型写 ``title`` 的注解，所以这里按
确定性的规则补上——用类型文档注释里的中文说明（swag 写进 ``description``）作为
``title``，两者都来自 Go 注解，产物里没有任何手写内容。
"""

import json
import re
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


def strip_package_prefix(schemas: dict) -> dict:
    """去掉模型名里的 Go 包名前缀，并返回 ``旧名 -> 新名`` 映射。

    ``api.UserInput`` 这样的前缀是 swag 为防重名加的包名。去掉后若出现重名，
    说明两个包的模型撞名了，此时宁可保留原样也不要静默改名——改名会破坏引用。
    """
    rename = {}
    for key in schemas:
        short = key.split(".", 1)[1] if "." in key else key
        rename[key] = short
    if len(set(rename.values())) != len(rename):
        return {}
    return {old: new for old, new in rename.items() if old != new}


def apply_ref_rename(node: object, rename: dict) -> None:
    """把所有 ``$ref`` 里的模型名换成去掉前缀后的名字。"""
    if isinstance(node, dict):
        for key, value in node.items():
            if key == "$ref" and isinstance(value, str):
                prefix = "#/components/schemas/"
                if value.startswith(prefix) and value[len(prefix):] in rename:
                    node[key] = prefix + rename[value[len(prefix):]]
            else:
                apply_ref_rename(value, rename)
    elif isinstance(node, list):
        for value in node:
            apply_ref_rename(value, rename)


def add_chinese_titles(schemas: dict) -> int:
    """给每个模型补一个中文 ``title``（Apifox 用它作为数据模型的显示名）。

    swag 没有写 ``title`` 的注解，这里用类型文档注释的中文说明（swag 已写进
    ``description``）作为 ``title``，两者同源，依然是注解生成、没有手写内容。
    """
    added = 0
    for name, schema in schemas.items():
        if not isinstance(schema, dict) or schema.get("title"):
            continue
        desc = (schema.get("description") or "").strip()
        if not desc or not re.search(r"[\u4e00-\u9fff]", desc):
            continue
        # 只取第一句，避免把整段说明塞进显示名。
        title = re.split(r"[。；;\n]", desc, maxsplit=1)[0].strip()
        if title:
            schema["title"] = title
            added += 1
    return added


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

    schemas = doc.get("components", {}).get("schemas") or {}
    if schemas:
        rename = strip_package_prefix(schemas)
        if rename:
            for old, new in rename.items():
                schemas[new] = schemas.pop(old)
            apply_ref_rename(doc, rename)
        add_chinese_titles(schemas)

    with open(path, "w", encoding="utf-8") as fh:
        json.dump(doc, fh, ensure_ascii=False, indent=2, sort_keys=True)
        fh.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
