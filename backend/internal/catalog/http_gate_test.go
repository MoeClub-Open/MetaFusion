package catalog

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// gateEngine 挂载真实路由表并把身份直接注入上下文：目录侧的鉴权中间件在无令牌时
// 不覆盖已存在的 catalog_user，因此这里不需要账号服务或数据库。
func gateEngine(u *User) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if u != nil {
			c.Set("catalog_user", u)
		}
		c.Next()
	})
	HTTP{Store: &Store{}}.Register(r)
	return r
}

// 受权限码保护的写端点必须"未登录 401 / 无码 403 / 有码才进处理器"。
// 探针用非法 JSON：处理器在闸门之后先解析 body，返回 invalid_payload(400)，
// 与 401/403 的差别即证明闸门存在且认的是权限码而不是"登录即可"。
func TestProtectedRouteGates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 持另一个目录码：能证明被拒的原因是"缺这个码"，而不是"没有权限声明"。
	otherCode := &User{ID: "u-other", Permissions: []string{PermissionEntityEdit}}
	relationUser := &User{ID: "u-rel", Permissions: []string{PermissionRelationEdit}}
	wildcard := &User{ID: "u-admin", Permissions: []string{permissionWildcard}}
	id := "00000000-0000-0000-0000-000000000001"

	for _, tc := range []struct {
		name       string
		method     string
		path       string
		withCode   *User
		wantDenied string // 无码/匿名时的期望错误码
	}{
		{"relation create", http.MethodPost, "/api/catalog/relations", relationUser, ""},
		{"relation update", http.MethodPut, "/api/catalog/relations/" + id, relationUser, ""},
		{"relation delete", http.MethodDelete, "/api/catalog/relations/" + id, relationUser, ""},
	} {
		// 匿名：401（未登录）与 403（无权限）必须是两个状态，前端据此决定跳登录还是提示无权限。
		w := httptest.NewRecorder()
		gateEngine(nil).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{")))
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "authentication_required") {
			t.Errorf("%s anonymous: status=%d body=%s want 401 authentication_required", tc.name, w.Code, w.Body.String())
		}
		// 登录但持别的目录码：403。
		w = httptest.NewRecorder()
		gateEngine(otherCode).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{")))
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "forbidden") {
			t.Errorf("%s without the code: status=%d body=%s want 403 forbidden", tc.name, w.Code, w.Body.String())
		}
		// 持码（含 * 通配）：过闸门，处理器按非法载荷报 400。
		for _, u := range []*User{tc.withCode, wildcard} {
			w = httptest.NewRecorder()
			gateEngine(u).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{")))
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_payload") {
				t.Errorf("%s with code %v: status=%d body=%s want 400 invalid_payload", tc.name, u.Permissions, w.Code, w.Body.String())
			}
		}
	}
}

// 这里的闸门是目录自己的 required()/AdminGate 语义：用真实验签器与真实令牌跑端到端。

// resetPreviewBucket 清掉预览路由的限流计数：限流桶是进程级全局状态，
// 用例之间共享配额会让断言依赖执行顺序。
func resetPreviewBucket() {
	routeAttempts.Range(func(k, _ any) bool {
		if s, ok := k.(string); ok && strings.HasPrefix(s, "u:u-rl|") {
			routeAttempts.Delete(k)
		}
		return true
	})
}

// 预览端点使用统一默认限流：第 181 次请求被 429 挡住并给 Retry-After。
// 计数桶按 IP+路由，本用例进出都清桶，避免影响其它用例。
func TestExternalSourcePreviewIsRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetPreviewBucket()
	defer resetPreviewBucket()
	u := &User{ID: "u-rl", Permissions: []string{PermissionEntityEdit}}
	engine := gateEngine(u)
	for i := 0; i < DefaultRateLimitPerMinute; i++ {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/external-sources/query", strings.NewReader("{")))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("request %d: status=%d body=%s want 400", i+1, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/external-sources/query", strings.NewReader("{")))
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), "rate_limited") {
		t.Fatalf("request beyond default limit: status=%d body=%s want 429 rate_limited", w.Code, w.Body.String())
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("429 must carry Retry-After")
	}
}

// 反向对照：只要求登录的端点对"无任何目录码的登录用户"照旧放行（400 而非 403），
// 避免把闸门加宽的改动顺手扩到实体草稿这类既有入口。
func TestLoginOnlyRouteStaysOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plain := &User{ID: "u-plain", Permissions: []string{"community.post.create"}}
	w := httptest.NewRecorder()
	gateEngine(plain).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/catalog/entities", strings.NewReader("{")))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("login-only endpoint rejected a logged-in user: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestExternalSourcesRequireLoginOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plain := &User{ID: "u-plain", Permissions: []string{"community.post.create"}}
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/external-sources"},
		{http.MethodPost, "/api/external-sources/query"},
	} {
		w := httptest.NewRecorder()
		gateEngine(nil).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"op":"search","q":"x"}`)))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s anonymous: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
		w = httptest.NewRecorder()
		body := "{"
		if tc.method == http.MethodGet {
			body = ""
		}
		gateEngine(plain).ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(body)))
		// GET list succeeds; POST with "{" is invalid_payload
		if tc.method == http.MethodGet {
			if w.Code != http.StatusOK {
				t.Fatalf("GET list for logged-in user: %d %s", w.Code, w.Body.String())
			}
		} else if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_payload") {
			t.Fatalf("POST query for logged-in user: %d %s want 400 invalid_payload", w.Code, w.Body.String())
		}
	}
}
