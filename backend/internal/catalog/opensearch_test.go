package catalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewOpenSearchClientValidatesURLAndCredentials(t *testing.T) {
	valid := "http://127.0.0.1:9200"
	if _, err := NewOpenSearchClient(valid, "", ""); err != nil {
		t.Fatalf("valid client rejected: %v", err)
	}
	for _, rawURL := range []string{
		"ftp://127.0.0.1:9200",
		"http://user:pass@127.0.0.1:9200",
		"http://127.0.0.1:9200?pretty=true",
		"http://127.0.0.1:9200#fragment",
	} {
		if _, err := NewOpenSearchClient(rawURL, "", ""); err == nil {
			t.Errorf("URL %q should be rejected", rawURL)
		}
	}
	if _, err := NewOpenSearchClient(valid, "admin", ""); err == nil {
		t.Error("username without password should be rejected")
	}
	if _, err := NewOpenSearchClient(valid, "", "secret"); err == nil {
		t.Error("password without username should be rejected")
	}
}

func TestMakeSearchDocumentIncludesExternalIDs(t *testing.T) {
	doc := makeSearchDocument(Entity{
		ID:      uuid.NewString(),
		Kind:    "work",
		Version: 2,
		Title:   "Example",
		ExternalIDs: map[string]string{
			"musicbrainz":  "abc-123",
			"bangumi_work": "42",
		},
	})
	text := strings.Join(doc.SearchText, "\n")
	exact := strings.Join(doc.SearchTextExact, "\n")
	for _, want := range []string{"musicbrainz:abc-123", "abc-123", "bangumi_work:42", "42"} {
		if !strings.Contains(text, want) {
			t.Errorf("search text %q does not contain %q", text, want)
		}
		if !strings.Contains(exact, want) {
			t.Errorf("exact search text %q does not contain %q", exact, want)
		}
	}
}

func TestSearchPageOnlyRequestsOnePage(t *testing.T) {
	firstID := uuid.NewString()
	secondID := uuid.NewString()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_search" {
			t.Errorf("unexpected search request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read search body: %v", err)
			return
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode search body: %v", err)
			return
		}
		if payload["track_total_hits"] != true {
			t.Errorf("track_total_hits = %#v, want true", payload["track_total_hits"])
		}
		if payload["size"] != float64(2) {
			t.Errorf("expected a two-item page: %s", body)
		}
		if !strings.Contains(string(body), "search_text_exact") {
			t.Error("search query does not include exact substring fallback")
		}
		if !strings.Contains(string(body), "published") {
			t.Error("anonymous search query does not restrict to published entities")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":20000,"relation":"eq"},"hits":[{"_id":"` + firstID + `","sort":[1,"` + firstID + `"]},{"_id":"` + secondID + `","sort":[1,"` + secondID + `"]}]}}`))
	}))
	defer server.Close()

	client, err := NewOpenSearchClient(server.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	client.ready.Store(true)
	page, err := client.searchPage(context.Background(), ListOptions{Query: "外部 ID", Kind: "work", Limit: 2}, nil, searchCursor{PIT: "test-pit"})
	if err != nil {
		t.Fatalf("searchIDs returned error: %v", err)
	}
	if page.Total != 20000 || len(page.IDs) != 2 || page.IDs[0] != firstID || page.IDs[1] != secondID {
		t.Fatalf("searchPage = %#v", page)
	}
}

func TestOpenSearchUnavailableIsExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := NewOpenSearchClient(server.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	client.ready.Store(true)
	store := &Store{OpenSearch: client}
	_, err = store.Search(context.Background(), ListOptions{Query: "missing", Limit: 50}, nil, "")
	if err != errSearchUnavailable {
		t.Fatalf("unavailable OpenSearch error = %v", err)
	}
}

