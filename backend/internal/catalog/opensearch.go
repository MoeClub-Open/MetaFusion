package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	openSearchAlias           = "metafusion-entities"
	openSearchIndex           = "metafusion-entities-v3"
	openSearchConsumer        = "opensearch"
	openSearchIndexGeneration = "v3"
	openSearchLockID          = int64(740219)
	openSearchResultWindow    = 10000
)

var errSearchUnavailable = errors.New("search_unavailable")

// OpenSearch performs text matching and pagination. PostgreSQL supplies current
// entity content and authorization; a failed index is an explicit service error.
type OpenSearchClient struct {
	baseURL  string
	username string
	password string
	http     *http.Client
	ready    atomic.Bool
	logMu    sync.Mutex
	lastLog  time.Time
}

func (c *OpenSearchClient) Ready() bool { return c != nil && c.ready.Load() }

func NewOpenSearchClient(rawURL, username, password string) (*OpenSearchClient, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("OPENSEARCH_URL must be an http(s) URL without embedded credentials, query, or fragment")
	}
	if (username == "") != (password == "") {
		return nil, fmt.Errorf("OPENSEARCH_USERNAME and OPENSEARCH_PASSWORD must be set together")
	}
	return &OpenSearchClient{
		baseURL:  strings.TrimRight(u.String(), "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *OpenSearchClient) request(ctx context.Context, method, path string, body []byte, contentType string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	return resp.StatusCode, b, err
}

func (c *OpenSearchClient) logIssue(message string, err error) {
	c.logMu.Lock()
	defer c.logMu.Unlock()
	if time.Since(c.lastLog) < time.Minute {
		return
	}
	c.lastLog = time.Now()
	log.Printf("%s: %v", message, err)
}

// searchPage returns one ordered page of IDs and the exact snapshot hit count
// reported by OpenSearch. The caller still applies PostgreSQL visibility and
// list filters before returning any entity data.
func (c *OpenSearchClient) searchPage(ctx context.Context, o ListOptions, u *User, cursor searchCursor) (searchHitPage, error) {
	if !c.ready.Load() {
		return searchHitPage{}, errSearchUnavailable
	}
	query := strings.TrimSpace(o.Query)
	if query == "" {
		return searchHitPage{}, fmt.Errorf("invalid_query_param")
	}

	filters := []any{map[string]any{"term": map[string]any{"record_type": "entity"}}}
	mustNot := []any{
		map[string]any{"terms": map[string]any{"status": []string{"deleted", "merged"}}},
	}
	match := map[string]any{"multi_match": map[string]any{
		"query": query, "type": "bool_prefix", "operator": "and",
		"fields": []string{"title_text^5", "title_text._2gram^3", "title_text._3gram^2", "search_text", "search_text._2gram", "search_text._3gram"},
	}}
	substring := map[string]any{"wildcard": map[string]any{"search_text_exact": map[string]any{
		"value": "*" + escapeOpenSearchWildcard(query) + "*", "case_insensitive": true,
	}}}
	if utf8.RuneCountInString(query) >= 3 {
		substring = map[string]any{"bool": map[string]any{"must": []any{
			map[string]any{"match": map[string]any{"search_text_grams": map[string]any{"query": query, "operator": "and"}}}, substring,
		}}}
	}
	must := []any{map[string]any{"bool": map[string]any{
		"should": []any{match, substring}, "minimum_should_match": 1,
	}}}
	term := func(field, value string) {
		if value != "" {
			filters = append(filters, map[string]any{"term": map[string]any{field: value}})
		}
	}
	terms := func(field string, values []string) {
		if len(values) > 0 {
			filters = append(filters, map[string]any{"terms": map[string]any{field: values}})
		}
	}
	term("kind", o.Kind)
	terms("kind", o.Kinds)
	term("status", o.Status)
	term("original_language", o.OriginalLanguage)
	term("work_ids", o.WorkID)
	term("content_unit_id", o.ContentUnitID)
	term("release_id", o.ReleaseID)
	term("medium_id", o.MediumID)
	term("parent_id", o.ParentID)
	if o.Field != "" {
		filters = append(filters, map[string]any{"nested": map[string]any{
			"path": "fields", "query": map[string]any{"bool": map[string]any{"filter": []any{
				map[string]any{"term": map[string]any{"fields.path": o.Field}},
				map[string]any{"term": map[string]any{"fields.value": o.Value}},
			}}},
		}})
	}
	if o.HasPictures {
		filters = append(filters, map[string]any{"term": map[string]any{"has_pictures": true}})
	}
	if o.TagsMode == "all" {
		for _, tag := range o.Tags {
			term("tags", tag)
		}
	} else {
		terms("tags", o.Tags)
	}
	if u == nil {
		term("status", "published")
	} else if !u.Can(PermissionLifecycleManage) {
		filters = append(filters, map[string]any{"bool": map[string]any{
			"should": []any{
				map[string]any{"term": map[string]any{"status": "published"}},
				map[string]any{"term": map[string]any{"created_by": u.ID}},
			}, "minimum_should_match": 1,
		}})
	}

	sort := []any{map[string]any{"_score": "desc"}, map[string]any{"entity_id": "asc"}}
	dir := "desc"
	if o.Sort == "title" {
		dir = "asc"
	}
	if o.Order != "" {
		dir = o.Order
	}
	switch o.Sort {
	case "created_at":
		sort = []any{map[string]any{"entity_id": dir}}
	case "updated_at":
		sort = []any{map[string]any{"updated_at": dir}, map[string]any{"entity_id": "asc"}}
	case "title":
		sort = []any{map[string]any{"_script": map[string]any{
			"type": "string", "order": dir, "script": map[string]any{
				"lang": "painless", "source": "String f = 'titles.' + params.locale; if (doc.containsKey(f) && doc[f].size() != 0) return doc[f].value; return doc['sort_title'].value;",
				"params": map[string]any{"locale": o.Locale},
			},
		}}, map[string]any{"entity_id": "asc"}}
	}
	payload := map[string]any{
		"size":             o.Limit,
		"track_total_hits": true,
		"_source":          false,
		"query":            map[string]any{"bool": map[string]any{"must": must, "filter": filters, "must_not": mustNot}},
		"sort":             sort,
		"pit":              map[string]any{"id": cursor.PIT, "keep_alive": "1m"},
	}
	if len(cursor.After) > 0 {
		payload["search_after"] = cursor.After
	} else {
		payload["from"] = o.Offset
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return searchHitPage{}, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	status, raw, err := c.request(reqCtx, http.MethodPost, "/_search", body, "application/json")
	if err != nil {
		return searchHitPage{}, err
	}
	if status < 200 || status >= 300 {
		if status == http.StatusNotFound {
			return searchHitPage{}, fmt.Errorf("search_cursor_expired")
		}
		return searchHitPage{}, fmt.Errorf("OpenSearch search returned HTTP %d", status)
	}
	var result struct {
		TimedOut bool   `json:"timed_out"`
		PIT      string `json:"pit_id"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Total struct {
				Value    int    `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
			Hits []struct {
				ID   string            `json:"_id"`
				Sort []json.RawMessage `json:"sort"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return searchHitPage{}, fmt.Errorf("decode OpenSearch response: %w", err)
	}
	if result.TimedOut || result.Shards.Failed > 0 || result.Hits.Total.Relation != "eq" {
		return searchHitPage{}, fmt.Errorf("OpenSearch returned incomplete search results")
	}
	page := searchHitPage{Total: result.Hits.Total.Value, PIT: cursor.PIT}
	if result.PIT != "" {
		page.PIT = result.PIT
	}
	for _, hit := range result.Hits.Hits {
		if _, err := uuid.Parse(hit.ID); err != nil {
			return searchHitPage{}, fmt.Errorf("OpenSearch returned an invalid entity ID")
		}
		if len(hit.Sort) != len(sort) {
			return searchHitPage{}, fmt.Errorf("OpenSearch returned invalid sort values")
		}
		page.IDs = append(page.IDs, hit.ID)
		page.After = hit.Sort
	}
	return page, nil
}

type searchCursor struct {
	PIT         string            `json:"pit"`
	After       []json.RawMessage `json:"after"`
	Fingerprint string            `json:"fingerprint"`
	Position    int               `json:"position"`
}

type searchHitPage struct {
	IDs   []string
	Total int
	PIT   string
	After []json.RawMessage
}

type EntitySearchPage struct {
	Items         []Entity `json:"items"`
	Total         int      `json:"total"`
	TotalRelation string   `json:"total_relation"`
	HasMore       bool     `json:"has_more"`
	NextCursor    string   `json:"next_cursor,omitempty"`
}

// Search keeps a PIT for one minute between pages. Counts describe that index
// snapshot, while returned documents are rechecked against current PostgreSQL.
func (s *Store) Search(ctx context.Context, o ListOptions, u *User, rawCursor string) (EntitySearchPage, error) {
	out := EntitySearchPage{Items: []Entity{}, TotalRelation: "index_snapshot"}
	if s.OpenSearch == nil || !s.OpenSearch.ready.Load() {
		return out, errSearchUnavailable
	}
	if o.Offset > openSearchResultWindow-o.Limit {
		return out, fmt.Errorf("search_window_exceeded")
	}
	if rawCursor != "" && o.Offset != 0 {
		return out, fmt.Errorf("pagination_conflict")
	}
	// Validate published field paths with exactly the same rules as browsing.
	validation := o
	validation.Query = ""
	if _, err := listFilter(ctx, s, validation, u, &[]any{}); err != nil {
		return out, err
	}
	identity := "anonymous"
	if u != nil {
		identity = u.ID + fmt.Sprint(u.Can(PermissionLifecycleManage))
	}
	bound := o
	bound.Offset = 0
	b, _ := json.Marshal(struct {
		Options  ListOptions
		Identity string
	}{bound, identity})
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(b))
	cursor := searchCursor{Fingerprint: fingerprint, Position: o.Offset}
	if rawCursor != "" {
		if len(rawCursor) > 16384 {
			return out, fmt.Errorf("invalid_search_cursor")
		}
		b, err := base64.RawURLEncoding.DecodeString(rawCursor)
		if err != nil || json.Unmarshal(b, &cursor) != nil || cursor.PIT == "" || len(cursor.After) == 0 || cursor.Fingerprint != fingerprint {
			return out, fmt.Errorf("invalid_search_cursor")
		}
	} else {
		status, raw, err := s.OpenSearch.request(ctx, http.MethodPost, "/"+openSearchAlias+"/_search/point_in_time?keep_alive=1m", nil, "")
		if err != nil || status != http.StatusOK {
			return out, errSearchUnavailable
		}
		var pit struct {
			ID string `json:"pit_id"`
		}
		if json.Unmarshal(raw, &pit) != nil || pit.ID == "" {
			return out, errSearchUnavailable
		}
		cursor.PIT = pit.ID
	}
	page, err := s.OpenSearch.searchPage(ctx, o, u, cursor)
	if err != nil {
		if err.Error() == "search_cursor_expired" {
			return out, err
		}
		s.OpenSearch.logIssue("OpenSearch search failed", err)
		return out, errSearchUnavailable
	}
	out.Total = page.Total
	// Only the requested page reaches PostgreSQL, never a 10,000-ID candidate set.
	validation.SearchIDs = page.IDs
	if len(page.IDs) > 0 {
		args := []any{}
		parts, err := listFilter(ctx, s, validation, u, &args)
		if err != nil {
			return out, err
		}
		rows, err := s.DB.QueryContext(ctx, "SELECT id::text FROM catalog.entities WHERE "+strings.Join(parts, " AND "), args...)
		if err != nil {
			return out, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return out, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, err
		}
		entities, err := s.getMany(ctx, ids, u)
		if err != nil {
			return out, err
		}
		for _, id := range page.IDs {
			if e, ok := entities[id]; ok {
				out.Items = append(out.Items, e)
			}
		}
	}
	cursor.Position += len(page.IDs)
	out.HasMore = len(page.IDs) == o.Limit && cursor.Position < page.Total
	if out.HasMore {
		cursor.PIT, cursor.After = page.PIT, page.After
		b, _ := json.Marshal(cursor)
		out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	} else {
		b, _ := json.Marshal(map[string]any{"pit_id": []string{page.PIT}})
		_, _, _ = s.OpenSearch.request(ctx, http.MethodDelete, "/_search/point_in_time", b, "application/json")
	}
	return out, nil
}

func escapeOpenSearchWildcard(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r == '\\' || r == '*' || r == '?' {
			out.WriteRune('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}
