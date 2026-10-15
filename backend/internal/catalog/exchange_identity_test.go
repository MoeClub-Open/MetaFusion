package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/testutil"
)

// 路由注册顺序必须让 /api/exchange/* 落在 attachUser 之后：gin 的 RouterGroup.Use 只对
// **之后**注册的路由生效（注册时复制当时的 handler 链），0be8ae9 把 registerExchange 插到
// api.Use(attachUser) 之前，这两个端点的 user(c) 就恒为 nil——提案接口带合法令牌也 401，
// 导出接口一律按匿名判可见性。
//
// 本用例**走真实 HTTP{Store}.Register(r)**（不像 http_gate_test 那样把 catalog_user 注入
// 上下文、绕过中间件），用真实验签器与真实令牌端到端钉住这条链：一旦顺序再被改坏，
// 提案会回到 401、本人的 draft 会回到 404，两条断言都会红。
func TestExchangeRoutesCarryIdentityMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := testKey(t)
	s := &Store{DB: testutil.Database(t), Verifier: testVerifier(t, key)}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	HTTP{Store: s}.Register(engine)

	owner := "22222222-2222-2222-2222-222222222222"
	token := signTestToken(t, key, func(c Claims) Claims {
		c.Subject, c.Username = owner, "exchange-owner"
		c.Permissions = []string{PermissionEntityEdit}
		return c
	})
	do := func(method, path, payload, bearer string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(payload))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	created, err := s.Save(context.Background(), Edit{Entity: Entity{Kind: "work", Title: "交换导出"}, EditNote: "fixture", Sources: fixtureSources()}, User{ID: owner, Permissions: []string{permissionWildcard}})
	if err != nil {
		t.Fatal(err)
	}
	if w := do(http.MethodGet, "/api/exchange/entities/"+created.ID, "", token); w.Code != 200 {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}
	if w := do(http.MethodPost, "/api/exchange/proposals", "{}", token); w.Code != 404 {
		t.Fatalf("retired proposal route: %d", w.Code)
	}

}

// 同一类失效的通杀检查：任何 /api 路由对"带合法令牌"的请求都不该回 401 authentication_required
// ——那说明这条链里压根没有身份中间件（gin 的 Use 只管之后注册的路由）。只有公开的
// /openapi.json 不在此列：它是接入方的机器可读契约，刻意无身份。/docs 与 /swagger 必须看到
// 身份中间件——它们是管理面，一旦被挪回 attachUser 之前，这里会以 401 抓住。
// 令牌刻意不带任何目录权限：带权限的请求会真的落库或出站抓取，这里只需要"链里有中间件"
// 这一个事实（无码是 403、载荷不合是 400，都不是 401）。
func TestEveryAPIRouteSeesIdentityMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := testKey(t)
	s := &Store{DB: testutil.Database(t), Verifier: testVerifier(t, key)}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	HTTP{Store: s}.Register(engine)
	token := signTestToken(t, key, func(c Claims) Claims {
		c.Subject = "33333333-3333-3333-3333-333333333333"
		c.Permissions = []string{"community.post.create"}
		return c
	})
	public := map[string]bool{"/api/openapi.json": true}
	probe := "00000000-0000-0000-0000-000000000001"
	for _, route := range engine.Routes() {
		if public[route.Path] {
			continue
		}
		path := strings.NewReplacer(":id", probe, ":code", probe).Replace(route.Path)
		req := httptest.NewRequest(route.Method, path, strings.NewReader("{"))
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if strings.Contains(w.Body.String(), "authentication_required") {
			t.Errorf("%s %s 带合法令牌仍回 401：这条链没有身份中间件（注册在 api.Use(attachUser) 之前？）", route.Method, path)
		}
	}
}
