package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func reviewEditor() User {
	u := fixtureUser("editor")
	u.Permissions = []string{PermissionEntityEdit, PermissionRelationEdit}
	return u
}

func TestPostgresPATAccountReviewExemptionAndScopedEditing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	e := f.save(Entity{Kind: "work", Title: "PAT account exemption"})
	u := User{ID: f.u.ID, Username: f.u.Username, FromPAT: true, CatalogReviewExempt: true, Permissions: []string{PermissionEntityEdit}}
	in := fixtureCommit(f, updateCommit(e, patchValue("/attributes/tags", []string{"exempt"})))
	out, err := f.s.PushCommit(ctx, in, u, false)
	if err != nil || !out.Applied || out.ReviewDecision != "exempt" {
		t.Fatalf("account-exempt PAT queued: %+v %v", out, err)
	}
	e, err = f.s.Get(ctx, e.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	u.CatalogReviewExempt = false
	queued := fixtureCommit(f, updateCommit(e, patchValue("/attributes/tags", []string{"pending"})))
	out, err = f.s.PushCommit(ctx, queued, u, false)
	if err != nil || out.Applied || out.ReviewStatus != "pending_review" {
		t.Fatalf("ordinary PAT did not queue: %+v %v", out, err)
	}
	u.CatalogReviewExempt = true
	u.Permissions = []string{"auth.audit.read"}
	denied := fixtureCommit(f, updateCommit(e, patchValue("/attributes/tags", []string{"forbidden"})))
	if _, err := f.s.PushCommit(ctx, denied, u, false); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("read-only owner PAT edited: %v", err)
	}
	current, err := f.s.Get(ctx, e.ID, nil)
	if err != nil || current.Version != e.Version || encode(current.Attributes) != encode(e.Attributes) {
		t.Fatalf("pending/read-only submission changed facts: %+v %v", current, err)
	}
}

func TestPostgresCommitReviewHTTPAndRelationDeletion(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u := reviewEditor()
	w := f.save(Entity{Kind: "work", Title: "member"})
	c := f.save(Entity{Kind: "collection", Title: "collection"})
	r, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "includes", SourceID: c.ID, TargetID: w.ID}, EditNote: "fixture", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	in := fixtureCommit(f, CommitOperation{Target: "relation", Action: "delete", ID: r.ID, BaseVersion: r.Version})
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("catalog_user", &u); c.Next() })
	HTTP{Store: f.s}.Register(engine)
	request := func(method, path, payload string) *httptest.ResponseRecorder {
		out := httptest.NewRecorder()
		engine.ServeHTTP(out, httptest.NewRequest(method, path, strings.NewReader(payload)))
		return out
	}
	if res := request(http.MethodPost, "/api/catalog/commits/preview", encode(in)); res.Code != 200 {
		t.Fatalf("preview: %d %s", res.Code, res.Body.String())
	}
	if res := request(http.MethodPost, "/api/catalog/commits", encode(in)); res.Code != 202 {
		t.Fatalf("queue: %d %s", res.Code, res.Body.String())
	}
	if _, err = relationByID(ctx, f.s.DB, r.ID); err != nil {
		t.Fatal("pending relation deletion changed facts")
	}
	if res := request(http.MethodGet, "/api/catalog/commits?review_status=pending_review", ""); res.Code != 200 || !strings.Contains(res.Body.String(), in.ID) {
		t.Fatalf("own queue: %d %s", res.Code, res.Body.String())
	}
	if _, err = f.s.ReviewCommit(ctx, in.ID, CommitReviewEdit{Decision: "approve"}, f.u); err != nil {
		t.Fatal(err)
	}
	if _, err = relationByID(ctx, f.s.DB, r.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("approved deletion: %v", err)
	}
}

