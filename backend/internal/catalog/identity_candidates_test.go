package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPostgresIdentityCandidateStructuralScopes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w1 := f.save(Entity{Kind: "work", Title: "one"})
	w2 := f.save(Entity{Kind: "work", Title: "two"})
	u1 := f.save(Entity{Kind: "content_unit", Title: "chapter", WorkID: w1.ID})
	f.save(Entity{Kind: "content_unit", Title: "chapter", WorkID: w2.ID})
	r1 := f.save(Entity{Kind: "release", Title: "release one"})
	r2 := f.save(Entity{Kind: "release", Title: "release two"})
	m1 := f.save(Entity{Kind: "medium", Title: "CD", ReleaseID: r1.ID, Attributes: map[string]any{"format": "cd"}})
	m2 := f.save(Entity{Kind: "medium", Title: "CD", ReleaseID: r2.ID, Attributes: map[string]any{"format": "cd"}})
	t1 := f.save(Entity{Kind: "track", Title: "Track 1", MediumID: m1.ID})
	f.save(Entity{Kind: "track", Title: "Track 1", MediumID: m2.ID})
	for _, tc := range []struct {
		q  IdentityCandidateQuery
		id string
	}{{IdentityCandidateQuery{Kind: "content_unit", Titles: []string{"chapter"}, WorkID: w1.ID}, u1.ID}, {IdentityCandidateQuery{Kind: "medium", Titles: []string{"CD"}, ReleaseID: r1.ID}, m1.ID}, {IdentityCandidateQuery{Kind: "track", Titles: []string{"Track 1"}, MediumID: m1.ID}, t1.ID}} {
		out, err := f.s.FindIdentityCandidates(ctx, tc.q, &f.u)
		if err != nil || !out.Complete || out.Total != 1 || len(out.Items) != 1 || out.Items[0].Matched.ID != tc.id {
			t.Fatalf("scope: %+v %v", out, err)
		}
	}
}

func TestIdentityCandidateStrictInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)
	for _, payload := range []string{`null`, `{}`, `{"kind":"work"}`, `{"kind":"other","titles":["x"]}`,
		`{"kind":"work","titles":[""]}`, `{"kind":"work","titles":["x"],"offset":1}`,
		`{"kind":"work","titles":["x"],"limit":0}`, `{"kind":"work","titles":["x"],"limit":1001}`,
		`{"kind":"work","titles":["x"],"work_id":"bad"}`, `{"kind":"work","titles":["x"]} {}`,
		`{"kind":"work","attributes":[{"key":"duration","value":5}]}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/catalog/entities/candidates", strings.NewReader(payload))
		req.RemoteAddr = "198.51.100.49:1234"
		r.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", payload, w.Code, w.Body.String())
		}
	}
}

func TestPostgresIdentityCandidatesMatchAndVisibility(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	var boundedKeys bool
	if err := f.s.DB.QueryRowContext(ctx, `SELECT bool_and(length(term)=32) FROM unnest(catalog.identity_candidate_terms(
		jsonb_build_object('attributes',jsonb_build_object('long_note',repeat('x',20000))))) term`).Scan(&boundedKeys); err != nil || !boundedKeys {
		t.Fatalf("unbounded identity index keys: %v %v", boundedKeys, err)
	}
	for raw, want := range map[string]string{"213.00": "213", "0.0000001": "1e-7", "1e21": "1e+21", "-0": "0"} {
		var got string
		if err := f.s.DB.QueryRowContext(ctx, `SELECT catalog.identity_scalar($1::jsonb)`, raw).Scan(&got); err != nil || got != want {
			t.Fatalf("scalar %s: %s != %s (%v)", raw, got, want, err)
		}
	}
	original := f.save(Entity{Kind: "work", Title: "Original", Translations: map[string]Translation{
		"ja": {Title: "夜明け", Aliases: []string{"  Star\tSONG　"}}}, ExternalLinks: linksFromIDMap(map[string]string{"wikidata": "Q123"}),
		Attributes: map[string]any{"duration": 213}})
	hidden := f.save(Entity{Kind: "work", Title: "Star song", Status: "draft"})
	f.save(Entity{Kind: "agent", Title: "Star song"})
	for _, query := range []IdentityCandidateQuery{
		{Kind: "work", Titles: []string{"star song"}},
		{Kind: "work", ExternalLinks: []IdentityExternalCriterion{{Kind: "wikidata", Value: "Q123"}}},
		{Kind: "work", Attributes: []IdentityAttributeCriterion{{Key: "duration", Value: "213"}}},
	} {
		out, err := f.s.FindIdentityCandidates(ctx, query, nil)
		if err != nil || !out.Complete || out.Total != 1 || out.Items[0].Matched.ID != original.ID || out.Items[0].Canonical.ID != original.ID {
			t.Fatalf("candidate=%+v err=%v", out, err)
		}
	}
	out, err := f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{Kind: "work", Titles: []string{"star song"}, Limit: queryInt(1)}, &f.u)
	if err != nil || out.Complete || out.Total != 2 || len(out.Items) != 1 {
		t.Fatalf("truncation=%+v %v", out, err)
	}
	// Historical aliases find a canonical identity in the same snapshot.
	target := f.save(Entity{Kind: "work", Title: "Current"})
	batch2Merge(t, f, original, target.ID)
	out, err = f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{Kind: "work", Titles: []string{"star song"}}, &f.u)
	if err != nil || out.Total != 2 {
		t.Fatalf("merged lookup=%+v %v", out, err)
	}
	for _, item := range out.Items {
		if item.Matched.ID == original.ID && (item.Canonical == nil || item.Canonical.ID != target.ID) {
			t.Fatalf("alias=%+v", item)
		}
		if item.Matched.ID != original.ID && item.Matched.ID != hidden.ID {
			t.Fatalf("wrong kind: %+v", item)
		}
	}
	// The expression scope is authoritative, even when titles match.
	w1, w2 := f.save(Entity{Kind: "work", Title: "Parent 1"}), f.save(Entity{Kind: "work", Title: "Parent 2"})
	e1 := f.save(Entity{Kind: "expression", Title: "Scope song", WorkID: w1.ID})
	f.save(Entity{Kind: "expression", Title: "Scope song", WorkID: w2.ID})
	out, err = f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{Kind: "expression", Titles: []string{"scope song"}, WorkID: w1.ID}, nil)
	if err != nil || out.Total != 1 || out.Items[0].Matched.ID != e1.ID {
		t.Fatalf("scope=%+v %v", out, err)
	}
}

func TestPostgresIdentityCandidatesSnapshotAndConcurrentWriters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := f.save(Entity{Kind: "work", Title: "Snapshot target"})
	in, docs, limit, err := normalizeIdentityCandidateQuery(IdentityCandidateQuery{Kind: "work", Titles: []string{"snapshot target"}})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	first, err := identityCandidatesFrom(ctx, tx, in, docs, limit, &f.u)
	if err != nil || first.Total != 1 {
		t.Fatalf("first=%+v %v", first, err)
	}
	changed := before
	changed.Title = "Changed after snapshot"
	changed.Translations = map[string]Translation{"en": {Title: changed.Title}}
	f.save(changed)
	f.save(Entity{Kind: "work", Title: "Snapshot target"})
	second, err := identityCandidatesFrom(ctx, tx, in, docs, limit, &f.u)
	if err != nil || second.Total != 1 || second.Items[0].Canonical.Title != "Snapshot target" || second.Items[0].Canonical.ID != before.ID {
		t.Fatalf("snapshot moved: %+v %v", second, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for turn := 0; turn < 8; turn++ {
				_, err := f.s.Save(ctx, Edit{Entity: Entity{Kind: "work", Title: fmt.Sprintf("Unrelated %d %d", worker, turn), Status: "draft"}, EditNote: "concurrent fixture", Sources: fixtureSources()}, f.u)
				if err != nil {
					failures <- err
					return
				}
				out, err := f.s.FindIdentityCandidates(ctx, in, &f.u)
				if err != nil || !out.Complete || out.Total != 1 {
					failures <- fmt.Errorf("lookup=%+v err=%v", out, err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	// The query uses the synchronous GIN index rather than a kind-wide scan.
	explainTx, err := f.s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer explainTx.Rollback()
	if _, err = explainTx.ExecContext(ctx, "SET LOCAL enable_seqscan=off"); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(docs)
	rows, err := explainTx.QueryContext(ctx, `EXPLAIN SELECT id FROM catalog.entities WHERE status<>'deleted'
        AND catalog.identity_candidate_terms(document) && ARRAY(SELECT DISTINCT unnest(catalog.identity_candidate_terms(value)) FROM jsonb_array_elements($1::jsonb))`, string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	plan := ""
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !strings.Contains(plan, "entities_identity_candidates_idx") {
		t.Fatalf("index unused: %s", plan)
	}
}

func TestPostgresIdentityCandidatesFailureDoesNotLookEmpty(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{Kind: "work", Titles: []string{"x"}}, &f.u); err == nil || out.Complete {
		t.Fatalf("cancelled read=%+v %v", out, err)
	}
	if _, err := f.s.DB.Exec(`DROP INDEX catalog.entities_identity_candidates_idx`); err != nil {
		t.Fatal(err)
	}
	if err := f.s.CheckCompatibleVersion(context.Background()); err == nil || !strings.Contains(err.Error(), "identity candidate") {
		t.Fatalf("missing index: %v", err)
	}
}
