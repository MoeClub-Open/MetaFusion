package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 纯函数口径：五个状态键一定齐全（没有行就是真实的 0），total 取所有分组之和，
// 未知状态值也计入 total——"这个状态是 0"与"没拿到这个状态"必须是两件事，
// 前者是事实，后者在响应里表现为键缺失（前端据此显示占位符而不是 0）。
func TestFoldStateCounts(t *testing.T) {
	empty := foldStateCounts(nil)
	if len(empty.States) != len(entityStates) || empty.Total != 0 {
		t.Fatalf("空表应给出齐全的五键与 total=0: %+v", empty)
	}
	for _, code := range entityStates {
		if empty.States[code] != 0 {
			t.Fatalf("没有行的状态 %s 应补齐为 0: %+v", code, empty.States)
		}
	}
	got := foldStateCounts([]stateGroup{{State: "active", Count: 3070}, {State: "deleted", Count: 288}, {State: "unexpected", Count: 5}})
	if got.States["active"] != 3070 || got.States["deleted"] != 288 {
		t.Fatalf("已知状态应原样带出: %+v", got.States)
	}
	for _, code := range []string{"merged"} {
		if got.States[code] != 0 {
			t.Fatalf("缺失状态 %s 应补 0: %+v", code, got.States)
		}
	}
	if got.Total != 3363 || got.States["unexpected"] != 5 {
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
	f.save(Entity{Kind: "work", Title: "计数用例·草稿", State: "active"})
	f.save(Entity{Kind: "work", Title: "计数用例·待审", State: "active"})
	if _, err := f.s.Lifecycle(ctx, published.ID, LifecycleEdit{ExpectedVersion: published.Version, EditNote: "计数用例清退", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatalf("造墓碑行: %v", err)
	}

	// 直接 SQL 是判据：逐状态与总数各比一次。
	want := map[string]int64{}
	rows, err := f.s.DB.QueryContext(ctx, "SELECT state, count(*) FROM catalog.entities GROUP BY state")
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

	got, err := f.s.StateCounts(ctx)
	if err != nil {
		t.Fatalf("StateCounts: %v", err)
	}
	if got.Total != wantTotal {
		t.Fatalf("total=%d, 直接 SQL=%d", got.Total, wantTotal)
	}
	for _, code := range entityStates {
		if got.States[code] != want[code] {
			t.Fatalf("状态 %s: 聚合=%d, 直接 SQL=%d", code, got.States[code], want[code])
		}
	}
	for code, n := range want {
		if got.States[code] != n {
			t.Fatalf("聚合漏掉直接 SQL 里的状态 %s=%d: %+v", code, n, got.States)
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
	var body EntityStateCounts
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 EntityStateCounts 的形状: %v %s", err, w.Body.String())
	}
	if body.Total != wantTotal {
		t.Fatalf("响应 total=%d, 直接 SQL=%d", body.Total, wantTotal)
	}
	for _, code := range entityStates {
		if body.States[code] != want[code] {
			t.Fatalf("响应状态 %s=%d, 直接 SQL=%d", code, body.States[code], want[code])
		}
	}

	// 列表口径未被改动：墓碑行照样不出现在列表里，计数只能由本端点给出。
	if n, err := f.s.Count(ctx, ListOptions{State: "deleted"}, &f.u); err != nil || n != 0 {
		t.Fatalf("status=deleted 的列表总数应仍为 0: %v %d", err, n)
	}
	if items, err := f.s.List(ctx, ListOptions{State: "deleted"}, &f.u); err != nil || len(items) != 0 {
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

func TestReadCacheCoalescesAndInvalidatesByToken(t *testing.T) {
	var cache readCache[int]
	var calls atomic.Int32
	gate := make(chan struct{})
	started := make(chan struct{}, 4)

	loader := func(ctx context.Context) (int, error) {
		n := int(calls.Add(1))
		started <- struct{}{}
		<-gate
		return n * 10, nil
	}

	const workers = 6
	results := make([]int, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = cache.getOrLoad(context.Background(), "tok-1", loader)
		}(i)
	}
	<-started
	// Give sibling goroutines time to join the in-flight call.
	time.Sleep(15 * time.Millisecond)
	close(gate)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("expected single coalesced loader call, got %d", calls.Load())
	}
	for i := 0; i < workers; i++ {
		if errs[i] != nil || results[i] != 10 {
			t.Fatalf("worker %d got (%d, %v), want (10, nil)", i, results[i], errs[i])
		}
	}

	// Same token hits memory cache without calling loader.
	got, err := cache.getOrLoad(context.Background(), "tok-1", func(context.Context) (int, error) {
		calls.Add(1)
		return 999, nil
	})
	if err != nil || got != 10 || calls.Load() != 1 {
		t.Fatalf("expected cache hit (10, calls=1), got (%d, err=%v, calls=%d)", got, err, calls.Load())
	}

	// Token change invalidates and reloads.
	got, err = cache.getOrLoad(context.Background(), "tok-2", func(context.Context) (int, error) {
		calls.Add(1)
		return 20, nil
	})
	if err != nil || got != 20 || calls.Load() != 2 {
		t.Fatalf("expected reload on token change (20, calls=2), got (%d, err=%v, calls=%d)", got, err, calls.Load())
	}
}

func TestReadCacheLeaderCancelDoesNotAbortWaiters(t *testing.T) {
	var cache readCache[string]
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()

	started := make(chan struct{})
	release := make(chan struct{})
	loader := func(ctx context.Context) (string, error) {
		close(started)
		select {
		case <-release:
			return "ok", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	leaderErr := make(chan error, 1)
	go func() {
		_, err := cache.getOrLoad(leaderCtx, "tok", loader)
		leaderErr <- err
	}()
	<-started

	waiterVal := make(chan string, 1)
	waiterErr := make(chan error, 1)
	go func() {
		v, err := cache.getOrLoad(context.Background(), "tok", loader)
		waiterVal <- v
		waiterErr <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancelLeader()
	if err := <-leaderErr; err != context.Canceled {
		t.Fatalf("expected leader context.Canceled, got %v", err)
	}
	close(release)
	if err := <-waiterErr; err != nil {
		t.Fatalf("waiter should succeed despite leader cancel, got %v", err)
	}
	if got := <-waiterVal; got != "ok" {
		t.Fatalf("waiter got %q, want ok", got)
	}
}

func TestFilterTagCountsAndCloneCatalogStats(t *testing.T) {
	all := []TagCount{
		{Name: "RPG", Count: 50},
		{Name: "销量100%", Count: 20},
		{Name: "下划线_标签", Count: 10},
		{Name: "Action RPG", Count: 5},
	}
	if got := filterTagCounts(all, "  rpg ", 10); len(got) != 2 || got[0].Name != "RPG" || got[1].Name != "Action RPG" {
		t.Fatalf("case-insensitive substring match failed: %+v", got)
	}
	if got := filterTagCounts(all, "%", 10); len(got) != 1 || got[0].Name != "销量100%" {
		t.Fatalf("literal percent match failed: %+v", got)
	}
	if got := filterTagCounts(all, "_", 10); len(got) != 1 || got[0].Name != "下划线_标签" {
		t.Fatalf("literal underscore match failed: %+v", got)
	}
	if got := filterTagCounts(all, "", 2); len(got) != 2 || got[0].Name != "RPG" || got[1].Name != "销量100%" {
		t.Fatalf("limit slice failed: %+v", got)
	}
	if got := filterTagCounts(all, "nonexistent", 10); got == nil || len(got) != 0 {
		t.Fatalf("empty filter result should be non-nil empty slice: %#v", got)
	}

	src := CatalogStats{
		EntityCoverageCounts: EntityCoverageCounts{Total: 3},
		Kinds:                map[string]EntityCoverageCounts{"work": {Total: 3}},
	}
	cloned := cloneCatalogStats(src)
	cloned.Kinds["work"] = EntityCoverageCounts{Total: 999}
	if src.Kinds["work"].Total != 3 {
		t.Fatalf("cloneCatalogStats leaked map mutation")
	}
}
