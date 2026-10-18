package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/testutil"
)

func TestPullRequestChangesUseLatestFrozenBase(t *testing.T) {
	raw := func(title, language string) json.RawMessage {
		return json.RawMessage(encode(map[string]any{"title": title, "language": language}))
	}
	changes := pullRequestChanges([]CollaborationCommit{
		{Changes: []CommitObject{{Target: "entity", ID: "work", BaseVersion: 1, Base: raw("A", "ja"), Before: raw("A", "ja"), After: raw("B", "ja")}}},
		{Changes: []CommitObject{{Target: "entity", ID: "work", BaseVersion: 2, Base: raw("A", "en"), Before: raw("B", "ja"), After: raw("B", "en")}}},
	})
	if len(changes) != 1 || changes[0].BaseVersion != 2 || !equalDocument(changes[0].Base, raw("A", "en")) || !equalDocument(changes[0].Committed, raw("B", "en")) {
		t.Fatalf("rebase must not attribute upstream language change to the PR: %+v", changes)
	}
	changes = pullRequestChanges([]CollaborationCommit{{Changes: []CommitObject{{Target: "entity", ID: "work", BaseVersion: 3, Base: raw("B", "en"), Before: raw("B", "en"), After: raw("B", "en")}}}})
	if len(changes) != 0 {
		t.Fatalf("base-only rebase is not a proposed fact change: %+v", changes)
	}
}

func TestCollaborationPageBounds(t *testing.T) {
	for _, tc := range []struct{ limit, offset, wantLimit, wantOffset int }{{0, 0, 20, 0}, {-1, -1, 20, 0}, {101, 7, 100, 7}, {5, 3, 5, 3}} {
		limit, offset := collaborationPage(tc.limit, tc.offset)
		if limit != tc.wantLimit || offset != tc.wantOffset {
			t.Fatalf("bounds (%d,%d) = (%d,%d)", tc.limit, tc.offset, limit, offset)
		}
	}
}

type collaborationReadFixture struct {
	t *testing.T
	s *Store
	u User
}

func newCollaborationReadFixture(t *testing.T) collaborationReadFixture {
	t.Helper()
	s := &Store{DB: testutil.Database(t)}
	if err := s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return collaborationReadFixture{t: t, s: s, u: User{ID: uuid.NewString(), Username: "Actual author"}}
}

func (f collaborationReadFixture) workspace() Workspace {
	f.t.Helper()
	w, err := f.s.CreateWorkspace(context.Background(), WorkspaceCreate{Name: "Private copy"}, f.u)
	if err != nil {
		f.t.Fatal(err)
	}
	return w
}

