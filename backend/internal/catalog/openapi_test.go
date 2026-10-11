package catalog

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"strings"
	"testing"
)

func TestOpenAPIRouteCoverage(t *testing.T) {
	doc := OpenAPI()
	if _, err := json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)
	paths := doc["paths"].(map[string]any)
	for _, route := range r.Routes() {
		// 文档面不进 OpenAPI 契约：openapi.json 本身就是这份文档，
		// /docs、/swagger 是 HTML 页面，/docs/assets/*filepath 是页面自托管的静态资源
		// （白名单在 docs_assets.go）。它们都不是给接入方调用的 API，登记进来只会让
		// 生成出来的 SDK 多出三个无意义的接口。
		if route.Path == "/api/openapi.json" || route.Path == "/api/docs" ||
			route.Path == "/api/swagger" || route.Path == "/api/docs/assets/*filepath" {
			continue
		}
		clean := strings.TrimPrefix(route.Path, "/api")
		clean = strings.TrimPrefix(clean, "/api")
		path := strings.ReplaceAll(clean, ":id", "{id}")
		path = strings.ReplaceAll(path, ":code", "{code}")
		path = strings.ReplaceAll(path, ":position", "{position}")
		p, ok := paths[path].(map[string]any)
		if !ok || p[strings.ToLower(route.Method)] == nil {
			t.Fatalf("undocumented endpoint: %s %s", route.Method, path)
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	entity := schemas["Entity"].(map[string]any)["properties"].(map[string]any)
	if entity["contents"] == nil || entity["subjects"] == nil || entity["canonical_entry_id"] != nil {
		t.Fatal("v2 DTO schema drift")
	}
}

func TestOpenAPIReverseCoverage(t *testing.T) {
	// 反向检查：文档中的每条 path+method 必须在 gin 路由中真实注册，
	// 防止文档残留已删除端点（如重复的 /catalog/shelves）。
	doc := OpenAPI()
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)
	registered := map[string]bool{}
	for _, route := range r.Routes() {
		clean := strings.TrimPrefix(route.Path, "/api")
		clean = strings.TrimPrefix(clean, "/api")
		path := strings.ReplaceAll(clean, ":id", "{id}")
		path = strings.ReplaceAll(path, ":code", "{code}")
		path = strings.ReplaceAll(path, ":position", "{position}")
		registered[strings.ToLower(route.Method)+" "+path] = true
	}
	paths := doc["paths"].(map[string]any)
	for path, v := range paths {
		for method := range v.(map[string]any) {
			if !registered[strings.ToLower(method)+" "+path] {
				t.Fatalf("documented but not registered: %s %s", method, path)
			}
		}
	}

	// 账号与收藏前缀已随子系统拆分离开本服务：文档里不得再出现它们，
	// 否则前端/Agent 会以为目录服务仍然接受这些请求。
	for _, p := range []string{"/setup", "/auth/login", "/auth/me", "/auth/settings", "/admin/users", "/oauth/clients", "/oauth/authorize", "/oauth/token", "/oidc/jwks", "/.well-known/openid-configuration", "/favorites/toggle", "/favorites/mine", "/users/{id}/favorites"} {
		if _, ok := paths[p]; ok {
			t.Fatalf("%s 已归子系统，不应再出现在目录服务的 OpenAPI 文档里", p)
		}
	}

	// 登录口径：目录侧只剩自己的写接口需要身份（token 由账号服务签发）。
	for _, p := range []struct {
		path, method string
		wantAuth     bool
	}{
		{"/catalog/entities", "post", true},
		{"/catalog/entities", "get", false},
		// 概览计数是管理台口径的聚合，必须登录 + catalog.lifecycle.manage：它含 deleted/merged，
		// 匿名读会绕过列表端点刻意的可见性过滤。
		{"/catalog/entities/stats", "get", true},
		{"/catalog/me/home-preferences", "get", true},
		{"/catalog/me/home-preferences", "put", true},
		{"/exchange/proposals", "post", true},
		{"/importer/sources", "get", true},
	} {
		op := paths[p.path].(map[string]any)[p.method].(map[string]any)
		_, hasSec := op["security"]
		if hasSec != p.wantAuth {
			t.Fatalf("%s %s security=%v, want auth=%v", p.method, p.path, hasSec, p.wantAuth)
		}
	}

	// 查询参数回归：标签聚合与货架 feed 的参数不得缺失。
	for _, p := range []struct{ path, method, param string }{
		{"/catalog/tags", "get", "limit"},
		{"/catalog/shelves/feed", "get", "per_shelf"},
		{"/catalog/entities", "get", "field"},
	} {
		op := paths[p.path].(map[string]any)[p.method].(map[string]any)
		params, _ := op["parameters"].([]any)
		found := false
		for _, v := range params {
			if v.(map[string]any)["name"] == p.param {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s %s missing query param %q", p.method, p.path, p.param)
		}
	}
}

// 路径模板变量与参数声明必须一一对应：{code} 这类变量漏声明会让文档成为无效 OpenAPI，
// 客户端（Scalar / Swagger / 生成器）解析路径参数时拿不到名字，只能猜。
func TestOpenAPIPathParametersMatchTemplates(t *testing.T) {
	doc := OpenAPI()
	for path, ops := range doc["paths"].(map[string]any) {
		want := pathTemplateParams(path)
		for method, op := range ops.(map[string]any) {
			declared := map[string]bool{}
			if params, ok := op.(map[string]any)["parameters"].([]any); ok {
				for _, p := range params {
					pm, _ := p.(map[string]any)
					if pm["in"] != "path" {
						continue
					}
					if name, ok := pm["name"].(string); ok {
						declared[name] = true
					}
				}
			}
			for _, name := range want {
				if !declared[name] {
					t.Fatalf("%s %s: 路径变量 {%s} 没有对应的 in=path 参数声明", method, path, name)
				}
			}
			for name := range declared {
				if !contains(want, name) {
					t.Fatalf("%s %s: 参数 %q 不在路径模板里", method, path, name)
				}
			}
		}
	}
}

// Definition configuration exposes only the current document and overwrite guard.
func TestOpenAPIDefinitionConfig(t *testing.T) {
	doc := OpenAPI()
	paths := doc["paths"].(map[string]any)
	if paths["/admin/catalog-definitions/{id}/rollback"] != nil || paths["/admin/catalog-definitions/{id}/diff"] != nil {
		t.Fatal("retired definition history paths remain in OpenAPI")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemas["DefinitionConfig"].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"etag", "document", "updated_at"} {
		if props[key] == nil {
			t.Fatalf("definition config missing %s", key)
		}
	}
}

// 首页分区的追加字段必须进文档：HomePreferences.sections（用户覆盖 + 自建）由结构体反射
// 生成，漏记会让按文档做严格校验的客户端把合法请求判成非法；Shelf.source 只在 feed 里出现。
func TestOpenAPIHomeSectionFields(t *testing.T) {
	doc := OpenAPI()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	prefsProps, ok := schemas["HomePreferences"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatal("HomePreferences schema 缺失")
	}
	for _, k := range []string{"order", "hidden", "sections"} {
		if prefsProps[k] == nil {
			t.Fatalf("HomePreferences schema 缺 %s 字段", k)
		}
	}
	sectionProps, ok := schemas["HomeSection"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatal("HomeSection schema 缺失（sections 的元素契约）")
	}
	for _, k := range []string{"slug", "names", "query", "sort", "icon"} {
		if sectionProps[k] == nil {
			t.Fatalf("HomeSection schema 缺 %s 字段", k)
		}
	}
	shelfProps, ok := schemas["Shelf"].(map[string]any)["properties"].(map[string]any)
	if !ok || shelfProps["source"] == nil {
		t.Fatal("Shelf schema 缺 source（feed 条目的来源标记）")
	}
	// 端点说明必须覆盖分区语义：前端只按文档对接，说明缺失等于契约没写。
	for _, p := range []struct{ path, method, want string }{
		{"/catalog/shelves/feed", "get", "source"},
		{"/catalog/me/home-preferences", "put", "too_many_sections"},
		{"/catalog/me/home-preferences", "put", "invalid_slug"},
	} {
		op := doc["paths"].(map[string]any)[p.path].(map[string]any)[p.method].(map[string]any)
		summary, _ := op["summary"].(string)
		if !strings.Contains(summary, p.want) {
			t.Fatalf("%s %s 说明未覆盖 %q：%q", p.method, p.path, p.want, summary)
		}
	}
}

// 下架端点（published → draft）的文档必须冻结：它是已发布条目唯一的降级入口，
// 漏记 requestBody 会让按文档做严格校验的客户端发不出请求；错误码与事件码写进 summary，
// 前端与 Agent 只按文档对接。
func TestOpenAPIUnpublishEndpoint(t *testing.T) {
	doc := OpenAPI()
	op, ok := doc["paths"].(map[string]any)["/catalog/entities/{id}/unpublish"].(map[string]any)
	if !ok {
		t.Fatal("下架端点未进 OpenAPI 文档")
	}
	post, ok := op["post"].(map[string]any)
	if !ok {
		t.Fatal("下架端点必须是 POST")
	}
	if _, ok := post["security"]; !ok {
		t.Fatal("下架端点必须声明 security（需要登录 + catalog.lifecycle.manage）")
	}
	body, _ := post["requestBody"].(map[string]any)
	ref, _ := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"].(string)
	if ref != "#/components/schemas/UnpublishEdit" {
		t.Fatalf("下架端点请求体 schema=%q, want UnpublishEdit", ref)
	}
	props, ok := doc["components"].(map[string]any)["schemas"].(map[string]any)["UnpublishEdit"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatal("UnpublishEdit schema 缺失")
	}
	// 与既有写端点同口径的三个字段；下架不指向别的实体，target_id 不属于它的契约。
	for _, k := range []string{"expected_version", "edit_note", "sources"} {
		if props[k] == nil {
			t.Fatalf("UnpublishEdit schema 缺 %s 字段", k)
		}
	}
	if props["target_id"] != nil {
		t.Fatal("下架没有合并目标，不该有 target_id")
	}
	summary, _ := post["summary"].(string)
	for _, want := range []string{"published", "invalid_status", "version_conflict", "entity.unpublished", "revisions"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("下架端点说明未覆盖 %q：%q", want, summary)
		}
	}
}

