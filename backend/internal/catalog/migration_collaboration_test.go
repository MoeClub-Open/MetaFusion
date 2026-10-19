package catalog

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func TestCollaborationMigrationPreservesHistoryAndConvertsProposals(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	if err := migrator.New(db, catalogMigrationsBefore(t, "000035")).Up(ctx); err != nil {
		t.Fatal(err)
	}
	author, reviewer := uuid.NewString(), uuid.NewString()
	pending, rejected, empty, applied, noop, deleted := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	unknown, verified := uuid.NewString(), uuid.NewString()
	entityID, rejectedID, appliedID, noopID, relationID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	unknownID, verifiedID := uuid.NewString(), uuid.NewString()
	created := time.Date(2026, 10, 18, 1, 2, 3, 0, time.UTC)
	closed := created.Add(time.Hour)
	entity := func(id string, version int, title string) map[string]any {
		return map[string]any{"id": id, "kind": "work", "version": version, "title": title, "state": "active", "review_status": "reviewed"}
	}
	preview := func(id string, before, after any) map[string]any {
		return map[string]any{"target": "entity", "id": id, "before": before, "after": after}
	}
	addCommit := func(id, decision string, request any, previews []any) {
		t.Helper()
		status := "reviewed"
		if decision == "pending" {
			status = "pending_review"
		}
		result := map[string]any{"id": id, "applied": decision == "approved" || decision == "exempt", "review_status": status, "review_decision": decision, "preview": previews}
		var reviewedBy, reviewedAt any
		if decision == "approved" || decision == "rejected" {
			reviewedBy, reviewedAt = reviewer, closed
		}
		if _, err := db.Exec(`INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,created_at,review_status,review_decision,reviewed_by,reviewed_at,review_note,author_snapshot)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'original decision note',$11)`, id, author, "immutable-hash-"+id, encode(request), encode(result), created, status, decision, reviewedBy, reviewedAt, encode(map[string]any{"id": author, "username": "Original author", "permissions": []string{"catalog.entity.edit"}})); err != nil {
			t.Fatal(err)
		}
	}
	request := func(note string) map[string]any {
		return map[string]any{"edit_note": note, "sources": []any{map[string]any{"key": "original", "citation": "Original historical source"}}, "operations": []any{}}
	}
	addCommit(pending, "pending", request("pending proposal"), []any{
		preview(entityID, entity(entityID, 3, "original"), entity(entityID, 4, "intermediate")),
		preview(entityID, entity(entityID, 4, "intermediate"), entity(entityID, 5, "final proposal")),
	})
	addCommit(rejected, "rejected", request("rejected creation"), []any{preview(rejectedID, nil, entity(rejectedID, 1, "unmerged creation"))})
	addCommit(empty, "pending", request("empty retained preview"), nil)
	// A review's frozen preview is not necessarily its eventual applied facts.
	addCommit(applied, "approved", nil, []any{preview(appliedID, entity(appliedID, 1, "old proposal base"), entity(appliedID, 2, "old frozen proposal"))})
	// No-op history has no revision to recover; its accurate preview survives.
	addCommit(noop, "exempt", request("no-op submission"), []any{preview(noopID, entity(noopID, 7, "unchanged"), entity(noopID, 7, "unchanged"))})
	addCommit(deleted, "exempt", nil, nil)
	if _, err := db.Exec(`UPDATE catalog.commits SET author_snapshot='{}' WHERE id=$1`, deleted); err != nil {
		t.Fatal(err)
	}
	addCommit(unknown, "exempt", nil, []any{preview(unknownID, entity(unknownID, 1, "wrong old base"), entity(unknownID, 2, "wrong old proposal"))})
	addCommit(verified, "exempt", nil, []any{preview(verifiedID, entity(verifiedID, 3, "verified retained base"), entity(verifiedID, 4, "frozen proposal"))})
	addRevision := func(id string, version int, commitID any, snapshot any, note string) int64 {
		t.Helper()
		var revisionID int64
		if err := db.QueryRow(`INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot,commit_id,created_at)
			VALUES($1,$2,$3,'Original author',$4,'[{"citation":"Preserved revision source"}]',$5,$6,$7) RETURNING id`, id, version, author, note, encode(snapshot), commitID, created).Scan(&revisionID); err != nil {
			t.Fatal(err)
		}
		return revisionID
	}
	addRevision(appliedID, 4, nil, entity(appliedID, 4, "actual main before"), "previous edit")
	firstRevision := addRevision(appliedID, 5, applied, entity(appliedID, 5, "actual intermediate"), "retained applied message")
	lastRevision := addRevision(appliedID, 6, applied, entity(appliedID, 6, "actual merged facts"), "second operation")
	relation := map[string]any{"id": relationID, "version": 2, "source_id": uuid.NewString(), "target_id": uuid.NewString(), "type": "credit_for"}
	deletedRevision := addRevision(relationID, 3, deleted, relation, "retained deletion message")
	addRevision(unknownID, 7, nil, entity(unknownID, 7, "retained but not immediate base"), "version 8 is missing")
	unknownRevision := addRevision(unknownID, 9, unknown, entity(unknownID, 9, "known current snapshot"), "history before version 9 is missing")
	verifiedRevision := addRevision(verifiedID, 4, verified, entity(verifiedID, 4, "actual accepted snapshot"), "matching before retained in preview")
	immutableDigest := func() string {
		t.Helper()
		var value string
		if err := db.QueryRow(`SELECT md5(string_agg(jsonb_build_array(id,actor_id,request_hash,request,result,author_snapshot,created_at)::text,'' ORDER BY id)) FROM catalog.commits`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	revisionDigest := func() string {
		t.Helper()
		var value string
		if err := db.QueryRow(`SELECT md5(string_agg(to_jsonb(r)::text,'' ORDER BY id)) FROM catalog.revisions r`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	original, originalRevisions := immutableDigest(), revisionDigest()
	var originalDecisions string
	if err := db.QueryRow(`SELECT md5(string_agg(id::text||jsonb_build_object(
		'review_status',review_status,'review_decision',review_decision,
		'reviewed_by',reviewed_by,'reviewed_at',reviewed_at,'review_note',review_note)::text,'' ORDER BY id)) FROM catalog.commits`).Scan(&originalDecisions); err != nil {
		t.Fatal(err)
	}
	upgrade := migrator.New(db, migrations.FS)
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if immutableDigest() != original || revisionDigest() != originalRevisions {
		t.Fatal("migration altered immutable submission or revision evidence")
	}
	var preservedDecisions string
	if err := db.QueryRow(`SELECT md5(string_agg(id::text||historical_review::text,'' ORDER BY id)) FROM catalog.commits`).Scan(&preservedDecisions); err != nil {
		t.Fatal(err)
	}
	if preservedDecisions != originalDecisions {
		t.Fatal("migration lost independent historical review decisions or notes")
	}
	count := func(query string, args ...any) int {
		t.Helper()
		var value int
		if err := db.QueryRow(query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := count(`SELECT count(*) FROM catalog.workspaces`); got != 3 {
		t.Fatalf("expected only 3 unmerged workspaces, got %d", got)
	}
	if got := count(`SELECT count(*) FROM catalog.pull_requests WHERE status='open'`); got != 2 {
		t.Fatalf("pending proposals did not become open PRs: %d", got)
	}
	if count(`SELECT count(*) FROM catalog.commit_authors WHERE user_id=$1`, author) != 8 ||
		count(`SELECT count(*) FROM catalog.commit_authors WHERE user_id=$1 AND name='Original author'`, author) != 7 ||
		count(`SELECT count(*) FROM catalog.commit_authors WHERE commit_id=$1 AND user_id=$2 AND name=''`, deleted, author) != 1 {
		t.Fatal("historical author IDs or original names were lost or invented")
	}
	if count(`SELECT count(*) FROM catalog.pull_requests WHERE source_base_id IS NOT NULL`) != 0 {
		t.Fatal("historical proposals gained invented source base commits")
	}
	if count(`SELECT count(*) FROM catalog.workspace_objects WHERE staged_authors<>'{}'::jsonb`) != 0 {
		t.Fatal("historical committed proposals gained invented staged authors")
	}
	for _, item := range []struct{ id, status string }{{pending, "open"}, {rejected, "closed"}, {empty, "open"}} {
		if count(`SELECT count(*) FROM catalog.commits c JOIN catalog.workspaces w ON w.id=c.workspace_id
			JOIN catalog.pull_requests p ON p.id=c.pull_request_id
			WHERE c.id=$1 AND w.owner_id=$2 AND w.head_commit_id=c.id AND p.source_head_id=c.id
			AND p.workspace_id=w.id AND p.author_id=$2 AND p.status=$3 AND c.merged_at IS NULL AND c.merged_by IS NULL`, item.id, author, item.status) != 1 {
			t.Fatalf("proposal %s lost its identity, owner or PR status", item.id)
		}
	}
	if count(`SELECT count(*) FROM catalog.workspace_objects o JOIN catalog.commits c ON c.workspace_id=o.workspace_id WHERE c.id=$1`, empty) != 0 {
		t.Fatal("missing preview was replaced with invented objects")
	}
	if count(`SELECT count(*) FROM catalog.workspace_objects o JOIN catalog.commits c ON c.workspace_id=o.workspace_id
		WHERE c.id=$1 AND o.base_version=3 AND o.base_document->>'title'='original'
		AND o.head_document->>'title'='final proposal' AND o.document=o.head_document`, pending) != 1 {
		t.Fatal("multiple operations were not collapsed to the original base and final proposal")
	}
	if count(`SELECT count(*) FROM catalog.commit_objects WHERE commit_id=$1 AND base_version=0 AND before_document IS NULL AND after_document->>'title'='unmerged creation'`, rejected) != 1 {
		t.Fatal("new-object absence was not preserved as SQL NULL")
	}
	if count(`SELECT count(*) FROM catalog.commit_objects WHERE commit_id=$1 AND base_version=4 AND before_document->>'title'='actual main before' AND after_document->>'title'='actual merged facts'`, applied) != 1 {
		t.Fatal("applied revisions did not take precedence over stale frozen preview")
	}
	if count(`SELECT count(*) FROM catalog.commit_objects WHERE base_document IS DISTINCT FROM before_document`) != 0 {
		t.Fatal("historical commit base differs from its only provable before-state")
	}
	if count(`SELECT count(*) FROM catalog.commit_objects WHERE commit_id=$1 AND base_version=7 AND before_document=after_document`, noop) != 1 {
		t.Fatal("no-op preview was not retained")
	}
	if count(`SELECT count(*) FROM catalog.commit_objects WHERE commit_id=$1 AND target='relation' AND base_version=2 AND before_document IS NOT NULL AND after_document IS NULL`, deleted) != 1 {
		t.Fatal("relation deletion snapshot did not recover its own before-state without a prior revision")
	}
	if count(`SELECT count(*) FROM catalog.commit_objects WHERE commit_id=$1`, unknown) != 0 {
		t.Fatal("unknown historical before-state was replaced with an invented creation or stale preview")
	}
	if count(`SELECT count(*) FROM catalog.commit_objects WHERE commit_id=$1 AND base_version=3 AND before_document->>'title'='verified retained base' AND after_document->>'title'='actual accepted snapshot'`, verified) != 1 {
		t.Fatal("matching frozen before-version did not recover a missing prior revision")
	}
	for _, revisionID := range []int64{firstRevision, lastRevision, deletedRevision, unknownRevision, verifiedRevision} {
		if count(`SELECT count(*) FROM catalog.revision_commits WHERE revision_id=$1`, revisionID) != 1 {
			t.Fatalf("revision %d lost commit provenance", revisionID)
		}
	}
	var message, mergedBy string
	var mergedAt time.Time
	var workspaceID sql.NullString
	if err := db.QueryRow(`SELECT message,merged_at,merged_by,workspace_id::text FROM catalog.commits WHERE id=$1`, applied).Scan(&message, &mergedAt, &mergedBy, &workspaceID); err != nil {
		t.Fatal(err)
	}
	if message != "retained applied message" || !mergedAt.Equal(closed) || mergedBy != reviewer || workspaceID.Valid {
		t.Fatalf("applied metadata was invented or lost: %q %v %q %v", message, mergedAt, mergedBy, workspaceID)
	}
	if count(`SELECT count(*) FROM catalog.commits WHERE parent_id IS NOT NULL`) != 0 {
		t.Fatal("migration fabricated historical parents")
	}
	if count(`SELECT count(*) FROM information_schema.columns WHERE table_schema='catalog' AND table_name='commits' AND column_name IN ('review_status','review_decision','reviewed_by','reviewed_at','review_note')`) != 0 {
		t.Fatal("old live review columns remain")
	}
	if count(`SELECT version FROM catalog.schema_contract WHERE singleton`) != 36 {
		t.Fatal("schema contract was not advanced")
	}
	if err := upgrade.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if immutableDigest() != original || revisionDigest() != originalRevisions {
		t.Fatal("repeated up altered preserved evidence")
	}
}
