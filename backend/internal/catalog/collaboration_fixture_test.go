package catalog

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// prepareWorkspace stages and commits a real server-side working copy. It never
// changes the main catalog; tests must explicitly push or merge the result.
func prepareWorkspace(t *testing.T, s *Store, u User, operations ...WorkspaceOperation) (WorkspaceChangesResult, CollaborationCommit, Workspace) {
	t.Helper()
	ctx := context.Background()
	w, err := s.CreateWorkspace(ctx, WorkspaceCreate{Name: "test working copy"}, u)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := s.SaveWorkspaceChanges(ctx, w.ID, WorkspaceChanges{ExpectedVersion: w.Version, Operations: operations}, u)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CommitWorkspace(ctx, w.ID, WorkspaceCommitCreate{ID: uuid.NewString(), ExpectedVersion: changes.Workspace.Version, Message: "test catalog change"}, u)
	if err != nil {
		t.Fatal(err)
	}
	w, err = s.GetWorkspace(ctx, w.ID, u)
	if err != nil {
		t.Fatal(err)
	}
	return changes, c, w
}
