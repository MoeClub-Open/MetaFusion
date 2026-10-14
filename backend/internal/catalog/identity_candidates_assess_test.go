package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAssessMatchActions(t *testing.T) {
	base := IdentityCandidateMatch{Matched: IdentityCandidateEntity{
		ID: "a", Kind: "work", Title: "Star Song",
		Translations: map[string]Translation{"en": {Title: "Star Song", Aliases: []string{"star theme"}}},
		ExternalLinks: []ExternalLink{linkFromID("wikidata", "Q1")},
		Attributes:    map[string]any{"catalog_number": "TECI-1", "publisher": "label-1"},
	}}
	q := IdentityCandidateQuery{
		Kind: "work", Titles: []string{"star song"},
		ExternalLinks: []IdentityExternalCriterion{{Kind: "wikidata", Value: "Q1"}},
		Context: &IdentityContext{
			Creator:       []string{"11111111-1111-1111-1111-111111111111"},
			Performer:     []string{"22222222-2222-2222-2222-222222222222"},
			Label:         []string{"label-1"},
			CatalogNumber: []string{"TECI-1"},
		},
	}
	peers := creditPeers{
		Creators:   []string{"11111111-1111-1111-1111-111111111111"},
		Performers: []string{"22222222-2222-2222-2222-222222222222"},
	}
	a := assessMatch(q, base, peers)
	if a.RecommendedAction != ActionReuse || a.Confidence < 0.9 {
		t.Fatalf("reuse: %+v", a)
	}
	if !containsString(a.MatchedFields, "external_id") || !containsString(a.MatchedFields, "title_exact") ||
		!containsString(a.MatchedFields, "ctx_creator") || !containsString(a.MatchedFields, "ctx_catalog_number") {
		t.Fatalf("reasons: %+v", a.MatchedFields)
	}

	// Context conflict → distinct even with title match.
	conflictQ := IdentityCandidateQuery{Kind: "work", Titles: []string{"star song"}, Context: &IdentityContext{
		Creator: []string{"33333333-3333-3333-3333-333333333333"},
	}}
	a = assessMatch(conflictQ, base, peers)
	if a.RecommendedAction != ActionDistinct || !containsString(a.Conflicts, "ctx_creator_mismatch") {
		t.Fatalf("distinct: %+v", a)
	}

	// Title only → review.
	a = assessMatch(IdentityCandidateQuery{Kind: "work", Titles: []string{"star song"}}, base, creditPeers{})
	if a.RecommendedAction != ActionReview || !containsString(a.MatchedFields, "title_exact") {
		t.Fatalf("review: %+v", a)
	}

	// Alias hit.
	a = assessMatch(IdentityCandidateQuery{Kind: "work", Titles: []string{"star theme"}}, base, creditPeers{})
	if !containsString(a.MatchedFields, "alias") || containsString(a.MatchedFields, "title_exact") {
		t.Fatalf("alias: %+v", a)
	}
}

func containsString(in []string, want string) bool {
	for _, v := range in {
		if v == want {
			return true
		}
	}
	return false
}

func TestIdentityBatchHTTPValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)
	for _, payload := range []string{
		`{}`, `{"queries":[]}`, `{"queries":[{"key":"a","kind":"work","titles":["x"]},{"key":"a","kind":"work","titles":["y"]}]}`,
		`{"queries":[{"key":"","kind":"work","titles":["x"]}]}`,
		`{"queries":[{"key":"a","kind":"work","titles":["x"],"context":{"creator":["not-a-uuid"]}}]}`,
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/catalog/entities/candidates/batch", strings.NewReader(payload))
		req.RemoteAddr = "198.51.100.50:1234"
		r.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", payload, w.Code, w.Body.String())
		}
	}
}

