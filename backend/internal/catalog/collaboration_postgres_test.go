package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func reviewEditor() User {
	u := fixtureUser("editor")
	u.Permissions = []string{PermissionEntityEdit, PermissionRelationEdit}
	return u
}
func factCounts(t *testing.T, s *Store) string {
	t.Helper()
	counts := []int64{}
	for _, table := range []string{"entities", "relations", "revisions", "outbox"} {
		var n int64
		if err := s.DB.QueryRow("SELECT count(*) FROM catalog." + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts = append(counts, n)
	}
	return encode(counts)
}
func testWorkspace(t *testing.T, f fixture, u User, entities ...Entity) Workspace {
	t.Helper()
	ids := []string{}
	for _, e := range entities {
		ids = append(ids, e.ID)
	}
	w, err := f.s.CreateWorkspace(context.Background(), WorkspaceCreate{Name: "Collaborative edit", EntityIDs: ids}, u)
	if err != nil {
		t.Fatal(err)
	}
	return w
}
func testWorkspaceSave(t *testing.T, f fixture, w Workspace, u User, ops ...WorkspaceOperation) WorkspaceChangesResult {
	t.Helper()
	out, err := f.s.SaveWorkspaceChanges(context.Background(), w.ID, WorkspaceChanges{ExpectedVersion: w.Version, Operations: ops}, u)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func testWorkspaceCommit(t *testing.T, f fixture, w Workspace, u User, message string) (Workspace, CollaborationCommit) {
	t.Helper()
	c, err := f.s.CommitWorkspace(context.Background(), w.ID, WorkspaceCommitCreate{ID: uuid.NewString(), ExpectedVersion: w.Version, Message: message}, u)
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.s.GetWorkspace(context.Background(), w.ID, u)
	if err != nil {
		t.Fatal(err)
	}
	return current, c
}
func workspaceUpdate(e Entity, patch ...CommitPatch) WorkspaceOperation {
	return WorkspaceOperation{Target: "entity", Action: "update", ID: e.ID, Patch: patch}
}
func pushWorkspaceInput(w Workspace) WorkspacePush {
	in := WorkspacePush{ExpectedVersion: w.Version, Title: w.Name}
	if w.HeadCommitID != nil {
		in.ExpectedHead = *w.HeadCommitID
	}
	return in
}

func TestPostgresWorkspaceSavesCommitsAndAtomicPush(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Original"})
	w := testWorkspace(t, f, f.u, work)
	before := factCounts(t, f.s)
	saved := testWorkspaceSave(t, f, w, f.u,
		workspaceUpdate(work, patchValue("/title", "First local edit")),
		WorkspaceOperation{Target: "entity", Action: "create", Ref: "agent", Document: json.RawMessage(`{"kind":"agent","title":"Author"}`)},
	)
	if saved.Refs["agent"] == "" || saved.Workspace.DirtyCount != 2 {
		t.Fatalf("save: %+v", saved)
	}
	saved = testWorkspaceSave(t, f, saved.Workspace, f.u,
		workspaceUpdate(work, patchValue("/title", "Final local edit")),
		WorkspaceOperation{Target: "relation", Action: "create", Ref: "credit", Document: json.RawMessage(encode(Relation{Type: "written_by", SourceID: work.ID, TargetID: saved.Refs["agent"]}))},
	)
	if factCounts(t, f.s) != before {
		t.Fatal("saving a copy changed official metadata")
	}
	w, c := testWorkspaceCommit(t, f, saved.Workspace, f.u, "Update title and author")
	if len(c.Changes) != 3 || c.Author.ID != f.u.ID || c.MergedAt != nil {
		t.Fatalf("commit: %+v", c)
	}
	if factCounts(t, f.s) != before {
		t.Fatal("committing a copy changed official metadata")
	}
	var beforeTitle, afterTitle string
	for _, o := range c.Changes {
		if o.Target == "entity" && o.ID == work.ID {
			var a, b Entity
			_ = json.Unmarshal(o.Before, &a)
			_ = json.Unmarshal(o.After, &b)
			beforeTitle = a.Title
			afterTitle = b.Title
		}
	}
	if beforeTitle != "Original" || afterTitle != "Final local edit" {
		t.Fatalf("intermediate edits were not coalesced: %s -> %s", beforeTitle, afterTitle)
	}
	out, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), f.u)
	if err != nil || !out.Applied {
		t.Fatalf("push: %+v %v", out, err)
	}
	current, err := f.s.Get(ctx, work.ID, nil)
	if err != nil || current.Title != "Final local edit" || current.Version != work.Version+1 {
		t.Fatalf("head: %+v %v", current, err)
	}
	rels, err := f.s.Relations(ctx, work.ID, nil)
	if err != nil || len(rels) != 1 || rels[0].Type != "written_by" {
		t.Fatalf("relations: %+v %v", rels, err)
	}
	archived, err := f.s.GetCollaborationCommit(ctx, c.ID, User{})
	if err != nil || archived.MergedAt == nil || archived.MergedBy == nil || *archived.MergedBy != f.u.ID {
		t.Fatalf("public merged commit: %+v %v", archived, err)
	}
	mergedFacts := factCounts(t, f.s)
	replayed, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), f.u)
	if err != nil || !replayed.Applied || factCounts(t, f.s) != mergedFacts {
		t.Fatalf("merged head replay changed facts: %+v %v", replayed, err)
	}
}

