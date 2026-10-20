package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/migrator"
	"github.com/metafusion/metafusion-app/internal/testutil"
	"github.com/metafusion/metafusion-app/migrations"
)

func versionAction(t *testing.T, f fixture, w Workspace, op string, ids ...string) WorkspaceVersionResult {
	t.Helper()
	head := ""
	if w.HeadCommitID != nil {
		head = *w.HeadCommitID
	}
	out, err := f.s.ApplyWorkspaceVersion(context.Background(), w.ID, WorkspaceVersionAction{ID: uuid.NewString(), ExpectedVersion: w.Version, ExpectedHead: head, Operation: op, CommitIDs: ids, Message: op}, f.u)
	if err != nil {
		var conflict *workspaceConflictError
		if errors.As(err, &conflict) {
			t.Fatalf("%s: %+v", op, conflict.Conflicts)
		}
		t.Fatal(op, err)
	}
	return out
}

func TestPostgresRecordVersioningRebaseReplay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	author := fixtureUser("editor")
	work := f.save(Entity{Kind: "work", Title: "Original"})
	w := testWorkspace(t, f, f.u, work)
	w, err := f.s.SetWorkspaceMembers(ctx, w.ID, WorkspaceMembersEdit{ExpectedVersion: w.Version, UserIDs: []string{author.ID}}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	saved := testWorkspaceSave(t, f, w, author, workspaceUpdate(work, patchValue("/title", "First")))
	w, first := testWorkspaceCommit(t, f, saved.Workspace, author, "first")
	saved = testWorkspaceSave(t, f, w, author, workspaceUpdate(work, patchValue("/translations/en/body", "Body")))
	w, second := testWorkspaceCommit(t, f, saved.Workspace, author, "second")
	remote := testWorkspace(t, f, f.u, work)
	saved = testWorkspaceSave(t, f, remote, f.u, workspaceUpdate(work, patchValue("/translations/en/title", "Upstream translation")))
	remote, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "upstream")
	if _, err = f.s.PushWorkspace(ctx, remote.ID, pushWorkspaceInput(remote), f.u); err != nil {
		t.Fatal(err)
	}
	before := factCounts(t, f.s)
	order := []string{second.ID, first.ID}
	in := WorkspaceRebase{ID: uuid.NewString(), ExpectedVersion: w.Version, ExpectedHead: *w.HeadCommitID, Order: &order}
	rebased, err := f.s.RebaseWorkspace(ctx, w.ID, in, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if factCounts(t, f.s) != before {
		t.Fatal("rebase changed public facts")
	}
	page, err := f.s.VersionHistory(ctx, w.ID, VersionHistoryQuery{VersionReadQuery: VersionReadQuery{Limit: 100, Offset: 0}}, f.u)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("replayed history: %+v %v", page, err)
	}
	for i, c := range page.Items {
		if c.Author.ID != author.ID || c.Committer.ID != f.u.ID || len(c.Authors) != 1 || c.Authors[0].ID != author.ID {
			t.Fatalf("mechanical replay changed attribution: %+v", c)
		}
		if len(c.Changes) != 1 {
			t.Fatalf("replay included unchanged tracked objects: %+v", c)
		}
		if c.SourceIDs[0] != order[1-i] {
			t.Fatal("rebase order ignored")
		}
	}
	again, err := f.s.RebaseWorkspace(ctx, w.ID, in, f.u)
	if err != nil || again.Commit.ID != rebased.Commit.ID || again.Workspace.Version != rebased.Workspace.Version {
		t.Fatal("rebase not idempotent", err)
	}
	if _, err = f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(rebased.Workspace), f.u); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Get(ctx, work.ID, &f.u)
	if err != nil || got.Title != "First" || got.Translations["en"].Title != "Upstream translation" || got.Translations["en"].Body != "Body" {
		t.Fatalf("replayed facts: %+v %v", got, err)
	}
	for _, id := range []string{first.ID, second.ID} {
		c, err := f.s.GetCollaborationCommit(ctx, id, f.u)
		if err != nil || c.MergedAt != nil || len(c.ReplacedBy) != 1 {
			t.Fatal("original rewritten or applied", err)
		}
	}
}

