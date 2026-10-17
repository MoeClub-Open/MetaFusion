package catalog

// PAT 下游接入的回归：本地形态预检、60 秒缓存与单飞、上限逐出、401/503 的分工，
// 以及"PAT 身份与 JWT 同形进上下文、授权仍走 Can"这条端到端链路。
// 账号服务用同形的桩代替（内省端点尚未在本仓实现，桩保证契约面被钉住）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/testutil"
)

// patToken 造一个形态合法的 PAT 明文（前缀 + 43 个 base62，对应契约里的"32 字节 base62"）。
func patToken(fill byte) string {
	return PATPrefix + strings.Repeat(string(fill), 43)
}

// fakeAuthDoc 与账号服务内省响应同形。
type fakeAuthDoc struct {
	Valid               bool     `json:"valid"`
	UserID              string   `json:"user_id"`
	Username            string   `json:"username"`
	Role                string   `json:"role"`
	Permissions         []string `json:"permissions"`
	CatalogReviewExempt bool     `json:"catalog_review_exempt"`
	ExpiresAt           string   `json:"expires_at,omitempty"`
}

func TestPATAccountReviewExemptionSurvivesIntrospectionAndExpiresWithCache(t *testing.T) {
	now := time.Now()
	token := patToken('x')
	f, srv := newFakeAuth(t, map[string]fakeAuthDoc{token: {Valid: true, UserID: "admin-owner", Permissions: []string{PermissionEntityEdit}, CatalogReviewExempt: true}})
	p := newPATIntrospector(srv.URL, func() time.Time { return now })
	ident, err := p.Introspect(context.Background(), token)
	if err != nil || ident == nil {
		t.Fatalf("introspection: %v", err)
	}
	u := ident.catalogUser()
	if !u.CanBypassReview() || u.Can(PermissionReviewManage) || u.Can(PermissionReviewBypass) {
		t.Fatalf("exemption changed delegated permissions: %+v", u)
	}
	f.mu.Lock()
	doc := f.byToken[token]
	doc.CatalogReviewExempt = false
	f.byToken[token] = doc
	f.mu.Unlock()
	now = now.Add(PATCacheTTL + time.Second)
	ident, err = p.Introspect(context.Background(), token)
	if err != nil || ident == nil || ident.catalogUser().CanBypassReview() {
		t.Fatalf("revoked exemption survived cache expiry: %+v %v", ident, err)
	}
}

// fakeAuth 是内省端点桩：按明文查表判定，并统计被调用次数（缓存/单飞的断言全靠它）。
type fakeAuth struct {
	mu      sync.Mutex
	calls   int
	byToken map[string]fakeAuthDoc
	// status 非 0 时一律回该状态码（模拟账号服务异常），delay 用于放大并发窗口。
	status int
	delay  time.Duration
}

func newFakeAuth(t *testing.T, tokens map[string]fakeAuthDoc) (*fakeAuth, *httptest.Server) {
	t.Helper()
	f := &fakeAuth{byToken: tokens}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != patIntrospectPath || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		f.calls++
		status, delay := f.status, f.delay
		f.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		doc, ok := f.byToken[in.Token]
		f.mu.Unlock()
		if !ok {
			doc = fakeAuthDoc{Valid: false}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeAuth) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeAuth) setStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

// 明显非法的形态（长度/字符集不对）必须在本地拒绝：不然任何伪造前缀的字符串都是一次免费的内省调用。
func TestPATMalformedRejectedLocally(t *testing.T) {
	f, srv := newFakeAuth(t, nil)
	p := NewPATIntrospector(srv.URL)
	for _, bad := range []string{
		PATPrefix,
		PATPrefix + "too-short",
		PATPrefix + strings.Repeat("a", patBodyLen-1),
		PATPrefix + strings.Repeat("a", patBodyLen+1),
		PATPrefix + strings.Repeat("a", 31),
		PATPrefix + strings.Repeat("a", 65),
		PATPrefix + strings.Repeat("a", patBodyLen-1) + "-",
		PATPrefix + strings.Repeat("a", patBodyLen-1) + "_",
		PATPrefix + strings.Repeat("a", patBodyLen-1) + "中",
		"mfq_" + strings.Repeat("a", patBodyLen),
	} {
		ident, err := p.Introspect(context.Background(), bad)
		if err != nil || ident != nil {
			t.Errorf("%q 应被本地判为无效（ident=%v err=%v）", bad, ident, err)
		}
	}
	if n := f.callCount(); n != 0 {
		t.Fatalf("形态非法不该打账号服务，实际调用 %d 次", n)
	}
}

