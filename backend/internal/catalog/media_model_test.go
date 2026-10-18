package catalog

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestDeclarativeRelationConstraints(t *testing.T) {
	d := Defaults()
	ref := func(string, []string) error { return nil }
	a := Entity{ID: "a", Kind: "expression", WorkID: "w"}
	b := Entity{ID: "b", Kind: "expression", WorkID: "w"}
	c := Entity{ID: "c", Kind: "expression", WorkID: "other"}
	r := Relation{ID: "new", Type: "expression_part", SourceID: "a", TargetID: "b"}
	if err := validateRelation(d, r, a, b, nil, ref, false); err != nil {
		t.Fatal(err)
	}
	if err := validateRelation(d, r, a, c, nil, ref, false); err == nil || err.Error() != "relation_scope_mismatch" {
		t.Fatalf("cross Work: %v", err)
	}
	prior := []Relation{{ID: "old", Type: "expression_part", SourceID: "a", TargetID: "c", Position: 0}}
	if err := validateRelation(d, r, a, b, prior, ref, false); err == nil || err.Error() != "duplicate_relation_position" {
		t.Fatalf("duplicate order: %v", err)
	}
	d.Relations["part_alternate"] = d.Relations["expression_part"]
	prior = []Relation{{ID: "old", Type: "part_alternate", SourceID: "b", TargetID: "a", Position: 1}}
	if err := validateRelation(d, r, a, b, prior, ref, false); err == nil || err.Error() != "relation_cycle" {
		t.Fatalf("cross-code composition cycle: %v", err)
	}
	for _, code := range []string{"sequel_of", "adaptation_of"} {
		def := d.Relations[code]
		def.CycleGroup = "creative_chain"
		d.Relations[code] = def
	}
	wa, wb, wc := Entity{ID: "wa", Kind: "work"}, Entity{ID: "wb", Kind: "work"}, Entity{ID: "wc", Kind: "work"}
	prior = []Relation{{ID: "one", Type: "sequel_of", SourceID: wa.ID, TargetID: wb.ID}, {ID: "two", Type: "adaptation_of", SourceID: wb.ID, TargetID: wc.ID}}
	if err := validateRelation(d, Relation{ID: "three", Type: "sequel_of", SourceID: wc.ID, TargetID: wa.ID}, wc, wa, prior, ref, false); err == nil || err.Error() != "relation_cycle" {
		t.Fatalf("cross-code creative cycle: %v", err)
	}
	d.Fields["credit_context"] = Field{Names: names("署名语境", "Credit context"), Type: "entity", Kinds: []string{"work", "content_unit", "expression"}, Enabled: true}
	rt := d.Relations["performed_by"]
	rt.Fields = append(rt.Fields, "credit_context")
	rt.ReferenceScopes = map[string]string{"credit_context": "source_work"}
	d.Relations["performed_by"] = rt
	r = Relation{ID: "credit", Type: "performed_by", SourceID: wa.ID, TargetID: "agent", Attributes: map[string]any{"credit_context": "b"}}
	lookup := func(id string) (Entity, error) { return Entity{ID: id, Kind: "expression", WorkID: wb.ID}, nil }
	if err := validateRelation(d, r, wa, Entity{Kind: "agent"}, nil, ref, false, lookup); err == nil || !strings.HasPrefix(err.Error(), "relation_reference_scope_mismatch") {
		t.Fatalf("wrong credit context: %v", err)
	}
	rt = d.Relations["performed_by"]
	rt.ReferenceScopes["credit_context"] = "source_unknown"
	d.Relations["performed_by"] = rt
	if err := d.Validate(); err == nil || !strings.HasPrefix(err.Error(), "invalid_reference_scope") {
		t.Fatalf("unknown scope accepted: %v", err)
	}
}