// 状态计数端点的文档必须冻结：它是墓碑数唯一的来源，五个状态键与 total 的 schema
// （additionalProperties:false）漏一个，按文档做严格校验的客户端就会判失败。
func TestOpenAPIEntityStatsEndpoint(t *testing.T) {
	doc := OpenAPI()
	op, ok := doc["paths"].(map[string]any)["/catalog/entities/stats"].(map[string]any)
	if !ok {
		t.Fatal("状态计数端点未进 OpenAPI 文档")
	}
	get, ok := op["get"].(map[string]any)
	if !ok {
		t.Fatal("状态计数端点必须是 GET")
	}
	if _, ok := get["security"]; !ok {
		t.Fatal("状态计数端点必须声明 security（需要登录 + catalog.lifecycle.manage）")
	}
	ref, _ := get["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"].(string)
	if ref != "#/components/schemas/EntityStatusCounts" {
		t.Fatalf("状态计数端点 200 响应 schema=%q, want EntityStatusCounts", ref)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props, ok := schemas["EntityStatusCounts"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatal("EntityStatusCounts schema 缺失")
	}
	if props["statuses"] == nil || props["total"] == nil {
		t.Fatalf("EntityStatusCounts schema 缺 statuses/total: %+v", props)
	}
	// 五个状态键逐个冻结，且与 Go 侧 entityStatuses（迁移 CHECK 的全集）逐字一致。
	statusProps, ok := props["statuses"].(map[string]any)["properties"].(map[string]any)
	if !ok || len(statusProps) != len(entityStatuses) {
		t.Fatalf("statuses 应逐个声明 %d 个状态: %+v", len(entityStatuses), statusProps)
	}
	for _, code := range entityStatuses {
		if statusProps[code] == nil {
			t.Fatalf("statuses 缺状态键 %s", code)
		}
	}
	summary, _ := get["summary"].(string)
	for _, want := range []string{"deleted", "merged", "catalog.lifecycle.manage"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("状态计数端点说明未覆盖 %q：%q", want, summary)
		}
	}
}

// GET /catalog/definitions 的响应比存储中的定义配置多一个 kinds（handler 拼的骨架名，
// 见 http.go：288）。文档必须表达出来：该 schema 声明 additionalProperties:false，
// 漏写会让按文档做严格校验的客户端判失败。
func TestOpenAPIDefinitionsResponseIncludesKinds(t *testing.T) {
	doc := OpenAPI()
	op := doc["paths"].(map[string]any)["/catalog/definitions"].(map[string]any)["get"].(map[string]any)
	resp := op["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	ref, _ := resp["$ref"].(string)
	name := strings.TrimPrefix(ref, "#/components/schemas/")
	if name == "" || name == "DefinitionConfig" {
		t.Fatalf("响应 schema 仍是 %q，漏掉 kinds", name)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props, ok := schemas[name].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatalf("%s 不是对象 schema", name)
	}
	for _, k := range []string{"etag", "document", "updated_at", "kinds"} {
		if props[k] == nil {
			t.Fatalf("%s schema 缺 %s 字段", name, k)
		}
	}
}
