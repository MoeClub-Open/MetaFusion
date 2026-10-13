package catalog

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestPostgresSourcePolicyRejectsTopLevelSelfBeforeWrites(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := countEntities(t, f, "")
	for _, source := range []Source{
		{Kind: "self", Citation: "self assertion"},
		{Kind: "url", Citation: "internal item", URL: "https://sub.FINDVERSE.CC.:8443/catalog/item"},
		{Kind: "publication", Citation: "internal publication", URL: "https://findverse.cc/catalog/item"},
	} {
		_, err := f.s.Save(ctx, Edit{Entity: Entity{Kind: "work", Title: "Untrusted new work"}, EditNote: "create", Sources: []Source{source}}, f.u)
		if err == nil || err.Error() != "source_not_eligible" {
			t.Fatalf("unqualified source accepted: %v", err)
		}
	}
	if after := countEntities(t, f, ""); after != before {
		t.Fatalf("source rejection wrote entities: %d -> %d", before, after)
	}
}

func TestPostgresImporterUnqualifiedEvidenceKeepsZeroWrites(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := countEntities(t, f, "")
	for _, tc := range []struct {
		urls []string
		want string
	}{
		{nil, "evidence_required"},
		{[]string{"javascript:alert(1)"}, "evidence_required"},
		{[]string{"https://findverse.cc/catalog/item"}, "source_not_eligible"},
	} {
		req := importerValuePayload()
		req.SourceURLs = tc.urls
		_, err := f.s.Import(ctx, req, f.u)
		if err == nil || err.Error() != tc.want {
			t.Fatalf("unqualified importer evidence = %v, want %s", err, tc.want)
		}
		if after := countEntities(t, f, ""); after != before {
			t.Fatalf("source rejection caused partial importer writes: %d -> %d", before, after)
		}
	}
}

func TestPostgresSourcePolicyPreservesHistoricalPictures(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Legacy picture work", Pictures: PicturesJSON{{
		URL: "https://images.example/cover.jpg", Source: fixtureSources()[0],
	}}})
	// Model an already-stored historical source. This isolated fixture update
	// does not create a new assertion through the production write API.
	legacy := Source{Kind: "self", Citation: "historical picture assertion"}
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.entities SET document=jsonb_set(document,'{pictures,0,source}',$2::jsonb) WHERE id=$1`, work.ID, encode(legacy)); err != nil {
		t.Fatal(err)
	}
	old, err := f.s.Get(ctx, work.ID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	old.Title += " revised"
	saved, err := f.s.Save(ctx, Edit{Entity: old, ExpectedVersion: old.Version, EditNote: "title correction", Sources: fixtureSources()}, f.u)
	if err != nil || !reflect.DeepEqual(saved.Pictures[0].Source, legacy) {
		t.Fatalf("unchanged legacy picture must survive unrelated edit: %v %+v", err, saved.Pictures)
	}
	saved.Pictures[0].Caption = Names{"en-US": "new factual caption"}
	if _, err := f.s.Save(ctx, Edit{Entity: saved, ExpectedVersion: saved.Version, EditNote: "new caption", Sources: fixtureSources()}, f.u); err == nil || !strings.Contains(err.Error(), "source_not_eligible") {
		t.Fatalf("modified legacy picture with self source accepted: %v", err)
	}
}

func TestPostgresSourcePolicyPreservesHistoricalInclusionsAndStatus(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	track, _ := newStatusTrack(t, f, f.u, 1)
	legacy := []Source{{Kind: "self", Citation: "historical inclusion assertion"}}
	if _, err := f.s.DB.ExecContext(ctx, `UPDATE catalog.track_contents SET sources=$2::jsonb WHERE track_id=$1`, track.ID, encode(legacy)); err != nil {
		t.Fatal(err)
	}
	old, err := f.s.Get(ctx, track.ID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	old.Title += " revised"
	saved, err := f.s.Save(ctx, Edit{Entity: old, ExpectedVersion: old.Version, EditNote: "title correction", Sources: fixtureSources()}, f.u)
	if err != nil || len(saved.Contents[0].Sources) != 1 || !reflect.DeepEqual(saved.Contents[0].Sources[0], legacy[0]) {
		t.Fatalf("unchanged legacy inclusion must survive unrelated edit: %v %+v", err, saved.Contents)
	}
	saved, err = f.s.EditTrackStatus(ctx, saved.ID, statusInput(saved, "pending_review"), f.u)
	if err != nil || !reflect.DeepEqual(saved.Contents[0].Sources[0], legacy[0]) {
		t.Fatalf("status-only edit must preserve legacy evidence: %v %+v", err, saved.Contents)
	}
	saved.Contents[0].Locator["chapter"] = "changed factual locator"
	if _, err := f.s.Save(ctx, Edit{Entity: saved, ExpectedVersion: saved.Version, EditNote: "new locator", Sources: fixtureSources()}, f.u); err == nil || !strings.Contains(err.Error(), "source_not_eligible") {
		t.Fatalf("changed inclusion reusing self source accepted: %v", err)
	}
	config, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Document.validateEntity(old, allowAllRef, true, "cd"); err != nil {
		t.Fatalf("historical definitions validation must retain self evidence: %v", err)
	}
}
