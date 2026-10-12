package api

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// specFixture 读取内嵌的 OpenAPI 规范（生成物，见 docs.go 与 make docs）。
func specFixture(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(openapiJSON(), &doc); err != nil {
		t.Fatalf("openapi.json 不是合法 JSON：%v", err)
	}
	return doc
}

// hasChinese 判断字符串里是否含有 CJK 汉字。
func hasChinese(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

type operation struct {
	method string
	path   string
	op     map[string]any
}

func operations(t *testing.T, doc map[string]any) []operation {
	t.Helper()
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("规范缺少 paths")
	}
	var out []operation
	for p, raw := range paths {
		ops, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("%s 不是对象", p)
		}
		for m, rawOp := range ops {
			op, ok := rawOp.(map[string]any)
			if !ok {
				t.Fatalf("%s %s 不是对象", m, p)
			}
			out = append(out, operation{method: strings.ToUpper(m), path: p, op: op})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].method < out[j].method
	})
	return out
}

// TestSpecIsOpenAPI31 规范必须是 OpenAPI 3.1，且整体结构完整。
func TestSpecIsOpenAPI31(t *testing.T) {
	doc := specFixture(t)
	if got := doc["openapi"]; got != "3.1.0" {
		t.Fatalf("openapi = %v，期望 3.1.0（由 swag --v3.1 生成）", got)
	}
	for _, key := range []string{"info", "paths", "components", "servers"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("规范缺少 %s", key)
		}
	}
	if ops := operations(t, doc); len(ops) < 30 {
		t.Errorf("规范只有 %d 个 operation，疑似生成不完整", len(ops))
	}
}

// TestSpecTextIsChinese 分组（tag）、接口摘要、参数与响应描述、字段描述都必须用中文。
func TestSpecTextIsChinese(t *testing.T) {
	doc := specFixture(t)

	// 分组：名称与描述都用中文。
	tags, _ := doc["tags"].([]any)
	if len(tags) == 0 {
		t.Fatal("规范缺少 tags，分组信息会丢失")
	}
	tagNames := map[string]bool{}
	for _, raw := range tags {
		tag, _ := raw.(map[string]any)
		name, _ := tag["name"].(string)
		desc, _ := tag["description"].(string)
		if !hasChinese(name) {
			t.Errorf("分组名不是中文：%q", name)
		}
		if !hasChinese(desc) {
			t.Errorf("分组 %q 的描述不是中文：%q", name, desc)
		}
		tagNames[name] = true
	}

	for _, o := range operations(t, doc) {
		where := o.method + " " + o.path

		// 每个接口都必须归属一个已声明的中文分组。
		opTags, _ := o.op["tags"].([]any)
		if len(opTags) == 0 {
			t.Errorf("%s 没有 tags（分组）", where)
		}
		for _, rawTag := range opTags {
			name, _ := rawTag.(string)
			if !tagNames[name] {
				t.Errorf("%s 使用了未声明的分组 %q", where, name)
			}
		}

		if summary, _ := o.op["summary"].(string); !hasChinese(summary) {
			t.Errorf("%s 缺少中文 summary：%q", where, summary)
		}

		// 参数描述。
		if params, ok := o.op["parameters"].([]any); ok {
			for _, raw := range params {
				p, _ := raw.(map[string]any)
				desc, _ := p["description"].(string)
				if !hasChinese(desc) {
					t.Errorf("%s 参数 %v 的描述不是中文：%q", where, p["name"], desc)
				}
			}
		}

		// 响应描述。
		responses, _ := o.op["responses"].(map[string]any)
		if len(responses) == 0 {
			t.Errorf("%s 没有声明任何响应", where)
		}
		for code, raw := range responses {
			r, _ := raw.(map[string]any)
			desc, _ := r["description"].(string)
			if !hasChinese(desc) {
				t.Errorf("%s 响应 %s 的描述不是中文：%q", where, code, desc)
			}
		}
	}
}

// TestSpecSchemaFieldsAreChinese schema 的每个字段都要有中文说明。
func TestSpecSchemaFieldsAreChinese(t *testing.T) {
	doc := specFixture(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	if len(schemas) == 0 {
		t.Fatal("规范缺少 components.schemas")
	}
	for name, raw := range schemas {
		schema, _ := raw.(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if len(props) == 0 {
			continue // 别名 / 数组等没有属性
		}
		for field, rawProp := range props {
			prop, _ := rawProp.(map[string]any)
			desc, _ := prop["description"].(string)
			if !hasChinese(desc) {
				t.Errorf("%s.%s 缺少中文字段描述：%q", name, field, desc)
			}
		}
	}
}

// TestSpecSecurityMatchesAuth 公开接口在规范里不应带 security，受保护接口必须带。
func TestSpecSecurityMatchesAuth(t *testing.T) {
	doc := specFixture(t)
	if _, ok := doc["security"]; ok {
		t.Error("规范不应有全局 security：公开接口会被误标为需要鉴权")
	}
	schemes, _ := doc["components"].(map[string]any)
	if _, ok := schemes["securitySchemes"].(map[string]any)["bearerAuth"]; !ok {
		t.Error("规范缺少 bearerAuth 鉴权定义")
	}

	// 只有这几个接口是公开的，其余都必须声明 bearerAuth。
	publicOps := map[string]bool{
		"POST /auth/login":  true,
		"POST /auth/logout": true, // 只清 Cookie，必须在会话过期后仍可用
		"GET /media/thumb":  true, // 只校验摘要签名
	}
	for _, o := range operations(t, doc) {
		key := o.method + " " + o.path
		_, requiresAuth := o.op["security"]
		if publicOps[key] {
			if requiresAuth {
				t.Errorf("%s 应为公开接口，却声明了 security", key)
			}
			continue
		}
		if !requiresAuth {
			t.Errorf("%s 需要鉴权，但没有声明 security", key)
		}
	}
}

// TestSpecHasNoSwagger2Leftovers swag 的已知缺陷不能出现在产物里。
func TestSpecHasNoSwagger2Leftovers(t *testing.T) {
	doc := specFixture(t)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	// {file} binary 会生成 Swagger 2.0 残留的 type: file，在 OpenAPI 3 里非法。
	if regexp.MustCompile(`"type"\s*:\s*"file"`).Match(raw) {
		t.Error(`规范里出现 "type": "file"，这是 Swagger 2.0 残留，在 OpenAPI 3 里非法`)
	}
	// 空的 externalDocs 会缺少必需的 url。
	if ed, ok := doc["externalDocs"].(map[string]any); ok {
		if url, _ := ed["url"].(string); url == "" {
			t.Error("externalDocs 缺少 url，会导致规范校验失败")
		}
	}
}

// TestSpecRoutePathParamsMatchGin swag 的路径参数语法必须和 Gin 路由一致。
// 早期手写规范用的 {segment}.ts 在 Gin 里并不存在对应的参数名，会导致
// 客户端按规范拼出的 URL 404。
func TestSpecRoutePathParamsMatchGin(t *testing.T) {
	doc := specFixture(t)
	paths, _ := doc["paths"].(map[string]any)
	for p := range paths {
		if strings.Contains(p, "index.m3u8") || strings.Contains(p, "access_key") {
			continue
		}
		for _, param := range regexp.MustCompile(`\{([^}]+)\}`).FindAllStringSubmatch(p, -1) {
			name := param[1]
			switch name {
			case "id", "name", "file":
			default:
				t.Errorf("%s 的路径参数 %q 与 Gin 路由不一致", p, name)
			}
		}
	}
}
