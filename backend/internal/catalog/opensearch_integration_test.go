package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// This test server must be disposable: each fixture replaces the test index.
func useTestSearch(t *testing.T, s *Store) {
	t.Helper()
	raw := os.Getenv("MF_OPENSEARCH_TEST_URL")
	if raw == "" {
		if os.Getenv("MF_REQUIRE_INTEGRATION_TESTS") == "1" {
			t.Fatal("MF_REQUIRE_INTEGRATION_TESTS=1 requires MF_OPENSEARCH_TEST_URL for isolated OpenSearch integration tests")
		}
		t.Skip("MF_OPENSEARCH_TEST_URL must identify an isolated OpenSearch test server")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		t.Fatal("OpenSearch integration tests require a disposable loopback server")
	}
	c, err := NewOpenSearchClient(raw, "", "")
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := c.request(context.Background(), http.MethodDelete, "/"+openSearchIndex, nil, "")
	if err != nil || (status != http.StatusOK && status != http.StatusNotFound) {
		t.Fatalf("reset isolated test index: status=%d err=%v", status, err)
	}
	s.OpenSearch = c
	if err := s.syncOpenSearch(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenSearchIntegrationPaginationAndVisibility(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.save(Entity{Kind: "work", Title: "検索 Alpha", Attributes: map[string]any{"tags": []string{"search-tag"}}, OriginalLanguage: "ja", Translations: map[string]Translation{"ja": {Title: "検索 Alpha"}, "en-US": {Title: "Alpha"}}})
	f.save(Entity{Kind: "work", Title: "検索 Beta"})
	hidden := f.save(Entity{Kind: "work", Title: "検索 Hidden", Status: "draft"})
	useTestSearch(t, f.s)
	o := ListOptions{Query: "検索", Limit: 1, Sort: "created_at", Order: "asc"}
	page, err := f.s.Search(ctx, o, nil, "")
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].ID != a.ID || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first page: %+v err=%v", page, err)
	}
	second, err := f.s.Search(ctx, o, nil, page.NextCursor)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == a.ID || second.Items[0].ID == hidden.ID || second.HasMore {
		t.Fatalf("second page: %+v err=%v", second, err)
	}
	changed := o
	changed.Query = "Beta"
	if _, err := f.s.Search(ctx, changed, nil, page.NextCursor); err == nil || err.Error() != "invalid_search_cursor" {
		t.Fatalf("cursor changed query: %v", err)
	}
	filtered, err := f.s.Search(ctx, ListOptions{Query: "検索", Limit: 50, Tags: []string{"search-tag"}, OriginalLanguage: "ja", Field: "tags", Value: `["search-tag"]`}, nil, "")
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != a.ID {
		t.Fatalf("combined filters: %+v err=%v", filtered, err)
	}
	// A published result in the PIT is now private in PostgreSQL. No stale
	// status in the index may disclose its content.
	if _, err := f.s.DB.ExecContext(ctx, "UPDATE catalog.entities SET status='draft' WHERE id=$1", a.ID); err != nil {
		t.Fatal(err)
	}
	stale, err := f.s.Search(ctx, o, nil, "")
	if err != nil || len(stale.Items) != 0 || stale.TotalRelation != "index_snapshot" {
		t.Fatalf("stale authorization: %+v err=%v", stale, err)
	}
}

func TestOpenSearchIntegrationStructuralRebuild(t *testing.T) {
	f := newFixture(t)
	work := f.save(Entity{Kind: "work", Title: "検索 Work"})
	cu := f.save(Entity{Kind: "content_unit", Title: "検索 Chapter", WorkID: work.ID})
	expr := f.save(Entity{Kind: "expression", Title: "検索 Text", WorkID: work.ID, ContentUnitID: cu.ID})
	release := f.save(Entity{Kind: "release", Title: "検索 Edition", Subjects: []Subject{{WorkID: work.ID, Role: "primary"}}})
	medium := f.save(Entity{Kind: "medium", Title: "検索 Medium", ReleaseID: release.ID})
	track := f.save(Entity{Kind: "track", Title: "検索 Track", MediumID: medium.ID, Contents: []Inclusion{{ExpressionID: expr.ID, Position: 0}}})
	useTestSearch(t, f.s)
	for _, tc := range []struct {
		o    ListOptions
		want string
	}{
		{ListOptions{Query: "検索", Limit: 50, WorkID: work.ID, Kind: "release"}, release.ID},
		{ListOptions{Query: "検索", Limit: 50, ContentUnitID: cu.ID}, expr.ID},
		{ListOptions{Query: "検索", Limit: 50, ReleaseID: release.ID}, medium.ID},
		{ListOptions{Query: "検索", Limit: 50, MediumID: medium.ID}, track.ID},
	} {
		p, err := f.s.Search(context.Background(), tc.o, nil, "")
		if err != nil || len(p.Items) != 1 || p.Items[0].ID != tc.want {
			t.Fatalf("structural filter %+v: %+v err=%v", tc.o, p, err)
		}
	}
}