func TestPostgresWorkspaceCommitIdempotencyAndCollaboratorAuthors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Collaborative work"})
	owner := reviewEditor()
	collaborator := reviewEditor()
	stranger := reviewEditor()
	w := testWorkspace(t, f, owner, work)
	if _, err := f.s.GetWorkspace(ctx, w.ID, stranger); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("private copy: %v", err)
	}
	var err error
	w, err = f.s.SetWorkspaceMembers(ctx, w.ID, WorkspaceMembersEdit{ExpectedVersion: w.Version, UserIDs: []string{collaborator.ID}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	saved := testWorkspaceSave(t, f, w, owner, workspaceUpdate(work, patchValue("/attributes/tags", []string{"owner edit"})))
	request := WorkspaceCommitCreate{ID: uuid.NewString(), ExpectedVersion: saved.Workspace.Version, Message: "Owner commit"}
	first, err := f.s.CommitWorkspace(ctx, w.ID, request, owner)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.s.CommitWorkspace(ctx, w.ID, request, owner)
	if err != nil || replay.ID != first.ID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	request.Message = "Different immutable content"
	if _, err = f.s.CommitWorkspace(ctx, w.ID, request, owner); !errors.Is(err, errIdempotencyConflict) {
		t.Fatalf("commit id reused: %v", err)
	}
	w, err = f.s.GetWorkspace(ctx, w.ID, collaborator)
	if err != nil {
		t.Fatal(err)
	}
	saved = testWorkspaceSave(t, f, w, collaborator, workspaceUpdate(work, patchValue("/title", "Collaborator title")))
	w, second := testWorkspaceCommit(t, f, saved.Workspace, collaborator, "Collaborator commit")
	if second.ParentID == nil || *second.ParentID != first.ID || second.Author.ID != collaborator.ID {
		t.Fatalf("author/parent: %+v", second)
	}
	if _, err = f.s.GetCollaborationCommit(ctx, second.ID, User{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unmerged commit leaked: %v", err)
	}
	out, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), owner)
	if err != nil || out.Applied || out.PullRequest == nil {
		t.Fatalf("PR push: %+v %v", out, err)
	}
	pr := out.PullRequest
	if _, err = f.s.MergePullRequest(ctx, pr.ID, PullRequestMerge{ExpectedVersion: pr.Version, ExpectedHead: pr.SourceHeadID}, owner); !errors.Is(err, errForbidden) {
		t.Fatalf("owner merged without permission: %v", err)
	}
	out, err = f.s.MergePullRequest(ctx, pr.ID, PullRequestMerge{ExpectedVersion: pr.Version, ExpectedHead: pr.SourceHeadID}, f.u)
	if err != nil || !out.Applied {
		t.Fatalf("merge: %+v %v", out, err)
	}
	for _, expected := range []CollaborationCommit{first, second} {
		got, err := f.s.GetCollaborationCommit(ctx, expected.ID, User{})
		if err != nil || got.Author.ID != expected.Author.ID || got.MergedBy == nil || *got.MergedBy != f.u.ID {
			t.Fatalf("lost author: %+v %v", got, err)
		}
		var associations int
		if err = f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.revision_commits WHERE commit_id=$1`, got.ID).Scan(&associations); err != nil || associations != 1 {
			t.Fatalf("revision provenance: %d %v", associations, err)
		}
	}
	w, err = f.s.GetWorkspace(ctx, w.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	_ = testWorkspaceSave(t, f, w, owner, workspaceUpdate(work, patchValue("/title", "Private staged title after merge")))
	replayed, err := f.s.MergePullRequest(ctx, pr.ID, PullRequestMerge{ExpectedVersion: pr.Version, ExpectedHead: pr.SourceHeadID}, f.u)
	if err != nil || !replayed.Applied || replayed.Workspace != nil || strings.Contains(encode(replayed), "Private staged title after merge") {
		t.Fatalf("reviewer replay exposed the owner's later worktree: %+v %v", replayed, err)
	}

}

func TestPostgresWorkspaceMergeAndConflictResolution(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Baseline", Attributes: map[string]any{"language": "ja"}})
	mine := testWorkspace(t, f, f.u, work)
	remote := testWorkspace(t, f, f.u, work)
	saved := testWorkspaceSave(t, f, mine, f.u, workspaceUpdate(work, patchValue("/attributes/language", "en")))
	mine, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "Change language")
	saved = testWorkspaceSave(t, f, remote, f.u, workspaceUpdate(work, patchValue("/title", "Remote title")))
	remote, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "Change title")
	if _, err := f.s.PushWorkspace(ctx, remote.ID, pushWorkspaceInput(remote), f.u); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.PushWorkspace(ctx, mine.ID, pushWorkspaceInput(mine), f.u); err != nil {
		t.Fatalf("disjoint fields must merge: %v", err)
	}
	head, err := f.s.Get(ctx, work.ID, nil)
	if err != nil || head.Title != "Remote title" || head.Attributes["language"] != "en" {
		t.Fatalf("merged result: %+v %v", head, err)
	}
	mine = testWorkspace(t, f, f.u, head)
	remote = testWorkspace(t, f, f.u, head)
	saved = testWorkspaceSave(t, f, mine, f.u, workspaceUpdate(head, patchValue("/title", "My title")))
	mine, oldCommit := testWorkspaceCommit(t, f, saved.Workspace, f.u, "My title")
	saved = testWorkspaceSave(t, f, remote, f.u, workspaceUpdate(head, patchValue("/title", "Current title")))
	remote, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "Current title")
	if _, err = f.s.PushWorkspace(ctx, remote.ID, pushWorkspaceInput(remote), f.u); err != nil {
		t.Fatal(err)
	}
	before := factCounts(t, f.s)
	_, err = f.s.PushWorkspace(ctx, mine.ID, pushWorkspaceInput(mine), f.u)
	var conflict *workspaceConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 || factCounts(t, f.s) != before {
		t.Fatalf("conflict: %+v %v", conflict, err)
	}
	c := conflict.Conflicts[0]
	if c.Pointer != "/title" || c.Base != "Remote title" || c.Current != "Current title" || c.Proposed != "My title" {
		t.Fatalf("three-way values: %+v", c)
	}
	rebased, err := f.s.RebaseWorkspace(ctx, mine.ID, WorkspaceRebase{ExpectedVersion: mine.Version, ExpectedHead: *mine.HeadCommitID, ID: uuid.NewString(), Message: "Resolve title", Resolutions: []WorkspaceResolution{{Target: "entity", ID: head.ID, Pointer: c.Pointer, CurrentVersion: c.CurrentVersion, Value: json.RawMessage(`"Resolved title"`)}}}, f.u)
	if err != nil || rebased.Commit == nil || rebased.Commit.Kind != "edit" {
		t.Fatalf("rebase: %+v %v", rebased, err)
	}
	preserved, err := f.s.GetCollaborationCommit(ctx, oldCommit.ID, f.u)
	if err != nil || string(preserved.Changes[0].After) != string(oldCommit.Changes[0].After) {
		t.Fatalf("old commit was rewritten: %+v %v", preserved, err)
	}
	if _, err = f.s.PushWorkspace(ctx, mine.ID, pushWorkspaceInput(rebased.Workspace), f.u); err != nil {
		t.Fatal(err)
	}
	head, err = f.s.Get(ctx, work.ID, nil)
	if err != nil || head.Title != "Resolved title" {
		t.Fatalf("resolved head: %+v %v", head, err)
	}
}

func TestPostgresWorkspaceCompareAndSwapPreventsLostEdits(t *testing.T) {
	f := newFixture(t)
	work := f.save(Entity{Kind: "work", Title: "CAS"})
	w := testWorkspace(t, f, f.u, work)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, title := range []string{"First tab", "Second tab"} {
		wg.Add(1)
		go func(title string) {
			defer wg.Done()
			<-start
			_, err := f.s.SaveWorkspaceChanges(context.Background(), w.ID, WorkspaceChanges{ExpectedVersion: w.Version, Operations: []WorkspaceOperation{workspaceUpdate(work, patchValue("/title", title))}}, f.u)
			errs <- err
		}(title)
	}
	close(start)
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else if err.Error() == "workspace_version_conflict" {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS successes=%d conflicts=%d", successes, conflicts)
	}
	current, err := f.s.Get(context.Background(), work.ID, nil)
	if err != nil || current.Title != "CAS" {
		t.Fatalf("save mutated head: %+v %v", current, err)
	}
}

func TestCatalogOnlyExposesWorkspaceFactWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	HTTP{Store: &Store{}}.Register(r)
	routes := map[string]bool{}
	for _, route := range r.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, old := range []string{"POST /api/catalog/entities", "PUT /api/catalog/entities/:id", "POST /api/catalog/relations", "PUT /api/catalog/relations/:id", "DELETE /api/catalog/relations/:id", "POST /api/catalog/tracks/:id/contents", "PUT /api/catalog/tracks/:id/contents/:position", "DELETE /api/catalog/tracks/:id/contents/:position", "POST /api/catalog/commits", "POST /api/catalog/commits/preview", "POST /api/catalog/checkout", "POST /api/catalog/commits/:id/review", "GET /api/catalog/commits/:id/submission"} {
		if routes[old] {
			t.Fatalf("old fact write/review route still registered: %s", old)
		}
	}
	for _, route := range []string{"POST /api/catalog/workspaces", "PUT /api/catalog/workspaces/:id/changes", "POST /api/catalog/workspaces/:id/commits", "POST /api/catalog/workspaces/:id/push", "POST /api/catalog/workspaces/:id/rebase", "POST /api/catalog/pull-requests/:id/merge"} {
		if !routes[route] {
			t.Fatalf("missing route: %s", route)
		}
		parts := strings.SplitN(route, " ", 2)
		path := strings.ReplaceAll(parts[1], ":id", uuid.NewString())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(parts[0], path, strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("write lacks auth: %s %d %s", route, w.Code, w.Body.String())
		}
	}
}

func TestPostgresWorkspaceStableIDsAndForwardParentReference(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w := testWorkspace(t, f, f.u)
	saved := testWorkspaceSave(t, f, w, f.u, WorkspaceOperation{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(`{"kind":"work","title":"Stable parent work"}`)})
	workID := saved.Refs["work"]
	w, first := testWorkspaceCommit(t, f, saved.Workspace, f.u, "Create work")
	saved = testWorkspaceSave(t, f, w, f.u,
		WorkspaceOperation{Target: "entity", Action: "create", Ref: "child", Document: json.RawMessage(`{"kind":"content_unit","title":"Child first","work_id":"` + workID + `","parent_id":{"$ref":"parent"},"position":1}`)},
		WorkspaceOperation{Target: "entity", Action: "create", Ref: "parent", Document: json.RawMessage(`{"kind":"content_unit","title":"Parent later","work_id":"` + workID + `","position":1}`)},
	)
	childID, parentID := saved.Refs["child"], saved.Refs["parent"]
	w, second := testWorkspaceCommit(t, f, saved.Workspace, f.u, "Create nested units")
	if second.ParentID == nil || *second.ParentID != first.ID {
		t.Fatal("commit chain lost")
	}
	before := factCounts(t, f.s)
	if _, err := f.s.Get(ctx, workID, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unmerged work visible: %v", err)
	}
	out, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), f.u)
	if err != nil || !out.Applied {
		t.Fatalf("forward-reference push: %+v %v", out, err)
	}
	if before == factCounts(t, f.s) {
		t.Fatal("push produced no facts")
	}
	child, err := f.s.Get(ctx, childID, nil)
	if err != nil || child.ParentID != parentID || child.WorkID != workID {
		t.Fatalf("child: %+v %v", child, err)
	}
	parent, err := f.s.Get(ctx, parentID, nil)
	if err != nil || parent.WorkID != workID {
		t.Fatalf("parent: %+v %v", parent, err)
	}
}

func TestPostgresWorkspaceSharedStagingRetainsEveryAuthor(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Shared staging"})
	owner := reviewEditor()
	collaborator := reviewEditor()
	w := testWorkspace(t, f, owner, work)
	var err error
	w, err = f.s.SetWorkspaceMembers(ctx, w.ID, WorkspaceMembersEdit{ExpectedVersion: w.Version, UserIDs: []string{collaborator.ID}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	saved := testWorkspaceSave(t, f, w, owner, workspaceUpdate(work, patchValue("/attributes/tags", []string{"owner staged"})))
	saved = testWorkspaceSave(t, f, saved.Workspace, collaborator, workspaceUpdate(work, patchValue("/title", "Collaborator staged")))
	w, c := testWorkspaceCommit(t, f, saved.Workspace, collaborator, "Commit shared changes")
	authors := map[string]bool{}
	for _, a := range c.Authors {
		authors[a.ID] = true
	}
	if c.Author.ID != collaborator.ID || !authors[owner.ID] || !authors[collaborator.ID] || len(authors) != 2 {
		t.Fatalf("staging authors lost: %+v", c)
	}
	staleVersion := w.Version - 1
	if _, err = f.s.PushWorkspace(ctx, w.ID, WorkspacePush{ExpectedVersion: staleVersion, ExpectedHead: *w.HeadCommitID}, owner); err == nil || err.Error() != "workspace_version_conflict" {
		t.Fatalf("new head bypassed CAS: %v", err)
	}
	out, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), owner)
	if err != nil || out.PullRequest == nil {
		t.Fatalf("push: %+v %v", out, err)
	}
	pr := out.PullRequest
	out, err = f.s.MergePullRequest(ctx, pr.ID, PullRequestMerge{ExpectedVersion: pr.Version, ExpectedHead: pr.SourceHeadID}, f.u)
	if err != nil || !out.Applied {
		t.Fatalf("merge: %+v %v", out, err)
	}
	merged, err := f.s.GetCollaborationCommit(ctx, c.ID, User{})
	if err != nil || len(merged.Authors) != 2 {
		t.Fatalf("public authors: %+v %v", merged, err)
	}
}

func TestPostgresPullRequestRequiresRepushAfterNewCommit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Frozen PR base"})
	editor := reviewEditor()
	w := testWorkspace(t, f, editor, work)
	saved := testWorkspaceSave(t, f, w, editor, workspaceUpdate(work, patchValue("/title", "First pushed title")))
	w, first := testWorkspaceCommit(t, f, saved.Workspace, editor, "First commit")
	out, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), editor)
	if err != nil || out.PullRequest == nil {
		t.Fatalf("push: %+v %v", out, err)
	}
	pr := *out.PullRequest
	saved = testWorkspaceSave(t, f, w, editor, workspaceUpdate(work, patchValue("/title", "Second unpushed title")))
	w, second := testWorkspaceCommit(t, f, saved.Workspace, editor, "Second commit")
	frozen, err := f.s.GetPullRequest(ctx, pr.ID, editor)
	if err != nil || frozen.SourceHeadID != first.ID {
		t.Fatalf("PR head mutated without push: %+v %v", frozen, err)
	}
	if len(frozen.Changes) != 1 {
		t.Fatalf("frozen PR changes: %+v", frozen.Changes)
	}
	var frozenWork Entity
	if err = json.Unmarshal(frozen.Changes[0].Document, &frozenWork); err != nil || frozenWork.Title != "First pushed title" {
		t.Fatalf("PR preview changed without push: %+v %v", frozenWork, err)
	}
	if _, err = f.s.MergePullRequest(ctx, pr.ID, PullRequestMerge{ExpectedVersion: pr.Version, ExpectedHead: pr.SourceHeadID}, f.u); err == nil || err.Error() != "workspace_head_conflict" {
		t.Fatalf("merged unpushed head: %v", err)
	}
	head, err := f.s.Get(ctx, work.ID, nil)
	if err != nil || head.Title != "Frozen PR base" {
		t.Fatalf("failed merge wrote fact: %+v %v", head, err)
	}
	out, err = f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), editor)
	if err != nil || out.PullRequest == nil || out.PullRequest.ID != pr.ID || out.PullRequest.SourceHeadID != second.ID {
		t.Fatalf("repush: %+v %v", out, err)
	}
	if _, err = f.s.MergePullRequest(ctx, pr.ID, PullRequestMerge{ExpectedVersion: pr.Version, ExpectedHead: pr.SourceHeadID}, f.u); err == nil || err.Error() != "pull_request_version_conflict" {
		t.Fatalf("old reviewed version merged: %v", err)
	}
	updated := out.PullRequest
	if _, err = f.s.MergePullRequest(ctx, updated.ID, PullRequestMerge{ExpectedVersion: updated.Version, ExpectedHead: updated.SourceHeadID}, f.u); err != nil {
		t.Fatal(err)
	}
	head, err = f.s.Get(ctx, work.ID, nil)
	if err != nil || head.Title != "Second unpushed title" {
		t.Fatalf("merged head: %+v %v", head, err)
	}
}

func TestPostgresWorkspaceBypassPATCannotEditAnotherOwnersRelations(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	source := f.save(Entity{Kind: "work", Title: "Another owner source"})
	owner := fixtureUser("editor")
	owner.Permissions = append(owner.Permissions, PermissionRelationEdit)
	target, err := f.s.Save(ctx, Edit{Entity: Entity{Kind: "agent", Title: "PAT owner target"}, EditNote: "Fixture"}, owner)
	if err != nil {
		t.Fatal(err)
	}
	pat := User{ID: owner.ID, Username: owner.Username, FromPAT: true, CatalogReviewExempt: true, Permissions: []string{PermissionRelationEdit}}
	w := testWorkspace(t, f, owner)
	unauthorized := WorkspaceOperation{Target: "relation", Action: "create", Ref: "credit", Document: json.RawMessage(encode(Relation{Type: "written_by", SourceID: source.ID, TargetID: target.ID}))}
	if _, err = f.s.SaveWorkspaceChanges(ctx, w.ID, WorkspaceChanges{ExpectedVersion: w.Version, Operations: []WorkspaceOperation{unauthorized}}, pat); !errors.Is(err, errForbidden) {
		t.Fatalf("staging ignored PAT relation scope: %v", err)
	}
	// A more capable session can stage the change, but the pushing PAT must
	// still prove the same operation rights at merge time.
	saved := testWorkspaceSave(t, f, w, owner, unauthorized)
	w, _ = testWorkspaceCommit(t, f, saved.Workspace, owner, "Try another owner's source")
	before := factCounts(t, f.s)
	if _, err = f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), pat); !errors.Is(err, errForbidden) || factCounts(t, f.s) != before {
		t.Fatalf("bypass inflated operation rights: %v", err)
	}
	ownSource, err := f.s.Save(ctx, Edit{Entity: Entity{Kind: "work", Title: "PAT own source"}, EditNote: "Fixture"}, owner)
	if err != nil {
		t.Fatal(err)
	}
	w = testWorkspace(t, f, pat)
	saved = testWorkspaceSave(t, f, w, pat, WorkspaceOperation{Target: "relation", Action: "create", Ref: "credit", Document: json.RawMessage(encode(Relation{Type: "written_by", SourceID: ownSource.ID, TargetID: target.ID}))})
	w, _ = testWorkspaceCommit(t, f, saved.Workspace, pat, "Maintain own source")
	if out, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), pat); err != nil || !out.Applied {
		t.Fatalf("legitimate relation PAT denied: %+v %v", out, err)
	}
}

func TestPostgresWorkspacePushIncludesMoreThanTwoHundredCommits(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w := testWorkspace(t, f, f.u)
	saved := testWorkspaceSave(t, f, w, f.u, WorkspaceOperation{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(`{"kind":"work","title":"Commit 0"}`)})
	id := saved.Refs["work"]
	w, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "Create work")
	const total = 205
	for i := 1; i < total; i++ {
		saved = testWorkspaceSave(t, f, w, f.u, WorkspaceOperation{Target: "entity", Action: "update", ID: id, Patch: []CommitPatch{patchValue("/title", fmt.Sprintf("Commit %d", i))}})
		w, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, fmt.Sprintf("Edit %d", i))
	}
	commits, err := f.s.WorkspaceCommits(ctx, w.ID, f.u)
	if err != nil || len(commits) != total {
		t.Fatalf("history truncated: %d %v", len(commits), err)
	}
	out, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), f.u)
	if err != nil || !out.Applied || len(out.Commits) != total {
		t.Fatalf("push truncated pending commits: %d %v", len(out.Commits), err)
	}
	var merged int
	if err = f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.commits WHERE workspace_id=$1 AND merged_at IS NOT NULL`, w.ID).Scan(&merged); err != nil || merged != total {
		t.Fatalf("merged count: %d %v", merged, err)
	}
	head, err := f.s.Get(ctx, id, nil)
	if err != nil || head.Title != "Commit 204" || head.Version != 1 {
		t.Fatalf("net head: %+v %v", head, err)
	}
}

