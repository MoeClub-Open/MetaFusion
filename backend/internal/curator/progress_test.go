package curator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/assistant"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

func toolCall(name string, args any) assistant.ToolCall {
	call := assistant.ToolCall{ID: uuid.NewString(), Type: "function"}
	call.Function.Name, call.Function.Arguments = name, string(rawJSON(args))
	return call
}

func restartProgress(t *testing.T, p agentProgress) agentProgress {
	t.Helper()
	var restored agentProgress
	if err := json.Unmarshal(rawJSON(p), &restored); err != nil {
		t.Fatal(err)
	}
	return restored
}

func TestProgressPreservesCreationIDAcrossRestart(t *testing.T) {
	p := agentProgress{}
	call, err := p.prepare(toolCall("catalog_workspace_create", map[string]any{"name": "repair"}), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	p.observe(call, nil, context.DeadlineExceeded)
	restored := restartProgress(t, p)
	if restored.Pending == nil || restored.WorkspaceID == "" {
		t.Fatal("unknown creation was lost")
	}
	request := toolCall("catalog_workspace_create", json.RawMessage(restored.Pending.Arguments))
	if _, err = restored.prepare(request, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if restored.WorkspaceID != p.WorkspaceID {
		t.Fatal("restart allocated a replacement workspace")
	}
	if _, err = restored.prepare(toolCall("catalog_workspace_create", map[string]any{"id": uuid.NewString(), "name": "repair"}), uuid.NewString()); err == nil {
		t.Fatal("unknown creation allowed another workspace")
	}
}

func TestProgressRequiresReadbackBeforeChangingUnknownStage(t *testing.T) {
	p := agentProgress{WorkspaceID: uuid.NewString()}
	args := map[string]any{"workspace_id": p.WorkspaceID, "expected_version": 2, "operations": []any{}}
	call, err := p.prepare(toolCall("catalog_workspace_stage", args), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	p.observe(call, nil, context.DeadlineExceeded)
	p = restartProgress(t, p)
	args["expected_version"] = 3
	if _, err = p.prepare(toolCall("catalog_workspace_stage", args), uuid.NewString()); err == nil {
		t.Fatal("unconfirmed stage could be replayed against a newer version")
	}
	p.observe(toolCall("catalog_workspace_get", map[string]any{"workspace_id": p.WorkspaceID}), catalog.Workspace{ID: p.WorkspaceID, Version: 3}, nil)
	if p.Pending != nil {
		t.Fatal("successful stage readback did not resolve pending outcome")
	}
	if _, err = p.prepare(toolCall("catalog_workspace_stage", args), uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

func TestProgressRecoversCommitAndRejectsDeletes(t *testing.T) {
	p := agentProgress{WorkspaceID: uuid.NewString()}
	commitID := uuid.NewString()
	call, err := p.prepare(toolCall("catalog_workspace_commit", map[string]any{"workspace_id": p.WorkspaceID, "id": commitID, "expected_version": 3, "message": "repair"}), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	p.observe(call, nil, context.Canceled)
	p = restartProgress(t, p)
	p.observe(toolCall("catalog_commit_get", map[string]any{"commit_id": commitID}), catalog.CollaborationCommit{ID: commitID, WorkspaceID: &p.WorkspaceID}, nil)
	if p.Pending != nil || p.CommitID != commitID {
		t.Fatal("original commit receipt was not recovered")
	}
	_, err = p.prepare(toolCall("catalog_workspace_stage", map[string]any{"workspace_id": p.WorkspaceID, "operations": []any{map[string]any{"target": "relation", "action": "delete", "id": uuid.NewString()}}}), uuid.NewString())
	if err == nil {
		t.Fatal("agent may delete a relation through the generic stage tool")
	}
}

type leaseProbe func(context.Context) error

func (probe leaseProbe) PingContext(ctx context.Context) error { return probe(ctx) }

func TestLeaseFailureCancelsExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	lease := &schedulerLease{cancel: cancel, conn: leaseProbe(func(context.Context) error { return errors.New("connection lost") })}
	if !errors.Is(lease.check(ctx), ErrLeaseLost) || ctx.Err() == nil || !lease.lost.Load() {
		t.Fatal("lost lease did not cancel work")
	}
}

func TestDisabledFindingScopes(t *testing.T) {
	s := New(nil, nil)
	settings := Settings{AllowNames: true}
	for _, code := range []string{CodeTrackPositionGap, CodeWorkMissingForm, CodeMissingPicture, CodeDuplicateTagValues} {
		if s.findingEnabled(settings, code) {
			t.Fatalf("disabled scope still enabled: %s", code)
		}
	}
	if !s.findingEnabled(settings, CodeTranslationZHKana) || !s.findingEnabled(settings, CodeDuplicateTitle) {
		t.Fatal("enabled name work was disabled")
	}
}

func TestInvalidDecisionFailsWithoutConfirmedPush(t *testing.T) {
	if _, err := confirmedDecision("malformed output", false); err == nil {
		t.Fatal("invalid output became a completed finding")
	}
	if _, err := confirmedDecision(`{"decision":"changed"}`, false); err == nil {
		t.Fatal("unconfirmed change became completed")
	}
	d, err := confirmedDecision("malformed output", true)
	if err != nil || d.Decision != "changed" {
		t.Fatal("confirmed push should survive a malformed final response")
	}
}
