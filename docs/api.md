# API 文档

完整接口文档由 Apifox 托管（规范来源为本仓库的 Go 注解，见
[OpenAPI 与 Apifox](/dev/openapi)）。

<ClientOnly>
  <DemoFrame src="https://mlist.apifox.cn/" title="MList API 文档" external :height="'calc(100vh - 190px)'" />
</ClientOnly>

## 调试

规范里的 `servers` 是相对路径 `/api/v1`，在 Apifox 的「环境」里配置 Base URL
（如 `http://192.168.1.10:8080`）后即可直接发送请求。

也可以用「访问令牌」在命令行调试：

```bash
TOKEN=ml_xxxxxxxx                  # 在「设置 → 访问令牌」中创建
H="Authorization: Bearer $TOKEN"
BASE=http://192.168.1.10:8080/api/v1

curl -H "$H" "$BASE/fs/list?path=/movies"
```
