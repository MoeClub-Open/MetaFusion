package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/metafusion/metafusion-app/internal/catalog"
)

// Keep version tool inputs derived from the catalog contract instead of
// introducing another set of operation DTOs or a second branch implementation.
func assistantVersionToolDefinitions() []map[string]any {
	doc := catalog.OpenAPI()
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	var expand func(any) any
	expand = func(value any) any {
		switch v := value.(type) {
		case map[string]any:
			out := map[string]any{}
			if ref, ok := v["$ref"].(string); ok {
				if item, ok := expand(schemas[strings.TrimPrefix(ref, "#/components/schemas/")]).(map[string]any); ok {
					out = item
				}
			}
			for k, item := range v {
				if k != "$ref" {
					out[k] = expand(item)
				}
			}
			return out
		case []any:
			out := []any{}
			for _, item := range v {
				out = append(out, expand(item))
			}
			return out
		default:
			return value
		}
	}
	out := []map[string]any{}
	for _, row := range [][3]string{{"fork", "WorkspaceFork", "Fork an accessible frozen commit into an independent private copy. Retain id after an unknown result."}, {"apply", "WorkspaceVersionAction", "Apply merge, cherry_pick, revert, restore, amend, squash, reset or discard in the working copy. This never publishes facts. Keep the request ID and version/head guards; inspect conflicts instead of overwriting."}, {"tag", "WorkspaceTagEdit", "Create, rename, move or delete a private branch tag under owner permission. Keep the request ID."}, {"stash", "WorkspaceStashAction", "Save, apply, pop or drop private working changes. Apply can return three-way conflicts; no facts are published."}} {
		params := expand(schemas[row[1]]).(map[string]any)
		if row[0] != "fork" {
			params["properties"].(map[string]any)["workspace_id"] = map[string]any{"type": "string", "format": "uuid"}
			required, _ := params["required"].([]string)
			params["required"] = append(append([]string{}, required...), "workspace_id")
		}
		out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": "catalog_version_" + row[0], "description": row[2], "parameters": params}})
	}
	out = append(out, map[string]any{"type": "function", "function": map[string]any{"name": "catalog_version_read", "description": "Read official history, record revisions, private branch changes, immutable commit or frozen PR changes/history, snapshots and attribution. Filter before pagination; follow total/limit/offset for exhaustive results.", "parameters": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"op"}, "properties": map[string]any{"op": map[string]any{"type": "string", "enum": []string{"head", "history", "status", "events", "tags", "stashes", "snapshot", "compare", "blame", "workspace_diff", "commit_diff", "pull_request_diff", "pull_request_commits", "revisions"}}, "workspace_id": map[string]any{"type": "string", "format": "uuid"}, "commit_id": map[string]any{"type": "string", "format": "uuid"}, "base_id": map[string]any{"type": "string", "format": "uuid"}, "pull_request_id": map[string]any{"type": "string", "format": "uuid"}, "author_id": map[string]any{"type": "string", "format": "uuid"}, "since": map[string]any{"type": "string", "format": "date-time"}, "until": map[string]any{"type": "string", "format": "date-time"}, "version": map[string]any{"type": "integer", "minimum": 1}, "basis": map[string]any{"type": "string", "enum": []string{"head", "base"}}, "target": map[string]any{"type": "string", "enum": []string{"entity", "relation"}}, "record_id": map[string]any{"type": "string", "format": "uuid"}, "pointer": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "offset": map[string]any{"type": "integer", "minimum": 0}}}}})
	return out
}

func executeVersionTool(ctx context.Context, s *catalog.Store, u catalog.User, name string, args json.RawMessage) (any, error) {
	if name != "catalog_version_read" && (u.ID == "" || u.IsThirdParty) {
		return nil, errors.New("forbidden")
	}
	switch name {
	case "catalog_version_fork":
		var in catalog.WorkspaceFork
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		return s.ForkWorkspace(ctx, in, u)
	case "catalog_version_apply":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			catalog.WorkspaceVersionAction
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		return s.ApplyWorkspaceVersion(ctx, in.WorkspaceID, in.WorkspaceVersionAction, u)
	case "catalog_version_tag":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			catalog.WorkspaceTagEdit
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		return s.EditWorkspaceTag(ctx, in.WorkspaceID, in.WorkspaceTagEdit, u)
	case "catalog_version_stash":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			catalog.WorkspaceStashAction
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		return s.ApplyWorkspaceStash(ctx, in.WorkspaceID, in.WorkspaceStashAction, u)
	case "catalog_version_read":
		var in struct {
			Op            string `json:"op"`
			WorkspaceID   string `json:"workspace_id"`
			CommitID      string `json:"commit_id"`
			BaseID        string `json:"base_id"`
			PullRequestID string `json:"pull_request_id"`
			AuthorID      string `json:"author_id"`
			Since         string `json:"since"`
			Until         string `json:"until"`
			Version       int64  `json:"version"`
			Basis         string `json:"basis"`
			Target        string `json:"target"`
			RecordID      string `json:"record_id"`
			Pointer       string `json:"pointer"`
			Limit         *int   `json:"limit"`
			Offset        int    `json:"offset"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		limit, err := assistantCollaborationPageLimit(in.Limit, in.Offset)
		if err != nil {
			return nil, err
		}
		query := catalog.VersionReadQuery{Limit: limit, Offset: in.Offset, Target: in.Target, RecordID: in.RecordID}
		history := catalog.VersionHistoryQuery{VersionReadQuery: query, AuthorID: in.AuthorID, Since: in.Since, Until: in.Until}
		switch in.Op {
		case "head":
			return s.CatalogVersionHead(ctx)
		case "history":
			return s.CatalogVersionHistory(ctx, history, u)
		case "status":
			return s.WorkspaceVersionStatus(ctx, in.WorkspaceID, u)
		case "events":
			return s.WorkspaceEvents(ctx, in.WorkspaceID, limit, in.Offset, u)
		case "tags":
			return s.WorkspaceTags(ctx, in.WorkspaceID, limit, in.Offset, u)
		case "stashes":
			return s.WorkspaceStashes(ctx, in.WorkspaceID, limit, in.Offset, u)
		case "snapshot":
			return s.GetCommitSnapshot(ctx, in.CommitID, query, u)
		case "compare":
			return s.CompareVersions(ctx, in.BaseID, in.CommitID, query, u)
		case "workspace_diff":
			return s.WorkspaceDiff(ctx, in.WorkspaceID, in.Basis, query, u)
		case "commit_diff":
			return s.CommitDiff(ctx, in.CommitID, query, u)
		case "pull_request_diff":
			return s.PullRequestDiff(ctx, in.PullRequestID, query, u)
		case "pull_request_commits":
			return s.PullRequestCommitHistory(ctx, in.PullRequestID, history, u)
		case "revisions":
			return s.Revisions(ctx, in.RecordID, catalog.RevisionQuery{Limit: limit, Offset: in.Offset, Version: in.Version, AuthorID: in.AuthorID, Since: in.Since, Until: in.Until}, &u)
		case "blame":
			return s.VersionFieldAuthor(ctx, in.CommitID, in.Target, in.RecordID, in.Pointer, u)
		}
	}
	return nil, errors.New("invalid_version_operation")
}
