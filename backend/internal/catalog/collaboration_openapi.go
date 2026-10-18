package catalog

import (
	"os"
	"reflect"
	"sort"
	"strings"
)

func addCollaborationOpenAPI(paths, schemas map[string]any, schema func(reflect.Type) map[string]any) {
	for _, value := range []any{
		Workspace{}, WorkspaceObject{}, WorkspacePage{}, WorkspaceCreate{}, WorkspaceEdit{}, WorkspaceCheckout{},
		WorkspaceOperation{}, WorkspaceChanges{}, WorkspaceChangesResult{}, WorkspaceMembersEdit{},
		WorkspaceCommitCreate{}, CollaborationCommit{}, CommitObject{}, CollaborationAuthor{},
		WorkspacePush{}, WorkspacePushResult{}, WorkspaceConflict{}, WorkspaceResolution{},
		WorkspaceRebase{}, WorkspaceRebaseResult{}, PullRequest{}, PullRequestPage{}, PullRequestCreate{},
		PullRequestEdit{}, PullRequestMerge{},
	} {
		schema(reflect.TypeOf(value))
	}
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
	props := func(name string) map[string]any { return schemas[name].(map[string]any)["properties"].(map[string]any) }
	required := func(name string, names ...string) { schemas[name].(map[string]any)["required"] = names }
	uuid := map[string]any{"type": "string", "format": "uuid"}
	version := map[string]any{"type": "integer", "minimum": 1, "description": "Compare-and-swap guard from the last workspace or pull-request read."}
	for _, name := range []string{"WorkspaceEdit", "WorkspaceCheckout", "WorkspaceChanges", "WorkspaceMembersEdit", "WorkspaceCommitCreate", "WorkspacePush", "WorkspaceRebase", "PullRequestCreate", "PullRequestEdit", "PullRequestMerge"} {
		props(name)["expected_version"] = version
	}
	for _, name := range []string{"WorkspacePush", "WorkspaceRebase", "PullRequestCreate", "PullRequestMerge"} {
		props(name)["expected_head"] = map[string]any{"type": "string", "format": "uuid", "description": "Exact immutable source head seen by the client; a changed head returns 409."}
	}
	required("WorkspaceEdit", "expected_version")
	required("WorkspaceCheckout", "expected_version")
	required("WorkspaceChanges", "expected_version", "operations")
	required("WorkspaceMembersEdit", "expected_version", "user_ids")
	required("WorkspaceCommitCreate", "id", "expected_version", "message")
	required("WorkspacePush", "expected_version", "expected_head")
	required("WorkspaceRebase", "id", "expected_version", "expected_head")
	required("PullRequestCreate", "workspace_id", "expected_version", "expected_head")
	required("PullRequestEdit", "expected_version")
	required("PullRequestMerge", "expected_version", "expected_head")
	required("WorkspaceOperation", "target", "action")
	required("WorkspaceResolution", "target", "id", "pointer", "current_version")
	required("WorkspaceConflict", "target", "id", "base_version", "current_version", "pointer", "base", "current", "proposed", "base_present", "current_present", "proposed_present", "kind")
	for _, name := range []string{"WorkspaceOperation", "WorkspaceObject", "CommitObject", "WorkspaceConflict", "WorkspaceResolution"} {
		props(name)["target"] = map[string]any{"type": "string", "enum": []string{"entity", "relation"}}
		props(name)["id"] = uuid
	}
	props("WorkspaceOperation")["action"] = map[string]any{"type": "string", "enum": []string{"create", "update", "delete", "content"}}
	props("WorkspaceOperation")["ref"] = map[string]any{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_-]{0,63}$", "description": "Batch-local name for a create; returned refs maps it to a persistent object UUID."}
	props("WorkspaceOperation")["document"] = map[string]any{"type": "object", "additionalProperties": true, "description": "Editable entity or relation document. Workspace creates allocate a stable UUID; request-local ref values can refer to a create in the same batch."}
	props("WorkspaceOperation")["patch"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 200, "items": ref("CommitPatch"), "description": "JSON Pointer set/remove changes to the workspace object. Arrays are atomic values."}
	schemas["WorkspaceOperation"].(map[string]any)["oneOf"] = []any{
		map[string]any{"required": []string{"ref", "document"}, "properties": map[string]any{"action": map[string]any{"enum": []string{"create"}}}, "not": map[string]any{"anyOf": []any{map[string]any{"required": []string{"id"}}, map[string]any{"required": []string{"patch"}}, map[string]any{"required": []string{"content_change"}}}}},
		map[string]any{"required": []string{"id"}, "properties": map[string]any{"action": map[string]any{"enum": []string{"update"}}}, "oneOf": []any{map[string]any{"required": []string{"document"}}, map[string]any{"required": []string{"patch"}}}, "not": map[string]any{"required": []string{"content_change"}}},
		map[string]any{"required": []string{"id", "content_change"}, "properties": map[string]any{"target": map[string]any{"enum": []string{"entity"}}, "action": map[string]any{"enum": []string{"content"}}}, "not": map[string]any{"anyOf": []any{map[string]any{"required": []string{"document"}}, map[string]any{"required": []string{"patch"}}}}},
		map[string]any{"required": []string{"id"}, "properties": map[string]any{"action": map[string]any{"enum": []string{"delete"}}}, "not": map[string]any{"anyOf": []any{map[string]any{"required": []string{"document"}}, map[string]any{"required": []string{"patch"}}, map[string]any{"required": []string{"content_change"}}}}},
	}
	props("WorkspaceChanges")["operations"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": ref("WorkspaceOperation")}
	for _, name := range []string{"WorkspaceCreate", "WorkspaceCheckout"} {
		for _, field := range []string{"entity_ids", "relation_ids"} {
			props(name)[field] = map[string]any{"type": "array", "maxItems": 100, "items": uuid, "description": "Entity and relation IDs combined must contain at most 100 entries."}
		}
	}
	props("WorkspaceMembersEdit")["user_ids"] = map[string]any{"type": "array", "maxItems": 100, "uniqueItems": true, "items": uuid}
	for _, name := range []string{"Workspace", "WorkspaceCreate", "WorkspaceEdit"} {
		props(name)["name"].(map[string]any)["maxLength"] = 200
	}
	for _, name := range []string{"WorkspacePush", "PullRequestCreate", "PullRequestEdit"} {
		props(name)["title"].(map[string]any)["maxLength"] = 200
	}
	props("WorkspaceCommitCreate")["message"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 2000, "description": "Non-empty commit message after trimming."}
	props("WorkspaceRebase")["message"] = map[string]any{"type": "string", "maxLength": 2000, "description": "Optional rebase commit message; a blank value uses the server default."}
	required("CommitContentChange", "action", "position")
	props("CommitContentChange")["action"] = map[string]any{"type": "string", "enum": []string{"add", "replace", "delete"}}
	props("CommitContentChange")["position"] = map[string]any{"type": "integer", "minimum": 0, "description": "Zero-based index in the workspace Track contents array."}
	props("Workspace")["state"] = map[string]any{"type": "string", "enum": []string{"active", "archived"}}
	props("WorkspaceEdit")["state"] = map[string]any{"type": "string", "enum": []string{"active", "archived"}}
	props("PullRequest")["status"] = map[string]any{"type": "string", "enum": []string{"open", "merged", "closed"}}
	props("PullRequestEdit")["status"] = map[string]any{"type": "string", "enum": []string{"open", "closed"}}
	props("WorkspaceCommitCreate")["id"] = uuid
	props("WorkspaceRebase")["id"] = uuid
	props("PullRequestCreate")["workspace_id"] = uuid
	props("CollaborationCommit")["kind"] = map[string]any{"type": "string", "enum": []string{"edit", "rebase"}}
	props("CollaborationCommit")["author"].(map[string]any)["description"] = "Authenticated caller who created this commit."
	props("CollaborationCommit")["authors"].(map[string]any)["description"] = "Frozen contributors to the staged edits; retained through merge independently of the commit caller and merger."
	props("WorkspaceConflict")["pointer"] = map[string]any{"type": "string", "description": "Conflicting JSON Pointer; an empty pointer denotes the whole object."}
	for _, field := range []string{"base_present", "current_present", "proposed_present"} {
		props("WorkspaceConflict")[field] = map[string]any{"type": "boolean", "description": "Whether the corresponding pointer or whole object exists. False is absence; true with a null value is an explicit JSON null."}
	}
	props("WorkspaceResolution")["value"] = map[string]any{"description": "Explicit resolved JSON value, including null. Use remove=true to remove the pointer."}
	props("WorkspaceResolution")["remove"] = map[string]any{"type": "boolean", "description": "Remove an absent selected side with true; to keep an explicit JSON null, send value:null instead. This also applies to an absent whole object at pointer ''."}
	schemas["WorkspaceResolution"].(map[string]any)["oneOf"] = []any{
		map[string]any{"required": []string{"value"}, "properties": map[string]any{"remove": map[string]any{"enum": []bool{false}}}},
		map[string]any{"required": []string{"remove"}, "properties": map[string]any{"remove": map[string]any{"enum": []bool{true}}}, "not": map[string]any{"required": []string{"value"}}},
	}
	for _, name := range []string{"WorkspaceObject", "CommitObject"} {
		for key, value := range props(name) {
			if key == "base" || key == "committed" || key == "document" || key == "before" || key == "after" {
				value.(map[string]any)["description"] = "Object snapshot or null when absent; private until merged."
			}
		}
	}
	for name, fields := range map[string][]string{
		"Workspace":           {"head_commit_id"},
		"CollaborationCommit": {"workspace_id", "parent_id", "merged_at", "merged_by", "pull_request_id"},
		"PullRequest":         {"source_base_id", "merged_at", "merged_by"},
	} {
		for _, field := range fields {
			props(name)[field].(map[string]any)["nullable"] = true
		}
	}
	props("WorkspacePushResult")["workspace"] = map[string]any{"allOf": []any{ref("Workspace")}, "nullable": true, "description": "Returned only to the workspace owner or members. A reviewer merging another user's request receives null, preserving private staged edits."}
	props("WorkspacePushResult")["pull_request"] = map[string]any{"allOf": []any{ref("PullRequest")}, "nullable": true}
	props("WorkspaceRebaseResult")["commit"] = map[string]any{"allOf": []any{ref("CollaborationCommit")}, "nullable": true}
	for _, name := range []string{"Workspace", "WorkspaceObject", "WorkspacePage", "WorkspaceChangesResult", "CollaborationCommit", "CollaborationAuthor", "CommitObject", "WorkspacePushResult", "WorkspaceRebaseResult", "PullRequest", "PullRequestPage"} {
		// Fields without omitempty are present even when their value is null.
		keys := []string{}
		for key := range props(name) {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		required(name, keys...)
	}
	for name, item := range map[string]string{"WorkspaceCommitList": "CollaborationCommit"} {
		schemas[name] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"items"}, "properties": map[string]any{"items": map[string]any{"type": "array", "items": ref(item)}}}
	}
	schemas["WorkspaceConflictResponse"] = map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"error", "applied", "conflicts"},
		"properties": map[string]any{
			"error":     map[string]any{"type": "string", "enum": []string{"workspace_conflict"}},
			"applied":   map[string]any{"type": "boolean", "enum": []bool{false}},
			"conflicts": map[string]any{"type": "array", "items": ref("WorkspaceConflict")},
		},
	}
	response := func(name, description string) map[string]any {
		return map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": ref(name)}}}
	}
	for _, row := range [][5]string{
		{"/catalog/workspaces", "get", "List accessible private workspaces, including archived copies, with total and limit/offset pagination. Newest updated first and then UUID descending.", "", "WorkspacePage"},
		{"/catalog/workspaces", "post", "Fork current visible entities and relations into a private server workspace; catalog facts, revisions and contributions remain unchanged.", "WorkspaceCreate", "Workspace"},
		{"/catalog/workspaces/{id}", "get", "Read one workspace, its working objects and current version; only its owner and members can access the live working copy. Review permission grants access only to frozen submitted commits.", "", "Workspace"},
		{"/catalog/workspaces/{id}", "patch", "Owner-only rename or archive of a workspace using its current version.", "WorkspaceEdit", "Workspace"},
		{"/catalog/workspaces/{id}/checkout", "post", "Add current visible entity and relation snapshots to the private workspace; existing edited objects are preserved.", "WorkspaceCheckout", "Workspace"},
		{"/catalog/workspaces/{id}/changes", "put", "Stage up to 100 ordered edits atomically in the private workspace. Repeated saves may be committed together. Stable create IDs remain usable by later commits. No catalog facts are changed; source evidence and identity verification are not prerequisites.", "WorkspaceChanges", "WorkspaceChangesResult"},
		{"/catalog/workspaces/{id}/members", "put", "Replace workspace collaborators by user UUID; only its owner may change membership. Members edit the same private copy with version checks.", "WorkspaceMembersEdit", "Workspace"},
		{"/catalog/workspaces/{id}/commits", "get", "Owner/member-only read of the complete immutable workspace commit history, ordered by creation time and UUID ascending. Reviewers access only the frozen submitted chain through the pull request.", "", "WorkspaceCommitList"},
		{"/catalog/workspaces/{id}/commits", "post", "Commit all staged changes as one immutable snapshot. Freeze all staged contributors separately from the authenticated commit caller. The client UUID provides durable retry identity. Further workspace edits become a later commit; catalog facts remain unchanged.", "WorkspaceCommitCreate", "CollaborationCommit"},
		{"/catalog/workspaces/{id}/push", "post", "Push the exact committed workspace head. Account review exemption applies its net changes atomically; otherwise creates or updates a pull request with 202 and applied=false. Uncommitted changes block push. Both paths use the same three-way merger and preserve original commit authors.", "WorkspacePush", "WorkspacePushResult"},
		{"/catalog/workspaces/{id}/rebase", "post", "Rebase committed net changes onto current catalog facts. Disjoint changes merge automatically; explicit resolutions carry current_version and create a new immutable commit. A rebase never applies catalog facts.", "WorkspaceRebase", "WorkspaceRebaseResult"},
		{"/catalog/pull-requests", "get", "List accessible pull requests with status and workspace filters, total and limit/offset pagination. Anonymous callers see merged requests only when every target remains public. Signed-in workspace owners, members and reviewers also see their accessible unmerged requests. Newest updated first and then UUID descending.", "", "PullRequestPage"},
		{"/catalog/pull-requests", "post", "Open or update a pull request for an exact committed workspace head. Creating a request never applies catalog facts.", "PullRequestCreate", "PullRequest"},
		{"/catalog/pull-requests/{id}", "get", "Read an accessible pull request, original authored commits and frozen proposed net changes. source_base_id is the exclusive frozen ancestry boundary and source_head_id is included. A merged request is anonymously readable only while every target remains public; otherwise anonymous callers receive 404. Unmerged requests require workspace owner, membership or catalog.review.manage.", "", "PullRequest"},
		{"/catalog/pull-requests/{id}", "patch", "Edit the title or close/reopen an accessible pull request with its current version; merged requests cannot be reopened.", "PullRequestEdit", "PullRequest"},
		{"/catalog/pull-requests/{id}/merge", "post", "Merge an exact pull-request version and source head; requires catalog.review.manage. Revalidate all net changes, merge them atomically and retain each original author separately from the merger. Conflicts leave catalog facts unchanged.", "PullRequestMerge", "WorkspacePushResult"},
		{"/catalog/commits/{id}", "get", "Read an immutable collaboration commit. A merged commit is anonymously readable only while every target remains public; otherwise anonymous callers receive 404. Unmerged commits require workspace ownership or membership; catalog.review.manage allows only commits inside a frozen submitted pull-request chain, never a private live workspace head.", "", "CollaborationCommit"},
	} {
		path, method := row[0], row[1]
		op := map[string]any{
			"tags": []string{"Collaboration"}, "summary": row[2],
			"security": []any{map[string]any{"session": []string{}}, map[string]any{"bearer": []string{}}},
			"responses": map[string]any{
				"200": response(row[4], "Success"),
				"400": map[string]any{"description": "Invalid strict JSON payload, query parameter or structural constraint. A staged operation error includes its zero-based operation index and applied=false."},
				"401": map[string]any{"description": "Authentication required."},
				"403": map[string]any{"description": "Insufficient workspace, edit or merge permission."},
				"404": map[string]any{"description": "Not found or inaccessible."},
				"409": map[string]any{"description": "workspace_version_conflict, workspace_head_conflict, pull_request_version_conflict, resolution_version_conflict, uncommitted_changes, no_changes, workspace_archived, pull_request_closed, pull_request_merged, workspace_head_required, resolution_stale or a three-way conflict. Facts remain unchanged. Read the latest copy before retrying.", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"anyOf": []any{ref("WorkspaceConflictResponse"), map[string]any{"type": "object", "required": []string{"error"}, "properties": map[string]any{"error": map[string]any{"type": "string"}, "applied": map[string]any{"type": "boolean", "enum": []bool{false}}}}}}}}},
				"503": map[string]any{"description": "Shared rate-budget or transaction dependency unavailable; retain immutable commit IDs when reconciling uncertain responses."},
			},
		}
		if row[3] != "" {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref(row[3])}}}
		}
		if method == "get" && (path == "/catalog/commits/{id}" || path == "/catalog/pull-requests/{id}" || path == "/catalog/pull-requests") {
			delete(op, "security")
		}
		if method == "get" && (path == "/catalog/workspaces" || path == "/catalog/pull-requests") {
			params := []any{
				map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}},
				map[string]any{"name": "offset", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 0, "default": 0}},
				map[string]any{"name": "page", "in": "query", "description": "One-based page; mutually exclusive with offset.", "schema": map[string]any{"type": "integer", "minimum": 1}},
			}
			if path == "/catalog/pull-requests" {
				params = append(params, map[string]any{"name": "status", "in": "query", "description": "Omit to list all accessible statuses.", "schema": map[string]any{"type": "string", "enum": []string{"open", "merged", "closed"}}})
				params = append(params, map[string]any{"name": "workspace_id", "in": "query", "description": "Optional workspace UUID filter; visibility checks still apply.", "schema": uuid})
			}
			op["parameters"] = params
		}
		if names := pathTemplateParams(path); len(names) > 0 {
			params := []any{}
			for _, name := range names {
				params = append(params, map[string]any{"name": name, "in": "path", "required": true, "schema": uuid})
			}
			op["parameters"] = params
		}
		if path == "/catalog/workspaces/{id}/push" {
			op["responses"].(map[string]any)["202"] = response("WorkspacePushResult", "Accepted as a pull request; applied=false and current catalog facts unchanged.")
			op["description"] = "Review exemption follows the account's current permission groups. Sessions use catalog.review.bypass; PAT and MCP use trusted catalog_review_exempt introspection independently of token operation scopes. Read-only keys cannot edit. Structural writes share one transaction and never apply a partial batch."
		}
		if paths[path] == nil {
			paths[path] = map[string]any{}
		}
		paths[path].(map[string]any)[method] = op
	}
	addCatalogAssetPreviewOpenAPI(paths)
	addCatalogAssetReferenceOpenAPI(paths, schemas, schema)
}

