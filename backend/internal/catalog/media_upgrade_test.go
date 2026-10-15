package catalog

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func TestUpgradeFrom18PreservesCatalogAndEditing(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	beforeFS := fstest.MapFS{}
	entries, err := fs.ReadDir(testutil.LegacyMigrations(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "000019_" {
			continue
		}
		data, err := fs.ReadFile(testutil.LegacyMigrations(), entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		beforeFS[entry.Name()] = &fstest.MapFile{Data: data}
	}
	if err = migrator.New(db, beforeFS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db}
	seedMigrationFixture(t, s)
	legacy := Defaults()
	delete(legacy.Fields, "creation_form")
	delete(legacy.Vocabularies, "creation_form")
	delete(legacy.Relations, "expression_part")
	delete(legacy.Relations, "edition_of")
	delete(legacy.Templates, "song")
	for code, template := range legacy.Templates {
		template.Match = nil
		template.Priority = 0
		legacy.Templates[code] = template
	}
	legacy.Fields["custom_note"] = Field{Names: names("人工字段", "Curator field"), Type: "text", ApplicableKinds: []string{"work"}, Enabled: true}
	custom := legacy.Templates["single"]
	custom.Names = names("人工布局", "Curator layout")
	custom.Priority = 24
	legacy.Templates["single"] = custom
	def := legacy.Relations["performed_by"]
	def.Enabled = false
	legacy.Relations["performed_by"] = def
	oldETag := uuid.NewString()
	if _, err = db.ExecContext(ctx, "UPDATE catalog.definition_config SET document=$1,etag=$2 WHERE singleton=true", encode(legacy), oldETag); err != nil {
		t.Fatal(err)
	}
	user := fixtureUser("admin")
	raw := func(kind, title string) Entity {
		t.Helper()
		e := Entity{ID: uuid.NewString(), Kind: kind, Version: 7, Title: title, State: "published", CreatedBy: user.ID, OriginalLanguage: "ja", Translations: map[string]Translation{"ja": {Title: title}}, Attributes: map[string]any{}}
		if _, err = db.ExecContext(ctx, "INSERT INTO catalog.entities(id,kind,version,title,status,created_by,document) VALUES($1,$2,$3,$4,$5,$6,$7)", e.ID, e.Kind, e.Version, e.Title, e.State, e.CreatedBy, encode(e)); err != nil {
			t.Fatal(err)
		}
		return e
	}
	work := raw("work", "Existing album")
	expr := raw("expression", "Existing recording")
	release := raw("release", "Existing limited edition")
	medium := raw("medium", "Existing CD")
	track := raw("track", "Existing track")
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO catalog.expressions(id,work_id) VALUES($1,$2)", []any{expr.ID, work.ID}},
		{"INSERT INTO catalog.release_subjects(release_id,work_id,role,position,attributes) VALUES($1,$2,'primary',0,'{}')", []any{release.ID, work.ID}},
		{"INSERT INTO catalog.mediums(id,release_id) VALUES($1,$2)", []any{medium.ID, release.ID}},
		{"INSERT INTO catalog.tracks(id,medium_id) VALUES($1,$2)", []any{track.ID, medium.ID}},
		{"INSERT INTO catalog.track_contents(track_id,expression_id,position,locator,attributes) VALUES($1,$2,0,'{}','{}')", []any{track.ID, expr.ID}},
		{"INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot) VALUES($1,7,$2,'legacy','legacy revision','[]',$3)", []any{track.ID, user.ID, encode(track)}},
	} {
		if _, err = db.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	var snapshot string
	if err = db.QueryRowContext(ctx, "SELECT (snapshot-'status'-'state'-'review_status')::text FROM catalog.revisions WHERE target_id=$1", track.ID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if err = s.CheckSchemaContract(ctx); err == nil || !strings.Contains(err.Error(), "inclusion sources") {
		t.Fatalf("old binary/schema mismatch not detected: %v", err)
	}
	if err = migrator.New(db, testutil.LegacyMigrations()).Up(ctx); err != nil {
		t.Fatal(err)
	}
	upgrader := migrator.New(db, migrations.FS)
	if err = upgrader.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err = upgrader.Up(ctx); err != nil {
		t.Fatalf("repeat up: %v", err)
	}
	got, err := s.Get(ctx, track.ID, nil)
	if err != nil || got.Version != 7 || len(got.Contents) != 1 || got.Contents[0].ExpressionID != expr.ID || len(got.Contents[0].Sources) != 0 {
		t.Fatalf("existing inclusion changed: %+v %v", got, err)
	}
	for _, e := range []Entity{work, expr, release, medium, track} {
		after, err := s.Get(ctx, e.ID, nil)
		if err != nil || after.ID != e.ID || after.Version != 7 || after.Title != e.Title {
			t.Fatalf("identity/version changed: %+v %v", after, err)
		}
	}
	var afterSnapshot string
	if err = db.QueryRowContext(ctx, "SELECT (snapshot-'status'-'state'-'review_status')::text FROM catalog.revisions WHERE target_id=$1", track.ID).Scan(&afterSnapshot); err != nil || snapshot != afterSnapshot {
		t.Fatal("migration changed revision history")
	}
	if err = s.SeedContent(ctx); err != nil {
		t.Fatal(err)
	}
	configuration, err := s.Definitions(ctx)
	if err != nil || configuration.ETag == oldETag || configuration.Document.Relations["performed_by"].Enabled ||
		configuration.Document.Templates["single"].Priority != 24 || configuration.Document.Templates["single"].Names["en-US"] != "Curator layout" ||
		configuration.Document.Fields["custom_note"].Type != "text" {
		t.Fatalf("curator configuration overwritten: %v", err)
	}
	if configuration.Document.Relations["expression_part"].Usage != "expression_composition" || configuration.Document.Templates["single"].Match == nil {
		t.Fatal("new capabilities not installed")
	}
	etag := configuration.ETag
	if err = s.SeedContent(ctx); err != nil {
		t.Fatal(err)
	}
	configuration, err = s.Definitions(ctx)
	if err != nil || configuration.ETag != etag {
		t.Fatal("repeat seed changed live definitions")
	}
	if err = s.CheckSchemaContract(ctx); err != nil {
		t.Fatal(err)
	}
	updated, err := s.EditTrackContent(ctx, track.ID, "replace", 0, TrackContentEdit{
		Inclusion: &Inclusion{ExpressionID: expr.ID, Position: 1, Locator: Locator{}}, ExpectedVersion: 7, EditNote: "update migrated track", Sources: fixtureSources(),
	}, user)
	if err != nil || updated.Version != 8 || updated.Contents[0].Position != 1 {
		t.Fatalf("migrated catalog not editable: %+v %v", updated, err)
	}
	editions, err := s.ReleaseEditions(ctx, release.ID, nil)
	if err != nil || editions.Group != nil {
		t.Fatal("migration guessed an edition group")
	}
	// Explicit empty selectors and customized blocks survive subsequent seeds.
	empty := []TemplateCondition{}
	blocks := []string{"relations"}
	template := configuration.Document.Templates["single"]
	template.Match = &empty
	template.Blocks = &blocks
	configuration.Document.Templates["single"] = template
	if _, err = s.SaveDefinitions(ctx, configuration.Document, etag, user, "configure fallback", fixtureSources()); err != nil {
		t.Fatal(err)
	}
	if err = s.SeedContent(ctx); err != nil {
		t.Fatal(err)
	}
	configuration, _ = s.Definitions(ctx)
	if len(*configuration.Document.Templates["single"].Match) != 0 || (*configuration.Document.Templates["single"].Blocks)[0] != "relations" {
		t.Fatal("explicit GUI choices overwritten")
	}
}
