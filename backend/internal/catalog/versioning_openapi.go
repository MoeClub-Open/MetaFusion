package catalog

import "reflect"

func addVersioningOpenAPI(paths, schemas map[string]any, schema func(reflect.Type) map[string]any) {
	for _, value := range []any{CatalogVersionHead{}, WorkspaceFork{}, WorkspaceVersionAction{}, WorkspaceVersionResult{}, WorkspaceStatus{}, CommitSnapshot{}, CommitPage{}, VersionComparison{}, WorkspaceEvent{}, WorkspaceEventPage{}, WorkspaceTag{}, WorkspaceTagEdit{}, WorkspaceTagPage{}, WorkspaceStash{}, WorkspaceStashAction{}, WorkspaceStashPage{}, WorkspaceDiff{}, CommitDiff{}, PullRequestDiff{}, PullRequestCommitPage{}, RevisionPage{}} {
		schema(reflect.TypeOf(value))
	}
	ref := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }
	props := func(name string) map[string]any { return schemas[name].(map[string]any)["properties"].(map[string]any) }
	for _, row := range [][2]string{{"WorkspaceDiff", "head_id"}, {"CommitDiff", "parent_id"}, {"PullRequestDiff", "source_base_id"}, {"PullRequestCommitPage", "source_base_id"}} {
		props(row[0])[row[1]] = map[string]any{"type": "string", "format": "uuid", "nullable": true}
	}
	for _, name := range []string{"WorkspaceDiff", "CommitDiff", "PullRequestDiff", "PullRequestCommitPage"} {
		fields := []string{}
		for _, key := range sortedMapKeys(props(name)) {
			fields = append(fields, key)
		}
		schemas[name].(map[string]any)["required"] = fields
	}
	schemas["Revision"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "version", "actor_id", "actor_name", "edit_note", "sources", "snapshot", "created_at"}, "properties": map[string]any{
		"id": map[string]any{"type": "integer", "format": "int64"}, "version": map[string]any{"type": "integer", "minimum": 1},
		"actor_id": map[string]any{"type": "string", "description": "Original actor UUID, or empty for historical system revisions."}, "actor_name": map[string]any{"type": "string"}, "edit_note": map[string]any{"type": "string"},
		"sources":  map[string]any{"type": "array", "items": ref("Source"), "nullable": true},
		"snapshot": map[string]any{"oneOf": []any{ref("Entity"), ref("Relation")}}, "created_at": map[string]any{"type": "string", "format": "date-time"}, "commit_id": map[string]any{"type": "string", "format": "uuid", "description": "Present only when the revision has a retained commit association."},
	}}
	props("RevisionPage")["items"] = map[string]any{"type": "array", "items": ref("Revision")}
	schemas["RevisionPage"].(map[string]any)["required"] = []string{"items", "total", "limit", "offset"}
	for _, name := range []string{"WorkspaceVersionAction", "WorkspaceTagEdit", "WorkspaceStashAction"} {
		schemas[name].(map[string]any)["required"] = []string{"id", "expected_version", "expected_head"}
		props(name)["id"] = map[string]any{"type": "string", "format": "uuid", "description": "Durable request ID; retries must retain ID and exact payload."}
		props(name)["expected_version"] = map[string]any{"type": "integer", "minimum": 1}
	}
	schemas["WorkspaceFork"].(map[string]any)["required"] = []string{"id", "commit_id", "name"}
	for _, name := range []string{"WorkspaceVersionAction", "WorkspaceStashAction"} {
		schemas[name].(map[string]any)["required"] = []string{"id", "expected_version", "expected_head", "operation"}
	}
	props("CollaborationCommit")["snapshot_scope"] = map[string]any{"type": "string", "enum": []string{"known_records", "tracked_records", "catalog"}}
	props("Workspace")["base_commit_id"] = map[string]any{"type": "string", "format": "uuid", "nullable": true}
	props("CollaborationCommit")["merge_parent_id"] = map[string]any{"type": "string", "format": "uuid", "nullable": true}
	props("WorkspaceCreate")["id"] = map[string]any{"type": "string", "format": "uuid", "description": "Optional client-selected workspace ID for durable idempotent creation; retain it across uncertain responses."}
	props("WorkspaceVersionAction")["operation"] = map[string]any{"type": "string", "enum": []string{"merge", "cherry_pick", "revert", "restore", "amend", "squash", "reset", "discard"}}
	props("WorkspaceVersionAction")["mode"] = map[string]any{"type": "string", "enum": []string{"soft", "hard"}, "description": "Reset only. Soft retains the working changes; hard replaces them with the chosen committed tree."}
	props("WorkspaceStashAction")["operation"] = map[string]any{"type": "string", "enum": []string{"save", "apply", "pop", "drop"}}
	for _, row := range [][5]string{
		{"/catalog/workspaces/{id}/diff", "get", "Read private working changes relative to the committed head (basis=head, default) or checkout base (basis=base). Includes version/head for detecting concurrent changes; no writes occur.", "", "WorkspaceDiff"},
		{"/catalog/commits/{id}/diff", "get", "Read the original changes retained in one accessible immutable commit, with record filtering and pagination. This is distinct from comparing two record-set snapshots.", "", "CommitDiff"},
		{"/catalog/pull-requests/{id}/diff", "get", "Read the net changes frozen in the selected PR version, filtered and paginated. Later working-copy edits are excluded; version/source_head_id identify the reviewed proposal.", "", "PullRequestDiff"},
		{"/catalog/pull-requests/{id}/commits", "get", "Read the frozen PR commit set, newest position first, with author/time/record filters before pagination. Never follows a mutable workspace head.", "", "PullRequestCommitPage"},
		{"/catalog/head", "get", "Read the official catalog head and transactional sequence. A head ID does not bypass record visibility.", "", "CatalogVersionHead"},
		{"/catalog/history", "get", "List visible official integration checkpoints, newest first. Migration 37 creates an explicit baseline, not fabricated snapshots of earlier history.", "", "CommitPage"},
		{"/catalog/workspaces/fork", "post", "Create an independent private branch from an accessible frozen commit and retain readable original ancestry/authors. Never copy a live unsubmitted working tree.", "WorkspaceFork", "Workspace"},
		{"/catalog/workspaces/{id}/version", "post", "Apply a merge, cherry-pick, inverse commit, snapshot restore, amend, suffix squash, reset or discard in the working copy. No catalog facts change. All writes use version/head CAS and durable request IDs. Rewrite is owner-only, limited to the unintegrated suffix, and retains originals and replacement links. Structural changes and publication use the existing permission checks and merger.", "WorkspaceVersionAction", "WorkspaceVersionResult"},
		{"/catalog/workspaces/{id}/status", "get", "Read committed merge feasibility, pending count and three-way conflicts without staging, committing or publishing.", "", "WorkspaceStatus"},
		{"/catalog/workspaces/{id}/events", "get", "Private immutable reflog of commit, fork, rewrite, tag and stash actions, newest first.", "", "WorkspaceEventPage"},
		{"/catalog/workspaces/{id}/tags", "get", "List private branch tags with stable commit references.", "", "WorkspaceTagPage"},
		{"/catalog/workspaces/{id}/tags", "post", "Owner-only create, move, rename or delete a branch tag. tag_id selects an existing tag; delete=true removes it. Version/head CAS and immutable events retain each change.", "WorkspaceTagEdit", "Workspace"},
		{"/catalog/workspaces/{id}/stashes", "get", "List active private working-change stashes, newest first.", "", "WorkspaceStashPage"},
		{"/catalog/workspaces/{id}/stashes", "post", "Save and clear dirty changes, apply or pop a saved change set with three-way conflict resolution, or drop a stash. Applying leaves changes uncommitted; no catalog facts change.", "WorkspaceStashAction", "Workspace"},
		{"/catalog/commits/{id}/snapshot", "get", "Read a frozen snapshot. catalog reconstructs official entities and relations at that checkpoint, filtered by current visibility before pagination; tracked_records contains the tracked branch set; historical known_records contains only retained historical records. Accounts, settings and stored file bytes are outside catalog versioning.", "", "CommitSnapshot"},
		{"/catalog/commits/{id}/compare", "get", "Compare two accessible frozen record-set snapshots. base is the other commit UUID. Missing records are represented as null. No separate entity-comparison workflow is introduced.", "", "VersionComparison"},
		{"/catalog/commits/{id}/blame", "get", "Read the most recent readable retained DAG edit commit that changed a selected record field; target, record_id and pointer identify it. This reports field attribution, not per-character authorship.", "", "CollaborationCommit"},
	} {
		path, method := row[0], row[1]
		op := map[string]any{"tags": []string{"Collaboration"}, "summary": row[2], "responses": map[string]any{"200": map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": ref(row[4])}}}, "400": map[string]any{"description": "Invalid operation, range, tag, stash or strict JSON payload."}, "403": map[string]any{"description": "Current edit, lifecycle, membership or ownership permission is required."}, "404": map[string]any{"description": "Not found or inaccessible."}, "409": map[string]any{"description": "Version/head conflict, published history rewrite, dirty changes, no changes or workspace_conflict. Conflicts roll back the entire operation.", "content": map[string]any{"application/json": map[string]any{"schema": ref("WorkspaceConflictResponse")}}}}}
		params := []any{}
		for _, name := range pathTemplateParams(path) {
			params = append(params, map[string]any{"name": name, "in": "path", "required": true, "schema": map[string]any{"type": "string", "format": "uuid"}})
		}
		if method == "get" && row[4] != "CatalogVersionHead" && row[4] != "WorkspaceStatus" && row[4] != "CollaborationCommit" {
			params = append(params, versionPageParameters()...)
		}
		if row[4] == "CommitSnapshot" || row[4] == "VersionComparison" || row[4] == "WorkspaceDiff" || row[4] == "CommitDiff" || row[4] == "PullRequestDiff" || row[4] == "CommitPage" || row[4] == "PullRequestCommitPage" {
			params = append(params, versionRecordParameters()...)
		}
		if row[4] == "CommitPage" || row[4] == "PullRequestCommitPage" {
			params = append(params, versionHistoryParameters()...)
		}
		if row[4] == "WorkspaceDiff" {
			params = append(params, map[string]any{"name": "basis", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"head", "base"}, "default": "head"}})
		}
		if row[4] == "VersionComparison" {
			params = append(params, map[string]any{"name": "base", "in": "query", "required": true, "schema": map[string]any{"type": "string", "format": "uuid"}})
		}
		if path == "/catalog/commits/{id}/blame" {
			for _, name := range []string{"target", "record_id", "pointer"} {
				params = append(params, map[string]any{"name": name, "in": "query", "required": true, "schema": map[string]any{"type": "string"}})
			}
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if row[3] != "" {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref(row[3])}}}
		}
		if len(path) >= 19 && path[:19] == "/catalog/workspaces" {
			op["security"] = []any{map[string]any{"session": []string{}}, map[string]any{"bearer": []string{}}}
			op["responses"].(map[string]any)["401"] = map[string]any{"description": "Authentication required."}
		}
		if paths[path] == nil {
			paths[path] = map[string]any{}
		}
		paths[path].(map[string]any)[method] = op
	}
	branch := paths["/catalog/workspaces/{id}/commits"].(map[string]any)["get"].(map[string]any)
	branch["parameters"] = append(branch["parameters"].([]any), append(versionRecordParameters(), versionHistoryParameters()...)...)
	branch["parameters"] = append(branch["parameters"].([]any), map[string]any{"name": "state", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"active", "pending", "integrated", "replaced", "all"}, "default": "active"}, "description": "Branch membership filter. pending excludes integrated commits; replaced retains inactive originals after rewriting."})
	revisions := paths["/catalog/entities/{id}/revisions"].(map[string]any)["get"].(map[string]any)
	revisions["parameters"] = append(revisions["parameters"].([]any), append(versionPageParameters(), versionHistoryParameters()...)...)
	revisions["parameters"] = append(revisions["parameters"].([]any), map[string]any{"name": "version", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1}, "description": "Exact stored revision version. Path ID can identify an entity or relation, including deleted relations with active endpoints. Visibility is applied before counting and pagination."})
}

func versionRecordParameters() []any {
	return []any{
		map[string]any{"name": "target", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"entity", "relation"}}},
		map[string]any{"name": "record_id", "in": "query", "schema": map[string]any{"type": "string", "format": "uuid"}},
	}
}

func versionHistoryParameters() []any {
	return []any{
		map[string]any{"name": "author_id", "in": "query", "description": "Original revision actor or retained commit participant, not the merger.", "schema": map[string]any{"type": "string", "format": "uuid"}},
		map[string]any{"name": "since", "in": "query", "description": "Inclusive RFC3339 timestamp. Official history uses merge time; branch and PR history use commit creation time; revisions use revision creation time.", "schema": map[string]any{"type": "string", "format": "date-time"}},
		map[string]any{"name": "until", "in": "query", "description": "Exclusive RFC3339 timestamp; must be later than since.", "schema": map[string]any{"type": "string", "format": "date-time"}},
	}
}

func versionPageParameters() []any {
	return []any{
		map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}},
		map[string]any{"name": "offset", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 0, "default": 0}},
		map[string]any{"name": "page", "in": "query", "description": "One-based page, mutually exclusive with offset.", "schema": map[string]any{"type": "integer", "minimum": 1}},
	}
}