func TestPostgresRecordVersioningRebaseConflictAndDrop(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Original"})
	w := testWorkspace(t, f, f.u, work)
	saved := testWorkspaceSave(t, f, w, f.u, workspaceUpdate(work, patchValue("/title", "Local")))
	w, original := testWorkspaceCommit(t, f, saved.Workspace, f.u, "local")
	remote := testWorkspace(t, f, f.u, work)
	saved = testWorkspaceSave(t, f, remote, f.u, workspaceUpdate(work, patchValue("/title", "Remote")))
	remote, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "remote")
	if _, err := f.s.PushWorkspace(ctx, remote.ID, pushWorkspaceInput(remote), f.u); err != nil {
		t.Fatal(err)
	}
	in := WorkspaceRebase{ID: uuid.NewString(), ExpectedVersion: w.Version, ExpectedHead: *w.HeadCommitID}
	_, err := f.s.RebaseWorkspace(ctx, w.ID, in, f.u)
	var conflict *workspaceConflictError
	if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 || conflict.Conflicts[0].SourceCommitID != original.ID {
		t.Fatalf("source conflict: %+v %v", conflict, err)
	}
	current, err := f.s.GetWorkspace(ctx, w.ID, f.u)
	if err != nil || current.Version != w.Version || *current.HeadCommitID != original.ID {
		t.Fatal("conflict partially rewrote branch", err)
	}
	empty := []string{}
	in.Order = &empty
	rebased, err := f.s.RebaseWorkspace(ctx, w.ID, in, f.u)
	if err != nil || rebased.Workspace.PendingCommits != 1 {
		t.Fatal("drop retained pending source", err)
	}
	var got Entity
	_ = json.Unmarshal(rebased.Workspace.Objects[0].Document, &got)
	if got.Title != "Remote" {
		t.Fatal("drop did not reset to selected base")
	}
	c, err := f.s.GetCollaborationCommit(ctx, original.ID, f.u)
	if err != nil || len(c.ReplacedBy) != 1 || c.MergedAt != nil {
		t.Fatal("drop lost original", err)
	}
}

func TestPostgresRecordVersioningRebaseMultiStepResolutions(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Original"})
	w := testWorkspace(t, f, f.u, work)
	saved := testWorkspaceSave(t, f, w, f.u, workspaceUpdate(work, patchValue("/title", "Local first")))
	w, first := testWorkspaceCommit(t, f, saved.Workspace, f.u, "first")
	saved = testWorkspaceSave(t, f, w, f.u, workspaceUpdate(work, patchValue("/title", "Local second")))
	w, second := testWorkspaceCommit(t, f, saved.Workspace, f.u, "second")
	remote := testWorkspace(t, f, f.u, work)
	saved = testWorkspaceSave(t, f, remote, f.u, workspaceUpdate(work, patchValue("/title", "Remote")))
	remote, _ = testWorkspaceCommit(t, f, saved.Workspace, f.u, "remote")
	if _, err := f.s.PushWorkspace(ctx, remote.ID, pushWorkspaceInput(remote), f.u); err != nil {
		t.Fatal(err)
	}
	base, err := f.s.CatalogVersionHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in := WorkspaceRebase{ID: uuid.NewString(), ExpectedVersion: w.Version, ExpectedHead: *w.HeadCommitID, OntoCommitID: base.CommitID}
	for i, expected := range []string{first.ID, second.ID} {
		_, err := f.s.RebaseWorkspace(ctx, w.ID, in, f.u)
		var conflict *workspaceConflictError
		if !errors.As(err, &conflict) || len(conflict.Conflicts) != 1 || conflict.Conflicts[0].SourceCommitID != expected {
			t.Fatalf("step %d: %+v %v", i, conflict, err)
		}
		c := conflict.Conflicts[0]
		in.Resolutions = append(in.Resolutions, WorkspaceResolution{CommitID: c.SourceCommitID, Target: c.Target, ID: c.ID, Pointer: c.Pointer, CurrentVersion: c.CurrentVersion, Value: json.RawMessage(encode([]string{"Merged first", "Merged second"}[i]))})
		current, err := f.s.GetWorkspace(ctx, w.ID, f.u)
		if err != nil || current.Version != w.Version {
			t.Fatal("partial replay survived conflict", err)
		}
	}
	out, err := f.s.RebaseWorkspace(ctx, w.ID, in, f.u)
	if err != nil {
		t.Fatal(err)
	}
	var e Entity
	_ = json.Unmarshal(out.Workspace.Objects[0].Document, &e)
	if e.Title != "Merged second" {
		t.Fatal("step-specific resolution lost")
	}
}

