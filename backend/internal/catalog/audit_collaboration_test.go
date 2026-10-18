package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestDirectoryAuditActorCredentialKind(t *testing.T) {
	for _, kind := range []string{"session", "pat", "oauth"} {
		t.Run(kind, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("catalog_user", &User{ID: "actor", Username: "name", FromPAT: kind == "pat", IsThirdParty: kind == "oauth"})
			got := DirectoryActor(c)
			if got.CredentialType != kind || got.UserID != "actor" || got.Username != "name" {
				t.Fatalf("identity projection: %+v", got)
			}
		})
	}
}

func TestPostgresAuditCollaborationPRSummary(t *testing.T) {
	f := newAuditFixture(t)
	u := reviewEditor()
	ctx := context.Background()
	w, err := f.s.CreateWorkspace(ctx, WorkspaceCreate{Name: "Correct release name"}, u)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.s.SaveWorkspaceChanges(ctx, w.ID, WorkspaceChanges{ExpectedVersion: w.Version, Operations: []WorkspaceOperation{{Target: "entity", Action: "create", Ref: "work", Document: json.RawMessage(`{"kind":"work","title":"PRIVATE_SOURCE_DOCUMENT"}`)}}}, u)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.s.CommitWorkspace(ctx, w.ID, WorkspaceCommitCreate{ID: uuid.NewString(), ExpectedVersion: saved.Workspace.Version, Message: "Correct title and membership"}, u)
	if err != nil {
		t.Fatal(err)
	}
	w, err = f.s.GetWorkspace(ctx, w.ID, u)
	if err != nil {
		t.Fatal(err)
	}
	input := PullRequestCreate{WorkspaceID: w.ID, WorkspacePush: WorkspacePush{ExpectedVersion: w.Version, ExpectedHead: c.ID, Title: "Correct release name"}}
	response := f.do(f.engine(&u), http.MethodPost, "/api/catalog/pull-requests", encode(input), "pr-summary-created")
	f.expect(http.StatusOK, response, "create PR")
	var p PullRequest
	if err := json.Unmarshal(response.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	assertSummary := func(requestID, status string, applied bool) {
		t.Helper()
		row := f.waitRow(requestID)
		var detail map[string]any
		if err := json.Unmarshal([]byte(row.changes), &detail); err != nil {
			t.Fatal(err)
		}
		if row.targetType != "pull_request" || row.targetID != p.ID || detail["target_name"] != input.Title || detail["status"] != status || detail["commit_count"] != float64(1) || detail["object_count"] != float64(1) {
			t.Fatalf("PR audit summary: %+v %s", row, row.changes)
		}
		if applied && detail["applied"] != true {
			t.Fatalf("successful merge not captured: %s", row.changes)
		}
		if strings.Contains(row.changes, "PRIVATE_SOURCE_DOCUMENT") {
			t.Fatal("full private documents must stay in commit history")
		}
	}
	assertSummary("pr-summary-created", "open", false)
	inputMerge := PullRequestMerge{ExpectedVersion: p.Version + 1, ExpectedHead: c.ID}
	path := "/api/catalog/pull-requests/" + p.ID + "/merge"
	response = f.do(f.engine(f.user), http.MethodPost, path, encode(inputMerge), "pr-summary-conflict")
	f.expect(http.StatusConflict, response, "conflicting merge")
	failed := f.waitRow("pr-summary-conflict")
	if failed.result != "failure" || strings.Contains(failed.changes, "applied") || strings.Contains(failed.changes, "target_name") {
		t.Fatalf("failed merge must not invent a success summary: %+v", failed)
	}
	inputMerge.ExpectedVersion = p.Version
	response = f.do(f.engine(f.user), http.MethodPost, path, encode(inputMerge), "pr-summary-merged")
	f.expect(http.StatusOK, response, "merge PR")
	assertSummary("pr-summary-merged", "merged", true)
}
