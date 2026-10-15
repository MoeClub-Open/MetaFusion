package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// entityListTitles 按给定 ListOptions 取回题名序列（默认只看已发布作品，与探索页同口径）。
func entityListTitles(t *testing.T, f fixture, o ListOptions) []string {
	t.Helper()
	o.Kind = "work"
	o.Status = "active"
	items, err := f.s.List(context.Background(), o, &f.u)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	out := make([]string, 0, len(items))
	for _, e := range items {
		out = append(out, e.Title)
	}
	return out
}

func sameOrder(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// 排序白名单在真库上的行为：白名单里的键必须真的换顺序（审计里的症状是三次请求
// id 序列完全相同，即参数被静默忽略），且 title 排序按界面语种题名而不是基础题名。
func TestPostgresEntityListSort(t *testing.T) {
	f := newFixture(t)
	// 基础题名与 zh-CN 翻译刻意反向：只有按请求语种取题名排序才能得到 Beta/Golf/Zulu 这一序，
	// 按基础题名排会得到 Alpha/Mid/Zeta。用拉丁字母是刻意的——汉字次序取决于库的 collation
	// （基线建库常为 C，按码点），拿它当断言会把"排序键取错"和"排序规则不同"混成一件事。
	f.save(Entity{Kind: "work", Title: "Zeta", Translations: map[string]Translation{"en-US": {Title: "Zeta"}, "zh-CN": {Title: "Beta"}}})
	f.save(Entity{Kind: "work", Title: "Mid", Translations: map[string]Translation{"en-US": {Title: "Mid"}, "zh-CN": {Title: "Golf"}}})
	f.save(Entity{Kind: "work", Title: "Alpha", Translations: map[string]Translation{"en-US": {Title: "Alpha"}, "zh-CN": {Title: "Zulu"}}})
	saved := []string{"Zeta", "Mid", "Alpha"}

	if got := entityListTitles(t, f, ListOptions{}); !sameOrder(got, []string{"Alpha", "Mid", "Zeta"}) {
		t.Fatalf("默认序应为最近更新倒序: %v", got)
	}
	if got := entityListTitles(t, f, ListOptions{Sort: "created_at", Order: "asc"}); !sameOrder(got, saved) {
		t.Fatalf("created_at asc 应为创建顺序（uuidv7 的 id 序）: %v", got)
	}
	if got := entityListTitles(t, f, ListOptions{Sort: "created_at", Order: "desc"}); !sameOrder(got, []string{"Alpha", "Mid", "Zeta"}) {
		t.Fatalf("created_at desc 应为创建倒序: %v", got)
	}
	if got := entityListTitles(t, f, ListOptions{Sort: "title", Locale: "zh-CN"}); !sameOrder(got, []string{"Zeta", "Mid", "Alpha"}) {
		t.Fatalf("title 默认方向应为 A→Z（按 zh-CN 翻译 Beta/Golf/Zulu）: %v", got)
	}
	if got := entityListTitles(t, f, ListOptions{Sort: "title", Order: "asc", Locale: "en-US"}); !sameOrder(got, []string{"Alpha", "Mid", "Zeta"}) {
		t.Fatalf("title 按 en-US 翻译应为 A/M/Z: %v", got)
	}
	// 原文语种档：原语言 zh-CN 的作品在英文界面下仍按 zh-CN 行排序（与 lib/titles.ts 的回退链同序）。
	if got := entityListTitles(t, f, ListOptions{Sort: "title", Order: "asc", Locale: "ja-JP"}); !sameOrder(got, []string{"Alpha", "Mid", "Zeta"}) {
		t.Fatalf("无 ja 行时应退到 en-US 行: %v", got)
	}
	if got := entityListTitles(t, f, ListOptions{Sort: "title", Order: "desc", Locale: "zh-CN"}); !sameOrder(got, []string{"Alpha", "Mid", "Zeta"}) {
		t.Fatalf("title desc 应为反向: %v", got)
	}
	// 语种缺失/非法时退到基础题名，不报错也不换回默认序。
	if got := entityListTitles(t, f, ListOptions{Sort: "title", Locale: "!!"}); !sameOrder(got, []string{"Alpha", "Mid", "Zeta"}) {
		t.Fatalf("非法语种应退到基础题名: %v", got)
	}
}

// 未知排序键/方向必须是显式 400，而不是"忽略参数后照常 200"。
func TestEntityListSortRejectsUnknownKey(t *testing.T) {
	args := []any{}
	if _, err := entityListOrderClause(ListOptions{Sort: "titles"}, &args); err == nil || err.Error() != "invalid_sort" {
		t.Fatalf("未知排序键应报 invalid_sort: %v", err)
	}
	o := ListOptions{Sort: "title", Order: "sideways"}
	if err := normalizeListSort(&o); err == nil || err.Error() != "invalid_order" {
		t.Fatalf("未知方向应报 invalid_order: %v", err)
	}
	o = ListOptions{Sort: "TITLE", Order: " ASC ", Locale: " zh-CN "}
	if err := normalizeListSort(&o); err != nil || o.Sort != "title" || o.Order != "asc" || o.Locale != "zh-CN" {
		t.Fatalf("合法参数应被归一化: %+v %v", o, err)
	}
	o = ListOptions{Sort: "title", Locale: "zh-CN; DROP TABLE"}
	if err := normalizeListSort(&o); err != nil || o.Locale != "" {
		t.Fatalf("形态非法的语种应被丢弃而非进入 SQL: %+v %v", o, err)
	}
}

// HTTP 契约：Query 里的 sort/order 被解析并生效，未知取值 400。
func TestHTTPEntityListSort(t *testing.T) {
	f := newFixture(t)
	f.save(Entity{Kind: "work", Title: "Zeta", Translations: map[string]Translation{"en": {Title: "Zeta"}}})
	f.save(Entity{Kind: "work", Title: "Alpha", Translations: map[string]Translation{"en": {Title: "Alpha"}}})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: f.s}.Register(r)
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	res := get("/api/catalog/entities?kind=work&status=published&sort=title&order=asc")
	if res.Code != http.StatusOK {
		t.Fatalf("sorted list = %d: %s", res.Code, res.Body.String())
	}
	var body struct {
		Items []Entity `json:"items"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.Items[0].Title != "Alpha" {
		t.Fatalf("sort=title 未生效: %+v", body.Items)
	}
	// 限流响应头随列表一起下发（四语字典承诺的那组头）。
	if res.Header().Get("X-RateLimit-Limit") != "180" || res.Header().Get("X-RateLimit-Remaining") == "" {
		t.Fatalf("缺少 X-RateLimit-* 响应头: %v", res.Header())
	}

	if res := get("/api/catalog/entities?sort=titlez"); res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_sort") {
		t.Fatalf("未知排序键 = %d %s, want 400 invalid_sort", res.Code, res.Body.String())
	}
	if res := get("/api/catalog/entities?sort=title&order=whatever"); res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "invalid_order") {
		t.Fatalf("未知方向 = %d %s, want 400 invalid_order", res.Code, res.Body.String())
	}
}
