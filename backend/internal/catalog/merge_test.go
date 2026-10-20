package catalog

import (
	"context"
	"testing"
)

func TestPostgresMergeReferences(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	field := v.Document.Fields["subject_attributes"]
	// Field.Fields 带 omitempty：defaults 里的空子 map 经 DB JSON 往返后为 nil。
	if field.Fields == nil {
		field.Fields = map[string]Field{}
	}
	field.Fields["credit_note"] = Field{Names: names("署名说明", "Credit note"), Type: "text", Enabled: true}
	v.Document.Fields["subject_attributes"] = field
	f.publish(v.Document, v.ETag)
	old := f.save(Entity{Kind: "work", Title: "Duplicate song"})
	target := f.save(Entity{Kind: "work", Title: "Song"})
	unit := f.save(Entity{Kind: "content_unit", Title: "Part", WorkID: old.ID})
	expression := f.save(Entity{Kind: "expression", Title: "Recording", WorkID: old.ID, ContentUnitID: unit.ID})
	release := f.save(Entity{Kind: "release", Title: "Single", Subjects: []Subject{{WorkID: old.ID, Role: "primary", Attributes: map[string]any{"credit_note": "Original sleeve credit"}}}})
	medium := f.save(Entity{Kind: "medium", Title: "CD", ReleaseID: release.ID})
	track := f.save(Entity{Kind: "track", Title: "01", MediumID: medium.ID, Contents: []Inclusion{{ExpressionID: expression.ID}}})
	actor := f.save(Entity{Kind: "agent", Title: "Singer"})
	_, err = f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "performed_by", SourceID: expression.ID, TargetID: actor.ID}, EditNote: "credit", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	merge := func(a, b Entity) {
		t.Helper()
		_, err := f.s.Lifecycle(ctx, a.ID, LifecycleEdit{ExpectedVersion: a.Version, TargetID: b.ID, EditNote: "merge duplicate identity", Sources: fixtureSources()}, f.u)
		if err != nil {
			t.Fatal(err)
		}
	}
	merge(old, target)
	got, err := f.s.Get(ctx, expression.ID, nil)
	if err != nil || got.WorkID != target.ID || got.Version != 2 {
		t.Fatalf("expression not rehomed: %+v %v", got, err)
	}
	occurrences, err := f.s.Occurrences(ctx, target.ID, nil)
	if err != nil || len(occurrences) != 1 {
		t.Fatalf("merged work occurrences: %d %v", len(occurrences), err)
	}
	readRelease, err := f.s.Get(ctx, release.ID, nil)
	if err != nil || len(readRelease.Subjects) != 1 || readRelease.Subjects[0].Attributes["credit_note"] != "Original sleeve credit" {
		t.Fatalf("merge lost subject attributes: %+v %v", readRelease.Subjects, err)
	}
	batchedRelease := occurrences[0]["release"].(Entity)
	if len(batchedRelease.Subjects) != 1 || batchedRelease.Subjects[0].Attributes["credit_note"] != "Original sleeve credit" {
		t.Fatalf("batched read lost subject attributes: %+v", batchedRelease.Subjects)
	}
	next := f.save(Entity{Kind: "expression", Title: "Same recording", WorkID: target.ID, ContentUnitID: unit.ID})
	merge(got, next)
	gotTrack, _ := f.s.Get(ctx, track.ID, nil)
	if gotTrack.Contents[0].ExpressionID != next.ID {
		t.Fatal("inclusion did not move")
	}
	rels, err := f.s.Relations(ctx, next.ID, nil)
	if err != nil || len(rels) != 1 {
		t.Fatalf("credit did not move: %v", err)
	}
	resolved, err := f.s.Resolve(ctx, old.ID, nil)
	if err != nil || resolved.ID != target.ID {
		t.Fatal("old identity is not resolvable")
	}
	revisions, err := f.s.Revisions(ctx, track.ID, RevisionQuery{}, &f.u)
	if err != nil || len(revisions.Items) != 2 {
		t.Fatal("missing reference revision")
	}
}

func TestPostgresPublicReferences(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	draft := f.retire(f.save(Entity{Kind: "work", Title: "Retired identity"}))
	_, err := f.s.Save(ctx, Edit{Entity: Entity{Kind: "expression", Title: "Public recording", State: "active", WorkID: draft.ID}, EditNote: "test disclosure", Sources: fixtureSources()}, f.u)
	if err == nil {
		t.Fatal("published reference disclosed draft")
	}
}
