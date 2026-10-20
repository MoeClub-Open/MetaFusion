package catalog

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestVersionQueryRejectsInvalidRanges(t *testing.T) {
	for _, raw := range []string{"limit=0", "limit=101", "offset=-1", "page=2&offset=0", "target=work", "record_id=bad", "author_id=bad", "since=yesterday", "since=2026-10-21T00:00:00Z&until=2026-10-21T00:00:00Z", "state=closed", "target=entity&target=relation"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?"+raw, nil)
		if _, err := versionHistoryQuery(c, true); err == nil {
			t.Errorf("accepted invalid query %q", raw)
		}
	}
	p := recordChangePage([]CommitObject{{Target: "entity", ID: uuid.NewString()}}, VersionReadQuery{Limit: 100, Offset: math.MaxInt})
	if p.Total != 1 || len(p.Changes) != 0 {
		t.Fatal("out-of-range page lost total or overflowed", p)
	}
}

func TestOpenAPIVersionReadCoverage(t *testing.T) {
	doc := OpenAPI()
	paths := doc["paths"].(map[string]any)
	for path, expected := range map[string][]string{
		"/catalog/history":                    {"limit", "offset", "target", "record_id", "author_id", "since", "until"},
		"/catalog/workspaces/{id}/commits":    {"id", "limit", "offset", "target", "record_id", "author_id", "since", "until", "state"},
		"/catalog/workspaces/{id}/diff":       {"id", "limit", "offset", "target", "record_id", "basis"},
		"/catalog/pull-requests/{id}/commits": {"id", "limit", "offset", "target", "record_id", "author_id", "since", "until"},
		"/catalog/entities/{id}/revisions":    {"id", "limit", "offset", "version", "author_id", "since", "until"},
	} {
		op := paths[path].(map[string]any)["get"].(map[string]any)
		seen := map[string]bool{}
		for _, param := range op["parameters"].([]any) {
			name := param.(map[string]any)["name"].(string)
			if seen[name] {
				t.Fatalf("duplicate %s parameter: %s", path, name)
			}
			seen[name] = true
		}
		for _, name := range expected {
			if !seen[name] {
				t.Errorf("%s missing %s", path, name)
			}
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	items := schemas["RevisionPage"].(map[string]any)["properties"].(map[string]any)["items"].(map[string]any)
	if items["items"].(map[string]any)["$ref"] != "#/components/schemas/Revision" {
		t.Fatal("revision response remains untyped")
	}
}

func TestPostgresVersionReadChangesAndFrozenPR(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := reviewEditor()
	a := f.save(Entity{Kind: "work", Title: "A"})
	b := f.save(Entity{Kind: "work", Title: "B"})
	w := testWorkspace(t, f, owner, a, b)
	saved := testWorkspaceSave(t, f, w, owner, workspaceUpdate(a, patchValue("/title", "First A")), workspaceUpdate(b, patchValue("/title", "First B")))
	w, first := testWorkspaceCommit(t, f, saved.Workspace, owner, "Both records")
	pushed, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), owner)
	if err != nil || pushed.PullRequest == nil {
		t.Fatalf("PR: %+v %v", pushed, err)
	}
	pr := *pushed.PullRequest
	saved = testWorkspaceSave(t, f, w, owner, workspaceUpdate(a, patchValue("/title", "Second A")))
	w = saved.Workspace
	before := factCounts(t, f.s)
	q := VersionReadQuery{Limit: 1, Target: "entity", RecordID: a.ID}
	diff, err := f.s.WorkspaceDiff(ctx, w.ID, "head", q, owner)
	if err != nil || diff.Total != 1 || diff.Version != w.Version || diff.Changes[0].ID != a.ID {
		t.Fatalf("working diff: %+v %v", diff, err)
	}
	base, err := f.s.WorkspaceDiff(ctx, w.ID, "base", VersionReadQuery{Limit: 1, Offset: 1}, owner)
	if err != nil || base.Total != 2 || len(base.Changes) != 1 {
		t.Fatalf("base diff: %+v %v", base, err)
	}
	if _, err = f.s.WorkspaceDiff(ctx, w.ID, "head", q, f.u); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reviewer read private workspace: %v", err)
	}
	commit, err := f.s.CommitDiff(ctx, first.ID, q, owner)
	if err != nil || commit.Total != 1 || len(commit.Changes) != 1 {
		t.Fatalf("commit diff: %+v %v", commit, err)
	}
	proposal, err := f.s.PullRequestDiff(ctx, pr.ID, q, f.u)
	if err != nil || proposal.Total != 1 || proposal.SourceHeadID != first.ID || proposal.Version != pr.Version {
		t.Fatalf("PR diff: %+v %v", proposal, err)
	}
	if string(commitMap(proposal.Changes[0].After)["title"]) != `"First A"` {
		t.Fatal("PR followed live working tree")
	}
	snapshot, err := f.s.GetCommitSnapshot(ctx, first.ID, q, owner)
	if err != nil || snapshot.Total != 1 || len(snapshot.Objects) != 1 || snapshot.Objects[0].ID != a.ID {
		t.Fatalf("snapshot filter: %+v %v", snapshot, err)
	}
	if before != factCounts(t, f.s) {
		t.Fatal("read endpoint wrote catalog facts")
	}
	w, second := testWorkspaceCommit(t, f, w, owner, "Later unpushed edit")
	hq := VersionHistoryQuery{VersionReadQuery: q, AuthorID: owner.ID, Since: first.CreatedAt.Add(-time.Second).Format(time.RFC3339Nano), Until: second.CreatedAt.Add(24 * time.Hour).Format(time.RFC3339Nano)}
	page, err := f.s.PullRequestCommitHistory(ctx, pr.ID, hq, f.u)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != first.ID {
		t.Fatalf("frozen PR history: %+v %v", page, err)
	}
	if _, err = f.s.PullRequestCommitHistory(ctx, pr.ID, hq, User{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("anonymous pending PR disclosure: %v", err)
	}
	owned := f
	owned.u = owner
	w = versionAction(t, owned, w, "amend").Workspace
	hq.State = "replaced"
	history, err := f.s.VersionHistory(ctx, w.ID, hq, owner)
	if err != nil || history.Total != 1 || history.Items[0].ID != second.ID {
		t.Fatalf("retained replacement: %+v %v", history, err)
	}
	hq.State = "pending"
	history, err = f.s.VersionHistory(ctx, w.ID, hq, owner)
	if err != nil || history.Total != 2 {
		t.Fatalf("pending history: %+v %v", history, err)
	}
	pushed, err = f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), owner)
	if err != nil {
		t.Fatal(err)
	}
	pr = *pushed.PullRequest
	if _, err = f.s.MergePullRequest(ctx, pr.ID, PullRequestMerge{ExpectedVersion: pr.Version, ExpectedHead: pr.SourceHeadID}, f.u); err != nil {
		t.Fatal(err)
	}
	hq.State = "pending"
	history, err = f.s.VersionHistory(ctx, w.ID, hq, owner)
	if err != nil || history.Total != 0 {
		t.Fatalf("merged commits still pending: %+v %v", history, err)
	}
	hq.State = ""
	official, err := f.s.CatalogVersionHistory(ctx, hq, User{})
	if err != nil || official.Total != 1 || len(official.Items) != 1 {
		t.Fatalf("official filters: %+v %v", official, err)
	}
	head, err := f.s.CatalogVersionHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	officialSnapshot, err := f.s.GetCommitSnapshot(ctx, head.CommitID, q, User{})
	if err != nil || officialSnapshot.Total != 1 || officialSnapshot.Objects[0].ID != a.ID {
		t.Fatalf("catalog snapshot filter: %+v %v", officialSnapshot, err)
	}
	if first.ParentID != nil {
		comparison, err := f.s.CompareVersions(ctx, *first.ParentID, head.CommitID, q, owner)
		if err != nil || comparison.Total != 1 {
			t.Fatalf("catalog compare filter: %+v %v", comparison, err)
		}
	}
}