func TestPostgresCommitReviewInclusionPreservesHiddenHistory(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u := reviewEditor()
	w := f.save(Entity{Kind: "work", Title: "inclusion work"})
	hidden := f.save(Entity{Kind: "expression", Title: "retired recording", WorkID: w.ID})
	visible := f.save(Entity{Kind: "expression", Title: "current recording", WorkID: w.ID})
	r := f.save(Entity{Kind: "release", Title: "release", Subjects: []Subject{{WorkID: w.ID, Role: "primary"}}})
	m := f.save(Entity{Kind: "medium", Title: "medium", ReleaseID: r.ID, Attributes: map[string]any{"format": "cd"}})
	track := f.save(Entity{Kind: "track", Title: "track", MediumID: m.ID, Contents: []Inclusion{{ExpressionID: hidden.ID, Position: 0, Locator: Locator{}}}})
	f.retire(hidden)
	original, _ := get(ctx, f.s.DB, track.ID)
	before := factCounts(t, f.s)
	in := fixtureCommit(f, CommitOperation{Target: "entity", Action: "content", ID: track.ID, BaseVersion: track.Version,
		ContentChange: &CommitContentChange{Action: "add", Position: 1, Inclusion: &Inclusion{ExpressionID: visible.ID, Position: 1, Locator: Locator{}}}})
	out, err := f.s.PushCommit(ctx, in, u, false)
	if err != nil || out.Applied || factCounts(t, f.s) != before {
		t.Fatalf("queued inclusion: %+v %v", out, err)
	}
	if strings.Contains(encode(out.Preview), hidden.ID) {
		t.Fatal("proposal preview disclosed hidden inclusion")
	}
	if _, err = f.s.ReviewCommit(ctx, in.ID, CommitReviewEdit{Decision: "approve"}, f.u); err != nil {
		t.Fatal(err)
	}
	after, err := get(ctx, f.s.DB, track.ID)
	if err != nil || after.Version != track.Version+1 || len(after.Contents) != 2 || encode(after.Contents[0]) != encode(original.Contents[0]) {
		t.Fatalf("hidden history changed: %+v %v", after, err)
	}
}