func TestPostgresWorkspaceResolutionCreditsOnlyNewChanges(t *testing.T) {
	for _, keepCurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("take_current=%v", keepCurrent), func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			author := fixtureUser("editor")
			work := f.save(Entity{Kind: "work", Title: "Resolution baseline"})
			mine := testWorkspace(t, f, author, work)
			remote := testWorkspace(t, f, f.u, work)
			saved := testWorkspaceSave(t, f, mine, author, workspaceUpdate(work, patchValue("/title", "Local proposal")))
			mine, _ = testWorkspaceCommit(t, f, saved.Workspace, author, "Local proposal")
			saved = testWorkspaceSave(t, f, remote, f.u, workspaceUpdate(work, patchValue("/title", "Upstream title")))
			remote, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "Upstream")
			if _, err := f.s.PushWorkspace(ctx, remote.ID, pushWorkspaceInput(remote), f.u); err != nil {
				t.Fatal(err)
			}
			_, err := f.s.PushWorkspace(ctx, mine.ID, pushWorkspaceInput(mine), author)
			var conflict *workspaceConflictError
			if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 {
				t.Fatalf("expected title conflict: %v", err)
			}
			c := conflict.Conflicts[0]
			title := "Manual new title"
			if keepCurrent {
				title = "Upstream title"
			}
			rebased, err := f.s.RebaseWorkspace(ctx, mine.ID, WorkspaceRebase{ExpectedVersion: mine.Version, ExpectedHead: *mine.HeadCommitID, ID: uuid.NewString(), Message: "Resolve divergence", Resolutions: []WorkspaceResolution{{Target: "entity", ID: work.ID, Pointer: c.Pointer, CurrentVersion: c.CurrentVersion, Value: json.RawMessage(encode(title))}}}, author)
			if err != nil || rebased.Commit == nil {
				t.Fatalf("resolve: %+v %v", rebased, err)
			}
			if !keepCurrent && rebased.Commit.Kind != "edit" {
				t.Fatalf("manual edit lost edit attribution: %+v", rebased.Commit)
			}
			if _, err = f.s.PushWorkspace(ctx, mine.ID, pushWorkspaceInput(rebased.Workspace), author); err != nil {
				t.Fatal(err)
			}
			contributions, err := f.s.UserContributions(ctx, author.ID, ContributionQuery{Year: 0, Page: 1, PageSize: 20})
			expected := int64(1)
			if keepCurrent {
				expected = 0
			}
			if err != nil || contributions.Summary.MergedCommits != expected {
				t.Fatalf("credits: %+v %v want %d", contributions, err, expected)
			}
			head, err := f.s.Get(ctx, work.ID, nil)
			if err != nil || head.Title != title {
				t.Fatalf("resolved head: %+v %v", head, err)
			}
		})
	}
}