func TestPostgresRevisionPagingAndDeletedRelation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Long revision history"})
	// Reproduce a long retained history without coupling the test to 100 edit calls.
	_, err := f.s.DB.ExecContext(ctx, `INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot,created_at)
 SELECT target_id,n,actor_id,actor_name,edit_note,sources,jsonb_set(snapshot,'{version}',to_jsonb(n)),created_at+n*interval '1 second'
 FROM catalog.revisions CROSS JOIN generate_series(2,125) n WHERE target_id=$1 AND version=1`, work.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int64]bool{}
	for offset := 0; offset < 125; offset += 40 {
		p, err := f.s.Revisions(ctx, work.ID, RevisionQuery{Limit: 40, Offset: offset}, nil)
		if err != nil || p.Total != 125 {
			t.Fatalf("page %d: %+v %v", offset, p, err)
		}
		for _, item := range p.Items {
			id := item["id"].(int64)
			if ids[id] {
				t.Fatal("duplicate revision page")
			}
			ids[id] = true
		}
	}
	if len(ids) != 125 {
		t.Fatalf("history truncated: %d", len(ids))
	}
	exact, err := f.s.Revisions(ctx, work.ID, RevisionQuery{Version: 2, AuthorID: f.u.ID}, nil)
	if err != nil || exact.Total != 1 || exact.Items[0]["version"] != int64(2) {
		t.Fatalf("exact revision: %+v %v", exact, err)
	}
	at := exact.Items[0]["created_at"].(time.Time)
	rangePage, err := f.s.Revisions(ctx, work.ID, RevisionQuery{Since: at.Format(time.RFC3339Nano), Until: at.Add(time.Second).Format(time.RFC3339Nano)}, nil)
	if err != nil || rangePage.Total != 1 {
		t.Fatalf("revision date bounds: %+v %v", rangePage, err)
	}
	// Hidden historical snapshots must be excluded before counting/limiting.
	if _, err = f.s.DB.ExecContext(ctx, `UPDATE catalog.revisions SET snapshot=jsonb_set(jsonb_set(snapshot,'{state}','"deleted"'),'{created_by}',to_jsonb($2::text)) WHERE target_id=$1 AND version>5`, work.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	visiblePage, err := f.s.Revisions(ctx, work.ID, RevisionQuery{Limit: 2}, nil)
	if err != nil || visiblePage.Total != 5 || len(visiblePage.Items) != 2 || visiblePage.Items[0]["version"] != int64(5) {
		t.Fatalf("visibility before paging: %+v %v", visiblePage, err)
	}
	agent := f.save(Entity{Kind: "agent", Title: "Creator"})
	r, err := f.s.SaveRelation(ctx, RelationEdit{Relation: Relation{Type: "created_by", SourceID: work.ID, TargetID: agent.ID}}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.DeleteRelation(ctx, r.ID, r.Version, "Remove relation", nil, f.u); err != nil {
		t.Fatal(err)
	}
	rp, err := f.s.Revisions(ctx, r.ID, RevisionQuery{}, nil)
	if err != nil || rp.Total != 2 {
		t.Fatalf("deleted relation history: %+v %v", rp, err)
	}
	if _, err = f.s.DB.ExecContext(ctx, `UPDATE catalog.entities SET state='deleted' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Revisions(ctx, r.ID, RevisionQuery{}, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("hidden relation endpoint disclosure: %v", err)
	}
}
