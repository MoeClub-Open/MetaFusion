package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
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
	if !slices.Contains(a.MatchedFields, "external_id") || !slices.Contains(a.MatchedFields, "title_exact") ||
		!slices.Contains(a.MatchedFields, "ctx_creator") || !slices.Contains(a.MatchedFields, "ctx_catalog_number") {
		t.Fatalf("reasons: %+v", a.MatchedFields)
	}

	conflictQ := IdentityCandidateQuery{Kind: "work", Titles: []string{"star song"}, Context: &IdentityContext{
		Creator: []string{"33333333-3333-3333-3333-333333333333"},
	}}
	a = assessMatch(conflictQ, base, peers)
	if a.RecommendedAction != ActionDistinct || !slices.Contains(a.Conflicts, "ctx_creator_mismatch") {
		t.Fatalf("distinct: %+v", a)
	}

	a = assessMatch(IdentityCandidateQuery{Kind: "work", Titles: []string{"star song"}}, base, creditPeers{})
	if a.RecommendedAction != ActionReview || !slices.Contains(a.MatchedFields, "title_exact") {
		t.Fatalf("review: %+v", a)
	}

	a = assessMatch(IdentityCandidateQuery{Kind: "work", Titles: []string{"star theme"}}, base, creditPeers{})
	if !slices.Contains(a.MatchedFields, "alias") || slices.Contains(a.MatchedFields, "title_exact") {
		t.Fatalf("alias: %+v", a)
	}

	nonofficialCand := IdentityCandidateMatch{Matched: IdentityCandidateEntity{
		ID: "b", Kind: "agent", Title: "Sakura",
		Translations: map[string]Translation{
			"zh-CN": {Title: "小樱", Nonofficial: true, NonofficialAliases: []string{"樱酱"}},
		},
		Attributes: map[string]any{"agent_type": "character"},
	}}
	a = assessMatch(IdentityCandidateQuery{Kind: "agent", Titles: []string{"小樱"}}, nonofficialCand, creditPeers{})
	if !slices.Contains(a.MatchedFields, "alias") || slices.Contains(a.MatchedFields, "title_exact") {
		t.Fatalf("nonofficial title should match as alias: %+v", a)
	}
	a = assessMatch(IdentityCandidateQuery{Kind: "agent", Titles: []string{"樱酱"}}, nonofficialCand, creditPeers{})
	if !slices.Contains(a.MatchedFields, "alias") {
		t.Fatalf("nonofficial_aliases should match as alias: %+v", a)
	}
	a = assessMatch(IdentityCandidateQuery{
		Kind:       "agent",
		Titles:     []string{"Sakura"},
		Attributes: []IdentityAttributeCriterion{{Key: "agent_type", Value: "person"}},
	}, nonofficialCand, creditPeers{})
	if a.RecommendedAction != ActionDistinct || !slices.Contains(a.Conflicts, "agent_type_mismatch") || a.ContextSummary["agent_type"] != "character" {
		t.Fatalf("agent_type conflict expected distinct: %+v", a)
	}
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