// 内省结果的三种结论：有效 / 明确无效（含账号服务直接回 401、403）/ 判不了（5xx、不可达）。
func TestPATIntrospectOutcomes(t *testing.T) {
	valid := patToken('a')
	unknown := patToken('b')
	refused := patToken('c')
	broken := patToken('d')
	down := patToken('e')
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	f, srv := newFakeAuth(t, map[string]fakeAuthDoc{
		valid: {Valid: true, UserID: "u-pat", Username: "kana", Permissions: []string{PermissionEntityEdit, "community.post.create"}, ExpiresAt: expires},
	})
	p := NewPATIntrospector(srv.URL)
	ctx := context.Background()

	ident, err := p.Introspect(ctx, valid)
	if err != nil || ident == nil {
		t.Fatalf("有效 PAT 应返回身份: ident=%v err=%v", ident, err)
	}
	if ident.UserID != "u-pat" || len(ident.Permissions) != 2 || ident.ExpiresAt.IsZero() {
		t.Fatalf("身份字段不符: %+v", ident)
	}

	// valid=false：明确无效，不是错误（调用方回 401 而不是 503）。
	if ident, err = p.Introspect(ctx, unknown); ident != nil || err != nil {
		t.Fatalf("valid=false 应按无效处理: ident=%v err=%v", ident, err)
	}
	// 账号服务直接回 401/403（吊销/过期也走这里）：同样是确定结论。
	f.setStatus(http.StatusUnauthorized)
	if ident, err = p.Introspect(ctx, refused); ident != nil || err != nil {
		t.Fatalf("内省 401 应按无效处理: ident=%v err=%v", ident, err)
	}
	// 5xx 与"端点不存在"（部署顺序不对）都算判不了 → 503，绝不因为查不到就判令牌无效。
	f.setStatus(http.StatusInternalServerError)
	if _, err = p.Introspect(ctx, broken); err == nil {
		t.Fatal("内省 500 必须返回错误（调用方按 503 处理）")
	}
	srv.Close()
	if _, err = p.Introspect(context.Background(), down); err == nil {
		t.Fatal("账号服务不可达必须返回错误（调用方按 503 处理）")
	}
	// 未配置 AUTH_URL：内省器存在但一律不可用。
	if _, err = NewPATIntrospector("").Introspect(context.Background(), patToken('f')); err == nil {
		t.Fatal("未配置 AUTH_URL 必须按不可用返回错误")
	}
}

// 缓存时长就是**吊销窗口**：60 秒内重复使用同一个 PAT 只打一次账号服务。
func TestPATCacheHonoursTTL(t *testing.T) {
	token := patToken('a')
	f, srv := newFakeAuth(t, map[string]fakeAuthDoc{token: {Valid: true, UserID: "u-1"}})
	now := time.Now()
	p := newPATIntrospector(srv.URL, func() time.Time { return now })
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		if _, err := p.Introspect(ctx, token); err != nil {
			t.Fatalf("第 %d 次内省失败: %v", i+1, err)
		}
	}
	if n := f.callCount(); n != 1 {
		t.Fatalf("60 秒内应只打一次账号服务，实际 %d 次", n)
	}
	if hits, misses, _, size := p.cache.stats(); hits == 0 || misses != 1 || size != 1 {
		t.Fatalf("缓存计数不符: hits=%d misses=%d size=%d", hits, misses, size)
	}

	now = now.Add(PATCacheTTL - time.Second)
	if _, err := p.Introspect(ctx, token); err != nil || f.callCount() != 1 {
		t.Fatalf("未过期不该重新内省: err=%v calls=%d", err, f.callCount())
	}
	// 过期后必须重新问：这就是"吊销最长 60 秒生效"的实现。
	now = now.Add(2 * time.Second)
	if _, err := p.Introspect(ctx, token); err != nil || f.callCount() != 2 {
		t.Fatalf("过期后必须重新内省: err=%v calls=%d", err, f.callCount())
	}
}

