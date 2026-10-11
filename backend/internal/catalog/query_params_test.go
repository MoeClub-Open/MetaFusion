package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// 原语言与封面筛选的真库契约：has_pictures=1 必须 200（不是 500），
// 且只返回 pictures 非空数组的条目。迁移后 pictures 必须符合数组契约。
// 未设置 MF_V2_TEST_DSN 时跳过。
func TestPostgresEntityListLanguageAndCoverContract(t *testing.T) {
	f := newFixture(t)
	withCover := f.save(Entity{Kind: "work", Title: "有封面", OriginalLanguage: "ja",
		Pictures: PicturesJSON{{URL: "https://example.test/c.jpg", Source: Source{Kind: "url", Citation: "test", URL: "https://example.test/c.jpg"}}}})
	_ = f.save(Entity{Kind: "work", Title: "无封面", OriginalLanguage: "en"})
	engine := fixtureEngine(f)

	covered, totalCovered := listIDs(t, engine, "/api/catalog/entities?has_pictures=1&limit=100")
	if totalCovered != 1 || len(covered) != 1 || covered[0] != withCover.ID {
		t.Fatalf("has_pictures=1 应只返回有封面条目: ids=%v total=%d", covered, totalCovered)
	}
	_, totalJa := listIDs(t, engine, "/api/catalog/entities?original_language=ja&limit=100")
	if totalJa != 1 {
		t.Fatalf("original_language=ja 应返回 1 条: total=%d", totalJa)
	}
	if _, total := listIDs(t, engine, "/api/catalog/entities?limit=100"); total != 2 {
		t.Fatalf("无筛选应返回 2 条: total=%d", total)
	}
}

// 审计 2026-09-19 第 5 条：GET /api/catalog/entities?q=%00（及 %FF/%C3）线上返回
// 500 {"error":"database_error"}——非法输入被报成服务故障，前端又把它渲染成"没有结果"。
// 这些用例钉住"参数层就拒绝"，且不需要数据库：Store 在参数闸门之后才会用到。
func TestListRejectsUnsendableQueryParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name string
		path string
		code string
	}{
		{"NUL", "/api/catalog/entities?q=%00", codeInvalidQueryParam},
		{"NUL in middle", "/api/catalog/entities?q=a%00b", codeInvalidQueryParam},
		{"invalid utf-8", "/api/catalog/entities?q=%FF", codeInvalidQueryParam},
		{"truncated utf-8", "/api/catalog/entities?q=%C3", codeInvalidQueryParam},
		{"newline", "/api/catalog/entities?q=a%0Ab", codeInvalidQueryParam},
		{"bare percent drops the value", "/api/catalog/entities?q=100%", codeInvalidQueryParam},
		{"overlong", "/api/catalog/entities?q=" + strings.Repeat("a", listTextParamLimit+1), codeQueryTooLong},
		{"attribute value", "/api/catalog/entities?kind=work&value=%00", codeInvalidQueryParam},
		{"tag", "/api/catalog/entities?tags=%FF", codeInvalidQueryParam},
		{"tags endpoint", "/api/catalog/tags?q=%00", codeInvalidQueryParam},
		{"limit not a number", "/api/catalog/entities?limit=abc", codeInvalidLimit},
		{"limit fraction", "/api/catalog/entities?limit=1.5", codeInvalidLimit},
		{"limit blank space", "/api/catalog/entities?limit=%20", codeInvalidLimit},
		{"limit zero", "/api/catalog/entities?limit=0", codeInvalidLimit},
		{"limit negative", "/api/catalog/entities?limit=-1", codeInvalidLimit},
		{"limit over cap", "/api/catalog/entities?limit=101", codeInvalidLimit},
		{"offset not a number", "/api/catalog/entities?offset=abc", codeInvalidOffset},
		{"offset negative", "/api/catalog/entities?offset=-1", codeInvalidOffset},
		{"page not a number", "/api/catalog/entities?page=abc", codeInvalidPage},
		{"page zero", "/api/catalog/entities?page=0", codeInvalidPage},
		{"page negative", "/api/catalog/entities?page=-3", codeInvalidPage},
		{"page overflow", "/api/catalog/entities?page=99999999999999999999", codeInvalidPage},
		{"page and offset together", "/api/catalog/entities?page=2&offset=24", codePaginationConflict},
	} {
		w := httptest.NewRecorder()
		gateEngine(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s want 400 %s", tc.name, w.Code, w.Body.String(), tc.code)
			continue
		}
		// 响应体必须是稳定机器码本身：不是 500 database_error、不是裸 SQL、也不是空体。
		if !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("%s: body=%s want code %s", tc.name, w.Body.String(), tc.code)
		}
	}
}

// 对照：合法取值不能被这道闸门误伤（制表符是允许的，见 checkQueryText）。
func TestCheckQueryTextAllowsLegitimateValues(t *testing.T) {
	for _, ok := range []string{"", "デルタルーン", "Deltarune: Chapter 1", "a\tb", strings.Repeat("a", listTextParamLimit), "\u00a0"} {
		if err := checkQueryText(ok); err != nil {
			t.Errorf("合法取值被拒: %q -> %v", ok, err)
		}
	}
}