func TestIdentityCoreTitleNormalization(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"株式会社京都アニメーション", "京都アニメーション"},
		{"京都アニメーション株式会社", "京都アニメーション"},
		{"Kyoto Animation Co., Ltd.", "kyotoanimation"},
		{"Poppin'Party", "poppinparty"},
		{"Poppin’ Party", "poppinparty"},
		{"Ｐｏｐｐｉｎ＇Ｐａｒｔｙ", "poppinparty"},
		{"BanG Dream! (TVアニメ)", "bangdream"},
		{"BanG Dream! [TV Anime]", "bangdream"},
		{"Dawn [CD]", "dawn"},
		{"BanG Dream! [Blu-ray]", "bangdream"},
		{"ヴァイオレット・エヴァーガーデン", "ヴァイオレットエヴァーガーデン"},
		{"涼宮ハルヒの憂鬱", "涼宮ハルヒの憂鬱"},
		{"佐々木とピーちゃん", "佐々木とピーちゃん"},
		{"!!!", "!!!"},
	}
	for _, tc := range cases {
		if got := identityCoreTitle(tc.in); got != tc.want {
			t.Errorf("identityCoreTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAssessMatchCoreSimilarAndSubtypeConflicts(t *testing.T) {
	// Core title match (punctuation / corporate affix difference) -> title_core, review
	kyoani := IdentityCandidateEntity{
		ID: "01a0f7ae-fc97-7cb7-8c21-c7e93c0bdad4", Kind: "agent", Title: "京都アニメーション",
		Attributes: map[string]any{"agent_type": "organization"},
	}
	aCore := assessMatch(
		IdentityCandidateQuery{Kind: "agent", Titles: []string{"株式会社京都アニメーション"}},
		IdentityCandidateMatch{Matched: kyoani, Canonical: &kyoani},
		creditPeers{},
	)
	if !slices.Contains(aCore.MatchedFields, "title_core") || aCore.RecommendedAction != ActionReview {
		t.Fatalf("expected title_core + review, got %+v", aCore)
	}

	// Approximate match (substring / trigram) -> title_similar, review
	bangS2 := IdentityCandidateEntity{
		ID: "01a0a7dd-0000-7000-8000-000000000002", Kind: "work", Title: "BanG Dream! 2nd Season",
		Attributes: map[string]any{"creation_form": "tv_anime"},
	}
	aSim := assessMatch(
		IdentityCandidateQuery{Kind: "work", Titles: []string{"BanG Dream!"}, IncludeApproximate: true},
		IdentityCandidateMatch{Matched: bangS2, Canonical: &bangS2},
		creditPeers{},
	)
	if !slices.Contains(aSim.MatchedFields, "title_similar") || aSim.RecommendedAction != ActionReview {
		t.Fatalf("expected title_similar + review, got %+v", aSim)
	}

	// Typo trigram match with attribute criterion -> both title_similar and attribute recorded
	poppinAlbum := IdentityCandidateEntity{
		ID: "01a0a8a7-0000-7000-8000-000000000003", Kind: "work", Title: "Poppin'Party",
		Attributes: map[string]any{"creation_form": "original_album"},
	}
	aTypo := assessMatch(
		IdentityCandidateQuery{
			Kind: "work", Titles: []string{"PoppinPatry"}, IncludeApproximate: true,
			Attributes: []IdentityAttributeCriterion{{Key: "creation_form", Value: "original_album"}},
		},
		IdentityCandidateMatch{Matched: poppinAlbum, Canonical: &poppinAlbum},
		creditPeers{},
	)
	if !slices.Contains(aTypo.MatchedFields, "title_similar") || !slices.Contains(aTypo.MatchedFields, "attribute") {
		t.Fatalf("expected title_similar + attribute, got %+v", aTypo)
	}

	// Subtype mismatch (song vs original_album) -> creation_form_mismatch -> distinct
	aMismatch := assessMatch(
		IdentityCandidateQuery{
			Kind: "work", Titles: []string{"Poppin'Party"},
			Attributes: []IdentityAttributeCriterion{{Key: "creation_form", Value: "song"}},
		},
		IdentityCandidateMatch{Matched: poppinAlbum, Canonical: &poppinAlbum},
		creditPeers{},
	)
	if !slices.Contains(aMismatch.Conflicts, "creation_form_mismatch") || aMismatch.RecommendedAction != ActionDistinct {
		t.Fatalf("expected creation_form_mismatch + distinct, got %+v", aMismatch)
	}

	// Collection subtype conflict -> collection_type_mismatch -> distinct
	colSeries := IdentityCandidateEntity{
		ID: "01a0a7dd-0000-7000-8000-000000000009", Kind: "collection", Title: "BanG Dream!",
		Attributes: map[string]any{"collection_type": "franchise"},
	}
	aColMismatch := assessMatch(
		IdentityCandidateQuery{
			Kind: "collection", Titles: []string{"BanG Dream!"},
			Attributes: []IdentityAttributeCriterion{{Key: "collection_type", Value: "series"}},
		},
		IdentityCandidateMatch{Matched: colSeries, Canonical: &colSeries},
		creditPeers{},
	)
	if !slices.Contains(aColMismatch.Conflicts, "collection_type_mismatch") || aColMismatch.RecommendedAction != ActionDistinct {
		t.Fatalf("expected collection_type_mismatch + distinct, got %+v", aColMismatch)
	}
}

func TestPostgresIdentityBatchContextRanksNotFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	artistA := f.save(Entity{Kind: "agent", Title: "Artist A"})
	artistB := f.save(Entity{Kind: "agent", Title: "Artist B"})
	label := f.save(Entity{Kind: "agent", Title: "Label Co"})
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
	if ids[sameA.ID].RecommendedAction != ActionReuse || ids[sameB.ID].RecommendedAction != ActionDistinct {
		t.Fatalf("unexpected actions: %+v", ids)
	}
	if batch.Results[0].Items[0].Matched.ID != sameA.ID {
		t.Fatalf("ranking: first=%s assessments=%+v", batch.Results[0].Items[0].Matched.ID, ids)
	}
}

func TestPostgresApproximateRecallAndExactPriority(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	yoake := f.save(Entity{Kind: "work", Title: "夜明け", Attributes: map[string]any{"creation_form": "song"}})
	bangBase := f.save(Entity{Kind: "work", Title: "BanG Dream!", Attributes: map[string]any{"creation_form": "tv_anime"}})
	dawn := f.save(Entity{Kind: "work", Title: "Dawn", Attributes: map[string]any{"creation_form": "song"}})

	// 2-character forward substring recall: "夜明" -> "夜明け"
	res2Char, err := f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{Kind: "work", Titles: []string{"夜明"}, IncludeApproximate: true}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	foundYoake := false
	for _, it := range res2Char.Items {
		if it.Matched.ID == yoake.ID && it.Assessment != nil && slices.Contains(it.Assessment.MatchedFields, "title_similar") {
			foundYoake = true
		}
	}
	if !foundYoake {
		t.Fatalf("expected 2-char approximate query to recall 夜明け: %+v", res2Char)
	}

	// Reverse substring recall: longer query "BanG Dream! 2nd Season" recalls shorter "BanG Dream!"
	resRev, err := f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{Kind: "work", Titles: []string{"BanG Dream! 2nd Season"}, IncludeApproximate: true}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	foundBase := false
	for _, it := range resRev.Items {
		if it.Matched.ID == bangBase.ID {
			foundBase = true
		}
	}
	if !foundBase {
		t.Fatalf("expected reverse substring query to recall BanG Dream!: %+v", resRev)
	}

	// Long (>96 runes) reverse substring recall: 100 'x' + " Dawn" recalls "Dawn"
	longQuery := strings.Repeat("x", 100) + " Dawn"
	resLongRev, err := f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{Kind: "work", Titles: []string{longQuery}, IncludeApproximate: true}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	foundDawn := false
	for _, it := range resLongRev.Items {
		if it.Matched.ID == dawn.ID {
			foundDawn = true
		}
	}
	if !foundDawn {
		t.Fatalf("expected >96-rune reverse substring query to recall Dawn: %+v", resLongRev)
	}

	// Exact match with large UUID must not be pushed out by approximate matches or attribute-only matches with smaller UUIDs under tight limit.
	unrelatedSmallID := "00000000-0000-7000-8000-000000000000"
	unrelatedDoc := Entity{ID: unrelatedSmallID, Kind: "work", Version: 1, Title: "Unrelated Song", State: "active", Attributes: map[string]any{"creation_form": "song"}}
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document) VALUES($1,'work',1,$2,'active',$3,$4)`, unrelatedSmallID, unrelatedDoc.Title, f.u.ID, encode(unrelatedDoc)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		smallID := fmt.Sprintf("00000000-0000-7000-8000-%012d", i+1)
		doc := Entity{ID: smallID, Kind: "work", Version: 1, Title: fmt.Sprintf("Starlight Horizon Part %d", i+1), State: "active", Attributes: map[string]any{"creation_form": "song"}}
		if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document) VALUES($1,'work',1,$2,'active',$3,$4)`, smallID, doc.Title, f.u.ID, encode(doc)); err != nil {
			t.Fatal(err)
		}
	}
	homonymSmallID := "00000000-0000-7000-8000-000000000099"
	homonymDoc := Entity{ID: homonymSmallID, Kind: "work", Version: 1, Title: "Starlight Horizon", State: "active", Attributes: map[string]any{"creation_form": "tv_anime"}}
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document) VALUES($1,'work',1,$2,'active',$3,$4)`, homonymSmallID, homonymDoc.Title, f.u.ID, encode(homonymDoc)); err != nil {
		t.Fatal(err)
	}
	largeExactID := "ffffffff-ffff-7fff-8fff-ffffffffffff"
	exactDoc := Entity{ID: largeExactID, Kind: "work", Version: 1, Title: "Starlight Horizon", State: "active", Attributes: map[string]any{"creation_form": "song"}}
	if _, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document) VALUES($1,'work',1,$2,'active',$3,$4)`, largeExactID, exactDoc.Title, f.u.ID, encode(exactDoc)); err != nil {
		t.Fatal(err)
	}
	lim := 3
	resRanked, err := f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{
		Kind:               "work",
		Titles:             []string{"Starlight Horizon"},
		Attributes:         []IdentityAttributeCriterion{{Key: "creation_form", Value: "song"}},
		IncludeApproximate: true,
		Limit:              &lim,
	}, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if len(resRanked.Items) == 0 || resRanked.Items[0].Matched.ID != largeExactID {
		t.Fatalf("exact match was displaced by approximate hits: %+v", resRanked)
	}
}