// 令牌自身过期比缓存窗口更早时，缓存不得把有效期往后拖。
func TestPATCacheDoesNotOutliveTokenExpiry(t *testing.T) {
	token := patToken('a')
	now := time.Now()
	f, srv := newFakeAuth(t, map[string]fakeAuthDoc{token: {
		Valid: true, UserID: "u-1",
		ExpiresAt: now.Add(10 * time.Second).UTC().Format(time.RFC3339),
	}})
	p := newPATIntrospector(srv.URL, func() time.Time { return now })
	if _, err := p.Introspect(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	now = now.Add(11 * time.Second)
	if _, err := p.Introspect(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if n := f.callCount(); n != 2 {
		t.Fatalf("令牌过期后必须重新内省（缓存不得越过 expires_at），实际 %d 次", n)
	}
}

// 并发同键只打一次：没有单飞，一批并发请求会把账号服务打爆（每个都是缓存未命中）。
func TestPATCacheSingleflight(t *testing.T) {
	token := patToken('a')
	f, srv := newFakeAuth(t, map[string]fakeAuthDoc{token: {Valid: true, UserID: "u-1"}})
	f.delay = 50 * time.Millisecond
	p := NewPATIntrospector(srv.URL)

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ident, err := p.Introspect(context.Background(), token); err != nil || ident == nil {
				t.Errorf("并发内省失败: ident=%v err=%v", ident, err)
			}
		}()
	}
	wg.Wait()
	if n := f.callCount(); n != 1 {
		t.Fatalf("并发同键应只打一次账号服务，实际 %d 次", n)
	}
}

// 缓存必须有上限与逐出：任何人都能构造 mfp_ 前缀的字符串来喂缓存。
func TestPATCacheIsBounded(t *testing.T) {
	c := newPATCache(time.Now, 0)
	if PATCacheMax < 16 {
		t.Fatalf("上限太小，用例失去意义: %d", PATCacheMax)
	}
	for i := 0; i < PATCacheMax*2; i++ {
		c.storeLocked(patHash(patToken('a')+strconv.Itoa(i)), patCacheEntry{cachedUntil: time.Now().Add(time.Hour)})
	}
	_, _, evictions, size := c.stats()
	if size > PATCacheMax {
		t.Fatalf("缓存条目 %d 超过上限 %d", size, PATCacheMax)
	}
	if evictions == 0 {
		t.Fatal("超过上限必须逐出，实际 evictions=0")
	}
}

// 满了要逐出时先清过期项（它们只是白占内存），不是无脑扔掉最老的活条目。
func TestPATCacheEvictsExpiredFirst(t *testing.T) {
	now := time.Now()
	c := newPATCache(func() time.Time { return now }, 0)
	for i := 0; i < PATCacheMax; i++ {
		c.storeLocked(patHash("stale-"+strconv.Itoa(i)), patCacheEntry{cachedUntil: now.Add(-time.Minute)})
	}
	c.storeLocked(patHash("fresh"), patCacheEntry{cachedUntil: now.Add(time.Hour)})
	if _, _, _, size := c.stats(); size != 1 {
		t.Fatalf("满仓时过期项应被全部清掉，剩余 %d 条", size)
	}
}

