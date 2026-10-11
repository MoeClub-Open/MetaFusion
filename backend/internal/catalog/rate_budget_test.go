package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSharedRateWindowAtomicAcrossRequests(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const requests = 24
	var wg sync.WaitGroup
	counts := make(chan int, requests)
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			used, retry, err := consumeRateWindow(ctx, f.s.DB, "u:shared")
			if err != nil {
				errs <- err
				return
			}
			if retry < 1 || retry > 60 {
				errs <- fmt.Errorf("invalid reset seconds: %d", retry)
			}
			counts <- used
		}()
	}
	wg.Wait()
	close(counts)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	seen := map[int]bool{}
	for used := range counts {
		if seen[used] {
			t.Fatalf("concurrent increments lost: duplicate count %d", used)
		}
		seen[used] = true
	}
	if len(seen) != requests || !seen[requests] {
		t.Fatalf("expected one shared counter reaching %d, got %v", requests, seen)
	}
	if _, err := f.s.DB.ExecContext(ctx, "UPDATE catalog.api_rate_windows SET window_start=now()-interval '2 minutes'"); err != nil {
		t.Fatal(err)
	}
	if used, _, err := consumeRateWindow(ctx, f.s.DB, "u:shared"); err != nil || used != 1 {
		t.Fatalf("expired shared window must reset: used=%d err=%v", used, err)
	}
}

func TestReplicaReadAndWriteUseOneBudget(t *testing.T) {
	f := newFixture(t)
	gin.SetMode(gin.TestMode)
	InvalidateRateLimitPolicyCache()
	t.Cleanup(InvalidateRateLimitPolicyCache)
	engine := func() *gin.Engine {
		r := gin.New()
		r.Use(routeLimiter(2, f.s.DB))
		r.GET("/read", func(c *gin.Context) { c.Status(200) })
		r.POST("/write", func(c *gin.Context) { c.Status(200) })
		return r
	}
	a, b := engine(), engine()
	call := func(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w
	}
	if w := call(a, http.MethodGet, "/read"); w.Code != 200 || w.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Fatalf("first replica read: %d %v", w.Code, w.Header())
	}
	if w := call(b, http.MethodPost, "/write"); w.Code != 200 || w.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Fatalf("second replica write: %d %v", w.Code, w.Header())
	}
	if w := call(a, http.MethodGet, "/read"); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("combined budget exceeded: %d %v", w.Code, w.Header())
	}
}

func TestOperationalRatePolicyNeedsNoInventedEvidence(t *testing.T) {
	f := newFixture(t)
	t.Cleanup(InvalidateRateLimitPolicyCache)
	ctx := context.Background()
	base, err := f.s.RateLimitPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SaveRateLimitPolicy(ctx, RateLimitPolicy{DefaultPerMinute: 50}, base.ETag, f.u, "adjust operational request budget", nil); err != nil {
		t.Fatalf("operator note without invented evidence must be accepted: %v", err)
	}
}

func TestSharedBudgetDatabaseFailureIsUnavailable(t *testing.T) {
	InvalidateRateLimitPolicyCache()
	t.Cleanup(InvalidateRateLimitPolicyCache)
	db, err := sql.Open("postgres", "postgres://127.0.0.1:1/nope?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	r := gin.New()
	r.GET("/read", routeLimiter(2, db), func(c *gin.Context) { c.Status(200) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/read", nil))
	if w.Code != 503 || w.Body.String() != `{"error":"rate_limit_unavailable"}` || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("database failure must stop the request with a stable retryable error: %d %s", w.Code, w.Body.String())
	}
}

func TestSharedBudgetNeverRewindsANewerWindow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := consumeRateWindow(ctx, f.s.DB, "u:delayed"); err != nil {
		t.Fatal(err)
	}
	// Model a newer request winning the row lock before an earlier request's
	// precomputed INSERT timestamp is applied. The older timestamp must never
	// reset the newer budget, including when a clock moves backwards.
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.api_rate_windows
		SET window_start=date_trunc('minute',now())+interval '1 minute', used=180`); err != nil {
		t.Fatal(err)
	}
	if used, _, err := consumeRateWindow(ctx, f.s.DB, "u:delayed"); err != nil || used != 181 {
		t.Fatalf("late older request rewound a newer budget: used=%d err=%v", used, err)
	}
	var ahead bool
	if err := f.s.DB.QueryRowContext(ctx, "SELECT window_start > date_trunc('minute',now()) FROM catalog.api_rate_windows").Scan(&ahead); err != nil || !ahead {
		t.Fatalf("newer window timestamp must be retained: ahead=%v err=%v", ahead, err)
	}
}
