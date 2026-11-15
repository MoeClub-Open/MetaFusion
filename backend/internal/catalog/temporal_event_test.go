package catalog

import (
	"context"
	"testing"
)

func TestTemporalEventDefinitionsAndValidation(t *testing.T) {
	d := Defaults()
	if err := d.Validate(); err != nil {
		t.Fatalf("Defaults().Validate() failed: %v", err)
	}
	if _, leaked := d.Vocabularies["creation_form"].Terms["event_profile"]; leaked {
		t.Fatal("event_profile leaked into creation_form vocabulary")
	}
	if _, leaked := d.Vocabularies["creation_form"].Terms["event_concert"]; leaked {
		t.Fatal("event_concert leaked into creation_form vocabulary")
	}
	for _, code := range []string{"subsidiary_of", "succeeded_by", "recorded_at", "released_at"} {
		rel, ok := d.Relations[code]
		if !ok || !rel.Enabled || !rel.Acyclic {
			t.Fatalf("missing or misconfigured relation %s: %+v", code, rel)
		}
	}
	if !contains(d.Relations["succeeded_by"].Fields, "transition_type") {
		t.Fatalf("succeeded_by missing transition_type field: %v", d.Relations["succeeded_by"].Fields)
	}

	ref := func(string, []string) error { return nil }
	// Group date RangeStart on former_names and activity_periods.
	if err := d.value(d.Fields["former_names"], []any{
		map[string]any{"name": "旧社名", "begin_date": "2020-05", "end_date": "2019-12"},
	}, ref, false); err == nil {
		t.Fatal("inverted former_names date range must be rejected")
	}
	if err := d.value(d.Fields["former_names"], []any{
		map[string]any{"name": "旧社名", "begin_date": "2019", "end_date": "2019-12", "ended": true},
	}, ref, false); err != nil {
		t.Fatalf("valid partial-date former_names range rejected: %v", err)
	}

	// Flat date pairs on entity/relation attributes (enforced on writes, skipped on historical reads).
	eventKeys := d.attributeKeys("event")
	if err := d.attributes(eventKeys, map[string]any{
		"event_type": "concert", "begin_date": "2026-08-10", "end_date": "2026-08-09",
	}, ref, false); err == nil {
		t.Fatal("inverted event begin_date/end_date must fail on write")
	}
	if err := d.attributes(eventKeys, map[string]any{
		"event_type": "concert", "begin_date": "2026-08-10", "end_date": "2026-08-09",
	}, ref, true); err != nil {
		t.Fatalf("historical read must not fail on legacy inverted dates: %v", err)
	}

	// Yearless birth_date must remain compatible with death_date.
	agentKeys := d.attributeKeys("agent")
	if err := d.attributes(agentKeys, map[string]any{
		"agent_type": "person", "birth_date": "--07-14", "death_date": "2020-01-01",
	}, ref, false); err != nil {
		t.Fatalf("yearless birth_date with death_date rejected: %v", err)
	}
}

func TestTemporalRelationshipFilterSemantics(t *testing.T) {
	if _, _, _, err := normalizeRelationshipQuery(RelationshipQueryRequest{
		IDs: []string{"0195f2c0-0000-7000-8000-000000000001"}, AsOf: "2020/01/01",
	}); err == nil || err.Error() != "invalid_date" {
		t.Fatalf("expected invalid_date for malformed as_of, got %v", err)
	}

	formerMember := map[string]any{"begin_date": "2015-04", "end_date": "2020-03", "ended": true}
	if matchesTemporalFilter(formerMember, relationshipFilters{ActiveOnly: true}) {
		t.Fatal("ended relation must be excluded by active_only")
	}
	if !matchesTemporalFilter(formerMember, relationshipFilters{ActiveOnly: true, AsOf: "2018"}) {
		t.Fatal("relation with known 2015..2020 span must match as_of=2018 even with active_only=true")
	}
	if matchesTemporalFilter(formerMember, relationshipFilters{AsOf: "2021"}) {
		t.Fatal("relation ended in 2020-03 must not match as_of=2021")
	}
	if matchesTemporalFilter(formerMember, relationshipFilters{AsOf: "2014"}) {
		t.Fatal("relation started in 2015-04 must not match as_of=2014")
	}
	unknownEnd := map[string]any{"begin_date": "2015", "ended": true}
	if matchesTemporalFilter(unknownEnd, relationshipFilters{ActiveOnly: true, AsOf: "2018"}) {
		t.Fatal("ended relation without end_date cannot be asserted active")
	}
}

