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
		Translations:  map[string]Translation{"en": {Title: "Star Song", Aliases: []string{"star theme"}}},
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

	// Nonofficial translation title and nonofficial_aliases score as alias, not title_exact.
	nonofficialCand := IdentityCandidateMatch{Matched: IdentityCandidateEntity{
		ID: "b", Kind: "agent", Title: "Sakura",
		Translations: map[string]Translation{
			"zh-CN": {Title: "小樱", Nonofficial: true, NonofficialAliases: []string{"樱酱"}},
		},
		Attributes: map[string]any{"agent_type": "character"},
	}}
	a = assessMatch(IdentityCandidateQuery{Kind: "agent", Titles: []string{"小樱"}}, nonofficialCand, creditPeers{})
	if !containsString(a.MatchedFields, "alias") || containsString(a.MatchedFields, "title_exact") {
		t.Fatalf("nonofficial title should match as alias: %+v", a)
	}
	a = assessMatch(IdentityCandidateQuery{Kind: "agent", Titles: []string{"樱酱"}}, nonofficialCand, creditPeers{})
	if !containsString(a.MatchedFields, "alias") {
		t.Fatalf("nonofficial_aliases should match as alias: %+v", a)
	}
	// Mismatched agent_type surfaces agent_type_mismatch and recommends distinct.
	a = assessMatch(IdentityCandidateQuery{
		Kind:       "agent",
		Titles:     []string{"Sakura"},
		Attributes: []IdentityAttributeCriterion{{Key: "agent_type", Value: "person"}},
	}, nonofficialCand, creditPeers{})
	if a.RecommendedAction != ActionDistinct || !containsString(a.Conflicts, "agent_type_mismatch") || a.ContextSummary["agent_type"] != "character" {
		t.Fatalf("agent_type conflict expected distinct: %+v", a)
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

func TestPostgresReleaseMediumFormatsAndNonofficialCandidates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Album Work", Attributes: map[string]any{"creation_form": "original_album"}})
	rel := f.save(Entity{
		Kind:     "release",
		Title:    "First Press",
		Subjects: []Subject{{WorkID: work.ID, Role: "primary", Position: 1}},
		Translations: map[string]Translation{
			"zh-CN": {Title: "初回限定盘", Nonofficial: true, NonofficialAliases: []string{"初限盘"}},
		},
	})
	f.save(Entity{Kind: "medium", Title: "Disc 1", ReleaseID: rel.ID, Position: 1, Attributes: map[string]any{"format": "cd"}})
	f.save(Entity{Kind: "medium", Title: "Disc 2", ReleaseID: rel.ID, Position: 2, Attributes: map[string]any{"format": "cd"}})
	f.save(Entity{Kind: "medium", Title: "Bonus BD", ReleaseID: rel.ID, Position: 3, Attributes: map[string]any{"format": "blu_ray"}})

	got, err := f.s.Get(ctx, rel.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediumFormats["cd"] != 2 || got.MediumFormats["blu_ray"] != 1 {
		t.Fatalf("Get medium_formats mismatch: %+v", got.MediumFormats)
	}
	listed, err := f.s.List(ctx, ListOptions{Kind: "release", WorkID: work.ID}, nil)
	if err != nil || len(listed) != 1 || listed[0].MediumFormats["cd"] != 2 || listed[0].MediumFormats["blu_ray"] != 1 {
		t.Fatalf("List medium_formats mismatch: %+v %v", listed, err)
	}
	var storedHasMediumFormats bool
	if err := f.s.DB.QueryRowContext(ctx, `SELECT document ? 'medium_formats' FROM catalog.entities WHERE id=$1`, rel.ID).Scan(&storedHasMediumFormats); err != nil || storedHasMediumFormats {
		t.Fatalf("medium_formats must not persist in entity document: %v %v", storedHasMediumFormats, err)
	}

	// Nonofficial alias is indexed in PostgreSQL identity_candidate_terms.
	cand, err := f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{Kind: "release", Titles: []string{"初限盘"}}, nil)
	if err != nil || cand.Total != 1 || cand.Items[0].Matched.ID != rel.ID {
		t.Fatalf("nonofficial_aliases lookup failed: %+v %v", cand, err)
	}
}