// 端到端：走真实路由 + 真库，PAT 与 JWT 的身份形状一致（授权仍走 User.Can）。
func TestPATEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	editor := patToken('e')       // scopes 含 catalog.relation.edit
	outsider := patToken('o')     // 只有互动码：对目录写接口越权
	adminNoScope := patToken('d') // 管理员但 scopes 为空
	fresh := patToken('f')        // 只在最后阶段用：账号服务届时已下线
	f, srv := newFakeAuth(t, map[string]fakeAuthDoc{
		editor:       {Valid: true, UserID: "44444444-4444-4444-4444-444444444444", Username: "pat-editor", Permissions: []string{PermissionRelationEdit}},
		outsider:     {Valid: true, UserID: "55555555-5555-5555-5555-555555555555", Username: "pat-outsider", Permissions: []string{"community.post.create"}},
		adminNoScope: {Valid: true, UserID: "66666666-6666-6666-6666-666666666666", Username: "pat-admin"},
		fresh:        {Valid: true, UserID: "77777777-7777-7777-7777-777777777777", Username: "pat-fresh", Permissions: []string{PermissionEntityEdit}},
	})
	// Verifier 刻意留空：PAT 路径不依赖本地验签材料。
	s := &Store{DB: testutil.Database(t), PAT: NewPATIntrospector(srv.URL)}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	HTTP{Store: s}.Register(engine)
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

	// 1) 读接口（需登录）：PAT 能读到自己的偏好，响应不带 mf_session cookie。
	w := do(http.MethodGet, "/api/catalog/me/home-preferences", "", editor)
	if w.Code != http.StatusOK {
		t.Fatalf("PAT 读个人偏好应 200，实际 %d：%s", w.Code, w.Body.String())
	}
	if cookie := w.Header().Get("Set-Cookie"); cookie != "" {
		t.Fatalf("PAT 请求不该产出 cookie: %s", cookie)
	}
	// 2) 越权写：scopes 里没有目录码 → 403（不是 401，也不是放行）。
	//    探针选**带权限码闸门**的关系写端点，载荷刻意非法：400 只可能出现在闸门放行之后。
	w = do(http.MethodPost, "/api/catalog/relations", "{", outsider)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "forbidden") {
		t.Fatalf("scopes 收紧的 PAT 写目录应 403，实际 %d：%s", w.Code, w.Body.String())
	}
	// 3) 持码的 PAT 过闸门（载荷不合由处理器回 400，证明身份进了上下文）。
	w = do(http.MethodPost, "/api/catalog/relations", "{", editor)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "invalid_payload") {
		t.Fatalf("持码 PAT 应过闸门到处理器，实际 %d：%s", w.Code, w.Body.String())
	}
	// 4) 管理员但 scopes 空：PAT 身份永不回落角色兜底，否则 scopes=[] 就是全权。
	w = do(http.MethodPost, "/api/catalog/relations", "{", adminNoScope)
	if w.Code != http.StatusForbidden {
		t.Fatalf("scopes 为空的管理员 PAT 不该按角色兜底放行，实际 %d：%s", w.Code, w.Body.String())
	}
	// 5) 无效 PAT：401 + 稳定机器码（不区分无效/吊销/过期）。形态合法，所以会问一次内省。
	w = do(http.MethodGet, "/api/catalog/me/home-preferences", "", patToken('z'))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), CodeInvalidToken) {
		t.Fatalf("无效 PAT 应 401 %s，实际 %d：%s", CodeInvalidToken, w.Code, w.Body.String())
	}
	// 5b) 形态明显非法（长度不对）：本地就拒，不打账号服务。
	w = do(http.MethodGet, "/api/catalog/me/home-preferences", "", PATPrefix+"not-a-real-token")
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), CodeInvalidToken) {
		t.Fatalf("形态非法的 PAT 应 401 %s，实际 %d：%s", CodeInvalidToken, w.Code, w.Body.String())
	}
	if n := f.callCount(); n != 4 {
		// editor / outsider / adminNoScope / 未知令牌各一次；editor 的第二次请求命中缓存，
		// 形态非法的明文在本地被拒，都不产生调用。
		t.Fatalf("内省调用次数应停在 4（缓存与本地预检生效），实际 %d", n)
	}
	// 6) 账号服务不可达：503 + 稳定机器码（不是 401——那会让调用方去换凭据而不是重试）。
	//    这里必须用一个尚未被内省过的令牌，否则会命中缓存，测不到依赖故障这条路径。
	srv.Close()
	w = do(http.MethodGet, "/api/catalog/me/home-preferences", "", fresh)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), CodeAuthUnavailable) {
		t.Fatalf("账号服务不可达应 503 %s，实际 %d：%s", CodeAuthUnavailable, w.Code, w.Body.String())
	}
	// 7) 未装配内省器（AUTH_URL 未配置）时同样按依赖不可用处理。
	noPAT := &Store{DB: s.DB}
	engine2 := gin.New()
	HTTP{Store: noPAT}.Register(engine2)
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/catalog/me/home-preferences", nil)
	req.Header.Set("Authorization", "Bearer "+patToken('g'))
	engine2.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), CodeAuthUnavailable) {
		t.Fatalf("未配置 AUTH_URL 应 503 %s，实际 %d：%s", CodeAuthUnavailable, w.Code, w.Body.String())
	}
	// 累计仍为 4：账号服务已下线（fresh 打不通，不计入）与未配置内省器都不产生调用。
	if n := f.callCount(); n != 4 {
		t.Fatalf("内省调用次数应停在 4（缓存/本地预检/依赖故障都不该再打），实际 %d", n)
	}
}