func TestOpenSearchTagMatchingModes(t *testing.T) {
	for _, mode := range []string{"", "any", "all"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Query struct {
						Bool struct {
							Filter []map[string]map[string]any `json:"filter"`
						} `json:"bool"`
					} `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					return
				}
				var matches []string
				for _, filter := range payload.Query.Bool.Filter {
					if tag, ok := filter["term"]["tags"].(string); ok {
						matches = append(matches, tag)
					}
					if tags, ok := filter["terms"]["tags"].([]any); ok {
						if mode == "all" || len(tags) != 2 || tags[0] != "A" || tags[1] != "B" {
							t.Errorf("unexpected OR filter: %v", tags)
						}
						matches = append(matches, "any")
					}
				}
				want := "any"
				if mode == "all" {
					want = "A,B"
				}
				if strings.Join(matches, ",") != want {
					t.Errorf("tag filters = %v, want %s", matches, want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`))
			}))
			defer server.Close()
			client, err := NewOpenSearchClient(server.URL, "", "")
			if err != nil {
				t.Fatal(err)
			}
			client.ready.Store(true)
			if _, err := client.searchPage(context.Background(), ListOptions{Query: "example", Limit: 24, Tags: []string{"A", "B"}, TagsMode: mode}, nil, searchCursor{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEnsureIndexUsesCurrentGeneration(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/"+openSearchIndex:
			http.NotFound(w, r)
		case r.Method == http.MethodPut && r.URL.Path == "/"+openSearchIndex:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read alias body: %v", err)
			}
			if !strings.Contains(string(body), "substring_grams") || strings.Contains(string(body), "aliases") {
				t.Errorf("index must contain current mappings and remain unaliased until rebuilt: %s", body)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected recovery request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client, err := NewOpenSearchClient(server.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ensureIndex(context.Background()); err != nil {
		t.Fatalf("ensureIndex recovery returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("creation made %d requests, want 2", calls)
	}
}

func TestIndexDocumentsWaitsForRefreshAndSendsExternalVersions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/_bulk" || r.URL.Query().Get("refresh") != "wait_for" {
			t.Errorf("unexpected bulk request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read bulk body: %v", err)
			return
		}
		lines := strings.Split(strings.TrimSpace(string(body)), "\n")
		if len(lines) != 4 {
			t.Errorf("bulk body has %d lines, want 4: %s", len(lines), body)
		}
		var action map[string]map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &action); err != nil {
			t.Errorf("decode bulk action: %v", err)
		}
		metadata := action["index"]
		if metadata["_index"] != openSearchIndex || metadata["version_type"] != "external_gte" {
			t.Errorf("unexpected bulk metadata: %#v", metadata)
		}
		if !strings.Contains(lines[1], "external-id") {
			t.Errorf("bulk document does not contain external ID: %s", lines[1])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":false,"items":[{"index":{"status":201}},{"index":{"status":201}}]}`))
	}))
	defer server.Close()

	client, err := NewOpenSearchClient(server.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	docs := []searchDocument{
		makeSearchDocument(Entity{ID: uuid.NewString(), Kind: "work", Version: 1, Title: "A", ExternalIDs: map[string]string{"source": "external-id"}}),
		makeSearchDocument(Entity{ID: uuid.NewString(), Kind: "work", Version: 1, Title: "B"}),
	}
	if err := client.writeDocuments(context.Background(), docs, nil, true); err != nil {
		t.Fatalf("writeDocuments returned error: %v", err)
	}
}