func TestOpenSearchIntegrationMusicShelfPagination(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	sh, err := f.s.ResolveShelf(ctx, "music", nil)
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, form := range []string{"song", "single", "original_album"} {
		e := f.save(Entity{Kind: "work", Title: "Music fixture " + form, Attributes: map[string]any{"creation_form": form}})
		allowed[e.ID] = true
	}
	for _, form := range []string{"tv_anime", "manga_serialized", "audio_drama"} {
		f.save(Entity{Kind: "work", Title: "Music fixture " + form, Attributes: map[string]any{"creation_form": form, "tags": []any{"music", "song"}}})
	}
	useTestSearch(t, f.s)
	opts := ListOptions{Query: "Music fixture", Limit: 2, Shelf: &sh.Query}
	page, err := f.s.Search(ctx, opts, nil, "")
	if err != nil || page.Total != 3 || len(page.Items) != 2 || !page.HasMore {
		t.Fatalf("first music page: %+v err=%v", page, err)
	}
	second, err := f.s.Search(ctx, opts, nil, page.NextCursor)
	if err != nil || second.Total != 3 || len(second.Items) != 1 || second.HasMore {
		t.Fatalf("second music page: %+v err=%v", second, err)
	}
	for _, e := range append(page.Items, second.Items...) {
		if !allowed[e.ID] {
			t.Fatalf("wrong form or duplicate search result: %+v", e)
		}
		delete(allowed, e.ID)
	}
	if len(allowed) != 0 {
		t.Fatalf("music results missing: %v", allowed)
	}
}

type openSearchTestTransport func(*http.Request) (*http.Response, error)

func (f openSearchTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestOpenSearchIntegrationPhysicallyDeletedEventReplay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	deleted := f.save(Entity{Kind: "work", Title: "Removed work"})
	live := f.save(Entity{Kind: "work", Title: "Historical title"})
	useTestSearch(t, f.s)
	c := f.s.OpenSearch

	// An old event must also remove a projection newer than that event.
	stale := deleted
	stale.Version = 100
	if err := c.writeDocuments(ctx, []searchDocument{makeSearchDocument(stale)}, nil, true); err != nil {
		t.Fatal(err)
	}
	neverIndexed := f.save(Entity{Kind: "work", Title: "Removed before indexing"})
	live.Title = "Current title"
	live.Translations = map[string]Translation{"en": {Title: live.Title}}
	live = f.save(live)
	if _, err := f.s.DB.ExecContext(ctx, `DELETE FROM catalog.deliveries WHERE consumer=$1 AND event_id IN
		(SELECT id FROM catalog.outbox WHERE entity_id=$2)`, openSearchConsumer, deleted.ID); err != nil {
		t.Fatal(err)
	}
	var err error
	deleted, err = f.s.Unpublish(ctx, deleted.ID, UnpublishEdit{ExpectedVersion: deleted.Version, EditNote: "unpublish fixture", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range []Entity{deleted, neverIndexed} {
		if _, err := f.s.Lifecycle(ctx, entity.ID, LifecycleEdit{ExpectedVersion: entity.Version, EditNote: "delete fixture", Sources: fixtureSources()}, f.u); err != nil {
			t.Fatal(err)
		}
	}
	// A delayed deletion notification cannot override a row that exists now.
	old := Entity{ID: live.ID, Kind: "work", Version: 1, Status: "deleted", Title: "Obsolete deleted payload"}
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.outbox(id,type,entity_id,version,payload) VALUES($1,'entity.deleted',$2,$3,$4)`,
		uuid.NewString(), old.ID, old.Version, encode(old)); err != nil {
		t.Fatal(err)
	}
	// This simulates historical physical removal in the isolated database only.
	if _, err := f.s.DB.ExecContext(ctx, "DELETE FROM catalog.entities WHERE id IN ($1,$2)", deleted.ID, neverIndexed.ID); err != nil {
		t.Fatal(err)
	}
	snapshot := func(table string) string {
		t.Helper()
		var fingerprint string
		if err := f.s.DB.QueryRowContext(ctx, "SELECT md5(string_agg(to_jsonb(r)::text,'' ORDER BY id)) FROM catalog."+table+" r").Scan(&fingerprint); err != nil {
			t.Fatal(err)
		}
		return fingerprint
	}
	outboxBefore, revisionsBefore, entitiesBefore := snapshot("outbox"), snapshot("revisions"), snapshot("entities")
	assertUnchanged := func() {
		t.Helper()
		if snapshot("outbox") != outboxBefore || snapshot("revisions") != revisionsBefore || snapshot("entities") != entitiesBefore {
			t.Fatal("index delivery changed current business rows or historical data")
		}
	}
	assertProjection := func() {
		t.Helper()
		for _, id := range []string{deleted.ID, neverIndexed.ID} {
			status, raw, err := c.request(ctx, http.MethodGet, "/"+openSearchIndex+"/_doc/"+id, nil, "")
			if err != nil || status != http.StatusNotFound {
				t.Fatalf("removed entity remains in OpenSearch: status=%d body=%s err=%v", status, raw, err)
			}
		}
		status, raw, err := c.request(ctx, http.MethodGet, "/"+openSearchIndex+"/_doc/"+live.ID, nil, "")
		var result struct {
			Source searchDocument `json:"_source"`
		}
		if err != nil || status != http.StatusOK || json.Unmarshal(raw, &result) != nil || result.Source.EntityVersion != live.Version || result.Source.SortTitle != live.Title {
			t.Fatalf("existing entity did not use current facts: status=%d body=%s err=%v", status, raw, err)
		}
	}
	pending := func() int {
		t.Helper()
		var count int
		if err := f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.outbox o WHERE NOT EXISTS
			(SELECT 1 FROM catalog.deliveries d WHERE d.consumer=$1 AND d.event_id=o.id)`, openSearchConsumer).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	// Execute the first real bulk, then lose its response: no delivery may be
	// acknowledged, and replay must accept the resulting delete/not_found items.
	loseAcknowledgement := true
	bulkCalls := 0
	c.http.Transport = openSearchTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/_bulk" {
			return http.DefaultTransport.RoundTrip(r)
		}
		bulkCalls++
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		operations := map[string]string{}
		for decoder.More() {
			var line map[string]json.RawMessage
			if err := decoder.Decode(&line); err != nil {
				return nil, err
			}
			for _, action := range []string{"index", "delete"} {
				if raw, ok := line[action]; ok {
					var metadata struct {
						ID string `json:"_id"`
					}
					if err := json.Unmarshal(raw, &metadata); err != nil {
						return nil, err
					}
					if _, duplicate := operations[metadata.ID]; duplicate {
						t.Errorf("same entity appeared twice in one bulk: %s", metadata.ID)
					}
					operations[metadata.ID] = action
				}
			}
		}
		if len(operations) != 3 || operations[live.ID] != "index" || operations[deleted.ID] != "delete" || operations[neverIndexed.ID] != "delete" {
			t.Errorf("mixed history did not produce one current operation per ID: %#v", operations)
		}
		response, err := http.DefaultTransport.RoundTrip(r)
		if err == nil && loseAcknowledgement {
			loseAcknowledgement = false
			_ = response.Body.Close()
			return nil, fmt.Errorf("test lost bulk acknowledgement after execution")
		}
		return response, err
	})
	unacknowledged := pending()
	if err := f.s.deliverOpenSearch(ctx, c); err == nil || pending() != unacknowledged {
		t.Fatalf("failed acknowledgement must preserve the entire pending batch: %v", err)
	}
	assertProjection()
	assertUnchanged()
	if err := f.s.deliverOpenSearch(ctx, c); err != nil || pending() != 0 {
		t.Fatalf("idempotent replay did not acknowledge every event: %v", err)
	}
	assertProjection()
	assertUnchanged()

	// Replay all original saved events as well; their payloads cannot resurrect
	// physically removed rows or replace the live row's current document.
	if _, err := f.s.DB.ExecContext(ctx, "DELETE FROM catalog.deliveries WHERE consumer=$1", openSearchConsumer); err != nil {
		t.Fatal(err)
	}
	if err := f.s.deliverOpenSearch(ctx, c); err != nil || pending() != 0 {
		t.Fatalf("full historical replay failed: %v", err)
	}
	assertProjection()
	assertUnchanged()
	if err := f.s.deliverOpenSearch(ctx, c); err != nil || bulkCalls != 3 {
		t.Fatalf("acknowledged events should not be sent again: calls=%d err=%v", bulkCalls, err)
	}
}

