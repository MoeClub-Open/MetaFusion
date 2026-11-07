package curator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/assistant"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

type toolOperation struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// Only catalog mutation arguments and compact receipts are persisted; fetched
// pages and a second copy of the catalog do not belong in the recovery journal.
type agentProgress struct {
	WorkspaceID   string           `json:"workspace_id,omitempty"`
	CommitID      string           `json:"commit_id,omitempty"`
	PullRequestID string           `json:"pull_request_id,omitempty"`
	Pending       *toolOperation   `json:"pending,omitempty"`
	Trace         []map[string]any `json:"-"`
}

func (s *Service) loadProgress(ctx context.Context, finding Finding) (agentProgress, error) {
	p := agentProgress{WorkspaceID: finding.WorkspaceID, PullRequestID: finding.PullRequestID}
	if finding.LastRunID == "" {
		return p, nil
	}
	var detail []byte
	err := s.Catalog.DB.QueryRowContext(ctx, `SELECT detail FROM curator.runs WHERE id=$1 AND finding_id=$2`, finding.LastRunID, finding.ID).Scan(&detail)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(detail, &p)
	return p, err
}

func (s *Service) saveProgress(ctx context.Context, finding Finding, run *Run, p *agentProgress) error {
	detail, err := json.Marshal(p)
	if err != nil {
		return err
	}
	trace, err := json.Marshal(p.Trace)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	tx, err := s.Catalog.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE curator.findings SET workspace_id=$3,pull_request_id=$4,updated_at=now()
		WHERE id=$1 AND last_run_id=$2 AND status='running'`, finding.ID, run.ID, nullableID(p.WorkspaceID), nullableID(p.PullRequestID))
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE curator.runs SET detail=$2,tool_trace=$3,workspace_id=$4,pull_request_id=$5 WHERE id=$1 AND status='running'`,
		run.ID, string(detail), string(trace), nullableID(p.WorkspaceID), nullableID(p.PullRequestID)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	run.Detail, run.ToolTrace = detail, trace
	run.WorkspaceID, run.PullRequestID = p.WorkspaceID, p.PullRequestID
	return nil
}

func workspaceMutation(tool string) bool {
	switch tool {
	case "catalog_workspace_create", "catalog_workspace_checkout", "catalog_workspace_stage", "catalog_workspace_commit", "catalog_workspace_push":
		return true
	}
	return false
}

func (s *Service) executionSettings(ctx context.Context, code string) (Settings, error) {
	if err := s.checkLease(ctx); err != nil {
		return Settings{}, err
	}
	settings, err := s.Settings(ctx)
	if err == nil && (!settings.Enabled || settings.Paused || !s.findingEnabled(settings, code)) {
		err = ErrInactive
	}
	return settings, err
}

func (p *agentProgress) prepare(call assistant.ToolCall, runID string) (assistant.ToolCall, error) {
	if !workspaceMutation(call.Function.Name) {
		return call, nil
	}
	var args map[string]any
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil || args == nil {
		return call, errors.New("curator_invalid_tool_arguments")
	}
	if call.Function.Name == "catalog_workspace_create" {
		id := stringValue(args["id"])
		if p.WorkspaceID != "" {
			if id != p.WorkspaceID || p.Pending == nil || p.Pending.Tool != call.Function.Name {
				return call, errors.New("curator_reuse_existing_workspace")
			}
		} else {
			if id == "" {
				id = uuid.NewSHA1(uuid.NameSpaceOID, []byte(runID+":"+call.ID)).String()
			}
			if _, err := uuid.Parse(id); err != nil {
				return call, errors.New("curator_invalid_workspace_id")
			}
			p.WorkspaceID = id
		}
		args["id"] = p.WorkspaceID
	} else if p.WorkspaceID == "" || stringValue(args["workspace_id"]) != p.WorkspaceID {
		return call, errors.New("curator_workspace_scope_mismatch")
	}
	if call.Function.Name == "catalog_workspace_stage" {
		var in catalog.WorkspaceChanges
		if json.Unmarshal([]byte(call.Function.Arguments), &in) != nil {
			return call, errors.New("curator_invalid_tool_arguments")
		}
		for _, op := range in.Operations {
			if op.Action == "delete" || op.ContentChange != nil && op.ContentChange.Action == "delete" {
				return call, errors.New("curator_delete_forbidden")
			}
		}
	}
	raw, _ := json.Marshal(args)
	operation := &toolOperation{Tool: call.Function.Name, Arguments: raw}
	if p.Pending != nil && (p.Pending.Tool != operation.Tool || !jsonEqual(p.Pending.Arguments, json.RawMessage(raw))) {
		return call, errors.New("curator_recover_pending_operation_first")
	}
	p.Pending = operation
	call.Function.Arguments = string(raw)
	return call, nil
}