func addCatalogAssetPreviewOpenAPI(paths map[string]any) {
	for _, scope := range []struct {
		path, description string
		private           bool
	}{
		{"workspaces", "Owner/member-only image preview referenced by the current workspace, its base/head snapshots or its retained immutable commits. Review permission alone never exposes a private working tree.", true},
		{"pull-requests", "Image preview referenced only by the pull request's frozen source_base_id-exclusive to source_head_id-inclusive commit chain. Uses the same reader visibility as the pull request; later workspace edits cannot add an image to this review.", false},
		{"commits", "Image preview referenced by this immutable commit's base/before/after snapshots, with the same reader visibility as the commit. Anonymous access requires an applied commit whose targets all remain public.", false},
	} {
		content := map[string]any{}
		for _, mime := range []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"} {
			content[mime] = map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}
		}
		op := map[string]any{
			"tags": []string{"Collaboration"}, "summary": scope.description,
			"description": "Returns at most 16 MiB of a referenced raster image. Browser-supplied upstream URLs and storage credentials are never accepted. Responses use Cache-Control: private, no-store and Vary: Cookie, Authorization.",
			"parameters": []any{
				map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string", "format": "uuid"}},
				map[string]any{"name": "assetID", "in": "path", "required": true, "schema": map[string]any{"type": "string", "format": "uuid"}},
			},
			"responses": map[string]any{
				"200": map[string]any{"description": "Referenced image bytes, never HTML or SVG.", "content": content, "headers": map[string]any{
					"Cache-Control":  map[string]any{"schema": map[string]any{"type": "string", "enum": []string{"private, no-store"}}},
					"Vary":           map[string]any{"schema": map[string]any{"type": "string", "enum": []string{"Cookie, Authorization"}}},
					"Content-Length": map[string]any{"schema": map[string]any{"type": "integer", "minimum": 1, "maximum": catalogAssetPreviewLimit}},
				}},
				"404": map[string]any{"description": "Scope inaccessible, asset not referenced in the permitted snapshots, or image not found."},
				"503": map[string]any{"description": "storage_unavailable, including an upstream response exceeding the 16 MiB limit or unsupported content type."},
			},
		}
		if scope.private {
			op["security"] = []any{map[string]any{"session": []string{}}, map[string]any{"bearer": []string{}}}
		}
		paths["/catalog/"+scope.path+"/{id}/assets/{assetID}/content"] = map[string]any{"get": op}
	}
}

