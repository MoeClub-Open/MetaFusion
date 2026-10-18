package migrator

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func TestBaselineManifestMatchesImmutableFixtures(t *testing.T) {
	m := New(nil, migrations.FS)
	files, err := m.LoadMigrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.loadBaseline(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range b.Legacy {
		data, err := fs.ReadFile(testutil.LegacyMigrations(), fmtFilename(e))
		if err != nil {
			t.Fatal(err)
		}
		if checksumOf(string(data)) != e.Checksum {
			t.Fatalf("fixture checksum drift: %d", e.Version)
		}
	}
	ledger := map[int64]AppliedMigration{}
	for _, e := range b.Legacy {
		ledger[e.Version] = AppliedMigration{Version: e.Version, Name: e.Name, Checksum: e.Checksum}
	}
	if legacy, err := m.validateLedger(ledger, files, b); err != nil || !legacy {
		t.Fatalf("valid legacy ledger: %v", err)
	}
	for _, scenario := range []string{"partial", "empty checksum", "modified", "name", "dirty", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			bad := map[int64]AppliedMigration{}
			for v, a := range ledger {
				bad[v] = a
			}
			a := bad[20]
			switch scenario {
			case "partial":
				delete(bad, 1)
			case "empty checksum":
				a.Checksum = ""
			case "modified":
				a.Checksum = checksumOf("changed")
			case "name":
				a.Name = "changed"
			case "dirty":
				a.Dirty = true
			case "unknown":
				bad[999] = AppliedMigration{Version: 999, Name: "unknown", Checksum: checksumOf("x")}
			}
			bad[20] = a
			if _, err := m.validateLedger(bad, files, b); err == nil {
				t.Fatal("invalid ledger accepted")
			}
		})
	}
}

func fmtFilename(e ledgerEntry) string { return fmt.Sprintf("%06d_%s.up.sql", e.Version, e.Name) }

// Compare schema definitions rather than schema-only dump formatting or owners.
func schemaShape(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`
 SELECT 'column:'||table_name||':'||column_name||':'||row_number() OVER (PARTITION BY table_name ORDER BY ordinal_position)||':'||data_type||':'||is_nullable||':'||coalesce(column_default,'') FROM information_schema.columns WHERE table_schema='catalog'
 UNION ALL SELECT 'constraint:'||c.relname||':'||p.conname||':'||pg_get_constraintdef(p.oid) FROM pg_constraint p JOIN pg_class c ON c.oid=p.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='catalog'
 UNION ALL SELECT 'index:'||indexname||':'||indexdef FROM pg_indexes WHERE schemaname='catalog'
 UNION ALL SELECT 'trigger:'||pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='catalog' AND NOT t.tgisinternal
 UNION ALL SELECT 'function:'||pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='catalog'
 UNION ALL SELECT 'sequence:'||sequencename||':'||start_value||':'||min_value||':'||max_value||':'||increment_by||':'||cycle FROM pg_sequences WHERE schemaname='catalog'
 ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBaselineFreshAndLegacySchemasMatch(t *testing.T) {
	ctx := context.Background()
	fresh, legacy := testutil.Database(t), testutil.Database(t)
	if err := New(legacy, testutil.LegacyMigrations()).Up(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := New(legacy, migrations.FS).GetAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := New(fresh, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	up := New(legacy, migrations.FS)
	if err := up.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := up.Up(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := up.GetAppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for v, a := range old {
		if !reflect.DeepEqual(a, after[v]) {
			t.Fatalf("old ledger %d changed", v)
		}
	}
	files, err := up.LoadMigrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	expected := len(old)
	for _, file := range files {
		if file.Direction == DirectionUp {
			expected++
		}
	}
	if len(after) != expected {
		t.Fatal("baseline and subsequent migrations must each add exactly one entry")
	}
	a, b := schemaShape(t, fresh), schemaShape(t, legacy)
	if !reflect.DeepEqual(a, b) {
		freshOnly, legacyOnly := map[string]bool{}, map[string]bool{}
		for _, v := range a {
			freshOnly[v] = true
		}
		for _, v := range b {
			legacyOnly[v] = true
		}
		for _, v := range a {
			if !legacyOnly[v] {
				t.Errorf("fresh only: %s", v)
			}
		}
		for _, v := range b {
			if !freshOnly[v] {
				t.Errorf("legacy only: %s", v)
			}
		}
		t.FailNow()
	}

	for _, db := range []*sql.DB{fresh, legacy} {
		var policy string
		if err := db.QueryRow("SELECT document::text FROM catalog.rate_limit_policy").Scan(&policy); err != nil || policy != "{}" {
			t.Fatalf("rate limit seed: %s %v", policy, err)
		}
		if err := New(db, migrations.FS).Down(ctx); err == nil || !strings.Contains(err.Error(), "irreversible") {
			t.Fatalf("down: %v", err)
		}
	}
}

func TestBaselineRejectsUnverifiedDatabaseWithoutWriting(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	legacy := New(db, testutil.LegacyMigrations())
	if err := legacy.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE schema_migrations SET checksum='' WHERE version=20"); err != nil {
		t.Fatal(err)
	}
	m := New(db, migrations.FS)
	if err := m.Up(ctx); err == nil {
		t.Fatal("unverified ledger accepted")
	}
	if err := m.Status(ctx); err == nil {
		t.Fatal("status concealed invalid ledger")
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM schema_migrations WHERE version=21 OR (version=20 AND checksum<>'')").Scan(&n); err != nil || n != 0 {
		t.Fatal("invalid ledger mutated")
	}
}

func TestBaselineAllowsProvisionedSchemaButRefusesUntrackedTables(t *testing.T) {
	ctx := context.Background()
	for _, partial := range []bool{false, true} {
		db := testutil.Database(t)
		if _, err := db.Exec("CREATE SCHEMA catalog"); err != nil {
			t.Fatal(err)
		}
		if partial {
			if _, err := db.Exec("CREATE TABLE catalog.entities (untracked text)"); err != nil {
				t.Fatal(err)
			}
		}
		err := New(db, migrations.FS).Up(ctx)
		if partial {
			if err == nil {
				t.Fatal("untracked structure adopted")
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 0 {
				t.Fatal("failed baseline wrote ledger")
			}
		} else if err != nil {
			t.Fatalf("role provisioning must permit empty schema: %v", err)
		}
	}
}

func TestMigrationLockUsesSingleSessionAndSerializes(t *testing.T) {
	db := testutil.Database(t)
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	source := testFS(map[string]string{
		"000001_lock.up.sql":   `DO $$ BEGIN IF NOT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND objid=88481001 AND granted) THEN RAISE EXCEPTION 'lock is not on migration session'; END IF; END $$; CREATE TABLE locked_migration(id int);`,
		"000001_lock.down.sql": "DROP TABLE locked_migration;",
	})
	m := New(db, source)
	if err := m.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Down(ctx); err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(2)
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errors <- m.Up(ctx) }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Force(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Down(ctx); err != nil {
		t.Fatal(err)
	}
	var locks int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid=88481001 AND granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database())").Scan(&locks); err != nil || locks != 0 {
		t.Fatalf("migration lock leaked: %d %v", locks, err)
	}
}