func TestPostgresReadOnlyPATCannotCommitPushOrResolveSharedChanges(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	owner := reviewEditor()
	member := reviewEditor()
	work := f.save(Entity{Kind: "work", Title: "Shared protected title"})
	w := testWorkspace(t, f, owner, work)
	var err error
	w, err = f.s.SetWorkspaceMembers(ctx, w.ID, WorkspaceMembersEdit{ExpectedVersion: w.Version, UserIDs: []string{member.ID}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	saved := testWorkspaceSave(t, f, w, owner, workspaceUpdate(work, patchValue("/title", "Staged by owner")))
	readonly := User{ID: member.ID, Username: member.Username, FromPAT: true, Permissions: []string{}}
	commitID := uuid.NewString()
	if _, err = f.s.CommitWorkspace(ctx, w.ID, WorkspaceCommitCreate{ID: commitID, ExpectedVersion: saved.Workspace.Version, Message: "Readonly tries committing owner changes"}, readonly); !errors.Is(err, errForbidden) {
		t.Fatalf("readonly PAT committed staged changes: %v", err)
	}
	if _, err = f.s.GetCollaborationCommit(ctx, commitID, owner); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("forbidden commit persisted: %v", err)
	}
	w, original := testWorkspaceCommit(t, f, saved.Workspace, owner, "Owner proposal")
	before := factCounts(t, f.s)
	if _, err = f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), readonly); !errors.Is(err, errForbidden) || factCounts(t, f.s) != before {
		t.Fatalf("readonly PAT pushed another author's commit: %v", err)
	}
	var requests int
	if err = f.s.DB.QueryRowContext(ctx, `SELECT count(*) FROM catalog.pull_requests WHERE workspace_id=$1`, w.ID).Scan(&requests); err != nil || requests != 0 {
		t.Fatalf("unauthorized push created PR: %d %v", requests, err)
	}
	work.Title = "Upstream protected title"
	current, err := f.s.Save(ctx, Edit{Entity: work, ExpectedVersion: work.Version, EditNote: "Upstream fixture"}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	resolution := WorkspaceRebase{ExpectedVersion: w.Version, ExpectedHead: original.ID, ID: uuid.NewString(), Message: "Readonly tries resolution", Resolutions: []WorkspaceResolution{{Target: "entity", ID: work.ID, Pointer: "/title", CurrentVersion: current.Version, Value: json.RawMessage(`"Unauthorized resolved title"`)}}}
	if _, err = f.s.RebaseWorkspace(ctx, w.ID, resolution, readonly); !errors.Is(err, errForbidden) {
		t.Fatalf("readonly PAT resolved another author's commit: %v", err)
	}
	unchanged, err := f.s.GetWorkspace(ctx, w.ID, owner)
	if err != nil || unchanged.Version != w.Version || unchanged.HeadCommitID == nil || *unchanged.HeadCommitID != original.ID {
		t.Fatalf("forbidden resolution changed copy: %+v %v", unchanged, err)
	}
}