func (f collaborationReadFixture) commit(workspaceID string, parent *string, merged bool, changes ...CommitObject) string {
	f.t.Helper()
	id := uuid.NewString()
	var mergedAt any
	if merged {
		mergedAt = time.Now().UTC()
	}
	if _, err := f.s.DB.Exec(`INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,author_snapshot,workspace_id,parent_id,message,merged_at)
		VALUES($1,$2,$7,'{}','{}',$3,$4,$5,'Authored message',$6)`, id, f.u.ID,
		encode(map[string]any{"username": f.u.Username, "permissions": []string{"private.permission"}}), workspaceID, parent, mergedAt, "read-fixture-"+id); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.s.DB.Exec(`INSERT INTO catalog.commit_authors(commit_id,user_id,name) VALUES($1,$2,$3)`, id, f.u.ID, f.u.Username); err != nil {
		f.t.Fatal(err)
	}
	for _, o := range changes {
		if _, err := f.s.DB.Exec(`INSERT INTO catalog.commit_objects(commit_id,target,target_id,base_version,base_document,before_document,after_document)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, id, o.Target, o.ID, o.BaseVersion, nullableJSON(o.Base), nullableJSON(o.Before), nullableJSON(o.After)); err != nil {
			f.t.Fatal(err)
		}
	}
	if _, err := f.s.DB.Exec(`UPDATE catalog.workspaces SET head_commit_id=$2 WHERE id=$1`, workspaceID, id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func (f collaborationReadFixture) pullRequest(workspaceID, head string, base *string, status string) string {
	f.t.Helper()
	id := uuid.NewString()
	if _, err := f.s.DB.Exec(`INSERT INTO catalog.pull_requests(id,workspace_id,author_id,title,status,source_head_id,source_base_id)
		VALUES($1,$2,$3,'Frozen proposal',$4,$5,$6)`, id, workspaceID, f.u.ID, status, head, base); err != nil {
		f.t.Fatal(err)
	}
	if status == "merged" {
		if _, err := f.s.DB.Exec(`UPDATE catalog.commits SET pull_request_id=$2 WHERE id=$1`, head, id); err != nil {
			f.t.Fatal(err)
		}
	}
	return id
}

func (f collaborationReadFixture) entity(kind string) string {
	f.t.Helper()
	id := uuid.NewString()
	if _, err := f.s.DB.Exec(`INSERT INTO catalog.entities(id,kind,version,title,state,created_by,document)
		VALUES($1,$2,1,'Public target','active',$3,$4)`, id, kind, f.u.ID,
		encode(map[string]any{"id": id, "kind": kind, "title": "Public target", "state": "active", "version": 1})); err != nil {
		f.t.Fatal(err)
	}
	return id
}

func TestCollaborationFrozenReadsAgainstPostgres(t *testing.T) {
	f := newCollaborationReadFixture(t)
	ctx := context.Background()
	w := f.workspace()
	id := uuid.NewString()
	base := json.RawMessage(`{"title":"Base"}`)
	old := f.commit(w.ID, nil, true)
	previous, first := old, ""
	before := base
	for n := 1; n <= 205; n++ {
		after := json.RawMessage(encode(map[string]any{"title": fmt.Sprintf("Proposal %d", n)}))
		previous = f.commit(w.ID, &previous, false, CommitObject{Target: "entity", ID: id, BaseVersion: 1, Base: base, Before: before, After: after})
		if n == 1 {
			first = previous
		}
		before = after
	}
	head := previous
	prID := f.pullRequest(w.ID, head, &old, "closed")
	late := f.commit(w.ID, &head, false, CommitObject{Target: "entity", ID: id, BaseVersion: 1, Base: base, Before: before, After: json.RawMessage(`{"title":"Unpushed"}`)})
	if _, err := f.s.DB.Exec(`INSERT INTO catalog.workspace_objects(workspace_id,target,target_id,base_document,head_document,document)
		VALUES($1,'entity',$2,$3,$3,'{"title":"Private dirty edit"}')`, w.ID, id, string(base)); err != nil {
		t.Fatal(err)
	}
	reviewer := User{ID: uuid.NewString(), Permissions: []string{PermissionReviewManage}}
	p, err := f.s.GetPullRequest(ctx, prID, reviewer)
	if err != nil || len(p.Commits) != 205 || p.Commits[0].ID != first || p.Commits[204].ID != head {
		t.Fatalf("frozen chain must include every submitted commit: count=%d err=%v", len(p.Commits), err)
	}
	if len(p.Changes) != 1 || !equalDocument(p.Changes[0].Document, before) {
		t.Fatalf("later commits and staged edits leaked into closed PR: %+v", p.Changes)
	}
	if _, err := f.s.GetWorkspace(ctx, w.ID, reviewer); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reviewer read private workspace: %v", err)
	}
	if _, err := f.s.WorkspaceCommits(ctx, w.ID, reviewer); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reviewer listed unpushed commits: %v", err)
	}
	if _, err := f.s.GetCollaborationCommit(ctx, late, reviewer); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reviewer read unpushed commit: %v", err)
	}
	if _, err := f.s.GetCollaborationCommit(ctx, head, reviewer); err != nil {
		t.Fatalf("reviewer could not inspect frozen PR commit: %v", err)
	}
	if _, err := f.s.GetPullRequest(ctx, prID, User{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("anonymous caller read closed private PR: %v", err)
	}
	all, err := f.s.WorkspaceCommits(ctx, w.ID, f.u)
	if err != nil || len(all) != 207 {
		t.Fatalf("private commit history truncated: count=%d err=%v", len(all), err)
	}
}

func TestCollaborationPublicReadsAgainstPostgres(t *testing.T) {
	f := newCollaborationReadFixture(t)
	ctx := context.Background()
	w := f.workspace()
	expression, track := f.entity("expression"), f.entity("track")
	snapshot := json.RawMessage(encode(map[string]any{"id": track, "kind": "track", "contents": []any{
		map[string]any{"expression_id": expression, "locator": map[string]any{"note": "Private source detail"}},
	}}))
	commitID := f.commit(w.ID, nil, true, CommitObject{Target: "entity", ID: track, BaseVersion: 1, Base: snapshot, Before: snapshot, After: snapshot})
	prID := f.pullRequest(w.ID, commitID, nil, "merged")
	c, err := f.s.GetCollaborationCommit(ctx, commitID, User{})
	if err != nil || len(c.Authors) != 1 || c.Authors[0].ID != f.u.ID {
		t.Fatalf("public merged commit: %+v err=%v", c, err)
	}
	if _, err := f.s.GetPullRequest(ctx, prID, User{}); err != nil {
		t.Fatalf("public merged PR: %v", err)
	}
	page, err := f.s.ListPullRequestPage(ctx, "merged", w.ID, 20, 0, User{})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("public list and total disagree: %+v err=%v", page, err)
	}
	if _, err := f.s.DB.Exec(`UPDATE catalog.entities SET state='deleted' WHERE id=$1`, expression); err != nil {
		t.Fatal(err)
	}
	for _, viewer := range []User{{}, f.u, {ID: uuid.NewString(), Permissions: []string{PermissionReviewManage}}} {
		if _, err := f.s.GetCollaborationCommit(ctx, commitID, viewer); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("hidden expression leaked through historical commit: %v", err)
		}
		if _, err := f.s.GetPullRequest(ctx, prID, viewer); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("hidden expression leaked through merged PR: %v", err)
		}
	}
	page, err = f.s.ListPullRequestPage(ctx, "merged", w.ID, 20, 0, User{})
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("hidden public PR affected listing or totals: %+v err=%v", page, err)
	}

	// A deleted relation remains inspectable while both endpoint entities are
	// public; deleting either endpoint hides the whole immutable commit.
	source, target := f.entity("agent"), f.entity("work")
	relation := json.RawMessage(encode(map[string]any{"id": uuid.NewString(), "source_id": source, "target_id": target, "type": "created_by"}))
	relationCommit := f.commit(w.ID, &commitID, true, CommitObject{Target: "relation", ID: uuid.NewString(), BaseVersion: 1, Base: relation, Before: relation})
	if _, err := f.s.GetCollaborationCommit(ctx, relationCommit, User{}); err != nil {
		t.Fatalf("public relation deletion should remain readable: %v", err)
	}
	if _, err := f.s.DB.Exec(`UPDATE catalog.entities SET state='merged' WHERE id=$1`, target); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.GetCollaborationCommit(ctx, relationCommit, User{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("relation endpoint leaked through public history: %v", err)
	}
}

func TestCollaborationPublicReadsRejectNoncanonicalUUIDsAgainstPostgres(t *testing.T) {
	f := newCollaborationReadFixture(t)
	ctx := context.Background()
	w := f.workspace()
	expression, track := f.entity("expression"), f.entity("track")
	source, target := f.entity("agent"), f.entity("work")
	for _, invalid := range []string{"not-a-uuid", strings.ToUpper(expression), strings.ReplaceAll(expression, "-", "")} {
		snapshot := json.RawMessage(encode(map[string]any{"kind": "track", "contents": []any{
			map[string]any{"expression_id": invalid},
		}}))
		id := f.commit(w.ID, nil, true, CommitObject{Target: "entity", ID: track, Base: snapshot, After: snapshot})
		if _, err := f.s.GetCollaborationCommit(ctx, id, User{}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("noncanonical inclusion %q must remain private without a cast error: %v", invalid, err)
		}
	}
	for _, endpoints := range []map[string]any{
		{"source_id": "not-a-uuid", "target_id": target},
		{"source_id": source, "target_id": strings.ToUpper(target)},
		{"source_id": source, "target_id": strings.ReplaceAll(target, "-", "")},
	} {
		snapshot := json.RawMessage(encode(endpoints))
		id := f.commit(w.ID, nil, true, CommitObject{Target: "relation", ID: uuid.NewString(), Base: snapshot, After: snapshot})
		if _, err := f.s.GetCollaborationCommit(ctx, id, User{}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("noncanonical endpoints must remain private without a cast error: %v", err)
		}
	}
	for _, tc := range []struct {
		target   string
		snapshot map[string]any
		public   bool
	}{
		{"legacy:missing-entity", map[string]any{"kind": "work"}, false},
		{strings.ToUpper(target), map[string]any{"kind": "work"}, false},
		{"legacy:relation", map[string]any{"source_id": source, "target_id": target, "type": "created_by"}, true},
	} {
		id := f.commit(w.ID, nil, true)
		var revision int64
		if err := f.s.DB.QueryRow(`INSERT INTO catalog.revisions(target_id,version,actor_id,actor_name,edit_note,sources,snapshot)
			VALUES($1,1,$2,'Historical author','Retained history','[]',$3) RETURNING id`, tc.target, f.u.ID, encode(tc.snapshot)).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.DB.Exec(`INSERT INTO catalog.revision_commits(revision_id,commit_id) VALUES($1,$2)`, revision, id); err != nil {
			t.Fatal(err)
		}
		_, err := f.s.GetCollaborationCommit(ctx, id, User{})
		if tc.public && err != nil || !tc.public && !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("legacy target %q visibility changed: %v", tc.target, err)
		}
	}
}

func TestCollaborationPaginationAgainstPostgres(t *testing.T) {
	f := newCollaborationReadFixture(t)
	ctx := context.Background()
	for n := 0; n < 23; n++ {
		w := f.workspace()
		head := f.commit(w.ID, nil, false)
		f.pullRequest(w.ID, head, nil, "open")
	}
	page, err := f.s.ListWorkspacePage(ctx, 0, 0, f.u)
	if err != nil || page.Limit != 20 || page.Total != 23 || len(page.Items) != 20 {
		t.Fatalf("first workspace page: %+v err=%v", page, err)
	}
	last, err := f.s.ListWorkspacePage(ctx, 20, 20, f.u)
	if err != nil || last.Total != 23 || len(last.Items) != 3 || last.Items[0].ID == page.Items[0].ID {
		t.Fatalf("last workspace page: %+v err=%v", last, err)
	}
	all, err := f.s.ListWorkspaces(ctx, f.u)
	if err != nil || len(all) != 23 {
		t.Fatalf("internal workspace listing truncated: count=%d err=%v", len(all), err)
	}
	prs, err := f.s.ListPullRequestPage(ctx, "open", "", 20, 20, f.u)
	if err != nil || prs.Total != 23 || len(prs.Items) != 3 || prs.Offset != 20 {
		t.Fatalf("filtered PR page: %+v err=%v", prs, err)
	}
	filtered, err := f.s.ListPullRequestPage(ctx, "open", prs.Items[0].WorkspaceID, 20, 0, f.u)
	if err != nil || filtered.Total != 1 || len(filtered.Items) != 1 {
		t.Fatalf("workspace-filtered PR page: %+v err=%v", filtered, err)
	}
	public, err := f.s.ListPullRequestPage(ctx, "", "", 20, 0, User{})
	if err != nil || public.Total != 0 || len(public.Items) != 0 {
		t.Fatalf("private PR activity leaked to anonymous list: %+v err=%v", public, err)
	}
	allPRs, err := f.s.ListPullRequests(ctx, f.u)
	if err != nil || len(allPRs) != 23 {
		t.Fatalf("internal PR listing truncated: count=%d err=%v", len(allPRs), err)
	}
}

func TestPullRequestPushFreezesBaseAndRetriesAgainstPostgres(t *testing.T) {
	f := newCollaborationReadFixture(t)
	ctx := context.Background()
	w := f.workspace()
	base := f.commit(w.ID, nil, true)
	head := f.commit(w.ID, &base, false)
	in := PullRequestCreate{WorkspaceID: w.ID, WorkspacePush: WorkspacePush{ExpectedVersion: 1, ExpectedHead: head}}
	p, err := f.s.CreatePullRequest(ctx, in, f.u)
	if err != nil || p.Title != "Authored message" || p.SourceBaseID == nil || *p.SourceBaseID != base || len(p.Commits) != 1 {
		t.Fatalf("new PR did not freeze the submitted range: %+v err=%v", p, err)
	}
	retried, err := f.s.CreatePullRequest(ctx, in, f.u)
	if err != nil || retried.ID != p.ID || retried.Version != p.Version {
		t.Fatalf("same head retry changed PR identity or version: %+v err=%v", retried, err)
	}
	title := "Reviewer-friendly title"
	p, err = f.s.EditPullRequest(ctx, p.ID, PullRequestEdit{ExpectedVersion: p.Version, Title: &title}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	retried, err = f.s.CreatePullRequest(ctx, in, f.u)
	if err != nil || retried.Title != title || retried.Version != p.Version {
		t.Fatalf("retry with no title reset an edited title: %+v err=%v", retried, err)
	}
}
