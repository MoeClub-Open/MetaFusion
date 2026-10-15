package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func TestCatalogStateMigrationConvertsStoredProjections(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	if err := migrator.New(db, catalogMigrationsBefore(t, "000032")).Up(ctx); err != nil {
		t.Fatal(err)
	}
	actor, commitID := uuid.NewString(), uuid.NewString()
	request := `{"immutable":"original submission","operations":[{"document":{"status":"published"}}]}`
	if _, err := db.Exec(`INSERT INTO catalog.commits(id,actor_id,request_hash,request,result) VALUES($1,$2,'original-hash',$3,'{"review_status":"reviewed","review_decision":"legacy","applied":true}')`, commitID, actor, request); err != nil {
		t.Fatal(err)
	}
	for _, old := range []string{"draft", "pending_review", "published", "deleted", "merged"} {
		id := uuid.NewString()
		document := map[string]any{"id": id, "kind": "work", "version": 9, "state": "active", "review_status": "reviewed", "title": "retained title", "attributes": map[string]any{"status": "business value"}}
		if _, err := db.Exec(`INSERT INTO catalog.entities(id,kind,version,title,status,created_by,document) VALUES($1,'work',9,'retained title','active',$2,$3)`, id, actor, encode(document)); err != nil {
			t.Fatal(err)
		}
		delete(document, "state")
		delete(document, "review_status")
		document["status"] = old
		if _, err := db.Exec(`INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot) VALUES($1,9,$2,'original author','original evidence','[]',$3)`, id, actor, encode(document)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO catalog.outbox(id,type,entity_id,version,payload) VALUES($3,'entity.unpublished',$1,9,$2)`, id, encode(document), uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	var facts string
	if err := db.QueryRow(`SELECT md5(string_agg((to_jsonb(e)-'status')::text,'' ORDER BY id)) FROM catalog.entities e`).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	upgrade := migrator.New(db, migrations.FS)
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := db.QueryRow(`SELECT md5(string_agg((to_jsonb(e)-'state')::text,'' ORDER BY id)) FROM catalog.entities e`).Scan(&after); err != nil || after != facts {
		t.Fatalf("entity facts changed: %v", err)
	}
	var bad int
	if err := db.QueryRow(`SELECT count(*) FROM catalog.revisions WHERE snapshot ? 'status' OR snapshot->>'state' NOT IN ('active','deleted','merged') OR snapshot->>'review_status'<>'reviewed' OR snapshot#>>'{attributes,status}'<>'business value' OR actor_name<>'original author' OR edit_note<>'original evidence' OR version<>9`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("historical facts not migrated: bad=%d err=%v", bad, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema='catalog' AND table_name='entities' AND column_name='status'`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("old column remains: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM catalog.outbox WHERE type='entity.unpublished' OR payload ? 'status'`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("old event format remains: %v", err)
	}
	var savedRequest, hash, decision string
	if err := db.QueryRow(`SELECT request::text,request_hash,review_decision FROM catalog.commits WHERE id=$1`, commitID).Scan(&savedRequest, &hash, &decision); err != nil {
		t.Fatal(err)
	}
	if hash != "original-hash" || decision != "exempt" {
		t.Fatal("receipt provenance changed")
	}
	var same bool
	if err := db.QueryRow(`SELECT request=$2::jsonb FROM catalog.commits WHERE id=$1`, commitID, request).Scan(&same); err != nil || !same {
		t.Fatal("immutable submission evidence changed")
	}
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
}
