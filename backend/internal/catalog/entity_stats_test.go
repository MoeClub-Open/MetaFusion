package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 纯函数口径：五个状态键一定齐全（没有行就是真实的 0），total 取所有分组之和，
// 未知状态值也计入 total——"这个状态是 0"与"没拿到这个状态"必须是两件事，
// 前者是事实，后者在响应里表现为键缺失（前端据此显示占位符而不是 0）。
func TestFoldStatusCounts(t *testing.T) {
	empty := foldStatusCounts(nil)
	if len(empty.Statuses) != len(entityStatuses) || empty.Total != 0 {
		t.Fatalf("空表应给出齐全的五键与 total=0: %+v", empty)
	}
	for _, code := range entityStatuses {
		if empty.Statuses[code] != 0 {
			t.Fatalf("没有行的状态 %s 应补齐为 0: %+v", code, empty.Statuses)
		}
	}
	got := foldStatusCounts([]statusGroup{{Status: "active", Count: 3070}, {Status: "deleted", Count: 288}, {Status: "unexpected", Count: 5}})
	if got.Statuses["active"] != 3070 || got.Statuses["deleted"] != 288 {
		t.Fatalf("已知状态应原样带出: %+v", got.Statuses)
	}
	for _, code := range []string{"merged"} {
		if got.Statuses[code] != 0 {
			t.Fatalf("缺失状态 %s 应补 0: %+v", code, got.Statuses)
		}
	}
	if got.Total != 3363 || got.Statuses["unexpected"] != 5 {
		t.Fatalf("total 必须含未预期的状态值，不静默丢行: %+v", got)
	}
}

// 真库（无 MF_V2_TEST_DSN 时跳过）：聚合端点与直接 SQL 的计数逐状态一致，
// 闸门按 catalog.lifecycle.manage 分档（未登录 401 / 别的目录码 403 / 掌码 200），
// 并且列表端点的可见性口径没被顺手改宽——status=deleted 的列表与总数仍是空。
func TestEntityStatsMatchesDirectSQLOnPostgres(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newFixture(t)
	ctx := context.Background()
	published := f.save(Entity{Kind: "work", Title: "计数用例·将成墓碑"})
	// 另留一条已发布：墓碑数要与"仍在线的数量"分开，别把清退写成发布态消失。
	f.save(Entity{Kind: "work", Title: "计数用例·已发布"})
	f.save(Entity{Kind: "work", Title: "计数用例·草稿", Status: "active"})
	f.save(Entity{Kind: "work", Title: "计数用例·待审", Status: "active"})
	if _, err := f.s.Lifecycle(ctx, published.ID, LifecycleEdit{ExpectedVersion: published.Version, EditNote: "计数用例清退", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatalf("造墓碑行: %v", err)
	}

	// 直接 SQL 是判据：逐状态与总数各比一次。
	want := map[string]int64{}
	rows, err := f.s.DB.QueryContext(ctx, "SELECT status, count(*) FROM catalog.entities GROUP BY status")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		want[status] = n
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	var wantTotal int64
	if err := f.s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.entities").Scan(&wantTotal); err != nil {
		t.Fatal(err)
	}
	if want["deleted"] != 1 || want["active"] != 3 {
		t.Fatalf("夹具没造出预期的状态分布: %+v", want)
	}

	got, err := f.s.StatusCounts(ctx)
	if err != nil {
		t.Fatalf("StatusCounts: %v", err)
	}
	if got.Total != wantTotal {
		t.Fatalf("total=%d, 直接 SQL=%d", got.Total, wantTotal)
	}
	for _, code := range entityStatuses {
		if got.Statuses[code] != want[code] {
			t.Fatalf("状态 %s: 聚合=%d, 直接 SQL=%d", code, got.Statuses[code], want[code])
		}
	}
	for code, n := range want {
		if got.Statuses[code] != n {
			t.Fatalf("聚合漏掉直接 SQL 里的状态 %s=%d: %+v", code, n, got.Statuses)
		}
	}

	engine := func(u *User) *gin.Engine {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			if u != nil {
				c.Set("catalog_user", u)
			}
			c.Next()
		})
		HTTP{Store: f.s}.Register(r)
		return r
	}
	editor := fixtureUser("editor")
	for _, tc := range []struct {
		name string
		u    *User
		want int
		code string
	}{
		{"anonymous", nil, http.StatusUnauthorized, "authentication_required"},
		{"other catalog code", &editor, http.StatusForbidden, "forbidden"},
	} {
		w := httptest.NewRecorder()
		engine(tc.u).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/catalog/entities/stats", nil))
		if w.Code != tc.want || !jsonHasError(w.Body.Bytes(), tc.code) {
			t.Fatalf("%s: status=%d body=%s, want %d %s", tc.name, w.Code, w.Body.String(), tc.want, tc.code)
		}
	}
	w := httptest.NewRecorder()
	engine(&f.u).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/catalog/entities/stats", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("掌码者应拿到 200: status=%d body=%s", w.Code, w.Body.String())
	}
	var body EntityStatusCounts
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 EntityStatusCounts 的形状: %v %s", err, w.Body.String())
	}
	if body.Total != wantTotal {
		t.Fatalf("响应 total=%d, 直接 SQL=%d", body.Total, wantTotal)
	}
	for _, code := range entityStatuses {
		if body.Statuses[code] != want[code] {
			t.Fatalf("响应状态 %s=%d, 直接 SQL=%d", code, body.Statuses[code], want[code])
		}
	}

	// 列表口径未被改动：墓碑行照样不出现在列表里，计数只能由本端点给出。
	if n, err := f.s.Count(ctx, ListOptions{Status: "deleted"}, &f.u); err != nil || n != 0 {
		t.Fatalf("status=deleted 的列表总数应仍为 0: %v %d", err, n)
	}
	if items, err := f.s.List(ctx, ListOptions{Status: "deleted"}, &f.u); err != nil || len(items) != 0 {
		t.Fatalf("listFilter 语义被改动: %v %d", err, len(items))
	}
}

// jsonHasError 断言错误响应是统一的单 error 字段形状（respond 的稳定机器码）。
func jsonHasError(raw []byte, code string) bool {
	var body struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(raw, &body) == nil && body.Error == code
}
