package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/metafusion/metafusion-app/internal/catalog"
)

const (
	maxAssistantToolArguments = 128 << 10
	maxAssistantToolOutput    = 512 << 10
	maxAssistantToolItems     = 25
)

// These tools are capabilities of the signed-in caller, never a second service
// identity. The model cannot select an actor, supply permissions or bypass review.
func assistantToolDefinitions() []map[string]any {
	str := func(description string) map[string]any {
		if description == "" {
			return map[string]any{"type": "string"}
		}
		return map[string]any{"type": "string", "description": description}
	}
	list := func(item any, limit int) map[string]any {
		return map[string]any{"type": "array", "items": item, "maxItems": limit}
	}
	integer := func(min, max int) map[string]any {
		return map[string]any{"type": "integer", "minimum": min, "maximum": max}
	}
	object := func(properties map[string]any, required ...string) map[string]any {
		out := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	tool := func(name, description string, schema any) map[string]any {
		return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description, "parameters": schema}}
	}
	kind := map[string]any{"type": "string", "enum": catalog.Kinds}
	limit := integer(1, maxAssistantToolItems)
	boolProp := func(description string) map[string]any {
		if description == "" {
			return map[string]any{"type": "boolean"}
		}
		return map[string]any{"type": "boolean", "description": description}
	}
	idProp := str("")
	ids25 := list(idProp, 25)
	searchFieldEnum := map[string]any{"type": "string", "enum": []string{"version", "original_language", "titles", "summary", "cover_url", "state", "parents"}}
	search := map[string]any{
		"q": str("Query text; omit to browse by filter."), "kind": kind,
		"work_id": idProp, "content_unit_id": idProp, "release_id": idProp, "medium_id": idProp, "parent_id": idProp,
		"field": str("Searchable attribute code/path."), "value": idProp,
		"limit": limit, "offset": integer(0, 1000000), "cursor": str("next_cursor (keep offset=0)."),
		"fields": list(searchFieldEnum, 7),
	}
	operation := object(map[string]any{
		"target": map[string]any{"type": "string", "enum": []string{"entity", "relation"}},
		"action": map[string]any{"type": "string", "enum": []string{"create", "update", "delete", "content"}},
		"id":     str("UUID or @local:<ref>."),
		"ref":    str("Local ref for create (omit id)."),
		"document": map[string]any{
			"type":                 "object",
			"additionalProperties": true,
			"description":          "Entity: kind,title,original_language,translations({locale:{title,summary,body,aliases,nonofficial,nonofficial_aliases}}),attributes,external_links([{kind,url,external_id,role}]),pictures,subjects,contents. Relation: type,source_id,target_id(UUID or {\"$ref\":\"<ref>\"}),position,attributes.",
		},
		"patch":          list(object(map[string]any{"path": str("JSON Pointer (e.g. /translations/ja-JP/summary, /attributes/<code>)."), "value": map[string]any{}, "remove": boolProp("")}, "path"), 100),
		"content_change": object(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"add", "replace", "delete"}}, "position": integer(0, 1000000), "inclusion": map[string]any{"type": "object", "additionalProperties": true}}, "action", "position"),
	}, "target", "action")
	version := map[string]any{"type": "integer", "minimum": 1}
	workspaceTools := func() []map[string]any {
		resolution := object(map[string]any{
			"commit_id": idProp, "target": map[string]any{"type": "string", "enum": []string{"entity", "relation"}},
			"id": idProp, "pointer": idProp, "current_version": map[string]any{"type": "integer", "minimum": 0},
			"value": map[string]any{}, "remove": boolProp(""),
		}, "target", "id", "pointer", "current_version")
		return []map[string]any{
			tool("catalog_workspace_create", "Create a working copy from entity/relation IDs.", object(map[string]any{"id": str("Optional idempotent UUID."), "name": idProp, "entity_ids": ids25, "relation_ids": ids25, "include_documents": boolProp("")})),
			tool("catalog_workspace_list", "List owned and shared working copies.", object(map[string]any{"limit": integer(1, 100), "offset": integer(0, 1000000)})),
			tool("catalog_workspace_get", "Read working copy state, CAS version, head, and staged objects.", object(map[string]any{"workspace_id": idProp, "include_documents": boolProp("")}, "workspace_id")),
			tool("catalog_workspace_edit", "Rename or archive a working copy.", object(map[string]any{"workspace_id": idProp, "expected_version": version, "name": idProp, "state": map[string]any{"type": "string", "enum": []string{"active", "archived"}}}, "workspace_id", "expected_version")),
			tool("catalog_workspace_checkout", "Check out additional records into a working copy.", object(map[string]any{"workspace_id": idProp, "expected_version": version, "entity_ids": ids25, "relation_ids": ids25, "include_documents": boolProp("")}, "workspace_id", "expected_version")),
			tool("catalog_workspace_stage", "Stage create/update/delete/content operations in a working copy without modifying main.", object(map[string]any{"workspace_id": idProp, "expected_version": version, "operations": list(operation, 25), "include_documents": boolProp("Default false.")}, "workspace_id", "expected_version", "operations")),
			tool("catalog_workspace_members", "Replace working copy collaborator user IDs (owner only).", object(map[string]any{"workspace_id": idProp, "expected_version": version, "user_ids": ids25}, "workspace_id", "expected_version", "user_ids")),
			tool("catalog_workspace_commit", "Seal staged changes into an immutable commit (id=fresh UUID; read catalog_commit_get on uncertain outcome).", object(map[string]any{"workspace_id": idProp, "id": idProp, "expected_version": version, "message": idProp}, "workspace_id", "id", "expected_version", "message")),
			tool("catalog_workspace_commits", "List branch commits newest first.", object(map[string]any{"workspace_id": idProp, "limit": integer(1, 100), "offset": integer(0, 1000000), "target": map[string]any{"type": "string", "enum": []string{"entity", "relation"}}, "record_id": idProp, "author_id": idProp, "since": str("RFC3339"), "until": str("RFC3339"), "state": map[string]any{"type": "string", "enum": []string{"active", "pending", "integrated", "replaced", "all"}}}, "workspace_id")),
			tool("catalog_workspace_push", "Push committed head (merges directly with bypass permission or opens/updates PR; only applied=true changes main).", object(map[string]any{"workspace_id": idProp, "expected_version": version, "expected_head": idProp, "title": idProp}, "workspace_id", "expected_version", "expected_head")),
			tool("catalog_workspace_rebase", "Rebase pending branch commits onto onto_commit_id or main head ([] order drops all).", object(map[string]any{"workspace_id": idProp, "expected_version": version, "expected_head": idProp, "id": idProp, "message": idProp, "onto_commit_id": idProp, "order": list(idProp, 1000), "resolutions": list(resolution, 100)}, "workspace_id", "expected_version", "expected_head", "id")),
			tool("catalog_pull_request_create", "Open or update a PR from the working-copy head.", object(map[string]any{"workspace_id": idProp, "expected_version": version, "expected_head": idProp, "title": idProp}, "workspace_id", "expected_version", "expected_head")),
			tool("catalog_pull_request_list", "List visible pull requests.", object(map[string]any{"status": map[string]any{"type": "string", "enum": []string{"open", "merged", "closed"}}, "workspace_id": idProp, "limit": integer(1, 100), "offset": integer(0, 1000000)})),
			tool("catalog_pull_request_get", "Read a pull request, commits, and proposed changes.", object(map[string]any{"pull_request_id": idProp}, "pull_request_id")),
			tool("catalog_pull_request_edit", "Rename, close, or reopen a pull request.", object(map[string]any{"pull_request_id": idProp, "expected_version": version, "title": idProp, "status": map[string]any{"type": "string", "enum": []string{"open", "closed"}}}, "pull_request_id", "expected_version")),
			tool("catalog_pull_request_merge", "Merge reviewed PR version and source head.", object(map[string]any{"pull_request_id": idProp, "expected_version": version, "expected_head": idProp}, "pull_request_id", "expected_version", "expected_head")),
			tool("catalog_commit_get", "Read an immutable commit and merge status.", object(map[string]any{"commit_id": idProp}, "commit_id")),
		}
	}
	definitions := []map[string]any{
		tool("catalog_search", "Search or browse catalog summaries. Follow has_more/next_cursor; call catalog_get for full records.", object(search)),
		tool("catalog_get", "Read one catalog entity (read-only). Filter fields/locales/attribute_keys to save context.", object(map[string]any{
			"id":             idProp,
			"fields":         map[string]any{"type": "array", "items": idProp, "maxItems": 30, "description": "e.g. translations, attributes, external_links, pictures, subjects, contents, original_language."},
			"locales":        list(idProp, 10),
			"attribute_keys": list(idProp, 30),
			"include_body":   boolProp("Default false."),
		}, "id")),
		tool("catalog_relationships", "Query structural, semantic, and reference edges for up to 10 entity IDs (check per-subject has_more).", object(map[string]any{
			"ids": list(idProp, 10), "direction": map[string]any{"type": "string", "enum": []string{"both", "outgoing", "incoming"}},
			"classes":    list(map[string]any{"type": "string", "enum": []string{"ownership", "placement", "inclusion", "semantic", "reference"}}, 5),
			"rule_codes": ids25, "peer_kinds": list(kind, len(catalog.Kinds)), "limit": limit, "offset": integer(0, 1000000),
			"active_only":      boolProp("Exclude ended/past edges."),
			"as_of":            str("YYYY[-MM[-DD]]"),
			"include_entities": boolProp("Default false; peer_id/peer_kind/peer_title are inline."),
		}, "ids")),
		tool("catalog_candidates", "Check identity candidates (exact, alias, former_names, core, approximate) before creating entities; inspect assessment (reuse|review|distinct).", object(map[string]any{
			"kind": kind, "titles": list(idProp, 20),
			"external_links":      list(object(map[string]any{"kind": idProp, "value": idProp}, "kind", "value"), 20),
			"attributes":          list(object(map[string]any{"key": idProp, "value": idProp}, "key", "value"), 20),
			"work_id":             idProp, "release_id": idProp, "medium_id": idProp, "limit": integer(1, 1000),
			"include_approximate": boolProp("Default true when titles supplied."),
			"context":             object(map[string]any{"creator": list(idProp, 20), "performer": list(idProp, 20), "label": list(idProp, 20), "catalog_number": list(idProp, 20)}),
		}, "kind")),
		tool("catalog_definitions", "Read dynamic definitions. Start with section=index, then query specific codes.", object(map[string]any{
			"section":    map[string]any{"type": "string", "enum": []string{"index", "fields", "relations", "structure", "vocabularies", "terms", "external_databases"}},
			"kind":       idProp,
			"codes":      list(idProp, maxAssistantToolItems),
			"vocabulary": str("Required for section=terms."),
			"locale":     idProp,
		})),
		tool("external_sources", "List read-only external source adapters.", object(map[string]any{})),
		tool("external_source_query", "Query external source adapter (bangumi, dlsite, dmm; optional if url_or_id is a URL).", object(map[string]any{
			"source":          idProp,
			"op":              map[string]any{"type": "string", "enum": []string{"search", "lookup", "detail"}},
			"q":               idProp, "id": idProp, "url_or_id": idProp, "entity_type": idProp, "limit": limit,
			"include_raw":     boolProp("Default false."),
			"include_preview": boolProp("Default true."),
		}, "op")),
		tool("public_fetch", "Fetch public HTTP(S) text or JSON without credentials (follow next_text_offset on excerpts).", object(map[string]any{
			"url":         idProp,
			"format":      map[string]any{"type": "string", "enum": []string{"text", "json"}},
			"text_offset": integer(0, 2097152), "max_chars": integer(1, 60000),
		}, "url")),
		tool("public_wiki", "Search or read MediaWiki (wikipedia, moegirl); pass title or query.", object(map[string]any{
			"site":        idProp,
			"language":    str("e.g. zh, ja, en."),
			"title":       idProp, "query": idProp, "limit": integer(1, 20),
			"text_offset": integer(0, 2097152), "max_chars": integer(1, 60000),
		}, "site")),
	}
	return append(append(definitions, workspaceTools()...), assistantVersionToolDefinitions()...)
}

func decodeAssistantToolArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 || len(raw) > maxAssistantToolArguments || !utf8.Valid(raw) {
		return errors.New("invalid_tool_arguments")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("invalid_tool_arguments")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return errors.New("invalid_tool_arguments")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid_tool_arguments")
	}
	return nil
}

func boundedAssistantToolOutput(value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxAssistantToolOutput {
		// Writes have already completed. Keep their IDs, CAS versions and
		// immutable heads recoverable instead of replacing success with an error.
		switch out := value.(type) {
		case catalog.WorkspacePushResult:
			if out.Workspace != nil {
				workspace := *out.Workspace
				workspace.Objects = nil
				out.Workspace = &workspace
			}
			for i := range out.Commits {
				out.Commits[i].Changes = nil
			}
			if out.PullRequest != nil {
				pr := *out.PullRequest
				pr.Changes = nil
				pr.Commits = nil
				out.PullRequest = &pr
			}
			return map[string]any{"receipt": out, "details_omitted": true, "editable_snapshot": false}, nil

		case catalog.Workspace:
			out.Objects = nil
			return map[string]any{"workspace": out, "objects_omitted": true, "editable_snapshot": false}, nil
		case catalog.WorkspaceChangesResult:
			out.Workspace.Objects = nil
			return map[string]any{"workspace": out.Workspace, "refs": out.Refs, "objects_omitted": true, "editable_snapshot": false}, nil
		case catalog.CollaborationCommit:
			out.Changes = nil
			return map[string]any{"commit": out, "changes_omitted": true, "editable_snapshot": false}, nil
		case catalog.WorkspaceRebaseResult:
			out.Workspace.Objects = nil
			if out.Commit != nil {
				commit := *out.Commit
				commit.Changes = nil
				out.Commit = &commit
			}
			return map[string]any{"workspace": out.Workspace, "commit": out.Commit, "details_omitted": true, "editable_snapshot": false}, nil
		case catalog.WorkspaceVersionResult:
			out.Workspace.Objects = nil
			if out.Commit != nil {
				copy := *out.Commit
				copy.Changes = nil
				out.Commit = &copy
			}
			return map[string]any{"workspace": out.Workspace, "commit": out.Commit, "details_omitted": true, "editable_snapshot": false}, nil
		case catalog.PullRequest:
			out.Changes = nil
			out.Commits = nil
			return map[string]any{"pull_request": out, "details_omitted": true, "editable_snapshot": false}, nil
		}
		// Never truncate JSON or silently drop facts. A model must not infer an
		// editable baseline or an exhaustive result from partial serialization.
		return map[string]any{"error": "tool_output_too_large", "complete": false, "max_bytes": maxAssistantToolOutput, "hint": "Request fewer records, select fields with catalog_get, or use narrower filters. Do not edit from an incomplete snapshot."}, nil
	}
	return value, nil
}