func TestMigration045PreservesDistinctAttributesAndRevisions(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	if err := migrator.New(db, catalogMigrationsBefore(t, "000045")).Up(ctx); err != nil {
		t.Fatal(err)
	}
	defs := Defaults()
	delete(defs.Relations, "produced_by")
	// Restrict composed_by to expression-only to verify 000045 respects custom source_kinds.
	compRel := defs.Relations["composed_by"]
	compRel.SourceKinds = []string{"expression"}
	defs.Relations["composed_by"] = compRel
	// Require custom field production_code (even with Enabled=false, matching validation.go:917) on lyricist_of.
	defs.Fields["production_code"] = Field{Names: names4("制作编号", "製作編號", "制作コード", "Production code"), Type: "text", Enabled: false, Required: true}
	lyrRel := defs.Relations["lyricist_of"]
	lyrRel.Fields = append(lyrRel.Fields, "production_code")
	defs.Relations["lyricist_of"] = lyrRel
	// Allow list field tags on credit_for and directed_by to verify JSONB array permutation dedup.
	for _, rcode := range []string{"credit_for", "directed_by"} {
		rd := defs.Relations[rcode]
		rd.Fields = append(rd.Fields, "tags")
		defs.Relations[rcode] = rd
	}
	// Simulate pre-existing partial collection_type vocabulary where event_catalog is explicitly disabled.
	evTerm := defs.Vocabularies["collection_type"].Terms["event_catalog"]
	evTerm.Enabled = false
	defs.Vocabularies["collection_type"].Terms["event_catalog"] = evTerm
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog.definition_config(singleton,document,etag) VALUES(true,$1,'etag-pre-45') ON CONFLICT(singleton) DO UPDATE SET document=EXCLUDED.document`, encode(defs)); err != nil {
		t.Fatal(err)
	}

	actor := uuid.NewString()
	studioID := uuid.NewString()
	composerID := uuid.NewString()
	directorID := uuid.NewString()
	animeWorkID := uuid.NewString()
	songWorkID := uuid.NewString()
	ostWorkID := uuid.NewString()
	mangaSerID := uuid.NewString()
	mangaVolID := uuid.NewString()
	gameWorkID := uuid.NewString()
	mangaColID := uuid.NewString()
	fateMusicColID := uuid.NewString()
	franColID := uuid.NewString()
	disabledColID := uuid.NewString()

	insertEntity := func(id, kind, title string, attrs map[string]any, links []ExternalLink) {
		doc := map[string]any{"id": id, "kind": kind, "version": 1, "title": title, "state": "active", "created_by": actor, "attributes": attrs, "external_links": links, "updated_at": "2025-01-01T00:00:00Z"}
		if _, err := db.ExecContext(ctx, `INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document,updated_at) VALUES($1,$2,1,$3,'active',$4,$5,'2025-01-01T00:00:00Z')`, id, kind, title, actor, encode(doc)); err != nil {
			t.Fatal(err)
		}
	}
	insertEntity(studioID, "agent", "京都アニメーション", map[string]any{"agent_type": "organization"}, nil)
	insertEntity(composerID, "agent", "上松範康", map[string]any{"agent_type": "person"}, nil)
	insertEntity(directorID, "agent", "石原立也", map[string]any{"agent_type": "person"}, nil)
	insertEntity(animeWorkID, "work", "響け！ユーフォニアム", map[string]any{"creation_form": "tv_anime"}, nil)
	insertEntity(songWorkID, "work", "Live Beyond!!", map[string]any{"creation_form": "original_album"}, []ExternalLink{{Kind: "recording_mbid", ExternalID: "11111111-2222-3333-4444-555555555555", URL: "https://musicbrainz.org/recording/11111111-2222-3333-4444-555555555555"}})
	insertEntity(ostWorkID, "work", "Fate/stay night Original Soundtrack", map[string]any{"creation_form": "soundtrack"}, nil)
	insertEntity(mangaSerID, "work", "Manga Serial", map[string]any{"creation_form": "manga_serialized"}, nil)
	insertEntity(mangaVolID, "work", "Manga Vol 1", map[string]any{"creation_form": "manga_volume"}, nil)
	insertEntity(gameWorkID, "work", "Franchise Game", map[string]any{"creation_form": "commercial_game"}, nil)
	insertEntity(mangaColID, "collection", "Same-Medium Manga Collection", map[string]any{}, nil)
	insertEntity(fateMusicColID, "collection", "Fate 配乐系列", map[string]any{}, nil)
	insertEntity(franColID, "collection", "Cross-Media Project", map[string]any{}, nil)
	insertEntity(disabledColID, "collection", "コミックマーケット105", map[string]any{}, nil)

	insertRel := func(typ, src, tgt string, attrs map[string]any) string {
		id := uuid.NewString()
		doc := map[string]any{"id": id, "version": 2, "type": typ, "source_id": src, "target_id": tgt, "position": 0, "attributes": attrs}
		if _, err := db.ExecContext(ctx, `INSERT INTO catalog.relations(id,version,type,source_id,target_id,document) VALUES($1,2,$2,$3,$4,$5)`, id, typ, src, tgt, encode(doc)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// 1) credit_for with アニメーション制作 -> produced_by
	relCreditID := insertRel("credit_for", animeWorkID, studioID, map[string]any{"credit_role": "アニメーション制作"})
	// 2) bare created_by {} -> subsumed by relCreditID and deleted at version 3
	relBareCreatedID := insertRel("created_by", animeWorkID, studioID, map[string]any{})
	// 3) created_by with distinct scope -> preserved as produced_by!
	relCtxCreatedID := insertRel("created_by", animeWorkID, studioID, map[string]any{"scope": "ep 1-13", "credit_role": "製作"})
	// 4) Two credit_for edges with mutually <@ equivalent permuted JSONB arrays -> deterministically keep one, never delete both!
	relPermA := insertRel("credit_for", animeWorkID, directorID, map[string]any{"credit_role": "監督", "tags": []string{"a", "b"}})
	relPermB := insertRel("credit_for", animeWorkID, directorID, map[string]any{"credit_role": "監督", "tags": []string{"b", "a"}})
	// 5) Custom composed_by restricted to expression -> work credit_for with 作曲 must stay credit_for.
	relCustomBlockedID := insertRel("credit_for", songWorkID, composerID, map[string]any{"credit_role": "作曲"})
	// 6) Custom lyricist_of requires production_code -> work credit_for without it must stay credit_for.
	relReqBlockedID := insertRel("credit_for", songWorkID, composerID, map[string]any{"credit_role": "作詞"})
	insertRel("includes", mangaColID, mangaSerID, map[string]any{})
	insertRel("includes", mangaColID, mangaVolID, map[string]any{})
	insertRel("includes", fateMusicColID, songWorkID, map[string]any{})
	insertRel("includes", fateMusicColID, ostWorkID, map[string]any{})
	insertRel("includes", franColID, animeWorkID, map[string]any{})
	insertRel("includes", franColID, gameWorkID, map[string]any{})

	if err := migrator.New(db, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}

	// Both relCreditID and relCtxCreatedID survive as produced_by; relBareCreatedID is deleted with revision & outbox version=3.
	var surviving int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM catalog.relations WHERE id IN ($1,$2) AND type='produced_by'`, relCreditID, relCtxCreatedID).Scan(&surviving); err != nil || surviving != 2 {
		t.Fatalf("expected both distinct produced_by edges to survive, got %d (%v)", surviving, err)
	}
	var permSurviving int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM catalog.relations WHERE id IN ($1,$2) AND type='directed_by'`, relPermA, relPermB).Scan(&permSurviving); err != nil || permSurviving != 1 {
		t.Fatalf("expected exactly 1 permuted-array directed_by edge to survive, got %d (%v)", permSurviving, err)
	}
	var customBlockedType string
	if err := db.QueryRowContext(ctx, `SELECT type FROM catalog.relations WHERE id=$1`, relCustomBlockedID).Scan(&customBlockedType); err != nil || customBlockedType != "credit_for" {
		t.Fatalf("expected custom source_kinds restriction to keep credit_for, got %q (%v)", customBlockedType, err)
	}
	var reqBlockedType string
	if err := db.QueryRowContext(ctx, `SELECT type FROM catalog.relations WHERE id=$1`, relReqBlockedID).Scan(&reqBlockedType); err != nil || reqBlockedType != "credit_for" {
		t.Fatalf("expected missing required field on target relation to keep credit_for, got %q (%v)", reqBlockedType, err)
	}
	var delOutboxVer, delRevVer int64
	if err := db.QueryRowContext(ctx, `SELECT version FROM catalog.outbox WHERE entity_id=$1 AND type='relation.deleted'`, relBareCreatedID).Scan(&delOutboxVer); err != nil || delOutboxVer != 3 {
		t.Fatalf("expected deleted relation outbox version=3, got %d (%v)", delOutboxVer, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT r.version FROM catalog.revisions r JOIN catalog.revision_commits rc ON rc.revision_id=r.id WHERE r.target_id=$1`, relBareCreatedID).Scan(&delRevVer); err != nil || delRevVer != 3 {
		t.Fatalf("expected deleted relation revision+revision_commit version=3, got %d (%v)", delRevVer, err)
	}

	// Song creation_form migrated + document.updated_at updated + revision recorded.
	var songForm, docUpdated string
	if err := db.QueryRowContext(ctx, `SELECT document #>> '{attributes,creation_form}', document->>'updated_at' FROM catalog.entities WHERE id=$1`, songWorkID).Scan(&songForm, &docUpdated); err != nil || songForm != "song" || docUpdated == "2025-01-01T00:00:00Z" || docUpdated == "" {
		t.Fatalf("song work migration mismatch: form=%q updated_at=%q err=%v", songForm, docUpdated, err)
	}

	// Same-medium manga_serialized + manga_volume stays series; anime + game becomes franchise.
	var mangaColType, fateMusicColType, franColType string
	if err := db.QueryRowContext(ctx, `SELECT document #>> '{attributes,collection_type}' FROM catalog.entities WHERE id=$1`, mangaColID).Scan(&mangaColType); err != nil || mangaColType != "series" {
		t.Fatalf("expected same-medium manga collection to be series, got %q (%v)", mangaColType, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT document #>> '{attributes,collection_type}' FROM catalog.entities WHERE id=$1`, fateMusicColID).Scan(&fateMusicColType); err != nil || fateMusicColType != "series" {
		t.Fatalf("expected single-domain Fate soundtrack collection to be series, got %q (%v)", fateMusicColType, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT document #>> '{attributes,collection_type}' FROM catalog.entities WHERE id=$1`, franColID).Scan(&franColType); err != nil || franColType != "franchise" {
		t.Fatalf("expected cross-media collection to be franchise, got %q (%v)", franColType, err)
	}
	var disabledColType string
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(document #>> '{attributes,collection_type}', '') FROM catalog.entities WHERE id=$1`, disabledColID).Scan(&disabledColType); err != nil || disabledColType != "" {
		t.Fatalf("expected disabled event_catalog term not to be written, got %q (%v)", disabledColType, err)
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
	f.save(Entity{Kind: "medium", Title: "Bonus BD", ReleaseID: rel.ID, Position: 3, Attributes: map[string]any{"format": "bd"}})

	got, err := f.s.Get(ctx, rel.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.MediumFormats["cd"] != 2 || got.MediumFormats["bd"] != 1 {
		t.Fatalf("Get medium_formats mismatch: %+v", got.MediumFormats)
	}
	listed, err := f.s.List(ctx, ListOptions{Kind: "release", WorkID: work.ID}, nil)
	if err != nil || len(listed) != 1 || listed[0].MediumFormats["cd"] != 2 || listed[0].MediumFormats["bd"] != 1 {
		t.Fatalf("List medium_formats mismatch: %+v %v", listed, err)
	}
	var storedHasMediumFormats bool
	if err := f.s.DB.QueryRowContext(ctx, `SELECT document ? 'medium_formats' FROM catalog.entities WHERE id=$1`, rel.ID).Scan(&storedHasMediumFormats); err != nil || storedHasMediumFormats {
		t.Fatalf("medium_formats must not persist in entity document: %v %v", storedHasMediumFormats, err)
	}

	cand, err := f.s.FindIdentityCandidates(ctx, IdentityCandidateQuery{Kind: "release", Titles: []string{"初限盘"}}, nil)
	if err != nil || cand.Total != 1 || cand.Items[0].Matched.ID != rel.ID {
		t.Fatalf("nonofficial_aliases lookup failed: %+v %v", cand, err)
	}
}
