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
)

func TestRetiringIndexesPreservesCatalog(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	before := fstest.MapFS{}
	files, err := fs.ReadDir(testutil.LegacyMigrations(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.IsDir() || file.Name() >= "000020_" {
			continue
		}
		data, err := fs.ReadFile(testutil.LegacyMigrations(), file.Name())
		if err != nil {
			t.Fatal(err)
		}
		before[file.Name()] = &fstest.MapFile{Data: data}
	}
	if err := migrator.New(db, before).Up(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	document := `{"title":"preserved","attributes":{"unrelated":"keep"}}`
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog.entities(id,kind,version,title,status,created_by,document) VALUES($1,'work',1,'preserved','published',$2,$3)`, id, uuid.NewString(), document); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX entities_search ON catalog.entities(title); CREATE INDEX entities_document ON catalog.entities USING gin(document)`); err != nil {
		t.Fatal(err)
	}
	var previous string
	if err := db.QueryRowContext(ctx, `SELECT row_to_json(e)::text FROM catalog.entities e WHERE id=$1`, id).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	if err := migrator.New(db, testutil.LegacyMigrations()).Up(ctx); err != nil {
		t.Fatal(err)
	}
	// A populated installation has published definitions. The new reference
	// migration deliberately rejects a nonempty catalog without that contract.
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog.definition_config(singleton,etag,document) VALUES(true,'fixture',$1)`, encode(Defaults())); err != nil {
		t.Fatal(err)
	}
	upgrade := migrator.New(db, catalogMigrationsBefore(t, "000030"))
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := db.QueryRowContext(ctx, `SELECT row_to_json(e)::text FROM catalog.entities e WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if previous != after {
		t.Fatal("index retirement modified an entity")
	}
	for _, name := range []string{"entities_search", "entities_document"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "catalog."+name).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("obsolete index %s remains", name)
		}
	}
	if err := upgrade.Down(ctx); err == nil || !strings.Contains(err.Error(), "irreversible") {
		t.Fatalf("down should refuse: %v", err)
	}
}