func runCatalogTool(ctx context.Context, s *catalog.Store, u catalog.User, name string, args json.RawMessage) (any, error) {
	value, err := executeCatalogTool(ctx, s, u, name, args)
	if err != nil {
		return nil, err
	}

	return boundedAssistantToolOutput(value)
}

func executeCatalogTool(ctx context.Context, s *catalog.Store, u catalog.User, name string, args json.RawMessage) (any, error) {
	if s == nil {
		return nil, errors.New("catalog_unavailable")
	}
	if strings.HasPrefix(name, "catalog_version_") {
		return executeVersionTool(ctx, s, u, name, args)
	}
	switch name {
	case "catalog_search":
		var in struct {
			Query         string   `json:"q"`
			Kind          string   `json:"kind"`
			WorkID        string   `json:"work_id"`
			ContentUnitID string   `json:"content_unit_id"`
			ReleaseID     string   `json:"release_id"`
			MediumID      string   `json:"medium_id"`
			ParentID      string   `json:"parent_id"`
			Field         string   `json:"field"`
			Value         string   `json:"value"`
			Limit         *int     `json:"limit"`
			Offset        int      `json:"offset"`
			Cursor        string   `json:"cursor"`
			Fields        []string `json:"fields"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		limit, err := assistantToolLimit(in.Limit)
		if err != nil {
			return nil, err
		}
		for _, text := range []string{in.Query, in.Kind, in.WorkID, in.ContentUnitID, in.ReleaseID, in.MediumID, in.ParentID, in.Field, in.Value} {
			if len(text) > 256 || strings.IndexFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\t' }) >= 0 {
				return nil, errors.New("invalid_query_param")
			}
		}
		if in.Offset < 0 || in.Offset > 1000000 || len(in.Cursor) > 16384 || in.Cursor != "" && in.Offset != 0 {
			return nil, errors.New("invalid_pagination")
		}
		if len(in.Fields) > 7 {
			return nil, errors.New("invalid_fields")
		}
		fieldSet, err := parseSearchSummaryFields(in.Fields)
		if err != nil {
			return nil, err
		}
		o := catalog.ListOptions{Kind: in.Kind, Query: strings.TrimSpace(in.Query), WorkID: in.WorkID, ContentUnitID: in.ContentUnitID, ReleaseID: in.ReleaseID, MediumID: in.MediumID, ParentID: in.ParentID, Field: in.Field, Value: in.Value, Limit: limit, Offset: in.Offset, Sort: "updated_at", Order: "desc"}
		if o.Query != "" {
			page, err := s.Search(ctx, o, &u, in.Cursor)
			if err != nil {
				return nil, err
			}
			return map[string]any{"items": assistantEntitySummariesProjected(ctx, s, page.Items, &u, fieldSet), "total": page.Total, "total_relation": page.TotalRelation, "has_more": page.HasMore, "next_cursor": page.NextCursor, "projection": "summary"}, nil
		}
		if in.Cursor != "" {
			return nil, errors.New("invalid_search_cursor")
		}
		items, total, err := s.ListWithTotal(ctx, o, &u)
		if err != nil {
			return nil, err
		}
		return map[string]any{"items": assistantEntitySummariesProjected(ctx, s, items, &u, fieldSet), "total": total, "total_relation": "eq", "has_more": int64(in.Offset+len(items)) < total, "offset": in.Offset, "limit": limit, "projection": "summary"}, nil
	case "catalog_get":
		var in struct {
			ID            string   `json:"id"`
			Fields        []string `json:"fields"`
			Locales       []string `json:"locales"`
			AttributeKeys []string `json:"attribute_keys"`
			IncludeBody   *bool    `json:"include_body"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		if len(in.Fields) > 30 || len(in.Locales) > 10 || len(in.AttributeKeys) > 30 {
			return nil, errors.New("invalid_fields")
		}
		entity, err := s.Get(ctx, in.ID, &u)
		if err != nil {
			return entity, err
		}
		if len(in.Locales) > 0 && len(entity.Translations) > 0 {
			allowed := make(map[string]struct{}, len(in.Locales))
			for _, loc := range in.Locales {
				allowed[strings.TrimSpace(loc)] = struct{}{}
			}
			filtered := make(map[string]catalog.Translation, len(in.Locales))
			for loc, tr := range entity.Translations {
				if _, ok := allowed[loc]; ok {
					filtered[loc] = tr
				}
			}
			entity.Translations = filtered
		}
		if len(in.AttributeKeys) > 0 && len(entity.Attributes) > 0 {
			allowed := make(map[string]struct{}, len(in.AttributeKeys))
			for _, k := range in.AttributeKeys {
				allowed[strings.TrimSpace(k)] = struct{}{}
			}
			filtered := make(map[string]any, len(in.AttributeKeys))
			for k, v := range entity.Attributes {
				if _, ok := allowed[k]; ok {
					filtered[k] = v
				}
			}
			entity.Attributes = filtered
		}
		includeBody := in.IncludeBody != nil && *in.IncludeBody
		if !includeBody {
			bodyLimit := 400
			if len(in.Fields) > 0 {
				bodyLimit = 1500
			}
			for loc, tr := range entity.Translations {
				if r := []rune(tr.Body); len(r) > bodyLimit {
					tr.Body = string(r[:bodyLimit]) + "… (truncated; pass include_body=true for full body)"
					entity.Translations[loc] = tr
				}
			}
		}
		if len(in.Fields) == 0 {
			return entity, err
		}
		raw, err := json.Marshal(entity)
		if err != nil {
			return nil, err
		}
		var all map[string]any
		if err = json.Unmarshal(raw, &all); err != nil {
			return nil, err
		}
		selected := map[string]any{"id": entity.ID, "kind": entity.Kind, "title": entity.Title, "url": "/catalog/" + entity.ID, "version": entity.Version}
		if len(entity.Pictures) > 0 && strings.TrimSpace(entity.Pictures[0].URL) != "" {
			selected["cover_url"] = compactCoverURL(entity.Pictures[0].URL)
		}
		for _, field := range in.Fields {
			if field == "url" || field == "cover_url" {
				continue
			}
			v, exists := all[field]
			if !exists {
				if isKnownOptionalEntityField(field) {
					continue
				}
				return nil, errors.New("invalid_fields")
			}
			selected[field] = v
		}
		return map[string]any{"entity": selected, "projection": "selected_fields", "editable_snapshot": false}, nil
	case "catalog_relationships":
		var in struct {
			catalog.RelationshipQueryRequest
			IncludeEntities *bool `json:"include_entities"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		if len(in.IDs) > 10 || len(in.RuleCodes) > 25 {
			return nil, errors.New("too_many_ids")
		}
		limit, err := assistantToolLimit(in.Limit)
		if err != nil {
			return nil, err
		}
		in.Limit = &limit
		if in.Offset != nil && (*in.Offset < 0 || *in.Offset > 1000000) {
			return nil, errors.New("invalid_offset")
		}
		out, err := s.QueryRelationships(ctx, in.RelationshipQueryRequest, &u)
		if err != nil {
			return nil, err
		}
		includeEntities := in.IncludeEntities != nil && *in.IncludeEntities
		return assistantRelationshipsOutputWithOptions(ctx, s, out, &u, includeEntities), nil
	case "catalog_candidates":
		in, err := decodeAssistantCandidateQuery(args)
		if err != nil {
			return nil, err
		}
		out, err := s.FindIdentityCandidates(ctx, in, &u)
		if err != nil {
			return nil, err
		}
		return assistantCandidateOutput(out), nil
	case "catalog_workspace_edit":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			catalog.WorkspaceEdit
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		out, err := s.EditWorkspace(ctx, in.WorkspaceID, in.WorkspaceEdit, u)
		if err != nil {
			return nil, err
		}
		return assistantWorkspaceCompactOutput(out, false), nil
	case "catalog_workspace_checkout":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			WorkspaceID      string `json:"workspace_id"`
			IncludeDocuments *bool  `json:"include_documents"`
			catalog.WorkspaceCheckout
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		out, err := s.CheckoutWorkspace(ctx, in.WorkspaceID, in.WorkspaceCheckout, u)
		if err != nil {
			return nil, err
		}
		includeDocs := in.IncludeDocuments == nil || *in.IncludeDocuments
		return assistantWorkspaceCompactOutput(out, includeDocs), nil
	case "catalog_workspace_stage":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			WorkspaceID      string `json:"workspace_id"`
			IncludeDocuments *bool  `json:"include_documents"`
			catalog.WorkspaceChanges
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		if len(in.Operations) > 25 {
			return nil, errors.New("invalid_workspace_size")
		}
		out, err := s.SaveWorkspaceChanges(ctx, in.WorkspaceID, in.WorkspaceChanges, u)
		if err != nil {
			return nil, err
		}
		includeDocs := in.IncludeDocuments != nil && *in.IncludeDocuments
		out.Workspace = assistantWorkspaceCompactOutput(out.Workspace, includeDocs)
		return out, nil
	case "catalog_workspace_members":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			catalog.WorkspaceMembersEdit
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		out, err := s.SetWorkspaceMembers(ctx, in.WorkspaceID, in.WorkspaceMembersEdit, u)
		if err != nil {
			return nil, err
		}
		return assistantWorkspaceCompactOutput(out, false), nil
	case "catalog_workspace_commit":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			catalog.WorkspaceCommitCreate
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		out, err := s.CommitWorkspace(ctx, in.WorkspaceID, in.WorkspaceCommitCreate, u)
		if err != nil {
			return nil, err
		}
		return assistantCompactCommitOutput(out), nil
	case "catalog_workspace_push":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			catalog.WorkspacePush
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		out, err := s.PushWorkspace(ctx, in.WorkspaceID, in.WorkspacePush, u)
		if err != nil {
			return nil, err
		}
		return assistantCompactPushOutput(out), nil
	case "catalog_workspace_rebase":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			catalog.WorkspaceRebase
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		out, err := s.RebaseWorkspace(ctx, in.WorkspaceID, in.WorkspaceRebase, u)
		if err != nil {
			return nil, err
		}
		out.Workspace = assistantWorkspaceCompactOutput(out.Workspace, false)
		if out.Commit != nil {
			c := assistantCompactCommitOutput(*out.Commit)
			out.Commit = &c
		}
		return out, nil
	case "catalog_workspace_create":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			catalog.WorkspaceCreate
			IncludeDocuments *bool `json:"include_documents"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		if len(in.EntityIDs)+len(in.RelationIDs) > 25 {
			return nil, errors.New("invalid_workspace_size")
		}
		out, err := s.CreateWorkspace(ctx, in.WorkspaceCreate, u)
		if err != nil {
			return nil, err
		}
		includeDocs := in.IncludeDocuments == nil || *in.IncludeDocuments
		return assistantWorkspaceCompactOutput(out, includeDocs), nil
	case "catalog_workspace_list":
		var in struct {
			Limit  *int `json:"limit"`
			Offset int  `json:"offset"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		limit, err := assistantCollaborationPageLimit(in.Limit, in.Offset)
		if err != nil {
			return nil, err
		}
		return s.ListWorkspacePage(ctx, limit, in.Offset, u)
	case "catalog_workspace_get":
		var in struct {
			WorkspaceID      string `json:"workspace_id"`
			IncludeDocuments *bool  `json:"include_documents"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		out, err := s.GetWorkspace(ctx, in.WorkspaceID, u)
		if err != nil {
			return nil, err
		}
		includeDocs := in.IncludeDocuments == nil || *in.IncludeDocuments
		return assistantWorkspaceCompactOutput(out, includeDocs), nil
	case "catalog_workspace_commits":
		var in struct {
			WorkspaceID string `json:"workspace_id"`
			Limit       *int   `json:"limit"`
			catalog.VersionHistoryQuery
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		limit, err := assistantCollaborationPageLimit(in.Limit, in.Offset)
		if err != nil {
			return nil, err
		}
		in.VersionHistoryQuery.Limit = limit
		return s.VersionHistory(ctx, in.WorkspaceID, in.VersionHistoryQuery, u)
	case "catalog_pull_request_create":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in catalog.PullRequestCreate
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		return s.CreatePullRequest(ctx, in, u)
	case "catalog_pull_request_list":
		var in struct {
			Limit       *int   `json:"limit"`
			Offset      int    `json:"offset"`
			Status      string `json:"status"`
			WorkspaceID string `json:"workspace_id"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		limit, err := assistantCollaborationPageLimit(in.Limit, in.Offset)
		if err != nil {
			return nil, err
		}
		return s.ListPullRequestPage(ctx, in.Status, in.WorkspaceID, limit, in.Offset, u)
	case "catalog_pull_request_get":
		var in struct {
			ID string `json:"pull_request_id"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		return s.GetPullRequest(ctx, in.ID, u)
	case "catalog_pull_request_edit":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			ID string `json:"pull_request_id"`
			catalog.PullRequestEdit
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		return s.EditPullRequest(ctx, in.ID, in.PullRequestEdit, u)
	case "catalog_pull_request_merge":
		if u.ID == "" || u.IsThirdParty {
			return nil, errors.New("forbidden")
		}
		var in struct {
			ID string `json:"pull_request_id"`
			catalog.PullRequestMerge
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		return s.MergePullRequest(ctx, in.ID, in.PullRequestMerge, u)
	case "catalog_commit_get":
		var in struct {
			ID string `json:"commit_id"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		return s.GetCollaborationCommit(ctx, in.ID, u)
	case "catalog_definitions":
		var in definitionQuery
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		defs, err := s.Definitions(ctx)
		if err != nil {
			return nil, err
		}
		if in.Section == "external_databases" {
			if s.DB == nil {
				return map[string]any{"etag": defs.ETag, "section": "external_databases", "definitions": map[string]any{}}, nil
			}
			items, dbErr := s.ListExternalDatabases(ctx, in.Kind, true)
			if dbErr != nil {
				return nil, dbErr
			}
			selected, selErr := assistantExternalDatabasesOutput(items, in.Codes)
			if selErr != nil {
				return nil, selErr
			}
			return map[string]any{"etag": defs.ETag, "section": "external_databases", "definitions": selected}, nil
		}
		out, err := assistantDefinitionOutput(defs, in)
		if err == nil && (in.Section == "" || in.Section == "index") && s.DB != nil {
			if items, dbErr := s.ListExternalDatabases(ctx, in.Kind, true); dbErr == nil {
				extCodes := make([]string, 0, len(items))
				for _, item := range items {
					extCodes = append(extCodes, item.Code)
				}
				out["external_database_codes"] = extCodes
			}
		}
		return out, err
	case "external_sources":
		if err := decodeAssistantToolArgs(args, &struct{}{}); err != nil {
			return nil, err
		}
		return s.ExternalSources(ctx)
	case "external_source_query":
		var in struct {
			catalog.ExternalSourceQueryRequest
			IncludeRaw     *bool `json:"include_raw"`
			IncludePreview *bool `json:"include_preview"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		if in.Limit < 0 || in.Limit > maxAssistantToolItems {
			return nil, errors.New("invalid_limit")
		}
		if in.Limit == 0 {
			in.Limit = 5
		}
		out, err := s.QueryExternalSource(ctx, in.ExternalSourceQueryRequest)
		if err != nil {
			return nil, err
		}
		if in.IncludePreview != nil && !*in.IncludePreview {
			out.Preview = nil
		}
		includeRaw := in.IncludeRaw != nil && *in.IncludeRaw
		if !includeRaw && (len(out.Items) > 0 || out.Preview != nil) {
			out.Raw = nil
		}
		return out, nil
	case "public_fetch":
		var in struct {
			catalog.PublicFetchRequest
			TextOffset int `json:"text_offset"`
			MaxChars   int `json:"max_chars"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		if err := validateAssistantSourceProjection(in.TextOffset, in.MaxChars); err != nil {
			return nil, err
		}
		out, err := s.FetchPublicSource(ctx, in.PublicFetchRequest)
		if err != nil {
			return nil, err
		}
		return assistantPublicSourceOutput(out, in.TextOffset, in.MaxChars), nil
	case "public_wiki":
		var in struct {
			catalog.PublicWikiRequest
			TextOffset int `json:"text_offset"`
			MaxChars   int `json:"max_chars"`
		}
		if err := decodeAssistantToolArgs(args, &in); err != nil {
			return nil, err
		}
		if err := validateAssistantSourceProjection(in.TextOffset, in.MaxChars); err != nil {
			return nil, err
		}
		if in.Limit < 0 || in.Limit > 20 {
			return nil, errors.New("invalid_limit")
		}
		out, err := s.QueryPublicWiki(ctx, in.PublicWikiRequest)
		if err != nil {
			return nil, err
		}
		return assistantPublicSourceOutput(out, in.TextOffset, in.MaxChars), nil
	default:
		return nil, errors.New("unknown_tool")
	}
}

func validateAssistantSourceProjection(offset, maxChars int) error {
	if offset < 0 || offset > 2097152 || maxChars < 0 || maxChars > 60000 {
		return errors.New("invalid_source_projection")
	}
	return nil
}

func assistantPublicSourceOutput(source catalog.PublicSourceResponse, offset, maxChars int) map[string]any {
	if maxChars == 0 {
		maxChars = 16000
	}
	out := map[string]any{"url": source.URL, "fetched_at": source.FetchedAt, "content_hash": source.ContentHash, "content_type": source.ContentType,
		"site": source.Site, "title": source.Title, "attribution": source.Attribution, "license": source.License}
	text := source.Text
	projection := "text"
	if text == "" && source.Data != nil {
		raw, err := json.Marshal(source.Data)
		if err == nil && len(raw) <= maxChars && offset == 0 {
			out["data"], out["complete"], out["projection"] = source.Data, true, "json"
			return out
		}
		text, projection = string(raw), "json_excerpt"
	}
	characters := []rune(text)
	if offset > len(characters) {
		offset = len(characters)
	}
	end := offset + maxChars
	if end > len(characters) {
		end = len(characters)
	}
	out["text"], out["projection"] = string(characters[offset:end]), projection
	out["text_offset"], out["total_chars"] = offset, len(characters)
	out["has_more_text"], out["complete"] = end < len(characters), offset == 0 && end == len(characters)
	if end < len(characters) {
		out["next_text_offset"] = end
	}
	return out
}

func assistantCandidateOutput(out catalog.IdentityCandidateResponse) any {
	if raw, err := json.Marshal(out); err == nil && len(raw) <= maxAssistantToolOutput {
		return out
	}
	// Preserve the complete identity set when attributes or article-sized
	// translations make full candidate details too large for one tool result.
	// IDs alone are not evidence that the model inspected those candidates.
	items := make([]map[string]any, 0, len(out.Items))
	for _, item := range out.Items {
		matched := map[string]any{"id": item.Matched.ID, "kind": item.Matched.Kind, "version": item.Matched.Version}
		if item.Matched.Title != "" {
			matched["title"] = truncateRunes(item.Matched.Title, 120)
		}
		entry := map[string]any{"matched": matched, "canonical": nil}
		if item.Canonical != nil {
			canonical := map[string]any{"id": item.Canonical.ID, "kind": item.Canonical.Kind, "version": item.Canonical.Version}
			if item.Canonical.Title != "" {
				canonical["title"] = truncateRunes(item.Canonical.Title, 120)
			}
			entry["canonical"] = canonical
		}
		if item.Assessment != nil {
			entry["assessment"] = item.Assessment
		}
		if item.ResolutionError != "" {
			entry["resolution_error"] = item.ResolutionError
		}
		items = append(items, entry)
	}
	return map[string]any{"items": items, "total": out.Total, "complete": out.Complete, "basis": out.Basis, "details_complete": false, "hint": "All returned candidate identities are preserved. Read catalog_get for candidate details before deciding which identity is correct or marking IDs reviewed."}
}

func decodeAssistantCandidateQuery(args json.RawMessage) (catalog.IdentityCandidateQuery, error) {
	var rawIn struct {
		catalog.IdentityCandidateQuery
		IncludeApproximate *bool `json:"include_approximate"`
	}
	if err := decodeAssistantToolArgs(args, &rawIn); err != nil {
		return catalog.IdentityCandidateQuery{}, err
	}
	in := rawIn.IdentityCandidateQuery
	limit := 10
	if in.Limit != nil {
		limit = *in.Limit
	}
	if limit < 1 || limit > 1000 {
		return catalog.IdentityCandidateQuery{}, errors.New("invalid_limit")
	}
	in.Limit = &limit
	if rawIn.IncludeApproximate != nil {
		in.IncludeApproximate = *rawIn.IncludeApproximate
	} else if len(in.Titles) > 0 {
		in.IncludeApproximate = true
	}
	return in, nil
}

func assistantToolLimit(limit *int) (int, error) {
	if limit == nil {
		return 10, nil
	}
	if *limit < 1 || *limit > maxAssistantToolItems {
		return 0, errors.New("invalid_limit")
	}
	return *limit, nil
}

func assistantEntitySummaries(items []catalog.Entity) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, e := range items {
		titles := map[string]string{}
		summary := ""
		for locale, tr := range e.Translations {
			if tr.Title != "" && tr.Title != e.Title {
				titles[locale] = tr.Title
			}
			if summary == "" && strings.TrimSpace(tr.Summary) != "" {
				summary = truncateRunes(strings.TrimSpace(tr.Summary), 160)
			}
		}
		item := map[string]any{"id": e.ID, "kind": e.Kind, "url": "/catalog/" + e.ID, "version": e.Version, "title": e.Title}
		if e.OriginalLanguage != "" {
			item["original_language"] = e.OriginalLanguage
		}
		if len(titles) > 0 {
			item["titles"] = titles
		}
		if summary != "" {
			item["summary"] = summary
		}
		if len(e.Pictures) > 0 && strings.TrimSpace(e.Pictures[0].URL) != "" {
			item["cover_url"] = compactCoverURL(e.Pictures[0].URL)
		}
		if e.State != "" && e.State != "active" {
			item["state"] = e.State
		}
		if e.WorkID != "" {
			item["work_id"] = e.WorkID
		}
		if e.ContentUnitID != "" {
			item["content_unit_id"] = e.ContentUnitID
		}
		if e.ReleaseID != "" {
			item["release_id"] = e.ReleaseID
		}
		if e.MediumID != "" {
			item["medium_id"] = e.MediumID
		}
		if e.ParentID != "" {
			item["parent_id"] = e.ParentID
		}
		out = append(out, item)
	}
	return out
}

func truncateRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func compactCoverURL(raw string) string {
	u := strings.TrimSpace(raw)
	for _, prefix := range []string{"https://findverse.cc/api/storage/", "http://findverse.cc/api/storage/", "https://metafusion.cc/api/storage/", "http://metafusion.cc/api/storage/"} {
		if strings.HasPrefix(strings.ToLower(u), prefix) {
			return "/api/storage/" + u[len(prefix):]
		}
	}
	return u
}

func assistantEntitySummariesWithCovers(ctx context.Context, s *catalog.Store, items []catalog.Entity, u *catalog.User) []map[string]any {
	out := assistantEntitySummaries(items)
	if s == nil || s.DB == nil || len(items) == 0 {
		return out
	}
	if covers, err := s.DisplayCovers(ctx, items, u); err == nil && len(covers) > 0 {
		for i, e := range items {
			if _, hasOwn := out[i]["cover_url"]; !hasOwn {
				if c, ok := covers[e.ID]; ok && strings.TrimSpace(c.URL) != "" {
					out[i]["cover_url"] = compactCoverURL(c.URL)
				}
			}
		}
	}
	return out
}

func isKnownOptionalEntityField(field string) bool {
	switch field {
	case "id", "kind", "title", "original_language", "translations", "attributes", "external_links", "pictures", "subjects", "contents", "work_id", "content_unit_id", "release_id", "medium_id", "parent_id", "state", "merged_into_id", "version", "created_at", "updated_at":
		return true
	default:
		return false
	}
}

func parseSearchSummaryFields(fields []string) (map[string]bool, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	allowed := make(map[string]bool, len(fields))
	for _, f := range fields {
		switch f {
		case "version", "original_language", "titles", "summary", "cover_url", "state", "parents":
			allowed[f] = true
		default:
			return nil, errors.New("invalid_fields")
		}
	}
	return allowed, nil
}

func assistantEntitySummariesProjected(ctx context.Context, s *catalog.Store, items []catalog.Entity, u *catalog.User, fields map[string]bool) []map[string]any {
	if len(fields) == 0 {
		return assistantEntitySummariesWithCovers(ctx, s, items, u)
	}
	var base []map[string]any
	if fields["cover_url"] {
		base = assistantEntitySummariesWithCovers(ctx, s, items, u)
	} else {
		base = assistantEntitySummaries(items)
	}
	for _, item := range base {
		if !fields["version"] {
			delete(item, "version")
		}
		if !fields["original_language"] {
			delete(item, "original_language")
		}
		if !fields["titles"] {
			delete(item, "titles")
		}
		if !fields["summary"] {
			delete(item, "summary")
		}
		if !fields["cover_url"] {
			delete(item, "cover_url")
		}
		if !fields["state"] {
			delete(item, "state")
		}
		if !fields["parents"] {
			delete(item, "work_id")
			delete(item, "content_unit_id")
			delete(item, "release_id")
			delete(item, "medium_id")
			delete(item, "parent_id")
		}
	}
	return base
}

func assistantRelationshipsOutput(ctx context.Context, s *catalog.Store, out catalog.RelationshipQueryResponse, u *catalog.User) map[string]any {
	return assistantRelationshipsOutputWithOptions(ctx, s, out, u, true)
}

func assistantRelationshipsOutputWithOptions(ctx context.Context, s *catalog.Store, out catalog.RelationshipQueryResponse, u *catalog.User, includeEntities bool) map[string]any {
	covers := map[string]string{}
	if includeEntities && s != nil && s.DB != nil && len(out.Entities) > 0 {
		ids := make([]string, 0, len(out.Entities))
		for id := range out.Entities {
			ids = append(ids, id)
		}
		for start := 0; start < len(ids); start += 100 {
			end := start + 100
			if end > len(ids) {
				end = len(ids)
			}
			if batch, err := s.EntityBatch(ctx, catalog.EntityBatchRequest{IDs: ids[start:end]}, u); err == nil {
				for id, ent := range batch.Items {
					if len(ent.Pictures) > 0 && strings.TrimSpace(ent.Pictures[0].URL) != "" {
						covers[id] = compactCoverURL(ent.Pictures[0].URL)
					} else if c, ok := batch.Covers[id]; ok && strings.TrimSpace(c.URL) != "" {
						covers[id] = compactCoverURL(c.URL)
					}
				}
			}
		}
	}
	var entities map[string]map[string]any
	if includeEntities {
		entities = make(map[string]map[string]any, len(out.Entities))
		for id, e := range out.Entities {
			titles := map[string]string{}
			for locale, tr := range e.Translations {
				if tr.Title != "" && tr.Title != e.Title {
					titles[locale] = tr.Title
				}
			}
			item := map[string]any{"id": e.ID, "kind": e.Kind, "title": e.Title}
			if len(titles) > 0 {
				item["titles"] = titles
			}
			if c := covers[id]; c != "" {
				item["cover_url"] = c
			}
			entities[id] = item
		}
	}
	pages := make([]map[string]any, 0, len(out.Pages))
	for _, p := range out.Pages {
		items := make([]map[string]any, 0, len(p.Items))
		for _, link := range p.Items {
			peerID := link.TargetID
			if peerID == p.SubjectID {
				peerID = link.SourceID
			}
			peer := out.Entities[peerID]
			edge := map[string]any{
				"key":        link.Key,
				"rule_code":  link.RuleCode,
				"class":      link.Class,
				"direction":  link.Direction,
				"peer_id":    peerID,
				"peer_kind":  peer.Kind,
				"peer_title": peer.Title,
			}
			if len(link.Via) > 0 {
				edge["source_id"] = link.SourceID
				edge["target_id"] = link.TargetID
			}
			if link.Version != nil && *link.Version > 0 {
				edge["version"] = *link.Version
			}
			if link.Position != 0 {
				edge["position"] = link.Position
			}
			if link.Role != "" {
				edge["role"] = link.Role
			}
			if link.Field != "" {
				edge["field"] = link.Field
			}
			if len(link.Locator) > 0 {
				edge["locator"] = link.Locator
			}
			if len(link.Attributes) > 0 {
				edge["attributes"] = link.Attributes
			}
			if len(link.References) > 0 {
				refs := make([]map[string]any, 0, len(link.References))
				for _, r := range link.References {
					rm := map[string]any{"field": r.Field, "entity_id": r.EntityID}
					if refEnt, ok := out.Entities[r.EntityID]; ok {
						if refEnt.Kind != "" {
							rm["kind"] = refEnt.Kind
						}
						if refEnt.Title != "" {
							rm["title"] = refEnt.Title
						}
					}
					refs = append(refs, rm)
				}
				edge["references"] = refs
			}
			if len(link.Via) > 0 {
				edge["via"] = link.Via
			}
			items = append(items, edge)
		}
		pages = append(pages, map[string]any{
			"subject_id": p.SubjectID,
			"items":      items,
			"limit":      p.Limit,
			"offset":     p.Offset,
			"has_more":   p.HasMore,
		})
	}
	res := map[string]any{
		"definition_etag": out.DefinitionETag,
		"pages":           pages,
		"unavailable_ids": out.UnavailableIDs,
	}
	if includeEntities {
		res["entities"] = entities
	}
	return res
}

func assistantCollaborationPageLimit(value *int, offset int) (int, error) {
	limit := 20
	if value != nil {
		limit = *value
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return 0, errors.New("invalid_pagination")
	}
	return limit, nil
}

func assistantWorkspaceOutput(ws catalog.Workspace) catalog.Workspace {
	return assistantWorkspaceCompactOutput(ws, true)
}

func assistantWorkspaceCompactOutput(ws catalog.Workspace, includeDocuments bool) catalog.Workspace {
	if len(ws.Objects) == 0 {
		return ws
	}
	objects := make([]catalog.WorkspaceObject, len(ws.Objects))
	copy(objects, ws.Objects)
	for i := range objects {
		objects[i].Base = nil
		objects[i].Committed = nil
		if !includeDocuments {
			objects[i].Document = nil
		}
	}
	ws.Objects = objects
	return ws
}

func assistantCompactCommitOutput(commit catalog.CollaborationCommit) catalog.CollaborationCommit {
	if len(commit.Changes) > 0 {
		changes := make([]catalog.CommitObject, len(commit.Changes))
		copy(changes, commit.Changes)
		for i := range changes {
			changes[i].Base = nil
			changes[i].Before = nil
			changes[i].After = nil
		}
		commit.Changes = changes
	}
	return commit
}

func assistantCompactPushOutput(out catalog.WorkspacePushResult) catalog.WorkspacePushResult {
	if out.Workspace != nil {
		ws := assistantWorkspaceCompactOutput(*out.Workspace, false)
		ws.Objects = nil
		out.Workspace = &ws
	}
	if len(out.Commits) > 0 {
		commits := make([]catalog.CollaborationCommit, len(out.Commits))
		for i, c := range out.Commits {
			commits[i] = assistantCompactCommitOutput(c)
		}
		out.Commits = commits
	}
	if out.PullRequest != nil {
		pr := *out.PullRequest
		pr.Changes = nil
		pr.Commits = nil
		out.PullRequest = &pr
	}
	return out
}
