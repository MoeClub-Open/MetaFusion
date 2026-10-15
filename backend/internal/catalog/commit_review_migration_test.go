package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func TestCommitReviewMigrationPreservesFactsVersionsAndHistory(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	if err := migrator.New(db, catalogMigrationsBefore(t, "000031")).Up(ctx); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, state := range []string{"draft", "pending_review", "published", "deleted", "merged"} {
		id := uuid.NewString()
		ids[state] = id
		doc := map[string]any{"id": id, "kind": "work", "version": 7, "status": state, "title": "preserved " + state, "attributes": map[string]any{"tags": []string{"original"}}, "translations": map[string]any{"ja": map[string]any{"title": "記録", "body": "## 本文\n出典付き本文"}}}
		if _, err := db.Exec(`INSERT INTO catalog.entities(id,kind,version,title,status,document,created_by) VALUES($1,'work',7,$2,$3,$4,$5)`, id, doc["title"], state, encode(doc), uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot) VALUES($1,7,$3,'admin','original evidence','[{"kind":"publication","citation":"original"}]',$2)`, id, encode(doc), uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	var facts, history string
	if err := db.QueryRow(`SELECT md5(string_agg((document-'status')::text,'' ORDER BY id)) FROM catalog.entities`).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT md5(string_agg(to_jsonb(r)::text,'' ORDER BY id)) FROM catalog.revisions r`).Scan(&history); err != nil {
		t.Fatal(err)
	}
	upgrade := migrator.New(db, migrations.FS)
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var afterFacts, afterHistory string
	if err := db.QueryRow(`SELECT md5(string_agg((document-'state'-'review_status')::text,'' ORDER BY id)) FROM catalog.entities`).Scan(&afterFacts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT md5(string_agg(to_jsonb(r)::text,'' ORDER BY id)) FROM catalog.revisions r`).Scan(&afterHistory); err != nil {
		t.Fatal(err)
	}
	if afterFacts != facts || afterHistory != history {
		t.Fatal("migration modified facts or historical evidence")
	}
	for old, id := range ids {
		var state, review string
		var version int
		if err := db.QueryRow(`SELECT status,document->>'review_status',version FROM catalog.entities WHERE id=$1`, id).Scan(&state, &review, &version); err != nil {
			t.Fatal(err)
		}
		want := "active"
		if old == "deleted" || old == "merged" {
			want = old
		}
		if state != want || review != "reviewed" || version != 7 {
			t.Fatalf("%s migrated to %s/%s v%d", old, state, review, version)
		}
	}
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
}