func factCounts(t *testing.T, s *Store) string {
	t.Helper()
	out := []int64{}
	for _, table := range []string{"entities", "relations", "revisions", "outbox", "notifications", "notification_receipts"} {
		var n int64
		if err := s.DB.QueryRow("SELECT count(*) FROM catalog." + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return encode(out)
}

func TestPostgresCommitReviewKeepsHeadUntilApproved(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u := reviewEditor()
	e := f.save(Entity{Kind: "work", Title: "original", Attributes: map[string]any{"tags": []string{"old"}}})
	in := fixtureCommit(f, updateCommit(e, patchValue("/attributes/tags", []string{"new"})))
	before := factCounts(t, f.s)
	out, err := f.s.PushCommit(ctx, in, u, false)
	if err != nil || out.Applied || out.ReviewStatus != "pending_review" || len(out.Preview) != 1 {
		t.Fatalf("queue: %+v %v", out, err)
	}
	if factCounts(t, f.s) != before {
		t.Fatal("pending submission wrote facts or events")
	}
	current, _ := f.s.Get(ctx, e.ID, nil)
	if current.Version != e.Version || current.ReviewStatus != "reviewed" || encode(current.Attributes) != encode(e.Attributes) {
		t.Fatal("pending proposal changed public head")
	}
	if _, err = f.s.CommitSubmission(ctx, in.ID, reviewEditor()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("another user's proposal leaked: %v", err)
	}
	if _, err = f.s.ReviewCommit(ctx, in.ID, CommitReviewEdit{Decision: "approve"}, u); !errors.Is(err, errForbidden) {
		t.Fatalf("review privilege: %v", err)
	}
	out, err = f.s.ReviewCommit(ctx, in.ID, CommitReviewEdit{Decision: "approve", Note: "checked source"}, f.u)
	if err != nil || !out.Applied || out.ReviewDecision != "approved" {
		t.Fatalf("approval: %+v %v", out, err)
	}
	current, _ = f.s.Get(ctx, e.ID, nil)
	if current.Version != e.Version+1 || encode(current.Attributes["tags"]) != `["new"]` {
		t.Fatalf("approval not applied: %+v", current)
	}
	var actor string
	if err = f.s.DB.QueryRow(`SELECT actor_id FROM catalog.revisions WHERE target_id=$1 AND version=$2`, e.ID, current.Version).Scan(&actor); err != nil || actor != u.ID {
		t.Fatalf("lost original author: %q %v", actor, err)
	}
	after := factCounts(t, f.s)
	if _, err = f.s.DB.Exec(`UPDATE catalog.commits SET request='null' WHERE id=$1`, in.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ReviewCommit(ctx, in.ID, CommitReviewEdit{Decision: "approve"}, f.u); err != nil || factCounts(t, f.s) != after {
		t.Fatal("approval was not idempotent")
	}
}

func TestPostgresCommitReviewAtomicCreateRejectAndBypass(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u := reviewEditor()
	makeCommit := func() CatalogCommit {
		return fixtureCommit(f,
			CommitOperation{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(`{"kind":"work","title":"new work"}`), ReviewedCandidateIDs: []string{}},
			CommitOperation{Target: "entity", Action: "create", Ref: "collection", Document: json.RawMessage(`{"kind":"collection","title":"new collection"}`), ReviewedCandidateIDs: []string{}},
			CommitOperation{Target: "relation", Action: "create", Ref: "edge", Document: json.RawMessage(`{"type":"includes","source_id":{"$ref":"collection"},"target_id":{"$ref":"work"}}`)})
	}
	in := makeCommit()
	before := factCounts(t, f.s)
	out, err := f.s.PushCommit(ctx, in, u, false)
	if err != nil || out.Applied || factCounts(t, f.s) != before {
		t.Fatalf("pending graph: %+v %v", out, err)
	}
	if _, err = f.s.Get(ctx, out.Refs["work"], &u); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending new entry exists: %v", err)
	}
	rejected, err := f.s.ReviewCommit(ctx, in.ID, CommitReviewEdit{Decision: "reject", Note: "needs evidence"}, f.u)
	if err != nil || rejected.Applied || rejected.ReviewDecision != "rejected" || rejected.ReviewStatus != "reviewed" {
		t.Fatalf("reject: %+v %v", rejected, err)
	}
	if _, err = f.s.ReviewCommit(ctx, in.ID, CommitReviewEdit{Decision: "approve"}, f.u); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.PushCommit(ctx, in, u, false); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = f.s.DB.QueryRow(`SELECT count(*) FROM catalog.entities`).Scan(&n); err != nil || n != 0 {
		t.Fatal("rejected commit was applied")
	}
	in = makeCommit()
	pending, err := f.s.PushCommit(ctx, in, u, false)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := f.s.ReviewCommit(ctx, in.ID, CommitReviewEdit{Decision: "approve"}, f.u)
	if err != nil || !approved.Applied || encode(approved.Refs) != encode(pending.Refs) {
		t.Fatalf("atomic approval or stable refs: %+v %v", approved, err)
	}
	u.Permissions = append(u.Permissions, PermissionReviewBypass)
	e, _ := f.s.Get(ctx, approved.Refs["work"], nil)
	direct := fixtureCommit(f, updateCommit(e, patchValue("/attributes/tags", []string{"bypass"})))
	out, err = f.s.PushCommit(ctx, direct, u, false)
	if err != nil || !out.Applied || out.ReviewDecision != "exempt" {
		t.Fatalf("bypass: %+v %v", out, err)
	}
}

func TestPostgresCommitReviewRevalidatesConflictsAndGuardsDirectWrites(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u := reviewEditor()
	e := f.save(Entity{Kind: "work", Title: "conflict", Attributes: map[string]any{"tags": []string{"old"}}})
	if _, err := f.s.Save(ctx, Edit{Entity: e}, u); err == nil || err.Error() != "commit_required" {
		t.Fatalf("direct entity write: %v", err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{}, u); err == nil || err.Error() != "commit_required" {
		t.Fatalf("direct relation write: %v", err)
	}
	if err := f.s.DeleteRelation(ctx, "", 0, "", nil, u); err == nil || err.Error() != "commit_required" {
		t.Fatalf("direct delete: %v", err)
	}
	in := fixtureCommit(f, updateCommit(e, patchValue("/attributes/tags", []string{"proposed"})))
	if _, err := f.s.PushCommit(ctx, in, u, false); err != nil {
		t.Fatal(err)
	}
	e.Attributes = map[string]any{"tags": []string{"concurrent"}}
	e = f.save(e)
	before := factCounts(t, f.s)
	_, err := f.s.ReviewCommit(ctx, in.ID, CommitReviewEdit{Decision: "approve"}, f.u)
	var conflict *commitConflictError
	if !errors.As(err, &conflict) || factCounts(t, f.s) != before {
		t.Fatalf("conflicting approval: %v", err)
	}
	out, _ := f.s.CommitReceipt(ctx, in.ID, u)
	if out.Applied || out.ReviewStatus != "pending_review" {
		t.Fatal("conflicting proposal must remain pending")
	}
	for _, path := range []string{"/status", "/state", "/review_status"} {
		bad := fixtureCommit(f, updateCommit(e, patchValue(path, "reviewed")))
		if _, err = f.s.PushCommit(ctx, bad, f.u, false); err == nil {
			t.Fatalf("server field %s accepted", path)
		}
	}
}
