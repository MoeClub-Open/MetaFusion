package catalog

// 限流策略的纯函数与热路径用例。真库往返（读写 + etag 冲突）在 ratelimit_postgres_test.go，
// 无 MF_V2_TEST_DSN 时跳过；本文件的判定不依赖数据库。

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 解析优先级一旦漂移，"给某账号单独提额"就会静默失效（现象与没配一样），
// 因此把每一级的边界钉住：账号 > 组 > 全局默认 > 路由内置额度。
func TestRateLimitPolicyResolvePrecedence(t *testing.T) {
	p := RateLimitPolicy{
		DefaultPerMinute: 50,
		Groups: map[string]RateLimitRule{
			"editor":    {PerMinute: 300},
			"moderator": {PerMinute: 900},
			"admin":     {Unlimited: true},
			"anonymous": {PerMinute: 10},
		},
		Accounts: map[string]RateLimitRule{
			"u-1": {PerMinute: 5000},
			"u-2": {Unlimited: true},
			// 空规则与 per_minute=0 都表示"未声明、继承下一级"，
			// 不是"禁止"：把两者混为一谈会让一次误填把账号锁死，
			// 而现象与"配置没生效"完全一样。
			"u-3":  {},
			"u-4":  {PerMinute: 0},
			"u-na": {Unlimited: true},
		},
	}
	cases := []struct {
		name          string
		identities    []string
		groups        []string
		anonymous     bool
		routeDefault  int
		wantLimit     int
		wantUnlimited bool
	}{
		{"账号额度压过组", []string{"u-1"}, []string{"editor"}, false, 120, 5000, false},
		{"账号解除限制压过组", []string{"u-2"}, []string{"editor"}, false, 120, 0, true},
		{"空规则继承组", []string{"u-3"}, []string{"editor"}, false, 120, 300, false},
		{"per_minute=0 视为未声明", []string{"u-4"}, []string{"moderator"}, false, 120, 900, false},
		{"第一个命中的标识生效（用户名是第二顺位）", []string{"", "u-1"}, []string{"moderator"}, false, 120, 5000, false},
		{"多组命中取最宽松", []string{"u-9"}, []string{"editor", "moderator"}, false, 120, 900, false},
		{"任一组解除限制即解除", []string{"u-9"}, []string{"editor", "admin"}, false, 120, 0, true},
		{"无匹配回落全局默认", []string{"u-9"}, []string{"viewer"}, false, 120, 50, false},
		{"匿名用保留组码", nil, nil, true, 120, 10, false},
		{"匿名不看账号表", []string{"u-na"}, nil, true, 120, 10, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, unlimited := p.Resolve(tc.identities, tc.groups, tc.anonymous, tc.routeDefault)
			if unlimited != tc.wantUnlimited || (!unlimited && got != tc.wantLimit) {
				t.Fatalf("Resolve = (%d, unlimited=%v)，期望 (%d, unlimited=%v)", got, unlimited, tc.wantLimit, tc.wantUnlimited)
			}
		})
	}
}

// 空文档必须逐字回到改动前的行为：每个受限路由用自己写死的额度。
// 这条是"配置读不到时不能把站点变成 429 风暴"的判据。
func TestRateLimitPolicyEmptyDocumentKeepsRouteDefaults(t *testing.T) {
	var empty RateLimitPolicy
	for _, tc := range []struct {
		identities []string
		groups     []string
		anonymous  bool
		def        int
	}{
		{[]string{"u-1"}, []string{"editor"}, false, 180},
		{[]string{"u-1"}, []string{"*"}, false, 10},
		{nil, nil, true, 300},
	} {
		got, unlimited := empty.Resolve(tc.identities, tc.groups, tc.anonymous, tc.def)
		if unlimited || got != tc.def {
			t.Fatalf("空文档 Resolve = (%d, unlimited=%v)，期望路由默认 %d", got, unlimited, tc.def)
		}
	}
}

// 校验只拒绝"一定是填错"的输入；对外只回一个稳定码，细节留服务端日志。
func TestValidateRateLimitPolicy(t *testing.T) {
	ok := []RateLimitPolicy{
		{},
		{DefaultPerMinute: 120},
		{DefaultUnlimited: true},
		{Groups: map[string]RateLimitRule{"anonymous": {PerMinute: 30}}},
		{Accounts: map[string]RateLimitRule{"11111111-1111-1111-1111-111111111111": {Unlimited: true}}},
		// unlimited 与 per_minute 同时给出时以 unlimited 为准：允许（想临时解除又不丢原额度）。
		{Accounts: map[string]RateLimitRule{"u": {PerMinute: 10, Unlimited: true}}},
		{DefaultPerMinute: rateLimitMaxPerMinute},
	}
	for i, p := range ok {
		if err := validateRateLimitPolicy(p); err != nil {
			t.Fatalf("第 %d 份策略应通过，实际 %v", i+1, err)
		}
	}
	bad := []RateLimitPolicy{
		{DefaultPerMinute: -1},
		{DefaultPerMinute: rateLimitMaxPerMinute + 1},
		{Groups: map[string]RateLimitRule{"": {PerMinute: 1}}},
		{Groups: map[string]RateLimitRule{"  padded  ": {PerMinute: 1}}},
		{Groups: map[string]RateLimitRule{"g": {PerMinute: -5}}},
		{Accounts: map[string]RateLimitRule{"u": {PerMinute: rateLimitMaxPerMinute + 1}}},
	}
	for i, p := range bad {
		err := validateRateLimitPolicy(p)
		if err == nil || err.Error() != "invalid_rate_limit" {
			t.Fatalf("第 %d 份策略应被拒且只回 invalid_rate_limit，实际 %v", i+1, err)
		}
	}
	// 条目上限：文档整体进内存并被每次解析读取，无上限会被一份超大 JSON 拖住。
	groups := map[string]RateLimitRule{}
	for i := 0; i <= rateLimitMaxGroups; i++ {
		groups[fmt.Sprintf("g-%d", i)] = RateLimitRule{PerMinute: 1}
	}
	if err := validateRateLimitPolicy(RateLimitPolicy{Groups: groups}); err == nil {
		t.Fatal("超量 groups 应被拒")
	}
}