func TestPostgresRecordVersioningMigrationPreservesExistingFacts(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	if err := migrator.New(db, catalogMigrationsBefore(t, "000037")).Up(ctx); err != nil {
		t.Fatal(err)
	}
	actor := uuid.NewString()
	work, expression, release, medium, track := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for kind, id := range map[string]string{"work": work, "expression": expression, "release": release, "medium": medium, "track": track} {
		doc := map[string]any{"id": id, "kind": kind, "title": "Original " + kind, "version": 1, "state": "active", "created_by": actor, "translations": map[string]any{"ja": map[string]any{"title": "原題", "body": "Original body"}}, "attributes": map[string]any{"retained": map[string]any{"nested": []any{1, "unchanged"}}}}
		if _, err := db.Exec(`INSERT INTO catalog.entities(id,kind,title,state,created_by,version,document) VALUES($1,$2,$3,'active',$4,1,$5)`, id, kind, doc["title"], actor, encode(doc)); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO catalog.expressions(id,work_id) VALUES($1,$2)`, []any{expression, work}},
		{`INSERT INTO catalog.mediums(id,release_id) VALUES($1,$2)`, []any{medium, release}},
		{`INSERT INTO catalog.tracks(id,medium_id) VALUES($1,$2)`, []any{track, medium}},
		{`INSERT INTO catalog.release_subjects(release_id,work_id,role,position,attributes) VALUES($1,$2,'primary',1,'{"original":true}')`, []any{release, work}},
		{`INSERT INTO catalog.track_contents(track_id,expression_id,position,locator,attributes,sources) VALUES($1,$2,1,'{"start":1}', '{"original":true}','[{"citation":"Original source"}]')`, []any{track, expression}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string]Entity{}
	for _, id := range []string{work, expression, release, medium, track} {
		e, err := get(ctx, db, id)
		if err != nil {
			t.Fatal(err)
		}
		before[id] = e
	}
	commitID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO catalog.commits(id,actor_id,request_hash,request,result,author_snapshot,message,kind) VALUES($1,$2,'retained-hash','{"original":true}','{"unchanged":true}','{}','Original','edit')`, commitID, actor); err != nil {
		t.Fatal(err)
	}
	if err := migrator.New(db, migrations.FS).Up(ctx); err != nil {
		t.Fatal(err)
	}
	for id, e := range before {
		var raw json.RawMessage
		if err := db.QueryRow(`SELECT document FROM catalog.catalog_record_versions WHERE sequence=1 AND target='entity' AND target_id=$1`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var snapshot Entity
		if err := json.Unmarshal(raw, &snapshot); err != nil {
			t.Fatal(err)
		}
		if !equalDocument(json.RawMessage(encode(e)), json.RawMessage(encode(snapshot))) {
			t.Fatalf("cutover lost %s fact: before=%s snapshot=%s", e.Kind, encode(e), encode(snapshot))
		}
	}
	var hash, request, result string
	if err := db.QueryRow(`SELECT request_hash,request::text,result::text FROM catalog.commits WHERE id=$1`, commitID).Scan(&hash, &request, &result); err != nil || hash != "retained-hash" || request != `{"original": true}` || result != `{"unchanged": true}` {
		t.Fatal("migration changed original receipt", err)
	}
}

func TestPostgresRecordVersioningBranchMerge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Record"})
	w := testWorkspace(t, f, f.u, work)
	saved := testWorkspaceSave(t, f, w, f.u, workspaceUpdate(work, patchValue("/translations/en/body", "one\ntwo\nthree\n")))
	w, base := testWorkspaceCommit(t, f, saved.Workspace, f.u, "base text")
	fork, err := f.s.ForkWorkspace(ctx, WorkspaceFork{ID: uuid.NewString(), CommitID: base.ID, Name: "branch"}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	saved = testWorkspaceSave(t, f, w, f.u, workspaceUpdate(work, patchValue("/translations/en/body", "ONE\ntwo\nthree\n")))
	w, left := testWorkspaceCommit(t, f, saved.Workspace, f.u, "left paragraph")
	saved = testWorkspaceSave(t, f, fork, f.u, workspaceUpdate(work, patchValue("/translations/en/body", "one\ntwo\nTHREE\n")))
	fork, right := testWorkspaceCommit(t, f, saved.Workspace, f.u, "right paragraph")
	before := factCounts(t, f.s)
	merged := versionAction(t, f, w, "merge", right.ID)
	if factCounts(t, f.s) != before {
		t.Fatal("branch merge changed official facts")
	}
	if merged.Commit.ParentID == nil || *merged.Commit.ParentID != left.ID || merged.Commit.MergeParentID == nil || *merged.Commit.MergeParentID != right.ID {
		t.Fatal("merge DAG lost a parent")
	}
	copyFromMerge, err := f.s.ForkWorkspace(ctx, WorkspaceFork{ID: uuid.NewString(), CommitID: merged.Commit.ID, Name: "from merge"}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	copyHistory, err := f.s.VersionHistory(ctx, copyFromMerge.ID, VersionHistoryQuery{VersionReadQuery: VersionReadQuery{Limit: 100, Offset: 0}}, f.u)
	if err != nil || copyHistory.Total != 4 {
		t.Fatalf("fork lost second-parent ancestry: %+v %v", copyHistory, err)
	}
	third, err := f.s.ForkWorkspace(ctx, WorkspaceFork{ID: uuid.NewString(), CommitID: base.ID, Name: "third branch"}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	thirdMerge := versionAction(t, f, third, "merge", merged.Commit.ID)
	thirdHistory, err := f.s.VersionHistory(ctx, third.ID, VersionHistoryQuery{VersionReadQuery: VersionReadQuery{Limit: 100, Offset: 0}}, f.u)
	if err != nil || thirdHistory.Total != 5 {
		t.Fatalf("nested merge lost ancestry: %+v %v", thirdHistory, err)
	}
	headBefore, err := f.s.CatalogVersionHead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.PushWorkspace(ctx, third.ID, pushWorkspaceInput(thirdMerge.Workspace), f.u); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Get(ctx, work.ID, &f.u)
	if err != nil || got.Translations["en"].Body != "ONE\ntwo\nTHREE\n" {
		t.Fatalf("merged text: %v %+v", err, got.Translations)
	}
	source, err := f.s.GetCollaborationCommit(ctx, right.ID, f.u)
	if err != nil || source.MergedAt == nil {
		t.Fatal("original source commit was not integrated", err)
	}
	replayed, err := f.s.PushWorkspace(ctx, fork.ID, pushWorkspaceInput(fork), f.u)
	if err != nil || !replayed.Applied || replayed.Workspace.PendingCommits != 0 {
		t.Fatal("source branch did not acknowledge integration", err)
	}
	headAfter, err := f.s.CatalogVersionHead(ctx)
	if err != nil || headAfter.CommitID == headBefore.CommitID {
		t.Fatal("official head did not move", err)
	}
	old, err := f.s.GetCommitSnapshot(ctx, headBefore.CommitID, VersionReadQuery{Limit: 100, Offset: 0}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range old.Objects {
		if o.ID == work.ID {
			var e Entity
			_ = json.Unmarshal(o.Document, &e)
			if e.Translations["en"].Body != "" {
				t.Fatal("historical checkpoint changed")
			}
		}
	}
	diff, err := f.s.CompareVersions(ctx, headBefore.CommitID, headAfter.CommitID, VersionReadQuery{Limit: 100, Offset: 0}, f.u)
	if err != nil || diff.Total != 1 {
		t.Fatalf("official checkpoint diff: %v %+v", err, diff)
	}
}

func TestPostgresRecordVersioningRewriteTagsStash(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	work := f.save(Entity{Kind: "work", Title: "Original"})
	w := testWorkspace(t, f, f.u, work)
	saved := testWorkspaceSave(t, f, w, f.u, workspaceUpdate(work, patchValue("/title", "First")))
	w, first := testWorkspaceCommit(t, f, saved.Workspace, f.u, "first")
	saved = testWorkspaceSave(t, f, w, f.u, workspaceUpdate(work, patchValue("/title", "Second")))
	w, second := testWorkspaceCommit(t, f, saved.Workspace, f.u, "second")
	squashed := versionAction(t, f, w, "squash", first.ID, second.ID)
	original, err := f.s.GetCollaborationCommit(ctx, first.ID, f.u)
	if err != nil || len(original.ReplacedBy) != 1 || original.ReplacedBy[0] != squashed.Commit.ID {
		t.Fatal("rewrite lost original linkage", err)
	}
	page, err := f.s.VersionHistory(ctx, w.ID, VersionHistoryQuery{VersionReadQuery: VersionReadQuery{Limit: 100, Offset: 0}}, f.u)
	if err != nil || page.Total != 1 {
		t.Fatal("inactive replacements remained active", err)
	}
	w = squashed.Workspace
	saved = testWorkspaceSave(t, f, w, f.u, workspaceUpdate(work, patchValue("/title", "Stashed")))
	w = saved.Workspace
	stashID := uuid.NewString()
	w, err = f.s.ApplyWorkspaceStash(ctx, w.ID, WorkspaceStashAction{ID: stashID, ExpectedVersion: w.Version, ExpectedHead: *w.HeadCommitID, Operation: "save", Name: "changes"}, f.u)
	if err != nil || w.DirtyCount != 0 {
		t.Fatal("stash save", err)
	}
	w, err = f.s.ApplyWorkspaceStash(ctx, w.ID, WorkspaceStashAction{ID: uuid.NewString(), ExpectedVersion: w.Version, ExpectedHead: *w.HeadCommitID, Operation: "pop", StashID: stashID}, f.u)
	if err != nil || w.DirtyCount != 1 {
		t.Fatal("stash pop", err)
	}
	amended := versionAction(t, f, w, "amend")
	w = amended.Workspace
	tag := WorkspaceTagEdit{ID: uuid.NewString(), ExpectedVersion: w.Version, ExpectedHead: *w.HeadCommitID, Name: "v1", CommitID: amended.Commit.ID}
	w, err = f.s.EditWorkspaceTag(ctx, w.ID, tag, f.u)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.s.EditWorkspaceTag(ctx, w.ID, tag, f.u)
	if err != nil || again.Version != w.Version {
		t.Fatal("tag retry was not idempotent", err)
	}
	tags, err := f.s.WorkspaceTags(ctx, w.ID, 20, 0, f.u)
	if err != nil || tags.Total != 1 {
		t.Fatal("tag missing", err)
	}
	events, err := f.s.WorkspaceEvents(ctx, w.ID, 100, 0, f.u)
	if err != nil || events.Total < 6 {
		t.Fatal("reflog missing", err)
	}
	if _, err = f.s.DB.Exec(`UPDATE catalog.commit_snapshots SET document='{}' WHERE commit_id=$1`, first.ID); err == nil {
		t.Fatal("immutable snapshot was overwritten")
	}
	if _, err = f.s.DB.Exec(`UPDATE catalog.commits SET message='tampered' WHERE id=$1`, first.ID); err == nil {
		t.Fatal("immutable commit was overwritten")
	}
	stranger := fixtureUser("member")
	if _, err = f.s.GetCollaborationCommit(ctx, first.ID, stranger); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("private replacement history leaked", err)
	}
}

func TestPostgresRecordVersioningRevertCreateAndRestore(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	saved, created, w := prepareWorkspace(t, f.s, f.u, WorkspaceOperation{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(`{"kind":"work","title":"Created record"}`)})
	if _, err := f.s.PushWorkspace(ctx, w.ID, pushWorkspaceInput(w), f.u); err != nil {
		t.Fatal(err)
	}
	copy, err := f.s.CreateWorkspace(ctx, WorkspaceCreate{}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	revert := versionAction(t, f, copy, "revert", created.ID)
	if _, err = f.s.PushWorkspace(ctx, copy.ID, pushWorkspaceInput(revert.Workspace), f.u); err != nil {
		t.Fatal("delete inverse", err)
	}
	e, err := get(ctx, f.s.DB, saved.Refs["work"])
	if err != nil || e.State != "deleted" {
		t.Fatal("creation was not reverted", err)
	}
	copy, err = f.s.CreateWorkspace(ctx, WorkspaceCreate{}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	restore := versionAction(t, f, copy, "revert", revert.Commit.ID)
	if _, err = f.s.PushWorkspace(ctx, copy.ID, pushWorkspaceInput(restore.Workspace), f.u); err != nil {
		t.Fatal("restore inverse", err)
	}
	e, err = get(ctx, f.s.DB, e.ID)
	if err != nil || e.State != "active" || e.Title != "Created record" {
		t.Fatal("deleted record was not restored", err)
	}
}
