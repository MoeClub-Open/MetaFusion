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

// resolveEngine 用真库 store 建一个只注入身份的引擎：被测的是 /entities/:id/resolve
// ——引用解析的唯一入口（u 为 nil 即匿名，与线上匿名浏览同一口径）。
func resolveEngine(s *Store, u *User) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if u != nil {
			c.Set("catalog_user", u)
		}
		c.Next()
	})
	HTTP{Store: s}.Register(r)
	return r
}

func resolveOnce(t *testing.T, eng *gin.Engine, id string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/catalog/entities/"+id+"/resolve", nil))
	return w.Code, w.Body.String()
}

// 发行版"出版厂牌"这类实体引用在 DTO 里只是一串 id（attributes.publisher 是纯字符串），
// 展示名只能由前端拿 /resolve 换。因此这条端点在引用失效时必须回 404 not_found，
// 而不是 200 + 空题名：前端据此显示可读占位（未知厂牌），而不是把裸 UUID 印在页面上。
//
// 真库用例（无 MF_V2_TEST_DSN 时跳过）：删/合并是状态跃迁而不是删行，走真 Lifecycle。
func TestResolveDanglingReferenceOnPostgres(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	anon := resolveEngine(f.s, nil)

	// 1) 引用可用：厂牌实体解析得到展示名。
	label := f.save(Entity{Kind: "agent", Title: "Epic Records"})
	code, body := resolveOnce(t, anon, label.ID)
	if code != http.StatusOK {
		t.Fatalf("可解析引用应 200，实际 %d（%s）", code, body)
	}
	var got Entity
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解析响应失败: %v（%s）", err, body)
	}
	if got.Title != "Epic Records" {
		t.Fatalf("解析出的题名 = %q，期望 Epic Records", got.Title)
	}

	// 2) 实体已删除（Lifecycle 写终态 deleted，不是删行）：引用失效 → 404 not_found。
	deleted, err := f.s.Lifecycle(ctx, label.ID, LifecycleEdit{
		ExpectedVersion: label.Version, EditNote: "删除厂牌测试条目", Sources: fixtureSources(),
	}, f.u)
	if err != nil {
		t.Fatalf("删除实体: %v", err)
	}
	if deleted.State != "deleted" {
		t.Fatalf("删除后状态 = %q，期望 deleted", deleted.State)
	}
	code, body = resolveOnce(t, anon, label.ID)
	if code != http.StatusNotFound || !strings.Contains(body, "not_found") {
		t.Fatalf("已删除实体的引用应 404 not_found，实际 %d（%s）", code, body)
	}

	// 3) 不可见（草稿）走同一出口：否则匿名者能靠状态码区分"不存在"与"存在但未发布"。
	draft := f.save(Entity{Kind: "agent", Title: "Unreleased Label", State: "active"})
	draft = f.retire(draft)
	if code, body = resolveOnce(t, anon, draft.ID); code != http.StatusNotFound || !strings.Contains(body, "not_found") {
		t.Fatalf("不可见实体的引用应 404 not_found，实际 %d（%s）", code, body)
	}

	// 4) 从没存在过的 id（历史引用指向已清掉的条目）同样 404，不 500、不空名。
	code, body = resolveOnce(t, anon, "00000000-0000-0000-0000-0000000000ff")
	if code != http.StatusNotFound || !strings.Contains(body, "not_found") {
		t.Fatalf("不存在的 id 应 404 not_found，实际 %d（%s）", code, body)
	}
}