func TestPostgresWorkspaceDynamicMutualReferencesAreNotStructuralCycles(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	definition, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	d := definition.Document
	d.Fields["peer_work"] = Field{Names: names("相关作品", "Related work"), Type: "entity", Kinds: []string{"work"}, ApplicableKinds: []string{"work"}, Enabled: true}
	d.Fields["uuid_note"] = Field{Names: names("原始备注", "Original note"), Type: "text", ApplicableKinds: []string{"work"}, Enabled: true}
	f.publish(d, definition.ETag)
	w := testWorkspace(t, f, f.u)
	saved := testWorkspaceSave(t, f, w, f.u,
		WorkspaceOperation{Target: "entity", Action: "create", Ref: "first", Document: json.RawMessage(`{"kind":"work","title":"First peer"}`)},
		WorkspaceOperation{Target: "entity", Action: "create", Ref: "second", Document: json.RawMessage(`{"kind":"work","title":"Second peer"}`)},
	)
	firstID, secondID := saved.Refs["first"], saved.Refs["second"]
	w, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "Create peer works")
	saved = testWorkspaceSave(t, f, w, f.u,
		WorkspaceOperation{Target: "entity", Action: "update", ID: firstID, Patch: []CommitPatch{patchValue("/attributes/peer_work", secondID), patchValue("/attributes/uuid_note", secondID)}},
		WorkspaceOperation{Target: "entity", Action: "update", ID: secondID, Patch: []CommitPatch{patchValue("/attributes/peer_work", firstID), patchValue("/attributes/uuid_note", firstID)}},
	)
	w, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "Link the two works")
	if out, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), f.u); err != nil || !out.Applied {
		t.Fatalf("legal dynamic mutual reference rejected: %+v %v", out, err)
	}
	for id, peer := range map[string]string{firstID: secondID, secondID: firstID} {
		head, err := f.s.Get(ctx, id, nil)
		if err != nil || head.Attributes["peer_work"] != peer || head.Attributes["uuid_note"] != peer {
			t.Fatalf("reference lost: %+v %v", head, err)
		}
	}
}