// listPagination 的归一表：不需要数据库，直接看 limit/offset 的换算。
func TestListPaginationNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		query         string
		limit, offset int
	}{
		{"", defaultListLimit, 0},
		{"limit=&offset=", defaultListLimit, 0},
		{"limit=24", 24, 0},
		{"limit=100&offset=200", 100, 200},
		{"page=1", defaultListLimit, 0},
		{"page=3", defaultListLimit, 100},
		{"page=3&limit=24", 24, 48},
		{"offset=48", defaultListLimit, 48},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/catalog/entities?"+tc.query, nil)
		limit, offset, err := listPagination(c)
		if err != nil {
			t.Errorf("%q: %v", tc.query, err)
			continue
		}
		if limit != tc.limit || offset != tc.offset {
			t.Errorf("%q: limit=%d offset=%d want %d/%d", tc.query, limit, offset, tc.limit, tc.offset)
		}
	}
}

// fixtureEngine 用真库夹具挂真实路由表：正例（合法参数）只有走到 Store 才能证明没被拒。
func fixtureEngine(f fixture) *gin.Engine {
	r := gin.New()
	u := f.u
	r.Use(func(c *gin.Context) {
		c.Set("catalog_user", &u)
		c.Next()
	})
	HTTP{Store: f.s}.Register(r)
	return r
}

// listIDs 请求列表端点，返回 items 的 id 序列与响应里的 total。
func listIDs(t *testing.T, engine *gin.Engine, path string) ([]string, int64) {
	t.Helper()
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s: status=%d body=%s", path, w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	raw, _ := body["items"].([]any)
	ids := make([]string, 0, len(raw))
	for _, it := range raw {
		if m, ok := it.(map[string]any); ok {
			ids = append(ids, m["id"].(string))
		}
	}
	total, _ := body["total"].(float64)
	return ids, int64(total)
}

// 审计第 10 条：API 完全忽略 page（page=2 与 page=1 返回同一批数据），而页面
// /explore?page=99999 又显示"共 0 条"、与侧栏"全部实体 N"自相矛盾。这里同时钉住三件事：
// page 真的换页、page 与 offset 等价、越界页仍给出与列表同口径的 total（不是 0）。
func TestPostgresEntityListPaginationContract(t *testing.T) {
	f := newFixture(t)
	created := map[string]bool{}
	for _, title := range []string{"Alpha", "Mid", "Zeta"} {
		created[f.save(Entity{Kind: "work", Title: title}).ID] = true
	}
	engine := fixtureEngine(f)

	page1, total1 := listIDs(t, engine, "/api/catalog/entities?kind=work&limit=1&page=1")
	page2, total2 := listIDs(t, engine, "/api/catalog/entities?kind=work&limit=1&page=2")
	byOffset, _ := listIDs(t, engine, "/api/catalog/entities?kind=work&limit=1&offset=1")
	if len(page1) != 1 || len(page2) != 1 {
		t.Fatalf("每页应为 1 条: %v %v", page1, page2)
	}
	if page1[0] == page2[0] {
		t.Fatalf("page=2 必须与 page=1 不同（参数被忽略正是本条审计症状）: %v", page1)
	}
	if total1 != 3 || total2 != 3 {
		t.Fatalf("total 应为筛选后的真实条数 3: page1=%d page2=%d", total1, total2)
	}
	if page2[0] != byOffset[0] {
		t.Fatalf("page=2&limit=1 必须与 offset=1&limit=1 同页: %v vs %v", page2, byOffset)
	}
	// 逐页拼起来必须是不重不漏的全集。
	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		ids, _ := listIDs(t, engine, "/api/catalog/entities?kind=work&limit=1&page="+strconv.Itoa(page))
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("第 %d 页重复返回 %s", page, id)
			}
			seen[id] = true
		}
	}
	if len(seen) != len(created) {
		t.Fatalf("逐页取回 %d 条，期望 %d 条", len(seen), len(created))
	}
	// 越界页：200 + 空 items，但 total 仍是真实总数——页面据它显示"共 3 条"而不是"共 0 条"。
	outOfRange, totalOut := listIDs(t, engine, "/api/catalog/entities?kind=work&limit=1&page=99999")
	if len(outOfRange) != 0 || totalOut != 3 {
		t.Fatalf("越界页应为空 items + 真实 total=3: items=%v total=%d", outOfRange, totalOut)
	}
	// 合法 UTF-8 与中文查询不能被参数闸门误伤（对照上面的 400 用例）。
	useTestSearch(t, f.s)
	ids, total := listIDs(t, engine, "/api/catalog/entities?kind=work&q=%E4%B8%AD%E6%96%87&limit=100")
	if total != 0 || len(ids) != 0 {
		t.Fatalf("不存在的题名应 200 空结果: %v", ids)
	}
	if _, total := listIDs(t, engine, "/api/catalog/entities?kind=work&limit=100"); total != 3 {
		t.Fatalf("limit=100 应被接受: total=%d", total)
	}
}
