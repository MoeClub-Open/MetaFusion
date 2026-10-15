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
	Source  string                     `json:"source"`
	Op      string                     `json:"op"`
	Fetch   *FetchMeta                 `json:"fetch,omitempty"`
	Fetches []FetchMeta                `json:"fetches,omitempty"`
	Raw     any                        `json:"raw,omitempty"`
	Preview *ExternalSourcePreviewResponse   `json:"preview,omitempty"`
	Items   []ExternalSourceSearchItem `json:"items,omitempty"`
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

func contentHash(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// fetchBangumi recorded into context recorder when present; always waits outbound throttle.
func fetchBangumi(ctx context.Context, path string, out any) error {
	if err := waitOutbound(ctx, "bangumi"); err != nil {
		return err
	}
	url := bangumiAPIBase + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", externalSourceUserAgent)
	req.Header.Set("Accept", "application/json")
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
	ctx, rec := withFetchRecorder(ctx)
	switch op {
	case "search":
		return s.externalSearch(ctx, rec, in)
	default:
		return s.externalLookupOrDetail(ctx, rec, in, op)
	}
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
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	switch source {
	case "bangumi":
		return searchBangumi(ctx, rec, q, in.EntityType, limit)
	case "dlsite", "dmm":
		// 这两个适配器目前只有商品页详情抓取，没有稳定的公开搜索 API。
		return ExternalSourceQueryResponse{}, fmt.Errorf("search_not_supported")
	default:
		return ExternalSourceQueryResponse{}, fmt.Errorf("not_supported")
	}
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
	path := "/v0/search/subjects"
	payload := map[string]any{"keyword": q, "limit": limit, "offset": 0}
	switch et {
	case "artist", "organization":
		path = "/v0/search/persons"
	case "character":
		path = "/v0/search/characters"
	}
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
	return ExternalSourceQueryResponse{
		Source:  "bangumi",
		Op:      "search",
		Fetch:   primary,
		Fetches: all,
		Raw:     raw,
		Items:   items,
	}, nil
}
