package catalog

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"strings"
	"testing"
)

func TestOpenAPIRouteCoverage(t *testing.T) {
	t.Setenv("STORAGE_CATALOG_LIFECYCLE_SECRET", "openapi-contract-test")
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
		path := registeredOpenAPIPath(route.Path)
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

func TestOpenAPICollaborationWorkflowContract(t *testing.T) {
	doc := OpenAPI()
	paths := doc["paths"].(map[string]any)
	for _, path := range []string{"/catalog/checkout", "/catalog/commits/preview", "/catalog/commits/{id}/submission", "/catalog/commits/{id}/review"} {
		if paths[path] != nil {
			t.Fatalf("retired workflow remains documented: %s", path)
		}
	}
	if paths["/catalog/commits"] != nil {
		t.Fatal("retired global commit write/list remains documented")
	}
	for _, item := range [][2]string{{"/catalog/entities", "post"}, {"/catalog/entities/{id}", "put"}, {"/catalog/relations", "post"}, {"/catalog/relations/{id}", "put"}, {"/catalog/relations/{id}", "delete"}} {
		if ops, ok := paths[item[0]].(map[string]any); ok && ops[item[1]] != nil {
			t.Fatalf("retired direct fact write remains documented: %v", item)
		}
	}
	push := paths["/catalog/workspaces/{id}/push"].(map[string]any)["post"].(map[string]any)
	if push["responses"].(map[string]any)["202"] == nil || !strings.Contains(push["description"].(string), "catalog_review_exempt") {
		t.Fatal("missing pull-request or account exemption contract")
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"Workspace", "WorkspaceChanges", "WorkspaceCommitCreate", "CollaborationCommit", "PullRequest", "PullRequestCreate", "WorkspaceRebase"} {
		if schemas[name] == nil {
			t.Fatalf("missing collaboration schema: %s", name)
		}
	}
	create := schemas["PullRequestCreate"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"workspace_id", "expected_version", "expected_head", "title"} {
		if create[name] == nil {
			t.Fatalf("embedded push field omitted from PullRequestCreate: %s", name)
		}
	}
	operation := schemas["WorkspaceOperation"].(map[string]any)["properties"].(map[string]any)
	if operation["source_refs"] != nil || operation["reviewed_candidate_ids"] != nil {
		t.Fatal("workspace operations must not impose source or candidate verification records")
	}
	if operation["document"].(map[string]any)["type"] != "object" {
		t.Fatal("workspace document must be JSON object, not RawMessage bytes")
	}
	for _, name := range []string{"CatalogCommit", "CommitReceipt", "CommitSubmission", "CommitReviewEdit"} {
		if schemas[name] != nil {
			t.Fatalf("retired write DTO remains in public schemas: %s", name)
		}
	}
	for _, name := range []string{"WorkspacePage", "PullRequestPage"} {
		properties := schemas[name].(map[string]any)["properties"].(map[string]any)
		for _, field := range []string{"items", "total", "limit", "offset"} {
			if properties[field] == nil {
				t.Fatalf("%s missing actual page field %s", name, field)
			}
		}
	}
	for _, path := range []string{"/catalog/workspaces", "/catalog/pull-requests"} {
		operation := paths[path].(map[string]any)["get"].(map[string]any)
		params := map[string]bool{}
		for _, item := range operation["parameters"].([]any) {
			params[item.(map[string]any)["name"].(string)] = true
		}
		if !params["limit"] || !params["offset"] || !params["page"] {
			t.Fatalf("%s missing actual pagination parameters", path)
		}
		if path == "/catalog/pull-requests" && (!params["status"] || !params["workspace_id"]) {
			t.Fatal("pull request list missing actual filters")
		}
	}
}

func TestOpenAPIReverseCoverage(t *testing.T) {
	t.Setenv("STORAGE_CATALOG_LIFECYCLE_SECRET", "openapi-contract-test")
	// 反向检查：文档中的每条 path+method 必须在 gin 路由中真实注册，
	// 防止文档残留已删除端点（如重复的 /catalog/shelves）。
	doc := OpenAPI()
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)
	registered := map[string]bool{}
	for _, route := range r.Routes() {
		path := registeredOpenAPIPath(route.Path)
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
		{"/catalog/workspaces", "post", true},
		{"/catalog/entities", "get", false},
		// 概览计数是管理台口径的聚合，必须登录 + catalog.lifecycle.manage：它含 deleted/merged，
		// 匿名读会绕过列表端点刻意的可见性过滤。
		{"/catalog/entities/stats", "get", true},
		{"/catalog/me/home-preferences", "get", true},
		{"/catalog/me/home-preferences", "put", true},
		{"/catalog/pull-requests/{id}/merge", "post", true},
		{"/catalog/pull-requests", "get", false},
		{"/catalog/pull-requests/{id}", "get", false},
		{"/catalog/commits/{id}", "get", false},
		{"/external-sources", "get", true},
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

func registeredOpenAPIPath(route string) string {
	segments := strings.Split(strings.TrimPrefix(route, "/api"), "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			segments[index] = "{" + strings.TrimPrefix(segment, ":") + "}"
		}
	}
	return strings.Join(segments, "/")
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
	if ref != "#/components/schemas/EntityStateCounts" {
		t.Fatalf("状态计数端点 200 响应 schema=%q, want EntityStateCounts", ref)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props, ok := schemas["EntityStateCounts"].(map[string]any)["properties"].(map[string]any)
	if !ok {
		t.Fatal("EntityStateCounts schema 缺失")
	}
	if props["states"] == nil || props["total"] == nil {
		t.Fatalf("EntityStateCounts schema 缺 statuses/total: %+v", props)
	}
	if props["pending_pull_requests"] == nil || props["pending_commits"] != nil {
		t.Fatal("entity stats must expose open pull requests instead of the retired submission queue")
	}
	// 五个状态键逐个冻结，且与 Go 侧 entityStates（迁移 CHECK 的全集）逐字一致。
	statusProps, ok := props["states"].(map[string]any)["properties"].(map[string]any)
	if !ok || len(statusProps) != len(entityStates) {
		t.Fatalf("statuses 应逐个声明 %d 个状态: %+v", len(entityStates), statusProps)
	}
	for _, code := range entityStates {
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

func TestOpenAPIWorkspaceFieldConflicts(t *testing.T) {
	doc := OpenAPI()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	props := schemas["WorkspaceConflict"].(map[string]any)["properties"].(map[string]any)
	for _, key := range []string{"target", "id", "base_version", "current_version", "pointer", "base", "current", "proposed", "base_present", "current_present", "proposed_present", "kind"} {
		if props[key] == nil {
			t.Fatalf("WorkspaceConflict.%s missing", key)
		}
	}
	response := schemas["WorkspaceConflictResponse"].(map[string]any)
	for _, key := range []string{"error", "applied", "conflicts"} {
		if !requiredSet(response["required"])[key] {
			t.Fatalf("conflict response required field missing: %s", key)
		}
	}
	for _, path := range []string{"/catalog/workspaces/{id}/push", "/catalog/workspaces/{id}/rebase", "/catalog/pull-requests/{id}/merge"} {
		op := doc["paths"].(map[string]any)[path].(map[string]any)["post"].(map[string]any)
		if !strings.Contains(encode(op["responses"].(map[string]any)["409"]), "WorkspaceConflictResponse") {
			t.Fatalf("%s missing structured conflict schema", path)
		}
	}
}

func TestOpenAPISourcesAreOptionalMetadata(t *testing.T) {
	doc := OpenAPI()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	src := schemas["SourceInput"].(map[string]any)
	if requiredSet(src["required"])["key"] {
		t.Fatal("source key must be optional")
	}
	for _, name := range []string{"DefinitionSave", "WorkspaceChanges", "WorkspaceCommitCreate", "WorkspacePush", "PullRequestCreate", "PullRequestMerge"} {
		object := schemas[name].(map[string]any)
		for _, field := range []string{"sources", "source_refs", "reviewed_candidate_ids"} {
			if requiredSet(object["required"])[field] {
				t.Fatalf("%s requires retired evidence field %s", name, field)
			}
		}
	}
}

func TestOpenAPIContributionYearAndPagination(t *testing.T) {
	doc := OpenAPI()
	op := doc["paths"].(map[string]any)["/users/{id}/contributions"].(map[string]any)["get"].(map[string]any)
	params := map[string]bool{}
	for _, value := range op["parameters"].([]any) {
		params[value.(map[string]any)["name"].(string)] = true
	}
	for _, key := range []string{"id", "year", "page", "page_size"} {
		if !params[key] {
			t.Fatalf("contribution query parameter missing: %s", key)
		}
	}
	if params["tab"] {
		t.Fatal("retired contribution tabs remain documented")
	}
}

func TestOpenAPIPrivateAssetPreviewsAndReferenceCheck(t *testing.T) {
	t.Setenv("STORAGE_CATALOG_LIFECYCLE_SECRET", "openapi-contract-test")
	doc := OpenAPI()
	paths := doc["paths"].(map[string]any)
	for _, scope := range []string{"workspaces", "pull-requests", "commits"} {
		path := "/catalog/" + scope + "/{id}/assets/{assetID}/content"
		op := paths[path].(map[string]any)["get"].(map[string]any)
		parameters := op["parameters"].([]any)
		if len(parameters) != 2 || parameters[1].(map[string]any)["name"] != "assetID" {
			t.Fatalf("%s missing assetID path parameter", path)
		}
		response := op["responses"].(map[string]any)["200"].(map[string]any)
		content := response["content"].(map[string]any)
		for _, mime := range []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"} {
			if content[mime] == nil {
				t.Fatalf("%s missing binary response %s", path, mime)
			}
		}
		if content["application/json"] != nil {
			t.Fatalf("%s must document image bytes instead of a JSON result", path)
		}
		if response["headers"].(map[string]any)["Cache-Control"] == nil {
			t.Fatalf("%s missing private cache contract", path)
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	push := schemas["WorkspacePushResult"].(map[string]any)
	workspace := push["properties"].(map[string]any)["workspace"].(map[string]any)
	if workspace["nullable"] != true || !requiredSet(push["required"])["workspace"] {
		t.Fatal("push result must retain a required nullable workspace field")
	}
	commitObject := schemas["CommitObject"].(map[string]any)
	if !requiredSet(commitObject["required"])["base"] {
		t.Fatal("immutable commit objects must document their distinct base snapshot")
	}
	internal := paths["/internal/storage/asset-references"].(map[string]any)["post"].(map[string]any)
	if internal["servers"].([]any)[0].(map[string]any)["url"] != "/" {
		t.Fatal("internal reference check must override the public /api server base")
	}
	if !strings.Contains(encode(internal["security"]), "storageService") {
		t.Fatal("internal reference check must require its dedicated service credential")
	}
	if schemas["CatalogAssetReferencesRequest"] == nil || schemas["CatalogAssetReferencesResponse"] == nil {
		t.Fatal("internal reference check must have exact request and response schemas")
	}
	t.Setenv("STORAGE_CATALOG_LIFECYCLE_SECRET", "")
	if OpenAPI()["paths"].(map[string]any)["/internal/storage/asset-references"] != nil {
		t.Fatal("disabled internal route must not remain documented")
	}
}

func TestOpenAPIReferencesResolve(t *testing.T) {
	doc := OpenAPI()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	var inspect func(any)
	inspect = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			if ref, ok := item["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/schemas/") {
				if schemas[strings.TrimPrefix(ref, "#/components/schemas/")] == nil {
					t.Errorf("unresolved schema reference: %s", ref)
				}
			}
			for _, nested := range item {
				inspect(nested)
			}
		case []any:
			for _, nested := range item {
				inspect(nested)
			}
		}
	}
	inspect(doc)
}

func TestOpenAPIExternalLinkRequiresKindAndURL(t *testing.T) {
	doc := OpenAPI()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	el, ok := schemas["ExternalLink"].(map[string]any)
	if !ok {
		t.Fatal("ExternalLink schema missing")
	}
	got := requiredSet(el["required"])
	for _, want := range []string{"kind", "url"} {
		if !got[want] {
			t.Fatalf("ExternalLink.required missing %q: %#v", want, el["required"])
		}
	}
	props, _ := el["properties"].(map[string]any)
	for _, want := range []string{"kind", "url"} {
		if props[want] == nil {
			t.Fatalf("ExternalLink.properties.%s missing", want)
		}
	}
}

func requiredSet(v any) map[string]bool {
	out := map[string]bool{}
	switch req := v.(type) {
	case []string:
		for _, s := range req {
			out[s] = true
		}
	case []any:
		for _, item := range req {
			if s, ok := item.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}