func TestFormerNamesSearchAndCandidateAssessment(t *testing.T) {
	e := Entity{
		ID:    "0195f2c0-0000-7000-8000-000000000010",
		Kind:  "agent",
		State: "active",
		Title: "株式会社ブシロードミュージック",
		Attributes: map[string]any{
			"agent_type": "organization",
			"former_names": []any{
				map[string]any{"name": "株式会社響ミュージック", "ended": true},
			},
		},
	}
	doc := makeSearchDocument(e)
	if !contains(doc.TitleText, "株式会社響ミュージック") || !contains(doc.SearchText, "株式会社響ミュージック") {
		t.Fatalf("former_names missing from search document: titles=%v", doc.TitleText)
	}
	cand := IdentityCandidateEntity{
		ID:         e.ID,
		Kind:       e.Kind,
		State:      e.State,
		Title:      e.Title,
		Attributes: e.Attributes,
	}
	assessment := assessMatch(
		IdentityCandidateQuery{Kind: "agent", Titles: []string{"響ミュージック"}, IncludeApproximate: true},
		IdentityCandidateMatch{Matched: cand},
		creditPeers{},
	)
	if !contains(assessment.MatchedFields, "title_core") {
		t.Fatalf("expected former_names core title match, got %+v", assessment)
	}
}

func TestMergeSeedDefinitionsTemporalAndEventUpgradeIsIdempotent(t *testing.T) {
	seed := Defaults()
	legacy := Defaults()
	for _, f := range []string{"ended", "date_precision", "earliest_date", "latest_date", "time_zone", "transition_type", "former_names", "event_type", "venue", "location", "cancelled"} {
		delete(legacy.Fields, f)
	}
	for _, v := range []string{"date_precision", "transition_type", "embedded_event_kind", "event_type"} {
		delete(legacy.Vocabularies, v)
	}
	for _, r := range []string{"subsidiary_of", "succeeded_by", "recorded_at", "released_at"} {
		delete(legacy.Relations, r)
	}
	ctxField := legacy.Fields["context"]
	ctxField.Kinds = []string{"work", "content_unit", "expression", "release"}
	legacy.Fields["context"] = ctxField
	perf := legacy.Relations["performed_by"]
	perf.SourceKinds = []string{"work", "content_unit", "expression", "release"}
	perf.Fields = []string{"role", "credit_role", "begin_date", "end_date", "scope"}
	legacy.Relations["performed_by"] = perf

	merged, added := mergeSeedDefinitions(legacy, seed)
	if len(added) == 0 {
		t.Fatal("expected merge additions for pre-046 definitions")
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("merged definitions invalid: %v", err)
	}
	if !contains(merged.Fields["context"].Kinds, "event") {
		t.Fatalf("context.Kinds missing event: %v", merged.Fields["context"].Kinds)
	}
	if !contains(merged.Relations["performed_by"].SourceKinds, "event") || !contains(merged.Relations["performed_by"].Fields, "ended") {
		t.Fatalf("performed_by not upgraded: %+v", merged.Relations["performed_by"])
	}
	if _, again := mergeSeedDefinitions(merged, seed); len(again) != 0 {
		t.Fatalf("second merge must be idempotent, got %v", again)
	}
}

func TestPostgresEventEntityAndTemporalLinks(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	band := f.save(Entity{
		Kind: "agent", Title: "Poppin'Party", State: "active",
		Attributes: map[string]any{
			"agent_type": "group",
			"former_names": []any{
				map[string]any{"name": "Poppin'Party (初期名义)", "begin_date": "2015-02", "end_date": "2015-04", "ended": true},
			},
		},
	})
	live := f.save(Entity{
		Kind: "event", Title: "BanG Dream! First☆LIVE Sprin'PARTY 2016!", State: "active",
		Attributes: map[string]any{
			"event_type": "concert", "venue": "品川ステラボール", "location": "東京",
			"begin_date": "2016-04-24", "end_date": "2016-04-24", "ended": true,
		},
		ExternalLinks: []ExternalLink{{
			Kind: "musicbrainz_event", URL: "https://musicbrainz.org/event/11111111-2222-3333-4444-555555555555",
			ExternalID: "11111111-2222-3333-4444-555555555555",
		}},
	})
	song := f.save(Entity{
		Kind: "work", Title: "Yes! BanG_Dream!", State: "active",
		Attributes: map[string]any{"creation_form": "song"},
	})
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{SourceID: live.ID, TargetID: band.ID, Type: "performed_by", Attributes: map[string]any{"begin_date": "2016-04-24", "end_date": "2016-04-24", "ended": true}},
		EditNote: "演出者",
	}, f.u); err != nil {
		t.Fatalf("save event performed_by: %v", err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{SourceID: song.ID, TargetID: live.ID, Type: "recorded_at"},
		EditNote: "现场演出曲目",
	}, f.u); err != nil {
		t.Fatalf("save recorded_at: %v", err)
	}

	activeOnly := true
	resActive, err := f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{live.ID}, ActiveOnly: &activeOnly}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resActive.Pages) != 1 || len(resActive.Pages[0].Items) != 1 || resActive.Pages[0].Items[0].RuleCode != "relation:recorded_at" {
		t.Fatalf("active_only should keep timeless recorded_at and drop ended performed_by, got %+v", resActive.Pages)
	}
	resAsOf, err := f.s.QueryRelationships(ctx, RelationshipQueryRequest{IDs: []string{live.ID}, AsOf: "2016-04-24"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resAsOf.Pages[0].Items) != 2 {
		t.Fatalf("as_of=2016-04-24 should include both edges, got %+v", resAsOf.Pages[0].Items)
	}
}