func (p *agentProgress) observe(call assistant.ToolCall, result any, toolErr error) {
	entry := map[string]any{"tool": call.Function.Name}
	if toolErr != nil {
		entry["error"] = safeCode(toolErr)
	} else {
		entry["result"] = summarizeToolResult(result)
	}
	if workspaceMutation(call.Function.Name) {
		entry["arguments"] = json.RawMessage(call.Function.Arguments)
		// Connection failures and cancellation leave an unknown outcome. Read
		// the original workspace/commit/PR before another mutation is accepted.
		if toolErr == nil || safeCode(toolErr) != "curator_error" && !errors.Is(toolErr, context.Canceled) && !errors.Is(toolErr, context.DeadlineExceeded) {
			p.Pending = nil
			if toolErr != nil && call.Function.Name == "catalog_workspace_create" {
				p.WorkspaceID = ""
			}
		}
	}
	p.Trace = append(p.Trace, entry)
	if toolErr != nil {
		return
	}
	switch value := result.(type) {
	case catalog.Workspace:
		p.observeWorkspace(value)
	case catalog.WorkspaceChangesResult:
		p.observeWorkspace(value.Workspace)
	case catalog.CollaborationCommit:
		if value.WorkspaceID != nil && *value.WorkspaceID == p.WorkspaceID {
			p.CommitID = value.ID
			if p.Pending != nil && p.Pending.Tool == "catalog_workspace_commit" {
				var args map[string]any
				_ = json.Unmarshal(p.Pending.Arguments, &args)
				if stringValue(args["id"]) == value.ID {
					p.Pending = nil
				}
			}
		}
	case catalog.PullRequest:
		p.observePR(value)
	case catalog.PullRequestPage:
		for _, pr := range value.Items {
			p.observePR(pr)
		}
	case catalog.WorkspacePushResult:
		if value.Workspace != nil {
			p.observeWorkspace(*value.Workspace)
		}
		if value.PullRequest != nil {
			p.observePR(*value.PullRequest)
		}
	}
}

func (p *agentProgress) observeWorkspace(w catalog.Workspace) {
	if w.ID != p.WorkspaceID {
		return
	}
	if w.HeadCommitID != nil {
		p.CommitID = *w.HeadCommitID
	}
	if p.Pending == nil {
		return
	}
	var args struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	_ = json.Unmarshal(p.Pending.Arguments, &args)
	if p.Pending.Tool == "catalog_workspace_create" || (p.Pending.Tool == "catalog_workspace_checkout" || p.Pending.Tool == "catalog_workspace_stage") && w.Version > args.ExpectedVersion {
		p.Pending = nil
	}
}

func (p *agentProgress) observePR(pr catalog.PullRequest) {
	if pr.WorkspaceID == p.WorkspaceID && (p.CommitID == "" || pr.SourceHeadID == p.CommitID) {
		p.PullRequestID = pr.ID
		if p.Pending != nil && p.Pending.Tool == "catalog_workspace_push" {
			p.Pending = nil
		}
	}
}

func (p *agentProgress) taskContext() string {
	if p.WorkspaceID == "" && p.Pending == nil {
		return ""
	}
	raw, _ := json.Marshal(p)
	return "\n先恢复以下已持久化进度，读取原副本、提交和 PR 确认结果；未知结果只沿用原请求参数和 ID，不得新建替代副本或重复实体：\n" + string(raw)
}

// An already pushed change can be finalized without buying another model turn.
func (s *Service) recoverReceipt(ctx context.Context, p *agentProgress, user catalog.User) (*catalog.WorkspacePushResult, error) {
	if p.PullRequestID != "" {
		pr, err := s.Catalog.GetPullRequest(ctx, p.PullRequestID, user)
		if err != nil {
			return nil, err
		}
		if pr.Status != "closed" {
			return &catalog.WorkspacePushResult{Applied: pr.Status == "merged", PullRequest: &pr}, nil
		}
		// An explicitly requeued finding must be reconsidered after its PR was
		// rejected. Keep the workspace, but do not treat a closed PR as success.
		p.PullRequestID = ""
		if p.Pending != nil && p.Pending.Tool == "catalog_workspace_push" {
			p.Pending = nil
		}
	}
	if p.Pending == nil || p.Pending.Tool != "catalog_workspace_push" {
		return nil, nil
	}
	var args struct {
		Head string `json:"expected_head"`
	}
	if err := json.Unmarshal(p.Pending.Arguments, &args); err != nil {
		return nil, err
	}
	commit, err := s.Catalog.GetCollaborationCommit(ctx, args.Head, user)
	if err != nil {
		return nil, err
	}
	if commit.MergedAt != nil {
		p.Pending = nil
		return &catalog.WorkspacePushResult{Applied: true, Commits: []catalog.CollaborationCommit{commit}}, nil
	}
	page, err := s.Catalog.ListPullRequestPage(ctx, "", p.WorkspaceID, 100, 0, user)
	if err != nil {
		return nil, err
	}
	for _, pr := range page.Items {
		if pr.SourceHeadID == args.Head {
			p.observePR(pr)
			return &catalog.WorkspacePushResult{Applied: pr.Status == "merged", PullRequest: &pr}, nil
		}
	}
	return nil, nil
}

func isBudgetFailure(code string) bool {
	return strings.Contains(code, "budget") || strings.Contains(code, "allowance")
}