// 限流热路径：额度确实按主体解析，且解除限制的主体不被计数、也不下发虚窗口头。
func TestRouteLimiterHonoursAccountAndGroupBudgets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 桶键含路由：用一次性路径，避免与其它用例（或 -count=2 的重复运行）共用 package 级桶。
	path := fmt.Sprintf("/__ratelimit_policy_probe__/%d", time.Now().UnixNano())
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		// 模拟 attachUser 已把身份放进上下文；没带测试头即匿名。
		if id := c.GetHeader("X-Test-User"); id != "" {
			c.Set("catalog_user", &User{ID: id, Username: id + "-name", Groups: []string{c.GetHeader("X-Test-Group")}})
		}
	})
	engine.GET(path, routeLimiter(2), func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	reset := func() {
		InvalidateRateLimitPolicyCache()
		routeAttempts.Range(func(k, _ any) bool { routeAttempts.Delete(k); return true })
	}
	reset()
	t.Cleanup(reset)
	storeRateLimitSnapshot(RateLimitConfig{ETag: "test", Policy: RateLimitPolicy{
		Groups:   map[string]RateLimitRule{"vip": {PerMinute: 4}},
		Accounts: map[string]RateLimitRule{"u-unlimited": {Unlimited: true}},
	}}, nil)

	call := func(id, group string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if id != "" {
			req.Header.Set("X-Test-User", id)
		}
		if group != "" {
			req.Header.Set("X-Test-Group", group)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	// 匿名：仍按路由内置额度 2。
	for i := 1; i <= 2; i++ {
		if got := call("", "").Code; got != http.StatusOK {
			t.Fatalf("匿名第 %d 次 = %d，期望 200", i, got)
		}
	}
	if got := call("", "").Code; got != http.StatusTooManyRequests {
		t.Fatalf("匿名第 3 次 = %d，期望 429", got)
	}

	// 组预设：vip = 4，比内置额度宽，且与匿名的桶互不影响。
	for i := 1; i <= 4; i++ {
		w := call("u-vip", "vip")
		if w.Code != http.StatusOK {
			t.Fatalf("vip 第 %d 次 = %d，期望 200", i, w.Code)
		}
		if got := w.Header().Get("X-RateLimit-Limit"); got != "4" {
			t.Fatalf("vip 第 %d 次 X-RateLimit-Limit = %q，期望 4", i, got)
		}
	}
	if got := call("u-vip", "vip").Code; got != http.StatusTooManyRequests {
		t.Fatalf("vip 第 5 次 = %d，期望 429", got)
	}

	// 账号解除限制：不计数、不下发 X-RateLimit-*（没有窗口就没有"还剩几次"）。
	for i := 1; i <= 10; i++ {
		w := call("u-unlimited", "vip")
		if w.Code != http.StatusOK {
			t.Fatalf("解除限制的账号第 %d 次 = %d，期望 200", i, w.Code)
		}
		if got := w.Header().Get("X-RateLimit-Limit"); got != "" {
			t.Fatalf("解除限制时不应下发 X-RateLimit-Limit，实际 %q", got)
		}
	}

	// 没有身份的请求主体是 IP，登录请求主体是账号：两者不共享配额。
	// 上面的匿名桶已耗尽，而 vip 桶仍在计数，正是这条判据。
	if got := call("u-other", "vip").Code; got != http.StatusOK {
		t.Fatalf("另一个 vip 账号 = %d，期望 200（账号之间不共享桶）", got)
	}
}

func TestAdminDefaultCanBeOverriddenByPolicy(t *testing.T) {
	p := RateLimitPolicy{DefaultPerMinute: 1, Accounts: map[string]RateLimitRule{"u": {PerMinute: 1}}, Groups: map[string]RateLimitRule{"admin": {PerMinute: 1}}}
	if limit, unlimited := p.Resolve([]string{"u"}, []string{"admin"}, false, 180); unlimited || limit != 1 {
		t.Fatal("explicit account quota must restrict the default admin exemption")
	}
	if limit, unlimited := p.Resolve([]string{"other"}, []string{"admin"}, false, 180); unlimited || limit != 1 {
		t.Fatal("explicit admin group quota must restrict the default admin exemption")
	}
	if _, unlimited := (RateLimitPolicy{}).Resolve([]string{"u"}, []string{"admin"}, false, 180); !unlimited {
		t.Fatal("admin remains unlimited when no explicit override exists")
	}
	if _, unlimited := p.Resolve([]string{"u"}, []string{"*"}, false, 180); unlimited {
		t.Fatal("wildcard permission is not admin group identity")
	}
}