func TestOpenSearchIntegrationDeepCursor(t *testing.T) {
	f := newFixture(t)
	// Populate real PostgreSQL rows and run the production rebuild. This proves
	// the result count can exceed OpenSearch's from/size window without a fallback.
	_, err := f.s.DB.ExecContext(context.Background(), `INSERT INTO catalog.entities(id,kind,version,title,status,created_by,document)
	SELECT gen_random_uuid(),'work',1,'Deep pagination','published',$1,
	jsonb_build_object('kind','work','title','Deep pagination','translations',jsonb_build_object('en',jsonb_build_object('title','Deep pagination')),'attributes','{}'::jsonb)
	FROM generate_series(1,10010)`, f.u.ID)
	if err != nil {
		t.Fatal(err)
	}
	useTestSearch(t, f.s)
	p, err := f.s.Search(context.Background(), ListOptions{Query: "Deep pagination", Sort: "created_at", Limit: 10, Offset: 9990}, nil, "")
	if err != nil || p.Total != 10010 || len(p.Items) != 10 || !p.HasMore {
		t.Fatalf("window boundary: %+v err=%v", p, err)
	}
	next, err := f.s.Search(context.Background(), ListOptions{Query: "Deep pagination", Sort: "created_at", Limit: 10}, nil, p.NextCursor)
	if err != nil || len(next.Items) != 10 || next.HasMore {
		t.Fatalf("past 10k: %+v err=%v", next, err)
	}
	seen := map[string]bool{}
	for _, e := range p.Items {
		seen[e.ID] = true
	}
	for _, e := range next.Items {
		if seen[e.ID] {
			t.Fatal(fmt.Sprintf("duplicate entity across pages: %s", e.ID))
		}
	}
}