func TestMediaProjectionsAndRuleReplay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Novel"})
	other := f.save(Entity{Kind: "work", Title: "Other novel"})
	makeExpression := func(title string) Entity { return f.save(Entity{Kind: "expression", Title: title, WorkID: work.ID}) }
	whole := makeExpression("Complete translation")
	first, second := makeExpression("Chapter one translation"), makeExpression("Chapter two translation")
	config, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config.Document.Relations["part_alternate"] = config.Document.Relations["expression_part"]
	config.Document.Relations["edition_alternate"] = config.Document.Relations["edition_of"]
	f.publish(config.Document, config.ETag)
	saveRelation := func(typ, source, target string, pos int) Relation {
		t.Helper()
		out, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: typ, SourceID: source, TargetID: target, Position: pos}, EditNote: "model fixture", Sources: fixtureSources()}, f.u)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	saveRelation("expression_part", whole.ID, second.ID, 2)
	saveRelation("part_alternate", whole.ID, first.ID, 1)
	got, err := f.s.ExpressionComposition(ctx, whole.ID, nil)
	if err != nil || len(got.Parts) != 2 || got.Parts[0].Entity.ID != first.ID || got.DefinitionETag == "" {
		t.Fatalf("ordered composition: %+v %v", got, err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "part_alternate", SourceID: first.ID, TargetID: whole.ID}, EditNote: "cycle", Sources: fixtureSources()}, f.u); err == nil || err.Error() != "relation_cycle" {
		t.Fatalf("Save cycle bypass: %v", err)
	}
	cross := f.save(Entity{Kind: "expression", Title: "Other work text", WorkID: other.ID})
	if _, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "expression_part", SourceID: whole.ID, TargetID: cross.ID, Position: 3}, EditNote: "cross Work", Sources: fixtureSources()}, f.u); err == nil || err.Error() != "relation_scope_mismatch" {
		t.Fatalf("Save scope bypass: %v", err)
	}
	// Impact replays ordering across codes, and failed configuration is atomic.
	config, _ = f.s.Definitions(ctx)
	def := config.Document.Relations["part_alternate"]
	def.MaxOutgoing = 1
	config.Document.Relations["part_alternate"] = def
	impact, err := f.s.DefinitionImpactFor(ctx, config.Document, f.u)
	if err != nil || len(impact.Issues) == 0 {
		t.Fatalf("rule replay did not reject incompatible cardinality: %+v %v", impact, err)
	}
	before := config.ETag
	if _, err := f.s.SaveDefinitions(ctx, config.Document, before, f.u, "incompatible", fixtureSources()); err == nil {
		t.Fatal("incompatible definitions saved")
	}
	after, _ := f.s.Definitions(ctx)
	if after.ETag != before {
		t.Fatal("failed replay changed live definitions")
	}
	group := f.save(Entity{Kind: "collection", Title: "Novel first publication"})
	makeRelease := func(title string) Entity {
		return f.save(Entity{Kind: "release", Title: title, Subjects: []Subject{{WorkID: work.ID, Role: "primary"}}})
	}
	regular, limited, related := makeRelease("Regular"), makeRelease("Limited"), makeRelease("Anthology")
	saveRelation("edition_of", regular.ID, group.ID, 0)
	saveRelation("edition_alternate", limited.ID, group.ID, 1)
	editions, err := f.s.ReleaseEditions(ctx, regular.ID, nil)
	if err != nil || editions.Group == nil || len(editions.Editions) != 2 {
		t.Fatalf("explicit editions: %+v %v", editions, err)
	}
	ungrouped, err := f.s.ReleaseEditions(ctx, related.ID, nil)
	if err != nil || ungrouped.Group != nil || len(ungrouped.Editions) != 0 {
		t.Fatalf("shared subjects implied editions: %+v %v", ungrouped, err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "edition_alternate", SourceID: regular.ID, TargetID: work.ID}, EditNote: "second group", Sources: fixtureSources()}, f.u); err == nil || err.Error() != "cardinality_exceeded" {
		t.Fatalf("cross-code edition cardinality: %v", err)
	}
	// Hiding one chapter/edition never reveals its ID through these projections.
	if _, err := f.s.Lifecycle(ctx, first.ID, LifecycleEdit{ExpectedVersion: first.Version, EditNote: "hide", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	got, err = f.s.ExpressionComposition(ctx, whole.ID, nil)
	if err != nil || len(got.Parts) != 1 || got.Parts[0].Entity.ID != second.ID {
		t.Fatalf("hidden part leaked: %+v %v", got, err)
	}
	if _, err := f.s.Lifecycle(ctx, limited.ID, LifecycleEdit{ExpectedVersion: limited.Version, EditNote: "hide", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	editions, err = f.s.ReleaseEditions(ctx, regular.ID, nil)
	if err != nil || len(editions.Editions) != 1 {
		t.Fatalf("hidden edition leaked: %+v %v", editions, err)
	}
}

func TestSingleInclusionEditsPreserveHiddenRecordsAndEvidence(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Song"})
	first := f.save(Entity{Kind: "expression", Title: "Master", WorkID: work.ID})
	hidden := f.save(Entity{Kind: "expression", Title: "Hidden master", WorkID: work.ID})
	release := f.save(Entity{Kind: "release", Title: "CD", Subjects: []Subject{{WorkID: work.ID, Role: "primary"}}})
	medium := f.save(Entity{Kind: "medium", Title: "Disc", ReleaseID: release.ID})
	hiddenSources := []Source{{Kind: "url", Citation: "Existing hidden inclusion", URL: "https://example.org/hidden-master"}}
	track := f.save(Entity{Kind: "track", Title: "Track", MediumID: medium.ID, Contents: []Inclusion{{ExpressionID: first.ID, Position: 0, Locator: Locator{}}, {ExpressionID: hidden.ID, Position: 1, Locator: Locator{}, Sources: hiddenSources}}})
	if _, err := f.s.Lifecycle(ctx, hidden.ID, LifecycleEdit{ExpectedVersion: hidden.Version, EditNote: "hide", Sources: fixtureSources()}, f.u); err != nil {
		t.Fatal(err)
	}
	editor := fixtureUser("editor")
	in := TrackContentEdit{ExpectedVersion: track.Version, Inclusion: &Inclusion{ExpressionID: first.ID, Position: 2, Locator: Locator{"relative_to": "track", "time_start_ms": float64(1)}}, EditNote: "official track mapping", Sources: []Source{{Kind: "url", Citation: "Official track list", URL: "https://example.org/track-list"}}}
	updated, err := f.s.EditTrackContent(ctx, track.ID, "replace", 0, in, editor)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != track.Version+1 || len(updated.Contents) != 1 || len(updated.Contents[0].Sources) != 0 {
		t.Fatalf("updated response: %+v", updated)
	}
	all, err := get(ctx, f.s.DB, track.ID)
	keptHidden := false
	for _, inclusion := range all.Contents {
		if inclusion.ExpressionID == hidden.ID {
			keptHidden = reflect.DeepEqual(inclusion.Sources, hiddenSources)
		}
	}
	if err != nil || len(all.Contents) != 2 || !keptHidden {
		t.Fatalf("untouched hidden inclusion or its evidence was lost: %+v %v", all, err)
	}
	if _, err = f.s.EditTrackContent(ctx, track.ID, "replace", 0, in, editor); !errors.Is(err, errVersionConflict) {
		t.Fatalf("stale edit accepted: %v", err)
	}
	if _, err = f.s.EditTrackContent(ctx, track.ID, "delete", 1, TrackContentEdit{ExpectedVersion: updated.Version, EditNote: "guess hidden", Sources: fixtureSources()}, editor); !errors.Is(err, errForbidden) {
		t.Fatalf("hidden inclusion edited: %v", err)
	}
	// Per-inclusion sources are optional and independent from the edit sources.
	explicitSources := []Source{{Kind: "url", Citation: "Explicit inclusion source", URL: "https://example.org/explicit-inclusion"}}
	explicit := *in.Inclusion
	explicit.Sources = explicitSources
	updated, err = f.s.EditTrackContent(ctx, track.ID, "replace", 2, TrackContentEdit{ExpectedVersion: updated.Version, Inclusion: &explicit, EditNote: "Explicit optional evidence"}, editor)
	if err != nil || len(updated.Contents) != 1 || !reflect.DeepEqual(updated.Contents[0].Sources, explicitSources) {
		t.Fatalf("explicit inclusion sources were not retained: %+v %v", updated, err)
	}
	// A whole-object edit may also explicitly clear sources without inheriting
	// the unrelated edit-level source supplied by the fixture.
	edited := f.save(Entity{Kind: "track", Title: "Whole-entity edit", MediumID: medium.ID, Contents: []Inclusion{updated.Contents[0]}})
	if !reflect.DeepEqual(edited.Contents[0].Sources, explicitSources) {
		t.Fatal("explicit sources changed during whole-object save")
	}
	edited.Contents[0].Sources = []Source{}
	edited.Title = "Retitled track"
	again := f.save(edited)
	if len(again.Contents[0].Sources) != 0 {
		t.Fatal("empty inclusion sources must not inherit edit-level evidence")
	}
	// Two editors reading one version cannot both overwrite the record.
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.s.EditTrackContent(ctx, track.ID, "delete", 2, TrackContentEdit{ExpectedVersion: updated.Version, EditNote: "concurrent removal", Sources: fixtureSources()}, editor)
			outcomes <- err
		}()
	}
	wg.Wait()
	close(outcomes)
	success, conflict := 0, 0
	for err := range outcomes {
		if err == nil {
			success++
		} else if errors.Is(err, errVersionConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrency: success=%d conflict=%d", success, conflict)
	}
}

func TestMergeReplaysCrossCodeCycleRules(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	config, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"sequel_of", "adaptation_of"} {
		rule := config.Document.Relations[code]
		rule.CycleGroup = "creative_chain"
		config.Document.Relations[code] = rule
	}
	f.publish(config.Document, config.ETag)
	a, b, c := f.save(Entity{Kind: "work", Title: "A"}), f.save(Entity{Kind: "work", Title: "B"}), f.save(Entity{Kind: "work", Title: "C"})
	for _, r := range []Relation{{Type: "sequel_of", SourceID: a.ID, TargetID: b.ID}, {Type: "adaptation_of", SourceID: b.ID, TargetID: c.ID}} {
		if _, err := f.s.SaveRelation(ctx, RelationEdit{Relation: r, EditNote: "chain", Sources: fixtureSources()}, f.u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.s.Lifecycle(ctx, c.ID, LifecycleEdit{ExpectedVersion: c.Version, TargetID: a.ID, EditNote: "merge loop", Sources: fixtureSources()}, f.u); err == nil || !strings.Contains(err.Error(), "relation_cycle") {
		t.Fatalf("merge bypassed cycle group: %v", err)
	}
	current, err := f.s.Get(ctx, c.ID, &f.u)
	if err != nil || current.State != "active" || current.Version != c.Version {
		t.Fatal("failed merge changed source")
	}
	relations, err := f.s.Relations(ctx, b.ID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	original := false
	for _, r := range relations {
		if r.Type == "adaptation_of" && r.TargetID == c.ID {
			original = true
		}
	}
	if !original {
		t.Fatal("failed merge rewrote target")
	}
}