func addCatalogAssetReferenceOpenAPI(paths, schemas map[string]any, schema func(reflect.Type) map[string]any) {
	if strings.TrimSpace(os.Getenv("STORAGE_CATALOG_LIFECYCLE_SECRET")) == "" {
		return
	}
	responseRef := schema(reflect.TypeOf(catalogAssetReferenceCheck{}))
	output := schemas["catalogAssetReferenceCheck"].(map[string]any)
	output["required"] = []string{"workspace_id", "entity_id", "asset_id", "referenced", "committed"}
	inputProperties := map[string]any{}
	for _, field := range []string{"workspace_id", "entity_id", "asset_id"} {
		value := map[string]any{"type": "string", "format": "uuid"}
		inputProperties[field] = value
		output["properties"].(map[string]any)[field] = value
	}
	for _, field := range []string{"referenced", "committed"} {
		inputProperties[field] = map[string]any{"type": "boolean", "enum": []bool{false}, "description": "Optional response-shaped placeholder; true is invalid input. Omit when checking a reference."}
	}
	schemas["CatalogAssetReferenceInput"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"workspace_id", "entity_id", "asset_id"}, "properties": inputProperties}
	for name, item := range map[string]map[string]any{"CatalogAssetReferencesRequest": {"$ref": "#/components/schemas/CatalogAssetReferenceInput"}, "CatalogAssetReferencesResponse": responseRef} {
		schemas[name] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"references"}, "properties": map[string]any{"references": map[string]any{"type": "array", "minItems": 1, "maxItems": 1000, "items": item}}}
	}
	paths["/internal/storage/asset-references"] = map[string]any{"post": map[string]any{
		"tags": []string{"Collaboration"}, "summary": "Storage-service-only read of exact catalog workspace and immutable-commit image references, including private and archived copies.",
		"description": "Read-only GC confirmation; does not modify any catalog or storage data. Registered only when STORAGE_CATALOG_LIFECYCLE_SECRET is configured. This root-level internal endpoint is outside /api and requires its dedicated shared service bearer secret; user sessions, PATs and MCP credentials are not accepted. Results preserve request order. referenced means a matching live/base/head or retained commit snapshot; committed means a retained immutable commit reference.",
		"servers":     []any{map[string]any{"url": "/"}},
		"security":    []any{map[string]any{"storageService": []string{}}},
		"requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/CatalogAssetReferencesRequest"}}}},
		"responses": map[string]any{
			"200": map[string]any{"description": "Exact reference booleans; private records are checked without a user visibility filter.", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/CatalogAssetReferencesResponse"}}}},
			"400": map[string]any{"description": "Invalid strict JSON, reference count outside 1..1000, invalid UUID or an input result boolean set to true."},
			"401": map[string]any{"description": "invalid_service_credential."},
			"500": map[string]any{"description": "Catalog reference query failed; callers must preserve storage data when confirmation is unavailable."},
		},
	}}
}
