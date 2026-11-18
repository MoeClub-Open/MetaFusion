package catalog

// 统一只读外部来源查询：search / lookup / detail。
// 返回上游原文 raw + FetchMeta（url / fetched_at / content_hash），以及既有 preview 映射（lookup/detail）。
// 永不写目录、不自动合并。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FetchMeta 是一次出站抓取的溯源元数据。
type FetchMeta struct {
	URL         string    `json:"url"`
	FetchedAt   time.Time `json:"fetched_at"`
	ContentHash string    `json:"content_hash"` // sha256:<hex>
}

// ExternalSourceQueryRequest 是 POST /api/external-sources/query 的请求。
// op=search 需要 q；op=lookup|detail 需要 id 或 url_or_id。
type ExternalSourceQueryRequest struct {
	Source     string `json:"source"`
	Op         string `json:"op"`
	Q          string `json:"q,omitempty"`
	ID         string `json:"id,omitempty"`
	URLOrID    string `json:"url_or_id,omitempty"`
	EntityType string `json:"entity_type,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// ExternalSourceSearchItem 是 search 结果的一条。
type ExternalSourceSearchItem struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	EntityType string `json:"entity_type,omitempty"`
	URL        string `json:"url,omitempty"`
	Snippet    string `json:"snippet,omitempty"`
	Raw        any    `json:"raw,omitempty"`
}

// ExternalSourceQueryResponse 是统一查询响应。
type ExternalSourceQueryResponse struct {
	Source  string                         `json:"source"`
	Op      string                         `json:"op"`
	Fetch   *FetchMeta                     `json:"fetch,omitempty"`
	Fetches []FetchMeta                    `json:"fetches,omitempty"`
	Raw     any                            `json:"raw,omitempty"`
	Preview *ExternalSourcePreviewResponse `json:"preview,omitempty"`
	Items   []ExternalSourceSearchItem     `json:"items,omitempty"`
	// Limit 是 search 实际生效的分页大小（我方 clamp 后、或上游回显）。
	Limit int `json:"limit,omitempty"`
	// Clamped 表示请求的 limit 与实际生效不一致（我方上限 50，或上游钳制）。
	Clamped bool `json:"clamped,omitempty"`
}

type fetchRecorder struct {
	mu    sync.Mutex
	items []FetchMeta
	raws  []json.RawMessage
}

type fetchRecorderKey struct{}

func withFetchRecorder(ctx context.Context) (context.Context, *fetchRecorder) {
	r := &fetchRecorder{}
	return context.WithValue(ctx, fetchRecorderKey{}, r), r
}

func recorderFrom(ctx context.Context) *fetchRecorder {
	r, _ := ctx.Value(fetchRecorderKey{}).(*fetchRecorder)
	return r
}

func (r *fetchRecorder) add(meta FetchMeta, raw []byte) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, meta)
	if raw != nil {
		cp := append(json.RawMessage(nil), raw...)
		r.raws = append(r.raws, cp)
	}
}

func (r *fetchRecorder) snapshot() (primary *FetchMeta, all []FetchMeta, primaryRaw any) {
	if r == nil {
		return nil, nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.items) == 0 {
		return nil, nil, nil
	}
	all = append([]FetchMeta(nil), r.items...)
	primary = &all[0]
	if len(r.raws) > 0 {
		var v any
		if json.Unmarshal(r.raws[0], &v) == nil {
			primaryRaw = v
		} else {
			primaryRaw = string(r.raws[0])
		}
	}
	return primary, all, primaryRaw
}

type ttlCacheEntry[V any] struct {
	val       V
	expiresAt time.Time
	seq       uint64
}

type ttlCache[K comparable, V any] struct {
	ttl        time.Duration
	maxEntries int
	mu         sync.Mutex
	seq        uint64
	entries    map[K]ttlCacheEntry[V]
}

func newTTLCache[K comparable, V any](ttl time.Duration, maxEntries int) *ttlCache[K, V] {
	return &ttlCache[K, V]{
		ttl:        ttl,
		maxEntries: maxEntries,
		entries:    map[K]ttlCacheEntry[V]{},
	}
}

func (c *ttlCache[K, V]) Get(key K) (V, bool) {
	var zero V
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(c.entries, key)
		return zero, false
	}
	return entry.val, true
}

func (c *ttlCache[K, V]) Set(key K, val V) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxEntries > 0 && len(c.entries) >= c.maxEntries {
		for k, entry := range c.entries {
			if now.After(entry.expiresAt) {
				delete(c.entries, k)
			}
		}
		for len(c.entries) >= c.maxEntries {
			var oldestK K
			var oldestSeq uint64
			first := true
			for k, entry := range c.entries {
				if first || entry.seq < oldestSeq {
					oldestK = k
					oldestSeq = entry.seq
					first = false
				}
			}
			if first {
				break
			}
			delete(c.entries, oldestK)
		}
	}
	c.seq++
	c.entries[key] = ttlCacheEntry[V]{
		val:       val,
		expiresAt: now.Add(c.ttl),
		seq:       c.seq,
	}
}

type inflightCall[V any] struct {
	done chan struct{}
	val  V
	err  error
}

type inflightGroup[K comparable, V any] struct {
	mu    sync.Mutex
	calls map[K]*inflightCall[V]
}

func newInflightGroup[K comparable, V any]() *inflightGroup[K, V] {
	return &inflightGroup[K, V]{calls: map[K]*inflightCall[V]{}}
}

func (g *inflightGroup[K, V]) Do(ctx context.Context, key K, timeout time.Duration, fn func(context.Context) (V, error)) (V, error) {
	g.mu.Lock()
	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			var zero V
			return zero, ctx.Err()
		case <-call.done:
			return call.val, call.err
		}
	}
	call := &inflightCall[V]{done: make(chan struct{})}
	g.calls[key] = call
	g.mu.Unlock()

	go func() {
		callCtx := context.WithoutCancel(ctx)
		if timeout > 0 {
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(callCtx, timeout)
			defer cancel()
		}
		call.val, call.err = fn(callCtx)
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		close(call.done)
	}()

	select {
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	case <-call.done:
		return call.val, call.err
	}
}

type externalQueryCacheKey struct {
	bangumiBase string
	dlsiteBase  string
	dmmBase     string
	dmmSoftBase string
	dmmGateBase string
	source      string
	op          string
	refOrQ      string
	entityType  string
	limit       int
}

var (
	externalQueryCache  = newTTLCache[externalQueryCacheKey, ExternalSourceQueryResponse](5*time.Minute, 2048)
	externalQueryFlight = newInflightGroup[externalQueryCacheKey, ExternalSourceQueryResponse]()
)

func contentHash(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fetchBangumi recorded into context recorder when present; always waits outbound throttle.
func fetchBangumi(ctx context.Context, path string, out any) error {
	_, err := fetchBangumiWithMeta(ctx, path, out)
	return err
}

func fetchBangumiWithMeta(ctx context.Context, path string, out any) (FetchMeta, error) {
	if err := waitOutbound(ctx, "bangumi"); err != nil {
		return FetchMeta{}, err
	}
	url := bangumiAPIBase + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FetchMeta{}, err
	}
	req.Header.Set("User-Agent", externalSourceUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := externalSourceHTTPClient.Do(req)
	if err != nil {
		return FetchMeta{}, fmt.Errorf("upstream_error")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return FetchMeta{}, fmt.Errorf("upstream_error")
	}
	meta := FetchMeta{URL: url, FetchedAt: time.Now().UTC(), ContentHash: contentHash(body)}
	recorderFrom(ctx).add(meta, body)
	if resp.StatusCode == http.StatusNotFound {
		return meta, fmt.Errorf("not_found")
	}
	if resp.StatusCode != http.StatusOK {
		return meta, fmt.Errorf("upstream_error")
	}
	if out == nil {
		return meta, nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return meta, fmt.Errorf("upstream_error")
	}
	return meta, nil
}

func fetchBangumiPOST(ctx context.Context, path string, payload any, out any) error {
	if err := waitOutbound(ctx, "bangumi"); err != nil {
		return err
	}
	url := bangumiAPIBase + path
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("invalid_payload")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", externalSourceUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := externalSourceHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("upstream_error")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("upstream_error")
	}
	meta := FetchMeta{URL: url, FetchedAt: time.Now().UTC(), ContentHash: contentHash(body)}
	recorderFrom(ctx).add(meta, body)
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("not_found")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream_error")
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("upstream_error")
	}
	return nil
}

// ExternalSources 列出有适配器的外部来源（原 ExternalSourceDefinitions）。
func (s *Store) ExternalSources(ctx context.Context) ([]ExternalSourceDefinition, error) {
	return s.ExternalSourceDefinitions(ctx)
}

// QueryExternalSource 统一只读查询。
func (s *Store) QueryExternalSource(ctx context.Context, in ExternalSourceQueryRequest) (ExternalSourceQueryResponse, error) {
	op := strings.ToLower(strings.TrimSpace(in.Op))
	switch op {
	case "search", "lookup", "detail":
	default:
		return ExternalSourceQueryResponse{}, fmt.Errorf("invalid_op")
	}
	src := strings.ToLower(strings.TrimSpace(in.Source))
	if src == "" {
		src = "auto"
	}
	refOrQ := ""
	limit := 0
	if op == "search" {
		refOrQ = strings.TrimSpace(in.Q)
		if refOrQ == "" {
			return ExternalSourceQueryResponse{}, fmt.Errorf("invalid_payload")
		}
		limit = in.Limit
	} else {
		refOrQ = strings.TrimSpace(in.URLOrID)
		if refOrQ == "" {
			refOrQ = strings.TrimSpace(in.ID)
		}
		if refOrQ == "" {
			return ExternalSourceQueryResponse{}, fmt.Errorf("invalid_payload")
		}
	}
	key := externalQueryCacheKey{
		bangumiBase: bangumiAPIBase,
		dlsiteBase:  dlsiteWWWBase,
		dmmBase:     dmmCoJPBase,
		dmmSoftBase: dmmSoftBase,
		dmmGateBase: dmmGateBase,
		source:      src,
		op:          op,
		refOrQ:      refOrQ,
		entityType:  strings.ToLower(strings.TrimSpace(in.EntityType)),
		limit:       limit,
	}
	if cached, ok := externalQueryCache.Get(key); ok {
		return cached, nil
	}
	return externalQueryFlight.Do(ctx, key, 30*time.Second, func(fctx context.Context) (ExternalSourceQueryResponse, error) {
		if cached, ok := externalQueryCache.Get(key); ok {
			return cached, nil
		}
		fctx, rec := withFetchRecorder(fctx)
		var res ExternalSourceQueryResponse
		var err error
		if op == "search" {
			res, err = s.externalSearch(fctx, rec, in)
		} else {
			res, err = s.externalLookupOrDetail(fctx, rec, in, op)
		}
		if err == nil {
			externalQueryCache.Set(key, res)
		}
		return res, err
	})
}

func (s *Store) externalLookupOrDetail(ctx context.Context, rec *fetchRecorder, in ExternalSourceQueryRequest, op string) (ExternalSourceQueryResponse, error) {
	ref := strings.TrimSpace(in.URLOrID)
	if ref == "" {
		ref = strings.TrimSpace(in.ID)
	}
	if ref == "" {
		return ExternalSourceQueryResponse{}, fmt.Errorf("invalid_payload")
	}
	preview, err := s.Preview(ctx, in.Source, ref, in.EntityType)
	if err != nil {
		return ExternalSourceQueryResponse{}, err
	}
	primary, all, raw := rec.snapshot()
	return ExternalSourceQueryResponse{
		Source:  preview.Source,
		Op:      op,
		Fetch:   primary,
		Fetches: all,
		Raw:     raw,
		Preview: &preview,
	}, nil
}

func (s *Store) externalSearch(ctx context.Context, rec *fetchRecorder, in ExternalSourceQueryRequest) (ExternalSourceQueryResponse, error) {
	q := strings.TrimSpace(in.Q)
	if q == "" {
		return ExternalSourceQueryResponse{}, fmt.Errorf("invalid_payload")
	}
	rawSource := strings.ToLower(strings.TrimSpace(in.Source))
	if rawSource == "" || rawSource == "auto" {
		rawSource = "bangumi"
	}
	source, err := normalizeExternalSourceDefinition(rawSource)
	if err != nil {
		return ExternalSourceQueryResponse{}, err
	}
	requested := in.Limit
	limit := requested
	if limit <= 0 {
		limit = 20
	}
	ourClamp := false
	if limit > 50 {
		limit = 50
		ourClamp = requested > 50
	}
	var out ExternalSourceQueryResponse
	switch source {
	case "bangumi":
		out, err = searchBangumi(ctx, rec, q, in.EntityType, limit)
	case "dlsite", "dmm":
		// 这两个适配器目前只有商品页详情抓取，没有稳定的公开搜索 API。
		return ExternalSourceQueryResponse{}, fmt.Errorf("search_not_supported")
	default:
		return ExternalSourceQueryResponse{}, fmt.Errorf("not_supported")
	}
	if err != nil {
		return ExternalSourceQueryResponse{}, err
	}
	if out.Limit <= 0 {
		out.Limit = limit
	}
	if ourClamp || (requested > 0 && out.Limit != requested) || out.Limit != limit {
		out.Clamped = true
	}
	return out, nil
}

type bangumiSearchHit struct {
	ID      int           `json:"id"`
	Name    string        `json:"name"`
	NameCN  string        `json:"name_cn"`
	Summary string        `json:"summary"`
	Type    int           `json:"type"`
	Images  bangumiImages `json:"images"`
}

type bangumiSearchResponse struct {
	Data   []bangumiSearchHit `json:"data"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

func searchBangumi(ctx context.Context, rec *fetchRecorder, q, entityType string, limit int) (ExternalSourceQueryResponse, error) {
	et, err := normalizeExternalSourceEntityType(entityType)
	if err != nil {
		return ExternalSourceQueryResponse{}, err
	}
	// 人物/角色搜索走不同端点；作品默认 subjects。
	// Bangumi OpenAPI：limit/offset 是 query 参数，写在 JSON body 会被忽略并默认 10。
	path := "/v0/search/subjects"
	payload := map[string]any{"keyword": q}
	switch et {
	case "artist", "organization":
		path = "/v0/search/persons"
	case "character":
		path = "/v0/search/characters"
	}
	path = path + "?limit=" + strconv.Itoa(limit) + "&offset=0"
	var resp bangumiSearchResponse
	if err := fetchBangumiPOST(ctx, path, payload, &resp); err != nil {
		return ExternalSourceQueryResponse{}, err
	}
	items := make([]ExternalSourceSearchItem, 0, len(resp.Data))
	for _, hit := range resp.Data {
		title := strings.TrimSpace(hit.NameCN)
		if title == "" {
			title = strings.TrimSpace(hit.Name)
		}
		id := strconv.Itoa(hit.ID)
		url := ""
		itemType := et
		switch et {
		case "artist", "organization":
			url = "https://bgm.tv/person/" + id
		case "character":
			url = "https://bgm.tv/character/" + id
		default:
			url = "https://bgm.tv/subject/" + id
			itemType = "work"
			if hit.Type > 0 {
				// keep work; media_type is in raw
			}
		}
		var raw any
		b, _ := json.Marshal(hit)
		_ = json.Unmarshal(b, &raw)
		items = append(items, ExternalSourceSearchItem{
			ID:         id,
			Title:      title,
			EntityType: itemType,
			URL:        url,
			Snippet:    strings.TrimSpace(hit.Summary),
			Raw:        raw,
		})
	}
	primary, all, raw := rec.snapshot()
	applied := limit
	clamped := false
	if resp.Limit > 0 {
		applied = resp.Limit
		if applied != limit {
			clamped = true
		}
	}
	if len(items) > applied {
		items = items[:applied]
		clamped = true
	}
	return ExternalSourceQueryResponse{
		Source:  "bangumi",
		Op:      "search",
		Fetch:   primary,
		Fetches: all,
		Raw:     raw,
		Items:   items,
		Limit:   applied,
		Clamped: clamped,
	}, nil
}