func TestWriteDocumentsMixedDeletesAndAcknowledgements(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		wantError      bool
	}{
		{"delete existing", `{"items":[{"index":{"status":201}},{"delete":{"status":200,"result":"deleted"}}]}`, false},
		{"delete absent", `{"errors":true,"items":[{"index":{"status":201}},{"delete":{"status":404,"result":"not_found"}}]}`, false},
		{"stale index version", `{"errors":true,"items":[{"index":{"status":409,"error":{"type":"version_conflict_engine_exception"}}},{"delete":{"status":404,"result":"not_found"}}]}`, false},
		{"missing index", `{"items":[{"index":{"status":201}},{"delete":{"status":404,"error":{"type":"index_not_found_exception"}}}]}`, true},
		{"delete absent with error", `{"items":[{"index":{"status":201}},{"delete":{"status":404,"result":"not_found","error":{"type":"index_not_found_exception"}}}]}`, true},
		{"unexplained delete 404", `{"items":[{"index":{"status":201}},{"delete":{"status":404}}]}`, true},
		{"delete conflict", `{"items":[{"index":{"status":201}},{"delete":{"status":409,"error":{"type":"version_conflict_engine_exception"}}}]}`, true},
		{"delete failed", `{"items":[{"index":{"status":201}},{"delete":{"status":500}}]}`, true},
		{"index failed", `{"items":[{"index":{"status":500}},{"delete":{"status":200}}]}`, true},
		{"unexplained index conflict", `{"items":[{"index":{"status":409}},{"delete":{"status":200}}]}`, true},
		{"wrong conflict type", `{"items":[{"index":{"status":409,"error":{"type":"other_error"}}},{"delete":{"status":200}}]}`, true},
		{"incomplete acknowledgement", `{"items":[{"index":{"status":201}}]}`, true},
		{"wrong action", `{"items":[{"index":{"status":201}},{"index":{"status":200}}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			liveID, deletedID := uuid.NewString(), uuid.NewString()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/_bulk" || r.URL.Query().Get("refresh") != "wait_for" {
					t.Errorf("unexpected bulk request: %s %s", r.Method, r.URL)
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read bulk: %v", err)
					return
				}
				lines := strings.Split(strings.TrimSpace(string(body)), "\n")
				if len(lines) != 3 {
					t.Errorf("expected index action/source and one delete action: %s", body)
					return
				}
				var action map[string]map[string]any
				if err := json.Unmarshal([]byte(lines[2]), &action); err != nil {
					t.Errorf("decode delete action: %v", err)
				}
				metadata := action["delete"]
				if len(action) != 1 || len(metadata) != 2 || metadata["_id"] != deletedID || metadata["_index"] != openSearchIndex {
					t.Errorf("delete must use current absence, without a historical version/source: %#v", action)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			client, err := NewOpenSearchClient(server.URL, "", "")
			if err != nil {
				t.Fatal(err)
			}
			doc := makeSearchDocument(Entity{ID: liveID, Kind: "work", Version: 7, Title: "Current"})
			err = client.writeDocuments(context.Background(), []searchDocument{doc}, []string{deletedID}, true)
			if (err != nil) != tc.wantError {
				t.Fatalf("writeDocuments error=%v, wantError=%v", err, tc.wantError)
			}
		})
	}
}

func TestWriteDocumentsDeleteOnlyAndRebuildRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("refresh") != "false" {
			t.Errorf("rebuild bulk should not wait for a per-batch refresh: %s", r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Count(string(body), "\n") != 1 || !strings.Contains(string(body), `"delete"`) {
			t.Errorf("delete-only bulk must contain one action line: %s", body)
		}
		_, _ = w.Write([]byte(`{"items":[{"delete":{"status":404,"result":"not_found"}}]}`))
	}))
	defer server.Close()
	client, err := NewOpenSearchClient(server.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.writeDocuments(context.Background(), nil, []string{uuid.NewString()}, false); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshIndexRequiresSuccessfulShards(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		status         int
		wantError      bool
	}{
		{"success", `{"_shards":{"total":1,"successful":1,"failed":0}}`, 200, false},
		{"failed shard", `{"_shards":{"total":1,"successful":0,"failed":1}}`, 200, true},
		{"missing shards", `{}`, 200, true},
		{"invalid response", `{`, 200, true},
		{"unavailable", `{}`, 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/"+openSearchIndex+"/_refresh" {
					t.Errorf("unexpected refresh request: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()
			client, err := NewOpenSearchClient(server.URL, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if err := client.refreshIndex(context.Background()); (err != nil) != tc.wantError {
				t.Fatalf("refreshIndex error=%v, wantError=%v", err, tc.wantError)
			}
		})
	}
}

func TestPostgresBrowseRejectsTextSearch(t *testing.T) {
	args := []any{}
	_, err := listFilter(context.Background(), &Store{}, ListOptions{Query: "bangumi:42"}, nil, &args)
	if err == nil || err.Error() != "text_query_requires_search" {
		t.Fatalf("text search must use OpenSearch: %v", err)
	}
}

func TestMakeSearchDocumentTruncatesByRunes(t *testing.T) {
	for _, tc := range []struct {
		name, unit string
		count      int
	}{
		{"ascii below limit", "a", 4095},
		{"ascii at limit", "a", 4096},
		{"ascii over limit", "a", 4097},
		{"Japanese bytes above limit", "あ", 2000},
		{"Japanese at limit", "あ", 4096},
		{"Japanese over limit", "あ", 4097},
		{"emoji bytes above limit", "🎵", 1100},
		{"emoji over limit", "🎵", 4097},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := strings.Repeat(tc.unit, tc.count)
			doc := makeSearchDocument(Entity{Title: "  " + value + "  "})
			count := tc.count
			if count > 4096 {
				count = 4096
			}
			want := strings.Repeat(tc.unit, count)
			if len(doc.TitleText) != 1 || doc.TitleText[0] != want {
				t.Fatalf("unexpected title truncation")
			}
			if len(doc.SearchText) != 1 || doc.SearchText[0] != want {
				t.Fatalf("unexpected analyzed text truncation")
			}
			// Exact-text chunks must still cover the full source, including its tail.
			if len(doc.SearchTextExact) == 0 || !strings.HasSuffix(value, doc.SearchTextExact[len(doc.SearchTextExact)-1]) {
				t.Fatalf("missing exact text tail")
			}
		})
	}
}
