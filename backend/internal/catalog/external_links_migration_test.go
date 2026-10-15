package catalog

import (
	"context"
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func catalogMigrationsBefore(t *testing.T, version string) fs.FS {
	t.Helper()
	before := fstest.MapFS{}
	files, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.IsDir() || strings.HasSuffix(file.Name(), ".sql") && file.Name()[:6] >= version {
			continue
		}
		data, err := fs.ReadFile(migrations.FS, file.Name())
		if err != nil {
			t.Fatal(err)
		}
		before[file.Name()] = &fstest.MapFile{Data: data}
	}
	return before
}

func TestExternalLinksMigrationPreservesDocumentsAndSources(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	if err := migrator.New(db, catalogMigrationsBefore(t, "000030")).Up(ctx); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db}
	if err := s.SeedContent(ctx); err != nil {
		t.Fatal(err)
	}
	id, emptyID, actor := uuid.NewString(), uuid.NewString(), uuid.NewString()
	document := map[string]any{"id": id, "kind": "work", "title": "retained title", "version": 7,
		"external_ids": map[string]any{"bangumi": "123", "fandom": "https://example.org/wiki/A_(B)", "historical_code": "unlisted/value"},
		"translations": map[string]any{"en": map[string]any{"title": "retained title", "body": "## History\nPreserved article."}},
		"attributes":   map[string]any{"custom": "keep"}, "pictures": []any{}}
	original := encode(document)
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog.entities(id,kind,version,title,status,created_by,document) VALUES($1,'work',7,'retained title','draft',$2,$3),($4,'work',1,'empty','draft',$2,'{"title":"empty","external_ids":{}}')`, id, actor, original, emptyID); err != nil {
		t.Fatal(err)
	}
	sources := `[ {"key":"fixture","kind":"url","citation":"original evidence","url":"https://evidence.example/item","pointers":["/external_ids/bangumi","/external_ids/fandom","/title"]} ]`
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot) VALUES($1,7,$2,'fixture','original note',$3,$4)`, id, actor, sources, original); err != nil {
		t.Fatal(err)
	}
	upgrade := migrator.New(db, catalogMigrationsBefore(t, "000031"))
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var raw, snapshot, evidence string
	if err := db.QueryRowContext(ctx, `SELECT document::text FROM catalog.entities WHERE id=$1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var migrated map[string]any
	if err := json.Unmarshal([]byte(raw), &migrated); err != nil {
		t.Fatal(err)
	}
	if _, ok := migrated["external_ids"]; ok {
		t.Fatal("legacy field remains")
	}
	links := migrated["external_links"].([]any)
	if len(links) != 3 {
		t.Fatalf("lost migrated links: %s", raw)
	}
	for _, value := range links {
		link := value.(map[string]any)
		if link["origin"] != "migration" || link["role"] != nil || link["source_refs"] != nil {
			t.Fatalf("inferred migration metadata: %#v", link)
		}
		if link["kind"] == "bangumi" && link["external_id"] != "123" {
			t.Fatal("external ID changed")
		}
	}
	delete(migrated, "external_links")
	delete(document, "external_ids")
	var expected map[string]any
	_ = json.Unmarshal([]byte(encode(document)), &expected)
	if !reflect.DeepEqual(migrated, expected) {
		t.Fatalf("unrelated document changed: %#v", migrated)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM catalog.external_links WHERE entity_id=$1`, id).Scan(&count); err != nil || count != 3 {
		t.Fatalf("side-table count=%d: %v", count, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT snapshot::text,sources::text FROM catalog.revisions WHERE target_id=$1`, id).Scan(&snapshot, &evidence); err != nil {
		t.Fatal(err)
	}
	var revision map[string]any
	_ = json.Unmarshal([]byte(snapshot), &revision)
	if !reflect.DeepEqual(revision["external_links"], links) {
		t.Fatalf("snapshot conversion differs: %s", snapshot)
	}
	var storedSources []Source
	if err := json.Unmarshal([]byte(evidence), &storedSources); err != nil {
		t.Fatal(err)
	}
	if len(storedSources) != 1 || storedSources[0].Citation != "original evidence" || storedSources[0].Key != "fixture" {
		t.Fatalf("evidence changed: %s", evidence)
	}
	if !reflect.DeepEqual(storedSources[0].Pointers, []string{"/external_links/0/external_id", "/external_links/1/url", "/title"}) {
		t.Fatalf("source pointers lost their fields: %s", evidence)
	}
	if err := db.QueryRowContext(ctx, `SELECT document::text FROM catalog.entities WHERE id=$1`, emptyID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "external_ids") || !strings.Contains(raw, `"external_links": []`) {
		t.Fatal(raw)
	}
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM catalog.external_links WHERE entity_id=$1`, id).Scan(&count); err != nil || count != 3 {
		t.Fatalf("repeated migration changed links: %d %v", count, err)
	}
}
