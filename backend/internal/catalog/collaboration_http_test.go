package catalog

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRespondCollaborationOperationErrorIncludesIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondCollaboration(c, nil, &commitOperationError{Index: 2, Cause: errors.New("invalid_workspace_operation")})
	if w.Code != 400 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Error     string `json:"error"`
		Applied   *bool  `json:"applied"`
		Operation *int   `json:"operation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error != "invalid_workspace_operation" || body.Applied == nil || *body.Applied || body.Operation == nil || *body.Operation != 2 {
		t.Fatalf("want structured operation error with operation=2, got %+v body=%s", body, w.Body.String())
	}
}

func TestRespondCollaborationConflictCarriesThreeVersions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	respondCollaboration(c, nil, &workspaceConflictError{Conflicts: []WorkspaceConflict{{Target: "entity", ID: "11111111-2222-4333-8444-555555555555", BaseVersion: 2, CurrentVersion: 3, Pointer: "/title", Base: "base", Current: "current", Proposed: "mine", Kind: "concurrent_edit"}}})
	var out struct {
		Error     string              `json:"error"`
		Applied   bool                `json:"applied"`
		Conflicts []WorkspaceConflict `json:"conflicts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private conflict values must not be cached")
	}
	if w.Code != 409 || out.Error != "workspace_conflict" || out.Applied || len(out.Conflicts) != 1 {
		t.Fatalf("conflict envelope: %d %s", w.Code, w.Body.String())
	}
	got := out.Conflicts[0]
	if got.Base != "base" || got.Current != "current" || got.Proposed != "mine" || got.BaseVersion != 2 || got.CurrentVersion != 3 {
		t.Fatalf("conflict lost versions: %+v", got)
	}
}

func TestRespondCollaborationStorageFailureIsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		err       error
		operation *int
	}{
		{name: "dependency", err: errors.New("storage_unavailable")},
		{name: "operation", err: &commitOperationError{Index: 2, Cause: errors.New("storage_unavailable")}, operation: func() *int { v := 2; return &v }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			respondCollaboration(c, nil, tc.err)
			var out struct {
				Error     string `json:"error"`
				Applied   *bool  `json:"applied"`
				Operation *int   `json:"operation"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if w.Code != 503 || out.Error != "storage_unavailable" || out.Applied == nil || *out.Applied {
				t.Fatalf("dependency status: %d %s", w.Code, w.Body.String())
			}
			if (out.Operation == nil) != (tc.operation == nil) || tc.operation != nil && *out.Operation != *tc.operation {
				t.Fatalf("operation: %+v", out)
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Retry-After") == "" {
				t.Fatalf("unavailable response headers: %+v", w.Header())
			}
		})
	}
}
