package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/testutil"
)

// 来源清单必须与代码里的适配器集合一一对应：
// 多一个 = 界面能选、点了只拿 not_supported；少一个 = 能导入却没有入口。
func TestImporterSourcesMatchAdapterCodes(t *testing.T) {
	items := buildImporterSources(nil)
	if len(items) != len(importerAdapterCodes) {
		t.Fatalf("items=%d, want %d（适配器集合）", len(items), len(importerAdapterCodes))
	}
	byID := map[string]ImporterSource{}
	for _, it := range items {
		byID[it.ID] = it
	}
	seeds := map[string]ExternalDatabase{}
	for _, d := range externalDatabaseSeeds() {
		seeds[d.Code] = d
	}
	for _, code := range importerAdapterCodes {
		it, ok := byID[code]
		if !ok {
			t.Fatalf("适配器 %q 不在来源清单里", code)
		}
		// 有适配器就必须有注册表种子行：否则展示元数据无处可取，只剩四语缺口。
		seed, ok := seeds[code]
		if !ok {
			t.Fatalf("适配器 %q 在 externalDatabaseSeeds 里没有对应行", code)
		}
		if err := validateNames(Names(it.Names)); err != nil {
			t.Errorf("%s 名称不满足四语判据：%v", code, err)
		}
		if it.Category != seed.Category || it.Icon != seed.Icon || it.Description != seed.Description || it.URLPattern != seed.URLPattern {
			t.Errorf("%s 元数据与注册表种子不一致：%+v vs %+v", code, it, seed)
		}
		if it.Names["zh-CN"] != seed.Names["zh-CN"] || it.Names["ja-JP"] != seed.Names["ja-JP"] || it.Names["en-US"] != seed.Names["en-US"] {
			t.Errorf("%s 名称与注册表种子不一致：%v vs %v", code, it.Names, seed.Names)
		}
	}
	// 反向：注册表里**没有适配器**的 code 一个都不许出现。此前导入弹窗把这些名字
	// 写死在 tab 上，点进去只会拿到 not_supported。
	for _, code := range []string{"musicbrainz", "discogs", "vgmdb", "tmdb", "imdb", "official_website", "bushiroad_music", "wikipedia", "wikidata", "vndb", "douban_movie", "bangumi_person", "bangumi_character"} {
		if _, ok := byID[code]; ok {
			t.Errorf("%s 没有导入适配器，却被列为可用导入源", code)
		}
	}
	// auto 是解析别名而非来源：Preview 探测具体适配器，Import 默认 bangumi，
	// 因此不能作为独立来源混进 items（否则管理台会多出一个"库"）。
	if _, ok := byID["auto"]; ok {
		t.Error("auto 是解析别名，不应作为独立来源出现在清单里")
	}
}

// 展示元数据跟着注册表走：后台改名/换图标后端点即变；注册表缺行时回落到种子，
// 不让一个真能抓取的来源因为元数据缺失而消失。
func TestImporterSourcesUseRegistryMetadata(t *testing.T) {
	rows := externalDatabaseSeeds()
	found := false
	for i := range rows {
		if rows[i].Code != importerAdapterCodes[0] {
			continue
		}
		found = true
		rows[i].Names = map[string]string{"zh-CN": "后台改过的名字", "zh-TW": "後台改過的名字", "ja": "変更後の名前", "ja-JP": "変更後の名前", "en-US": "Renamed by admin"}
		rows[i].Category = "work"
		rows[i].Icon = "Database"
		rows[i].Description = "后台改过的说明"
	}
	if !found {
		t.Fatalf("种子行 %q 不存在", importerAdapterCodes[0])
	}
	items := buildImporterSources(rows)
	if len(items) != len(importerAdapterCodes) {
		t.Fatalf("items=%d, want %d（适配器集合）", len(items), len(importerAdapterCodes))
	}
	got := items[0]
	if got.ID != importerAdapterCodes[0] {
		t.Fatalf("首项=%s，want %s", got.ID, importerAdapterCodes[0])
	}
	if got.Names["zh-CN"] != "后台改过的名字" {
		t.Errorf("名称未取注册表行：%v", got.Names)
	}
	if got.Category != "work" || got.Icon != "Database" || got.Description != "后台改过的说明" {
		t.Errorf("注册表行的展示元数据未生效：%+v", got)
	}
	// 注册表无行（未播种/被手工删）→ 种子兜底，来源仍在。
	fallback := buildImporterSources(nil)
	if len(fallback) != len(importerAdapterCodes) || fallback[0].ID != importerAdapterCodes[0] || strings.TrimSpace(fallback[0].Names["zh-CN"]) == "" {
		t.Fatalf("注册表缺行时未回落到种子元数据：%+v", fallback)
	}
}

