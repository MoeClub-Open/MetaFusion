package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOutboundThrottleWaits(t *testing.T) {
	lim := newOutboundLimiter(100, 1)
	ctx := context.Background()
	if err := lim.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := lim.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("throttle waited too long: %v", time.Since(start))
	}
}

func TestQueryExternalSourceLookupRecordsFetchMeta(t *testing.T) {
	stubBangumi(t)
	outboundLimiters["bangumi"] = newOutboundLimiter(1000, 10)
	f := newFixture(t)
	out, err := f.s.QueryExternalSource(context.Background(), ExternalSourceQueryRequest{
		Source: "bangumi", Op: "detail", URLOrID: "https://bgm.tv/subject/7", EntityType: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Op != "detail" || out.Source != "bangumi" || out.Preview == nil || out.Preview.ExternalID == "" {
		t.Fatalf("bad response: %+v", out)
	}
	if out.Fetch == nil || out.Fetch.URL == "" || !strings.HasPrefix(out.Fetch.ContentHash, "sha256:") {
		t.Fatalf("missing fetch meta: %+v", out.Fetch)
	}
	if out.Raw == nil {
		t.Fatal("expected raw payload")
	}
	if len(out.Fetches) == 0 {
		t.Fatal("expected fetches list")
	}
}

func TestQueryExternalSourceRejectsBadOp(t *testing.T) {
	_, err := (&Store{}).QueryExternalSource(context.Background(), ExternalSourceQueryRequest{Op: "merge"})
	if err == nil || err.Error() != "invalid_op" {
		t.Fatalf("got %v", err)
	}
}

func TestQueryExternalSourceSearchNotSupportedForDLsite(t *testing.T) {
	_, err := (&Store{}).QueryExternalSource(context.Background(), ExternalSourceQueryRequest{
		Source: "dlsite", Op: "search", Q: "test",
	})
	if err == nil || err.Error() != "search_not_supported" {
		t.Fatalf("got %v", err)
	}
}

func TestSearchBangumiPassesLimitAsQuery(t *testing.T) {
	mux := http.NewServeMux()
	var seenPath string
	var seenBody []byte
	mux.HandleFunc("/v0/search/subjects", func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.String()
		seenBody, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		limit := r.URL.Query().Get("limit")
		n := 10
		if limit != "" {
			if v, err := strconv.Atoi(limit); err == nil && v > 0 {
				n = v
			}
		}
		items := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			items = append(items, map[string]any{
				"id": i + 1, "name": "Item", "name_cn": "条目", "summary": "s", "type": 2,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items, "total": 100, "limit": n, "offset": 0})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		bangumiAPIBase = "https://api.bgm.tv"
	})
	bangumiAPIBase = srv.URL
	outboundLimiters["bangumi"] = newOutboundLimiter(1000, 10)

	f := newFixture(t)
	for _, want := range []int{1, 3, 5, 11, 20} {
		out, err := f.s.QueryExternalSource(context.Background(), ExternalSourceQueryRequest{
			Source: "bangumi", Op: "search", Q: "MyGO", EntityType: "work", Limit: want,
		})
		if err != nil {
			t.Fatalf("limit=%d: %v", want, err)
		}
		if !strings.Contains(seenPath, "limit="+strconv.Itoa(want)) {
			t.Fatalf("limit=%d not in query path: %s", want, seenPath)
		}
		if strings.Contains(string(seenBody), `"limit"`) {
			t.Fatalf("limit must not be in JSON body (Bangumi ignores it): %s", seenBody)
		}
		if out.Limit != want || len(out.Items) != want || out.Clamped {
			t.Fatalf("limit=%d got limit=%d items=%d clamped=%v", want, out.Limit, len(out.Items), out.Clamped)
		}
	}
}

func TestSearchBangumiReportsUpstreamClamp(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/search/subjects", func(w http.ResponseWriter, r *http.Request) {
		// Simulate upstream hard-cap at 10 regardless of query limit.
		items := make([]map[string]any, 0, 10)
		for i := 0; i < 10; i++ {
			items = append(items, map[string]any{"id": i + 1, "name": "Item", "name_cn": "条目", "type": 2})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items, "total": 50, "limit": 10, "offset": 0})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		bangumiAPIBase = "https://api.bgm.tv"
	})
	bangumiAPIBase = srv.URL
	outboundLimiters["bangumi"] = newOutboundLimiter(1000, 10)

	f := newFixture(t)
	out, err := f.s.QueryExternalSource(context.Background(), ExternalSourceQueryRequest{
		Source: "bangumi", Op: "search", Q: "x", EntityType: "work", Limit: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Limit != 10 || !out.Clamped || len(out.Items) != 10 {
		t.Fatalf("want limit=10 clamped=true items=10, got limit=%d clamped=%v items=%d", out.Limit, out.Clamped, len(out.Items))
	}
}

func TestSearchBangumiClampsOurMax(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/search/subjects", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		items := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			items = append(items, map[string]any{"id": i + 1, "name": "Item", "name_cn": "条目", "type": 2})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items, "total": 200, "limit": n, "offset": 0})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		bangumiAPIBase = "https://api.bgm.tv"
	})
	bangumiAPIBase = srv.URL
	outboundLimiters["bangumi"] = newOutboundLimiter(1000, 10)

	f := newFixture(t)
	out, err := f.s.QueryExternalSource(context.Background(), ExternalSourceQueryRequest{
		Source: "bangumi", Op: "search", Q: "x", EntityType: "work", Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Limit != 50 || !out.Clamped || len(out.Items) != 50 {
		t.Fatalf("want our max 50 clamped, got limit=%d clamped=%v items=%d", out.Limit, out.Clamped, len(out.Items))
	}
}

func TestQueryExternalSourceCacheSingleflightAndSkipsNonEpisodeTypes(t *testing.T) {
	var subjectHits atomic.Int32
	var episodeHits atomic.Int32
	var personHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/subjects/7", func(w http.ResponseWriter, _ *http.Request) {
		subjectHits.Add(1)
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"type":4,"name":"テストゲーム","name_cn":"测试游戏","summary":"简介","date":"2024-01-05","images":{"large":"https://example.com/cover.jpg"}}`))
	})
	mux.HandleFunc("/v0/subjects/8", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":8,"type":2,"name":"テストアニメ","name_cn":"测试动画","summary":"简介","date":"2024-01-05","images":{"large":"https://example.com/cover2.jpg"}}`))
	})
	mux.HandleFunc("/v0/subjects/7/persons", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":9,"name":"Alice","type":1,"relation":"原作"}]`))
	})
	mux.HandleFunc("/v0/subjects/8/persons", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":9,"name":"Alice","type":1,"relation":"导演"}]`))
	})
	mux.HandleFunc("/v0/subjects/7/characters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/v0/subjects/8/characters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/v0/persons/9", func(w http.ResponseWriter, _ *http.Request) {
		personHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9,"name":"Alice","name_cn":"","type":1,"career":["原作"],"summary":"作词家","images":{"large":""}}`))
	})
	mux.HandleFunc("/v0/episodes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("subject_id") == "7" {
			episodeHits.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"total":0}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		bangumiAPIBase = "https://api.bgm.tv"
	})
	bangumiAPIBase = srv.URL
	outboundLimiters["bangumi"] = newOutboundLimiter(1000, 10)

	s := &Store{}
	ctx := context.Background()
	const workers = 6
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			src := ""
			if idx%2 == 1 {
				src = "auto"
			}
			_, errs[idx] = s.QueryExternalSource(ctx, ExternalSourceQueryRequest{
				Source: src, Op: "detail", URLOrID: "https://bgm.tv/subject/7", EntityType: "work",
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	if _, err := s.QueryExternalSource(ctx, ExternalSourceQueryRequest{
		Source: "auto", Op: "detail", URLOrID: "https://bgm.tv/subject/7", EntityType: "work",
	}); err != nil {
		t.Fatal(err)
	}
	if got := subjectHits.Load(); got != 1 {
		t.Fatalf("expected singleflight+cache to hit subject 7 once, got %d", got)
	}
	if got := episodeHits.Load(); got != 0 {
		t.Fatalf("expected type=4 game to skip /v0/episodes, got %d hits", got)
	}

	// A different subject sharing person 9 should reuse bangumiPersonCache.
	if _, err := s.QueryExternalSource(ctx, ExternalSourceQueryRequest{
		Source: "bangumi", Op: "detail", URLOrID: "https://bgm.tv/subject/8", EntityType: "work",
	}); err != nil {
		t.Fatal(err)
	}
	if got := personHits.Load(); got != 1 {
		t.Fatalf("expected person 9 cache reuse across subjects, got %d hits", got)
	}
}