func TestPostgresIdentityBatchContextRanksNotFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	artistA := f.save(Entity{Kind: "agent", Title: "Artist A"})
	artistB := f.save(Entity{Kind: "agent", Title: "Artist B"})
	label := f.save(Entity{Kind: "agent", Title: "Label Co"})
	// catalog_number / publisher apply to release, not work.
	sameA := f.save(Entity{Kind: "release", Title: "Shared Title", Attributes: map[string]any{"publisher": label.ID, "catalog_number": "CAT-1"}})
	sameB := f.save(Entity{Kind: "release", Title: "Shared Title", Attributes: map[string]any{"catalog_number": "CAT-OTHER"}})
	if _, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "created_by", SourceID: sameA.ID, TargetID: artistA.ID, Attributes: map[string]any{}}, EditNote: "ctx", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "created_by", SourceID: sameB.ID, TargetID: artistB.ID, Attributes: map[string]any{}}, EditNote: "ctx", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "performed_by", SourceID: sameA.ID, TargetID: artistA.ID, Attributes: map[string]any{}}, EditNote: "ctx", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}

	batch, err := f.s.FindIdentityCandidatesBatch(ctx, IdentityBatchRequest{Queries: []IdentityBatchQuery{{
		Key: "q1",
		IdentityCandidateQuery: IdentityCandidateQuery{
			Kind: "release", Titles: []string{"shared title"},
			Context: &IdentityContext{
				Creator:       []string{artistA.ID},
				Performer:     []string{artistA.ID},
				Label:         []string{label.ID},
				CatalogNumber: []string{"CAT-1"},
			},
		},
	}}}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Results) != 1 || batch.Results[0].Error != "" || batch.Results[0].Total < 2 {
		t.Fatalf("context must not shrink candidates: %+v", batch)
	}
	ids := map[string]CandidateAssessment{}
	for _, item := range batch.Results[0].Items {
		if item.Assessment == nil {
			t.Fatalf("missing assessment: %+v", item)
		}
		ids[item.Matched.ID] = *item.Assessment
	}
	if _, ok := ids[sameA.ID]; !ok {
		t.Fatalf("missing sameA: %+v", ids)
	}
	if _, ok := ids[sameB.ID]; !ok {
		t.Fatalf("context filtered sameB (must not): %+v", ids)
	}
	if ids[sameA.ID].RecommendedAction != ActionReuse {
		t.Fatalf("sameA should reuse: %+v", ids[sameA.ID])
	}
	if ids[sameB.ID].RecommendedAction != ActionDistinct {
		t.Fatalf("sameB should distinct: %+v", ids[sameB.ID])
	}
	// Higher confidence first.
	if batch.Results[0].Items[0].Matched.ID != sameA.ID {
		t.Fatalf("ranking: first=%s assessments=%+v", batch.Results[0].Items[0].Matched.ID, ids)
	}

	// Per-query error does not fail the batch.
	batch, err = f.s.FindIdentityCandidatesBatch(ctx, IdentityBatchRequest{Queries: []IdentityBatchQuery{
		{Key: "ok", IdentityCandidateQuery: IdentityCandidateQuery{Kind: "release", Titles: []string{"shared title"}}},
		{Key: "bad", IdentityCandidateQuery: IdentityCandidateQuery{Kind: "nope", Titles: []string{"x"}}},
	}}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Results[0].Error != "" || batch.Results[1].Error != "invalid_kind" {
		t.Fatalf("per-query errors: %+v", batch)
	}
}

func TestIdentityCandidateResponseIncludesAssessmentJSON(t *testing.T) {
	a := CandidateAssessment{MatchedFields: []string{"title_exact"}, Confidence: 0.3, RecommendedAction: ActionReview}
	item := IdentityCandidateMatch{Matched: IdentityCandidateEntity{ID: "1", Kind: "work", Title: "t"}, Assessment: &a}
	b, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"recommended_action":"review"`) || !strings.Contains(string(b), `"matched_fields"`) {
		t.Fatal(string(b))
	}
}