// 端点形状与闸门：与 /external-sources/query 同权限（authentication (signed-in)），
// 未登录 401、没这个码 403、持码 200 且 items 的键集合就是文档里声明的那几个。
func TestImporterSourcesEndpointGateAndShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := "/api/external-sources"

	w := httptest.NewRecorder()
	gateEngine(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "authentication_required") {
		t.Fatalf("匿名：status=%d body=%s，want 401 authentication_required", w.Code, w.Body.String())
	}
	// 登录即可：任意已登录用户可读来源清单。
	plain := &User{ID: "u-plain", Permissions: []string{"community.post.create"}}
	w = httptest.NewRecorder()
	gateEngine(plain).ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("已登录：status=%d body=%s，want 200", w.Code, w.Body.String())
	}
	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 {items:[...]}：%v body=%s", err, w.Body.String())
	}
	if len(body.Items) != len(importerAdapterCodes) {
		t.Fatalf("items=%d, want %d", len(body.Items), len(importerAdapterCodes))
	}
	for _, it := range body.Items {
		keys := make([]string, 0, len(it))
		for k := range it {
			keys = append(keys, k)
		}
		want := map[string]bool{"id": true, "names": true, "category": true, "icon": true, "description": true, "url_pattern": true}
		for _, k := range keys {
			if !want[k] {
				t.Errorf("响应多出未声明字段 %q（客户端按 additionalProperties=false 校验会失败）", k)
			}
			delete(want, k)
		}
		if len(want) > 0 {
			t.Errorf("%v 缺少字段 %v", it["id"], want)
		}
	}
}

// 真库：清单读注册表行（后台改名即时生效），且后加的无适配器行不会冒充可导入来源。
func TestImporterSourcesFollowRegistryRows(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE catalog.external_databases SET names=jsonb_set(names,'{zh-CN}','"后台改的名"'), icon='Database' WHERE code='bangumi'`); err != nil {
		t.Fatal(err)
	}
	items, err := s.ImporterSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != len(importerAdapterCodes) || items[0].ID != "bangumi" {
		t.Fatalf("items=%+v，want 以 bangumi 为首的适配器集合", items)
	}
	if items[0].Names["zh-CN"] != "后台改的名" || items[0].Icon != "Database" {
		t.Errorf("未读到注册表行：%+v", items[0])
	}
	// 管理员新增一个库 ≠ 这个库能导入：没有适配器的 code 不许出现。
	if _, err := s.CreateExternalDatabase(ctx, ExternalDatabase{
		Code:        "custom_library",
		Names:       map[string]string{"zh-CN": "自建库", "zh-TW": "自建庫", "ja": "自作ライブラリ", "ja-JP": "自作ライブラリ", "en-US": "Custom Library"},
		Category:    "all",
		URLPattern:  "https://example.com/item/{id}",
		Icon:        "Globe",
		Description: "管理员新增，但没有适配器",
	}); err != nil {
		t.Fatal(err)
	}
	items, err = s.ImporterSources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == "custom_library" {
			t.Fatal("没有适配器的注册表行被当成可用导入源")
		}
	}
	if len(items) != len(importerAdapterCodes) {
		t.Fatalf("items=%d，want %d", len(items), len(importerAdapterCodes))
	}
}

// 文档与实现同形：响应 schema 的属性集合必须与 DTO 一致，否则客户端会按文档拒绝真实响应。
func TestOpenAPIImporterSourceSchema(t *testing.T) {
	doc := OpenAPI()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	src, ok := schemas["ImporterSource"].(map[string]any)
	if !ok {
		t.Fatal("ImporterSource schema 缺失")
	}
	props, _ := src["properties"].(map[string]any)
	for _, k := range []string{"id", "names", "category", "icon", "description", "url_pattern"} {
		if props[k] == nil {
			t.Errorf("ImporterSource 缺少属性 %q", k)
		}
	}
	if len(props) != 6 {
		t.Errorf("ImporterSource 属性数=%d，want 6：%v", len(props), props)
	}
	list, ok := schemas["ImporterSourceList"].(map[string]any)
	if !ok {
		t.Fatal("ImporterSourceList schema 缺失")
	}
	if list["properties"].(map[string]any)["items"] == nil {
		t.Error("ImporterSourceList 缺少 items 属性")
	}
	op, ok := doc["paths"].(map[string]any)["/external-sources"].(map[string]any)["get"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI 未声明 GET /external-sources")
	}
	ref := op["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"]
	if ref != "#/components/schemas/ImporterSourceList" {
		t.Errorf("200 响应 schema=%v，want ImporterSourceList", ref)
	}
	if _, ok := op["security"]; !ok {
		t.Error("/external-sources 需登录，必须声明 security")
	}
}
