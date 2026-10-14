package catalog

import (
	"reflect"
	"strings"
	"time"
)

// pathTemplateParams 提取路径里的模板变量名（/a/{id}/b/{code} → [id code]），
// 保持出现顺序并去重，供 OpenAPI 逐个声明 in=path 参数。
func pathTemplateParams(path string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, seg := range strings.Split(path, "{") {
		i := strings.Index(seg, "}")
		if i <= 0 {
			continue
		}
		if name := seg[:i]; name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// OpenAPI derives DTO schemas from the same structs decoded by the handlers.
// Dynamic attributes are further constrained by the published definitions endpoint.
func OpenAPI() map[string]any {
	schemas := map[string]any{}
	var schema func(reflect.Type) map[string]any
	schema = func(t reflect.Type) map[string]any {
		if t.Kind() == reflect.Pointer {
			return schema(t.Elem())
		}
		if t == reflect.TypeOf(time.Time{}) {
			return map[string]any{"type": "string", "format": "date-time"}
		}
		switch t.Kind() {
		case reflect.Struct:
			name := t.Name()
			if _, ok := schemas[name]; !ok {
				schemas[name] = map[string]any{}
				props := map[string]any{}
				for i := 0; i < t.NumField(); i++ {
					f := t.Field(i)
					key := strings.Split(f.Tag.Get("json"), ",")[0]
					if key != "" && key != "-" {
						props[key] = schema(f.Type)
					}
				}
				schemas[name] = map[string]any{"type": "object", "properties": props, "additionalProperties": false}
			}
			return map[string]any{"$ref": "#/components/schemas/" + name}
		case reflect.Map:
			return map[string]any{"type": "object", "additionalProperties": schema(t.Elem())}
		case reflect.Slice:
			return map[string]any{"type": "array", "items": schema(t.Elem())}
		case reflect.Bool:
			return map[string]any{"type": "boolean"}
		case reflect.Int, reflect.Int64:
			return map[string]any{"type": "integer"}
		case reflect.Float64:
			return map[string]any{"type": "number"}
		case reflect.String:
			return map[string]any{"type": "string"}
		default:
			return map[string]any{}
		}
	}
	for _, v := range []any{CatalogCommit{}, CommitReceipt{}, FieldConflict{}, CommitConflict{}, CheckoutRequest{}, CatalogCheckout{}, Entity{}, IdentityCandidateQuery{}, IdentityCandidateResponse{}, IdentityContext{}, CandidateAssessment{}, IdentityBatchRequest{}, IdentityBatchResponse{}, IdentityBatchQuery{}, IdentityBatchResult{}, EntitySearchPage{}, EntityLinksPage{}, RelationshipQueryRequest{}, RelationshipQueryResponse{}, ReleaseTOC{}, ExpressionComposition{}, ReleaseEditions{}, TrackContentEdit{}, TrackStatusEdit{}, Edit{}, Relation{}, RelationEdit{}, LifecycleEdit{}, UnpublishEdit{}, DefinitionConfig{}, Definitions{}, ExternalDatabase{}, Shelf{}, HomePreferences{}, HomeSection{}, UserContributions{}, ContributionItem{}, UserContributionStats{}, ExternalSourceQueryRequest{}, ExternalSourceQueryResponse{}, FetchMeta{}, ImporterPreviewRequest{}, ImporterPreviewResponse{}, ImporterSource{}, VersionInfo{}, DefinitionImpact{}, Notification{}, RateLimitConfig{}, RateLimitSave{}} {
		schema(reflect.TypeOf(v))
	}
	// Historical responses can contain self; new evidence cannot use it or this
	// instance's host. Keep a separate input schema rather than rewriting history.
	if src, ok := schemas["Source"].(map[string]any); ok {
		props := src["properties"].(map[string]any)
		props["kind"] = map[string]any{"type": "string", "enum": []string{"url", "publication", "self"}, "description": "self is historical-only and is rejected in new evidence"}
		inputProps := map[string]any{}
		for k, v := range props {
			inputProps[k] = v
		}
		inputProps["kind"] = map[string]any{"type": "string", "enum": []string{"url", "publication"}}
		props["key"] = map[string]any{"type": "string", "pattern": "^[A-Za-z][A-Za-z0-9_-]{0,63}$", "description": "Commit-local evidence pool name referenced by CommitOperation.source_refs; required on every commit source and unique within one commit (source_key_required / duplicate_source_key / invalid_source_key). Kept on stored revisions"}
		props["pointers"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Server-written on revisions only: JSON Pointer field paths this source supports for that revision, copied from source_refs; absent means the whole operation. Never accepted in input (source_pointers_not_allowed)"}
		props["scope"] = map[string]any{"type": "string", "enum": []string{"commit"}, "description": "Read-only, historical revisions only: commit = this source was the whole commit's evidence pool (written before per-operation source_refs existed) and does not prove any specific field of this revision. Never accepted in input (source_scope_not_allowed)"}
		inputProps["key"] = props["key"]
		delete(inputProps, "pointers")
		delete(inputProps, "scope")
		schemas["SourceInput"] = map[string]any{"type": "object", "properties": inputProps, "required": []string{"kind", "citation"}, "description": "Externally eligible evidence; self and URLs on the configured instance host (including subdomains) return source_not_eligible. Publication citations may omit URL. Community sources are eligible."}
		for _, name := range []string{"Edit", "RelationEdit", "LifecycleEdit", "UnpublishEdit", "TrackContentEdit", "TrackStatusEdit", "CommitOperation", "RateLimitSave", "ExternalSourceQueryRequest"} {
			if obj, ok := schemas[name].(map[string]any); ok {
				if p, ok := obj["properties"].(map[string]any); ok {
					if _, exists := p["sources"]; exists {
						p["sources"] = map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/SourceInput"}}
					}
				}
			}
		}
	}
	schemas["RateLimitSave"].(map[string]any)["required"] = []string{"policy", "expected_etag", "edit_note"}
	schemas["CommitOperation"].(map[string]any)["properties"].(map[string]any)["document"] = map[string]any{"type": "object", "additionalProperties": true}
	schemas["CommitPatch"].(map[string]any)["properties"].(map[string]any)["value"] = map[string]any{}
	schemas["CatalogCommit"].(map[string]any)["required"] = []string{"id", "definitions_etag", "edit_note", "sources", "operations"}

	commitSchema := schemas["CatalogCommit"].(map[string]any)
	commitProps := commitSchema["properties"].(map[string]any)
	commitProps["id"].(map[string]any)["format"] = "uuid"
	commitProps["operations"].(map[string]any)["minItems"] = 1
	commitProps["operations"].(map[string]any)["maxItems"] = 100
	opSchema := schemas["CommitOperation"].(map[string]any)
	opSchema["required"] = []string{"target", "action"}
	opProps := opSchema["properties"].(map[string]any)
	opProps["target"].(map[string]any)["enum"] = []string{"entity", "relation"}
	opProps["action"].(map[string]any)["enum"] = []string{"create", "update"}
	opSchema["oneOf"] = []any{
		map[string]any{"properties": map[string]any{"target": map[string]any{"const": "entity"}, "action": map[string]any{"const": "create"}}, "required": []string{"ref", "document", "reviewed_candidate_ids"}},
		map[string]any{"properties": map[string]any{"target": map[string]any{"const": "relation"}, "action": map[string]any{"const": "create"}}, "required": []string{"ref", "document"}},
		map[string]any{"properties": map[string]any{"action": map[string]any{"const": "update"}}, "required": []string{"id", "base_version", "patch"}},
	}
	opProps["patch"].(map[string]any)["minItems"] = 1
	opProps["patch"].(map[string]any)["maxItems"] = 200
	opProps["reviewed_candidate_ids"].(map[string]any)["maxItems"] = 1000
	opProps["source_refs"] = map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"$ref": "#/components/schemas/SourceRef"}, "description": "Required evidence mapping for this operation: only the referenced commit sources (by key) are written to this operation's revision, each with its pointers. Errors: source_refs_required (missing or empty), unknown_source_ref, duplicate_source_ref, invalid_source_pointer, source_pointer_not_in_document (create: pointer must resolve in the document), source_pointer_not_patched (update: pointer must equal, contain or lie under a patch path); every pool source must be referenced by some operation (source_unused)"}
	opSchema["required"] = []string{"target", "action", "source_refs"}
	commitProps["sources"] = map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"$ref": "#/components/schemas/SourceInput"}, "description": "Commit evidence pool; every source needs a unique key and must be referenced by at least one operation's source_refs"}
	schemas["SourceRef"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"source"}, "properties": map[string]any{
		"source":   map[string]any{"type": "string", "description": "key of a source in CatalogCommit.sources"},
		"pointers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "JSON Pointer paths (e.g. /attributes/catalog_number) this source supports; omitted = the whole operation"},
	}}
	schemas["CommitPatch"].(map[string]any)["required"] = []string{"path"}
	schemas["CommitPatch"].(map[string]any)["oneOf"] = []any{map[string]any{"required": []string{"value"}, "properties": map[string]any{"remove": map[string]any{"const": false}}}, map[string]any{"required": []string{"remove"}, "properties": map[string]any{"remove": map[string]any{"const": true}}, "not": map[string]any{"required": []string{"value"}}}}

	identitySchema := schemas["IdentityCandidateQuery"].(map[string]any)
	identitySchema["required"] = []string{"kind"}
	identityProps := identitySchema["properties"].(map[string]any)
	identityProps["kind"].(map[string]any)["enum"] = Kinds
	identityProps["limit"].(map[string]any)["minimum"] = 1
	identityProps["limit"].(map[string]any)["maximum"] = 1000
	identityProps["limit"].(map[string]any)["default"] = 1000
	if ctxProp, ok := identityProps["context"].(map[string]any); ok {
		ctxProp["description"] = "Optional ranking context (creator/performer/label agent UUIDs, catalog_number values). Adjusts confidence and recommended_action only — never filters candidates out. Same-name different-artist entities remain in the result."
	}
	if matchSchema, ok := schemas["IdentityCandidateMatch"].(map[string]any); ok {
		if props, ok := matchSchema["properties"].(map[string]any); ok {
			if a, ok := props["assessment"].(map[string]any); ok {
				a["description"] = "Advisory assessment: matched_fields, optional conflicts, context_summary, confidence 0-1, recommended_action reuse|review|distinct. Server never auto-merges."
			}
		}
	}
	batchReq := schemas["IdentityBatchRequest"].(map[string]any)
	batchReq["required"] = []string{"queries"}
	batchProps := batchReq["properties"].(map[string]any)
	batchProps["queries"].(map[string]any)["minItems"] = 1
	batchProps["queries"].(map[string]any)["maxItems"] = 50
	batchQuery := schemas["IdentityBatchQuery"].(map[string]any)
	batchQuery["required"] = []string{"key", "kind"}
	// Anonymous embed of IdentityCandidateQuery is promoted by encoding/json but the
	// OpenAPI walker skips tag-less fields; copy the query properties explicitly.
	bqProps, _ := batchQuery["properties"].(map[string]any)
	if bqProps == nil {
		bqProps = map[string]any{}
		batchQuery["properties"] = bqProps
	}
	if iq, ok := schemas["IdentityCandidateQuery"].(map[string]any); ok {
		if iqProps, ok := iq["properties"].(map[string]any); ok {
			for k, v := range iqProps {
				if _, exists := bqProps[k]; !exists {
					bqProps[k] = v
				}
			}
		}
	}
	if k, ok := bqProps["key"].(map[string]any); ok {
		k["minLength"] = 1
		k["maxLength"] = 64
		k["description"] = "Client correlation key; unique within the batch"
	} else {
		bqProps["key"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 64, "description": "Client correlation key; unique within the batch"}
	}
	if k, ok := bqProps["kind"].(map[string]any); ok {
		k["enum"] = Kinds
	}
	if assess, ok := schemas["CandidateAssessment"].(map[string]any); ok {
		assess["required"] = []string{"matched_fields", "confidence", "recommended_action"}
		if props, ok := assess["properties"].(map[string]any); ok {
			if a, ok := props["recommended_action"].(map[string]any); ok {
				a["enum"] = []string{"reuse", "review", "distinct"}
				a["description"] = "Advisory only: reuse = strong evidence to reuse existing identity; review = human/agent should inspect; distinct = context conflicts suggest a different entity. Server never auto-merges."
			}
			if f, ok := props["matched_fields"].(map[string]any); ok {
				f["description"] = "Evidence codes: title_exact, alias, external_id, attribute, ctx_creator, ctx_performer, ctx_label, ctx_catalog_number"
			}
			if f, ok := props["conflicts"].(map[string]any); ok {
				f["description"] = "Mismatch codes when the candidate has conflicting context: ctx_creator_mismatch, ctx_performer_mismatch, ctx_label_mismatch, ctx_catalog_number_mismatch"
			}
		}
	}
	trackStatusSchema := schemas["TrackStatusEdit"].(map[string]any)
	trackStatusSchema["required"] = []string{"status", "expected_version", "edit_note", "sources"}
	trackStatusProperties := trackStatusSchema["properties"].(map[string]any)
	trackStatusProperties["status"].(map[string]any)["enum"] = []string{"draft", "pending_review", "published"}
	trackStatusProperties["expected_version"].(map[string]any)["minimum"] = 1
	trackStatusProperties["edit_note"].(map[string]any)["minLength"] = 1
	trackStatusProperties["sources"].(map[string]any)["minItems"] = 1
	schemas["DefinitionSave"] = map[string]any{"type": "object", "required": []string{"document", "expected_etag", "edit_note", "sources"}, "properties": map[string]any{"document": schema(reflect.TypeOf(Definitions{})), "expected_etag": map[string]any{"type": "string"}, "edit_note": map[string]any{"type": "string"}, "sources": schema(reflect.TypeOf([]Source{}))}}
	schemas["DefinitionImpactRequest"] = map[string]any{"type": "object", "required": []string{"document"}, "properties": map[string]any{"document": schema(reflect.TypeOf(Definitions{}))}}
	paths := map[string]any{}
	add := func(path, method, summary, request, response string, auth bool) {
		// 账号/收藏前缀已归子系统，本服务只剩目录自己的三类路径。
		tag := "Catalog"
		if strings.HasPrefix(path, "/admin/catalog-definitions") || path == "/catalog/definitions" {
			tag = "Definitions"
		}
		// 外部权威库的读写（/catalog/external-databases 与 /admin/external-databases）
		// 归第三个 tag：tags 清单里声明了它，这些操作就不该留在 Catalog 组里。
		if strings.Contains(path, "external-databases") {
			tag = "ExternalDatabases"
		}
		// 站内通知（收件箱 / 未读数 / 标记已读 / 跨服务投递）自成一组：
		// 它读的不是目录数据，而是"发给调用者的事件"，与 Catalog 组混在一起会误导接入方。
		if strings.HasPrefix(path, "/notifications") {
			tag = "Notifications"
		}
		// API 限流策略是运行参数而非元数据定义：与 definitions 混在一组会让接入方
		// 以为改配额会影响元数据契约（它不会，两种文档各自持有自己的 etag）。
		if strings.HasPrefix(path, "/admin/rate-limits") {
			tag = "RateLimits"
		}
		op := map[string]any{
			"tags":    []string{tag},
			"summary": summary,
			"responses": map[string]any{
				"200": map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + response}}}},
				"400": map[string]any{"description": "Invalid payload, source, definition or structural constraint"},
				"401": map[string]any{"description": "Authentication required"},
				"403": map[string]any{"description": "Insufficient edit or review permission"},
				"404": map[string]any{"description": "Not found or not visible"},
				"409": map[string]any{"description": "Version conflict; read current data before retrying"},
			},
		}
		if auth {
			op["security"] = []any{map[string]any{"session": []string{}}, map[string]any{"bearer": []string{}}}
		}
		// 路径模板变量必须逐个声明：只声明 {id} 会让 {code} 这类路径留下
		// "路径里有模板变量、参数列表里没有"的空洞（OpenAPI 3.0 里属于无效文档）。
		if names := pathTemplateParams(path); len(names) > 0 {
			list := make([]any, 0, len(names))
			for _, name := range names {
				paramSchema := map[string]any{"type": "string"}
				if name == "position" {
					paramSchema = map[string]any{"type": "integer", "minimum": 0}
				}
				list = append(list, map[string]any{"name": name, "in": "path", "required": true, "schema": paramSchema})
			}
			op["parameters"] = list
		}
		if request != "" {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + request}}}}
		}
		if paths[path] == nil {
			paths[path] = map[string]any{}
		}
		paths[path].(map[string]any)[method] = op
	}
	schemas["Result"] = map[string]any{"type": "object", "additionalProperties": true}
	// 状态计数的响应形状：五个状态键逐个显式声明（entityStatuses 与迁移 000001 的 CHECK 同集），
	// 客户端因此知道列表端点永远不给的 deleted / merged 一定会出现，而不是"看运气有没有键"。
	statusProps := map[string]any{}
	for _, code := range entityStatuses {
		statusProps[code] = map[string]any{"type": "integer", "minimum": 0}
	}
	schemas["EntityStatusCounts"] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"statuses": map[string]any{"type": "object", "additionalProperties": false, "description": "Every status of the catalog.entities.status constraint, zero-filled: a missing key is not the same statement as a real zero", "properties": statusProps},
		"total":    map[string]any{"type": "integer", "minimum": 0},
	}}
	// 可用导入源清单：items 的元素类型单列，客户端不必猜 names 的语种键与 category 取值。
	schemas["ImporterSourceList"] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"items": schema(reflect.TypeOf([]ImporterSource{})),
	}}
	// 收件箱列表：items 元素与 total/unread 逐个声明。unread 与 items 同一次响应给出是有意的
	// （见 ListNotifications）：分两次请求会出现"角标 3、列表全已读"的中间窗口。
	schemas["NotificationList"] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"items":  schema(reflect.TypeOf([]Notification{})),
		"total":  schema(reflect.TypeOf(0)),
		"unread": schema(reflect.TypeOf(0)),
	}}
	schemas["NotificationUnread"] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"unread": schema(reflect.TypeOf(0)),
	}}
	schemas["NotificationReadResult"] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"ok":      schema(reflect.TypeOf(false)),
		"unread":  schema(reflect.TypeOf(0)),
		"updated": schema(reflect.TypeOf(0)),
	}}
	schemas["NotificationDelivery"] = map[string]any{"type": "object", "additionalProperties": false, "required": []string{"recipient_id", "type"}, "properties": map[string]any{
		"recipient_id": map[string]any{"type": "string", "description": "Recipient user id (UUID) as resolved by the calling service"},
		"type":         map[string]any{"type": "string", "enum": NotificationTypes(), "description": "Notification type code; an unknown code is 400 invalid_notification_type"},
		"subject_type": map[string]any{"type": "string", "description": "topic | entity | import (free-form, at most 64 chars)"},
		"subject_id":   map[string]any{"type": "string", "description": "Clickable target id (at most 200 chars)"},
		"payload":      map[string]any{"type": "object", "additionalProperties": true, "description": "Per-type display payload; unknown keys are stored as-is and returned to the recipient"},
		"dedupe_key":   map[string]any{"type": "string", "description": "Aggregation key (at most 300 chars); defaults to type:subject_id. Events sharing (recipient_id, dedupe_key) merge into one row: count increments, read_at resets to unread and updated_at is bumped"},
		"event_id":     map[string]any{"type": "string", "description": "Stable identity of this event (at most 200 chars), so that an upstream retry is idempotent: delivering the same event_id again on the same (recipient_id, dedupe_key) leaves count, read_at and updated_at untouched. Omitted means a new event every call"},
	}}
	schemas["PublishedDefinitions"] = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"etag":               schema(reflect.TypeOf("")),
		"document":           schema(reflect.TypeOf(Definitions{})),
		"updated_at":         schema(reflect.TypeOf(time.Time{})),
		"kinds":              schema(reflect.TypeOf(map[string]KindRecord{})),
		"relationship_rules": schema(reflect.TypeOf([]RelationshipRule{})),
	}}
	for _, r := range [][6]string{
		{"/catalog/releases/{id}/editions", "get", "Read explicitly grouped editions in one visible snapshot. Shared release subjects do not imply a group; ungrouped releases return group=null and editions=[].", "", "ReleaseEditions", ""},
		{"/catalog/expressions/{id}/composition", "get", "Read ordered direct parts and containing expressions from relations declaring expression_composition usage. Hidden or retired peers are omitted.", "", "ExpressionComposition", ""},
		{"/catalog/tracks/{id}/contents", "post", "Add one inclusion with Track expected_version and edit evidence; returns the visible updated Track. No other inclusion is replaced.", "TrackContentEdit", "Entity", "auth"},
		{"/catalog/tracks/{id}/contents/{position}", "put", "Replace one inclusion identified by its previous position; inclusion.position may reorder it. Track expected_version prevents stale edits.", "TrackContentEdit", "Entity", "auth"},
		{"/catalog/tracks/{id}/contents/{position}", "delete", "Delete one visible inclusion with Track expected_version and edit evidence in the request body. Hidden records are preserved.", "TrackContentEdit", "Entity", "auth"},
		{"/catalog/tracks/{id}/status", "patch", "Change only Track status, version and updated_at from current database facts; never replace contents, structural rows or other document keys. Strict TrackStatusEdit body: status, expected_version, edit_note and sources; unknown keys, including contents, return 400 invalid_payload. Accepts draft/pending_review/published with the same editing and publication permissions as whole-entity Save; anonymous callers get 401, unauthorized callers 403. Published requires translations and all references (including unchanged contents) to be anonymously visible; invalid references are rejected. deleted/merged and unknown statuses return 400 invalid_status; demotion from published must use the unpublish endpoint (400 use_lifecycle_endpoint). Non-Track targets return 400 not_track, stale expected_version returns 409 version_conflict. Full facts are recorded in revisions and entity.saved outbox in the same transaction, and the response applies current content visibility.", "TrackStatusEdit", "Entity", "auth"},
		{"/version", "get", "Build identity of the running catalog process: the version and git SHA injected at build time, the build timestamp, and when this process started (all UTC, RFC 3339). Anonymous and read-only — it exists so that a deploy, a cutover or a rollback can be answered with 'which commit is actually running' instead of guessing from behaviour. Deliberately free of configuration, credentials, host names and database or object-storage details; anything the build did not inject comes back as the literal string unknown (an empty field would be indistinguishable from a broken build)", "", "VersionInfo", ""},
		{"/catalog/definitions", "get", "Published dynamic definitions, fixed entity-skeleton names, and a relationship registry. Structural endpoints are database-constrained; their forward and reverse display names are editable in definitions.structure", "", "PublishedDefinitions", ""}, {"/catalog/developer/request-logs", "get", "Own API request log, newest first (authenticated calls only; route is the gin template path without entity ids or query strings; 30-day retention)", "", "Result", ""},
		{"/catalog/entities", "get", "Entity search (kind/kinds/q/status/work_id/content_unit_id/release_id/medium_id/parent_id/field/value/tags/original_language/has_pictures; field takes a published field code; a dotted path works only when every level is searchable and enabled, so the seed definitions expose top-level codes such as tags, duration, edition_date and barcode; items + total (browse: exact PostgreSQL count; text search: OpenSearch snapshot with total_relation=index_snapshot, has_more and next_cursor), plus 400 invalid_sort / invalid_order for a sort key or direction outside the whitelist, and 400 invalid_query_param / query_too_long / invalid_limit / invalid_offset / invalid_page / pagination_conflict for parameters that cannot be sent as given (see each parameter). Throttled to a 180-request-per-minute shared read/write default budget across catalog replicas, counted per account for a signed-in caller and per client IP otherwise; the live policy (group presets and per-account overrides, including unlimited) is readable at GET /admin/rate-limits. Every throttled response carries X-RateLimit-Limit, X-RateLimit-Remaining and X-RateLimit-Reset (seconds until the window resets), and an exhausted budget answers 429 rate_limited with Retry-After; a subject marked unlimited is not counted at all and therefore carries none of those headers, because there is no window to report)", "", "EntitySearchPage", ""}, {"/catalog/entities", "post", "Create entity with eligible external evidence. Supports Idempotency-Key with durable replay protection. New identity candidates return 409 identity_candidates_changed with identity_review; reviewed_candidate_ids explicitly confirms inspected candidates. No candidates requires no review field", "Edit", "Entity", "auth"},
		{"/catalog/entities/stats", "get", "Entity counts grouped by status over the whole catalog.entities table, plus the real table total (requires catalog.lifecycle.manage; throttled to a 180/min shared read/write budget (admin is unlimited by default, overridable by account/admin-group policy), per account when signed in and otherwise per client IP). The list endpoint's shared filter always excludes deleted and merged rows — that visibility rule is deliberate and unchanged — so this aggregate is the only source for the tombstone count. statuses always carries all five codes (draft, pending_review, published, deleted, merged), zero-filled when a status has no rows, and total equals count(*) of the table, so a key that is present with 0 is a fact while an absent key means the count was not obtained", "", "EntityStatusCounts", "auth"},
		{"/catalog/tags", "get", "Tag frequency aggregation over published entities' attributes.tags (q filter, limit<=500)", "", "Result", ""},
		{"/catalog/releases/{id}/toc", "get", "Read a visible release with ordered media and tracks plus deduplicated visible expressions in one consistent snapshot (throttled to a 180/min shared read/write budget (admin is unlimited by default, overridable by account/admin-group policy), per account when signed in and otherwise per client IP; override per group or per account via /admin/rate-limits). Hidden child entities and their inclusions are omitted; each entity carries its own version and definition_etag identifies the live field contract. Errors: 400 invalid_id / not_release, 404 not_found", "", "ReleaseTOC", ""},
		{"/catalog/entities/{id}/quality", "get", "Computed-on-demand quality audit of one visible entity (never stored, advisory only, never modifies data). status: complete = automated checks found no issue (UI label 无缺项; NOT human-verified), partial = structural gaps (warning issues), needs_verification = data problems such as dangling references (error issues). checks lists the check codes actually run: dangling_reference, missing_required_field, release_missing_media, medium_missing_tracks, track_empty_contents, track_position_gap (within one medium, top-level track positions must be contiguous from 1; starting at 2 or 0-based is partial). Required relationships are not checked (relationship rules carry no minimum). Shares the catalog 180/min read/write budget. Errors: 400 invalid_id, 404 not_found", "", "Result", ""},
		{"/catalog/quality/batch", "post", "Batch quality audit, JSON body {ids:[...]} with 1-100 UUIDs in one repeatable-read snapshot; returns {reports:[...], errors:{id:invalid_id|not_found}} so one bad id never fails the batch. Same semantics as GET /catalog/entities/{id}/quality. Errors: 400 ids_required / too_many_ids. Shares the catalog 180/min read/write budget", "Result", "Result", ""},
		{"/catalog/entities/{id}", "get", "Read visible entity", "", "Entity", ""}, {"/catalog/entities/{id}", "put", "Replace entity with optimistic version check. Changes to title, localized titles or external_links recheck identity candidates and require explicit reviewed_candidate_ids when candidates exist (409 identity_candidates_changed). Track reads omit inclusions of expressions the reader cannot see; a whole-entity PUT that removes or changes such hidden historical inclusions is forbidden unless the editor has catalog.lifecycle.manage, so a filtered read cannot erase uninspected facts. Whole-entity replacement, pictures included: the pictures array is ordered and **array order is the display order**, so pictures[0] is the manually selected current cover (the server never re-sorts). taken_at is image creation/publication metadata. Optional version_label (localized names), usage_period {begin,end} describes a historically or concurrently valid image version; dates accept YYYY, YYYY-MM, YYYY-MM-DD or RFC3339 and a definitely reversed period is rejected with invalid_picture_period. Open/overlapping periods are allowed and never auto-select the cover. Optional role must be an enabled code of the picture_role vocabulary (empty = undeclared), asset_id must be a storage-service asset UUID (self-hosted cover bound with binding_role=cover_image). Writes are refused with duplicate_picture (same URL twice on one entity), too_many_pictures (over 40, because every picture is copied into each revision snapshot) and invalid_term (or unknown_vocabulary when the picture_role vocabulary itself is gone) for a role code outside the vocabulary", "Edit", "Entity", "auth"}, {"/catalog/entities/{id}/resolve", "get", "Resolve merged identity", "", "Entity", ""}, {"/catalog/entities/{id}/identity", "get", "Resolve canonical identity plus historical aliases (X01 identity contract: canonical_id, aliases, entity; read-only, rewrites nothing)", "", "Result", ""}, {"/catalog/entities/identity", "post", "Batch identity resolution for cross-service alias aggregation (files/comments/favorites); JSON body {ids:[...]} up to 500, unresolvable identities (missing, invisible or redirect cycles) listed in missing; database, cancellation and timeout failures abort the entire request with a server error rather than returning partial items", "Result", "Result", ""}, {"/catalog/entities/{id}/lifecycle", "post", "Merge or retire (requires catalog.lifecycle.manage)", "LifecycleEdit", "Entity", "auth"}, {"/catalog/entities/{id}/unpublish", "post", "Demote a published entity back to draft, the only way back from published (requires catalog.lifecycle.manage; Save refuses demotion with use_lifecycle_endpoint). Request body is UnpublishEdit: expected_version, edit_note and at least one source, validated like every other write. Only published -> draft is accepted: draft/pending_review have nothing to unpublish and deleted/merged are terminal, so all four answer 400 invalid_status (never 500); a stale expected_version is 409 version_conflict. The transition is recorded in the existing revision trail as a revision row plus an entity.unpublished outbox event, so GET /catalog/entities/{id}/revisions lists it while the contribution stats keep their calibration (audit_actions still counts only entity.deleted / entity.merged)", "UnpublishEdit", "Entity", "auth"},
		{"/catalog/entities/{id}/revisions", "get", "Read visible revision history; historical track contents are filtered by current expression visibility, while stored revision snapshots remain unchanged", "", "Result", ""}, {"/catalog/entities/{id}/relations", "get", "Read contextual forward and reverse relations; the response carries subject_id (the queried entity) and entities covering both ends of every returned relation — including the subject itself, so callers can render either side without an extra lookup", "", "Result", ""}, {"/catalog/entities/{id}/occurrences", "get", "Read own reverse inclusions, scoped by entity kind (expression=itself, content_unit=its expressions, work=its expressions); an inclusion is returned only when its expression, release, medium and track are visible, and returned track contents omit hidden expressions", "", "Result", ""}, {"/catalog/expressions/details", "post", "Batch expression details (entity + own inclusions + same-content-unit siblings + credit) for release pages; JSON body {ids:[...]}. Own and sibling inclusions require visible expressions and carrier chains; hidden expression IDs, locators and inclusion attributes are omitted", "Result", "Result", ""}, {"/catalog/external-databases", "get", "List active external authority database definitions", "", "Result", ""},
		{"/catalog/entities/{id}/links", "get", "Read visible direct structural and semantic links from their canonical stores; each link has a direction relative to subject_id. The returned window includes endpoint entities, the live definitions ETag, and has_more. Fixed links are edited only through their owning entities. Pagination follows endpoint visibility and historical semantic attribute filtering (throttled to a 180/min shared read/write budget (admin is unlimited by default, overridable by account/admin-group policy), per account when signed in and otherwise per client IP; override per group or per account via /admin/rate-limits); this first version does not claim per-edge evidence or historical rule versions", "", "EntityLinksPage", ""},
		{"/catalog/checkout", "post", "Read versioned entities and relations in one repeatable-read snapshot (0..100 IDs total). Includes definitions_etag, visibility-filtered Track contents; this is a working copy, not an identity query. Authentication required", "CheckoutRequest", "CatalogCheckout", "auth"},
		{"/catalog/commits/preview", "post", "Validate an atomic commit through the real write path, then roll back every fact, revision, notification and outbox event. Up to 100 ordered create/update operations; use explicit local references {\"$ref\":\"name\"}. Preview IDs are provisional. No durable receipt. The preview receipt carries quality_warnings: advisory QualityIssue items (code, severity, entity_id, field, value, message) from the same automated audit as /catalog/entities/{id}/quality, evaluated on the uncommitted state of every affected entity; they never block the commit. Shares the catalog 180/min read/write budget", "CatalogCommit", "CommitReceipt", "auth"},
		{"/catalog/commits", "post", "Atomically push up to 100 ordered entity/relation create/update operations with evidence and definitions_etag. Entity creates and identity-changing entity updates require reviewed_candidate_ids (empty array allowed): formal/localized titles plus external IDs are rechecked within Work/Release/Medium scope under sorted identity-term locks; changed candidate sets return 409 identity_candidates_changed. @local:ref can identify a previously created candidate in this batch. Public legacy entity POST/PUT also recheck changed identity and allow explicit reviewed_candidate_ids; this review permits inspected distinct objects with shared titles, rather than imposing global semantic uniqueness. Canonical UUID commit id is a durable idempotency key: same owner+payload replays receipt; changed payload returns 409 idempotency_conflict. Updates carry authoritative base_version and granular JSON Pointer set/remove patches: disjoint edits merge, identical edits are no-ops, overlapping divergent edits return 409 commit_conflict with applied=false, a conflict summary (operation/id/base_version/current_version) and conflicts[] of FieldConflict (pointer, base, current, proposed, kind, suggestions: manual_merge|take_current|rebase_keep_mine); the same conflicts array is also top-level. Arrays are atomic; server-owned fields and scopes cannot be patched. Hidden Track contents require specialized content endpoints. Any validation/conflict rolls back the entire batch. Schema changes return 409 definitions_conflict. Deadlines 15s, lock waits 3s; shares the catalog 180/min read/write budget. A lost response is recovered with GET /catalog/commits/{id}, not a new commit id. Evidence: sources is a keyed pool (every source needs a unique key) and every operation must declare source_refs [{source:key, pointers:[json-pointer]}]; each revision then stores only its referenced sources with their pointers, so batch evidence never mixes across entities. BREAKING: old commit format (sources without key, operations without source_refs) is rejected with no compatibility mode — clients must upgrade. Preview and push both reject an operation without source_refs as 400 with error=source_refs_required, applied=false and operation=<0-based index>; a source without key is source_key_required; a pool source no operation references is source_unused; related codes include unknown_source_ref, duplicate_source_ref, duplicate_source_key, invalid_source_pointer, source_pointer_not_in_document, source_pointer_not_patched. Historical revisions written before source_refs carry sources with scope=commit (whole-commit pool, not field-level evidence)", "CatalogCommit", "CommitReceipt", "auth"},
		{"/catalog/commits/{id}", "get", "Read durable push receipt by UUID, only for its original actor; 404 if no committed receipt or not owned. A 404 during an in-flight push is not proof of failure; retry the same immutable payload/id to reconcile", "", "CommitReceipt", "auth"},
		{"/catalog/entities/candidates", "post", "Read-only identity candidates from the authoritative PostgreSQL index in one repeatable-read snapshot, including canonical resolution. Required kind plus at least one criterion: titles (base/localized title and aliases; case/whitespace normalized), external_links [{kind,value}] or attributes [{key,value}] (exact scalar or scalar-list value). Criteria are OR, optional work_id restricts expression/content_unit, release_id restricts medium and medium_id restricts track scope. Optional context {creator,performer,label,catalog_number} only adjusts ranking/confidence/recommended_action — it NEVER filters candidates (same-name different-artist entities remain). Each item carries assessment {matched_fields,conflicts?,context_summary?,confidence,recommended_action:reuse|review|distinct}; advisory only, server never auto-merges. At most 20 criteria per category, values up to 256 bytes. limit=1000, max=1000; total is the exact matched-original count in this snapshot, complete=false when truncated. Never treat partial or unresolved canonical candidates as absence; narrow broad criteria instead of blindly retrying. Current caller visibility only, deleted originals excluded. items contain matched and canonical summaries; summaries must not be used for entity PUT. This read does not reserve identity or lock a later creation. Invalid kind/criteria/work_id/limit/context return 400; strict JSON; shares the catalog 180/min read/write budget. No OpenSearch dependency or offset pagination", "IdentityCandidateQuery", "IdentityCandidateResponse", ""},
		{"/catalog/entities/candidates/batch", "post", "Batch identity candidate check (1-50 queries) in one repeatable-read snapshot. Body {queries:[{key, kind, titles?, external_links?, attributes?, work_id?, release_id?, medium_id?, limit?, context?}...]}; key is a client correlation id unique in the batch. Each query has the same semantics as POST /catalog/entities/candidates (context ranks only, never filters; assessment with recommended_action reuse|review|distinct; server never auto-merges). Per-query validation failures are returned as results[].error without failing the whole batch; empty queries or >50 return 400 queries_required / too_many_queries; duplicate/blank key return 400 duplicate_query_key / invalid_query_key. Shares the catalog 180/min read/write budget", "IdentityBatchRequest", "IdentityBatchResponse", ""},
		{"/catalog/relationships/query", "post", "Query direct structural, semantic and definitions-declared nested attribute-reference neighbors for 1-20 entity UUIDs (deduplicated in request order) in one read-only repeatable-read snapshot with the definitions ETag. direction=both/outgoing/incoming; self-links are outgoing. rule_codes are validated against /catalog/definitions relationship_rules, including disabled historical rules; peer_kinds filter the other endpoint. Visibility and attribute-reference checks precede per-subject pagination (limit=25, max=100; offset>=0). Unavailable subjects have no page and are listed in unavailable_ids without distinguishing hidden from missing. attribute: rules carry field paths; references identify third-party typed entity references and via marks a third-party subject. entities contains only deduplicated RelationshipEntity summaries for visible subjects, returned endpoints and references, never full editable entities or recursive expansion. Strict JSON; invalid_ids/invalid_id/invalid_direction/invalid_rule_code/invalid_peer_kind/invalid_limit/invalid_offset return 400. Default shared read/write budget 180/min per account or client IP, counted atomically in PostgreSQL across replicas, overridable through the shared rate-limit policy; exhausted budgets return 429 with Retry-After", "RelationshipQueryRequest", "RelationshipQueryResponse", ""},
		{"/catalog/compare", "get", "Compare two to six entities of any kinds (throttled to a 180/min shared read/write budget (admin is unlimited by default, overridable by account/admin-group policy), per account when signed in and otherwise per client IP; override per group or per account via /admin/rate-limits); releases/mediums include their containment tree", "", "Result", ""},
		{"/exchange/entities/{id}", "get", "Export an entity snapshot for another instance, respecting caller visibility and current expression visibility in track contents; missing or invisible entities return 404, while database and internal decoding failures return a generic server error", "", "Entity", ""},
		{"/exchange/proposals", "post", "Submit an external edit proposal (always lands in pending_review)", "Edit", "Entity", "auth"},
		{"/external-sources", "get", "List external source adapters that can be queried (requires authentication). Each item is {id, names, category, icon, description, url_pattern} from the external authority database registry joined with the compiled adapter set (bangumi/dlsite/dmm). Read-only; never writes the catalog", "", "ImporterSourceList", "auth"},
		{"/external-sources/query", "post", "Unified read-only external source query: op=search|lookup|detail. Returns raw upstream data plus FetchMeta {url, fetched_at, content_hash} and, for lookup/detail, the existing preview mapping. search needs q; lookup/detail need id or url_or_id. source may be empty/auto (detect from URL/ID for lookup/detail; search defaults to bangumi). DLsite/DMM search returns search_not_supported. Outbound fetches use a per-source wait throttle (~1 req/s), separate from the inbound API rate_budget. Never auto-imports, merges or modifies catalog entities. Requires authentication", "ExternalSourceQueryRequest", "ExternalSourceQueryResponse", "auth"},
		{"/catalog/relations", "post", "Create contextual relation (requires catalog.relation.edit; supports Idempotency-Key, persistent: same key replays the first result across restarts/replicas, same key with a different payload returns 409 idempotency_conflict)", "RelationEdit", "Relation", "auth"}, {"/catalog/relations/{id}", "put", "Replace relation context (requires catalog.relation.edit)", "RelationEdit", "Relation", "auth"}, {"/catalog/relations/{id}", "delete", "Remove relation with evidence (requires catalog.relation.edit)", "LifecycleEdit", "Result", "auth"},
		{"/admin/catalog-definitions", "get", "Read the single live definition configuration (requires catalog.definitions.manage)", "", "DefinitionConfig", "auth"},
		{"/admin/catalog-definitions", "put", "Replace the live definition document after impact validation; expected_etag prevents stale edits (requires catalog.definitions.manage)", "DefinitionSave", "DefinitionConfig", "auth"},
		{"/admin/catalog-definitions/impact", "post", "Replay a proposed definition document against current data without saving it (requires catalog.definitions.manage)", "DefinitionImpactRequest", "DefinitionImpact", "auth"},
		{"/admin/rate-limits", "get", "Read the live API rate-limit policy (requires catalog.definitions.manage). One cached policy document drives every throttled route; counters are shared atomically through PostgreSQL across replicas; it lives outside /catalog/definitions on purpose, so changing a quota never invalidates the metadata contract or replays it over the catalog. Budgets resolve per request in this order: accounts (matched against the token subject, then preferred_username) → groups (the most permissive match wins, since groups only grant) → default_per_minute → the route's built-in budget. The reserved group code anonymous applies to requests with no token, whose subject is the client IP. A rule with unlimited=true skips counting entirely for that subject (no X-RateLimit-* headers are sent, because there is no window to report); an empty document means 'inherit every built-in budget', which is exactly the behaviour before this endpoint existed", "", "RateLimitConfig", "auth"},
		{"/admin/rate-limits", "put", "Replace the live API rate-limit policy; expected_etag prevents stale edits (requires catalog.definitions.manage). The whole policy object is replaced, never merged: removing a group or account entry is how a previous override is withdrawn, and a partial merge would make deletion impossible to express. Errors: 409 version_conflict when expected_etag is missing or stale, 400 invalid_rate_limit for a negative or above-ceiling per_minute (over 1000000 is a typo, not generosity), a blank, whitespace-padded or over-long subject key, more than 200 group entries or 2000 account entries, and edit_note_required for a missing operational decision note. Sources are optional for runtime policy; when supplied, invalid_source and source_not_eligible apply. unlimited and per_minute may both be set, and unlimited wins; the effective budgets are visible in GET /admin/rate-limits without waiting for a restart", "RateLimitSave", "RateLimitConfig", "auth"},
		{"/admin/external-databases", "get", "List external authority databases (requires catalog.definitions.manage)", "", "Result", "auth"},
		{"/admin/external-databases", "post", "Create external authority database (requires catalog.definitions.manage); names must carry zh-CN, zh-TW, en-US and ja or ja-JP (four_locale_names_required)", "ExternalDatabase", "Result", "auth"},
		{"/admin/external-databases/{code}", "put", "Update external authority database (requires catalog.definitions.manage); names must carry zh-CN, zh-TW, en-US and ja or ja-JP (four_locale_names_required)", "ExternalDatabase", "Result", "auth"},
		{"/admin/external-databases/{code}", "delete", "Delete external authority database (requires catalog.definitions.manage)", "", "Result", "auth"},
		{"/catalog/shelves", "get", "List enabled shelf rules (shared by homepage and admin)", "", "Result", ""},
		{"/catalog/shelves/feed", "get", "Evaluate shelf rules with their items, ordered by caller preferences; each shelf's items follow its own sort key: updated (default) | created | title. For a signed-in caller the shelf list is merged before ordering: a preferences section whose slug matches a system shelf overrides that shelf for this caller only (names merged per locale, query/sort replaced, blank icon keeps the system icon), other sections are appended as caller-private shelves, order interleaves system and custom slugs, and hidden removes both kinds; every entry's shelf carries source: system (possibly overridden) or custom, so the client knows whether the row can be deleted; anonymous callers always see source: system and no sections. Each entry also returns total: the full count of matching entities for that caller (not capped by per_shelf), while items is capped for display", "", "Result", ""},
		{"/catalog/me/home-preferences", "get", "Read caller homepage section preferences (order, hidden and sections); all three are arrays", "", "Result", "auth"},
		{"/catalog/me/home-preferences", "put", "Replace caller homepage section preferences; sections is the caller's full 'override + private' list of at most 20 entries, each entry having slug (lowercase code [a-z0-9][a-z0-9_-]{1,63}; a slug equal to a system shelf slug overrides that shelf for this caller only, it is not a conflict), names (zh-CN required and non-empty, other locales optional), query (same shape rules as shelf rules), sort (updated | created | title, empty means updated) and icon; a duplicate slug keeps the first declaration. Errors: invalid_slug, invalid_name, invalid_sort, invalid_tags, invalid_fields, invalid_vocab_terms, invalid_relations, too_many_sections. Unknown slugs in order/hidden are accepted and ignored instead of rejected (an administrator deleting a shelf must not block saving), and order/hidden are deduplicated", "HomePreferences", "Result", "auth"},
		{"/admin/shelves", "get", "List shelf rules (requires catalog.shelves.manage)", "", "Result", "auth"},
		{"/admin/shelves", "post", "Create shelf rule (requires catalog.shelves.manage); names must carry zh-CN, zh-TW, en-US and ja or ja-JP (four_locale_names_required); sort accepts updated (default) | created | title, other values are rejected with invalid_sort; query rejects blank codes with invalid_tags / invalid_relations and malformed field codes or blank values with invalid_fields / invalid_vocab_terms (the same shape check user homepage sections use); source is output-only and only set by /catalog/shelves/feed", "Shelf", "Result", "auth"},
		{"/admin/shelves/{id}", "get", "Read shelf rule (requires catalog.shelves.manage)", "", "Result", "auth"},
		{"/admin/shelves/{id}", "put", "Update shelf rule (requires catalog.shelves.manage); names must carry zh-CN, zh-TW, en-US and ja or ja-JP (four_locale_names_required); sort accepts updated (default) | created | title, other values are rejected with invalid_sort; query rejects blank codes with invalid_tags / invalid_relations and malformed field codes or blank values with invalid_fields / invalid_vocab_terms", "Shelf", "Result", "auth"},
		{"/admin/shelves/{id}", "delete", "Delete shelf rule (requires catalog.shelves.manage)", "", "Result", "auth"},
		{"/notifications", "get", "Read the caller's own notification inbox, newest activity first (requires authentication; no recipient parameter exists — the recipient is always the token holder, so another user's inbox is not addressable). Returns items + total + unread in one response: the list page needs all three consistent, and splitting them would leave a window where the badge says 3 while the list renders fully read. limit defaults to 20 and is silently clamped to 100, offset defaults to 0 and is clamped to >= 0. Ordering is by updated_at (latest activity) desc, so a notification that aggregated a new event floats back to the top instead of staying at its first event's position. Notification types: comment.replied (someone replied to a post/topic the caller wrote, delivered by the community service), entity.included (the caller's work was declared as a subject of a release or included in a collection), entity.review_approved / entity.review_rejected (an editor or reviewer published or rejected one of the caller's entities), import.completed (a receipt for the caller's own import job). Each item carries type, actor_id/actor_name (possibly empty for system events), subject_type/subject_id for the click target, a per-type payload for rendering, count (how many events were merged into this row) and read. Retention is planned at 180 days but no cleanup job runs in this round", "", "NotificationList", "auth"},
		{"/notifications/unread-count", "get", "Unread badge count for the caller (requires authentication). Deliberately the lightest endpoint of the feature: one COUNT over the partial index catalog.notifications_unread(recipient_id) WHERE read_at IS NULL, no rows read, no JOIN, no payload decoding, no aggregation — it exists to be polled by the top bar badge. Shares the catalog 180-request-per-minute read/write budget, with no second bucket or duplicate charge; configurable by group or account via /admin/rate-limits. Answers a single unread field; 0 is a real zero from the count, never a placeholder", "", "NotificationUnread", "auth"},
		{"/notifications/read-all", "post", "Mark every unread notification of the caller as read (requires authentication; writes an audit row with action notification.all_read). The recipient condition is part of the UPDATE predicate, so this can only ever touch the caller's own rows. Returns ok, updated (how many rows this call marked) and unread (read back from the database after the update, not assumed to be 0). No 'until' parameter: the inbox is per-recipient, so 'all currently unread' is the whole semantics", "", "NotificationReadResult", "auth"},
		{"/notifications/{id}/read", "post", "Mark one notification as read (requires authentication; writes an audit row with action notification.read). The recipient is part of the UPDATE predicate — a notification belonging to somebody else and a notification that does not exist are both 404 not_found, because answering 403 for the former and 404 for the latter would let a caller probe whether a given id exists in another user's inbox. read_at keeps its first value (COALESCE), so re-reading does not rewrite history; the response carries the caller's remaining unread count", "", "NotificationReadResult", "auth"},
		{"/notifications/internal", "post", "Service-to-service notification delivery (community -> catalog). Requires X-Internal-Token (INTERNAL_API_TOKEN); disabled with 503 internal_api_disabled when unset. The caller supplies actor_id/actor_name as the event author snapshot in the request body; no end-user bearer token is accepted or required. Validates recipient_id, type, subject and dedupe fields before writing; delivery failures are logged by the caller without failing the original community action", "NotificationDelivery", "NotificationReadResult", ""},
	} {
		add(r[0], r[1], r[2], r[3], r[4], r[5] != "")
	}
	for path, methods := range paths {
		if path == "/version" {
			continue
		}
		for _, raw := range methods.(map[string]any) {
			if op, ok := raw.(map[string]any); ok {
				op["responses"].(map[string]any)["503"] = map[string]any{"description": "rate_limit_unavailable when the shared budget database is unavailable; Retry-After: 1. Other declared dependency errors may also return 503."}
			}
		}
	}
	querySchema := schemas["RelationshipQueryRequest"].(map[string]any)
	querySchema["required"] = []string{"ids"}
	queryProps := querySchema["properties"].(map[string]any)
	queryProps["ids"] = map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "items": map[string]any{"type": "string", "format": "uuid"}, "description": "Canonicalized and deduplicated in request order"}
	queryProps["direction"] = map[string]any{"type": "string", "enum": []string{"both", "outgoing", "incoming"}, "default": "both"}
	queryProps["rule_codes"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Codes from the live relationship registry; empty means any rule"}
	queryProps["peer_kinds"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": Kinds}, "description": "Other endpoint kinds; empty means any kind"}
	queryProps["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 25}
	queryProps["offset"] = map[string]any{"type": "integer", "minimum": 0, "default": 0}
	schemas["RelationshipQueryResponse"].(map[string]any)["required"] = []string{"definition_etag", "pages", "entities", "unavailable_ids"}
	schemas["RelationshipQueryPage"].(map[string]any)["required"] = []string{"subject_id", "items", "limit", "offset", "has_more"}
	queryOp := paths["/catalog/relationships/query"].(map[string]any)["post"].(map[string]any)
	queryOp["responses"].(map[string]any)["429"] = map[string]any{"description": "rate_limited; Retry-After reports seconds until another request is allowed"}
	if fc, ok := schemas["FieldConflict"].(map[string]any); ok {
		fc["required"] = []string{"operation", "pointer", "base", "current", "proposed", "kind", "suggestions"}
		fc["description"] = "One divergent JSON Pointer from a three-way merge on commit preview/push. base/current/proposed are decoded JSON values (null when the pointer was absent). remove=true means the proposed patch deletes the pointer."
		props := fc["properties"].(map[string]any)
		props["kind"] = map[string]any{"type": "string", "enum": []string{"concurrent_edit", "stale_base"}, "description": "concurrent_edit = both sides changed relative to base; stale_base = proposed equals base while current diverged"}
		props["suggestions"] = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"manual_merge", "take_current", "rebase_keep_mine"}}, "description": "Ordered resolution hints; agents/UI present them, never auto-apply"}
		props["pointer"] = map[string]any{"type": "string", "description": "JSON Pointer of the conflicting field (e.g. /title, /attributes/catalog_number)"}
		props["base"] = map[string]any{"description": "Value at base_version (null if absent)"}
		props["current"] = map[string]any{"description": "Value on the server now (null if absent)"}
		props["proposed"] = map[string]any{"description": "Value from the commit patch (null when remove=true or proposed absent)"}
	}
	if cc, ok := schemas["CommitConflict"].(map[string]any); ok {
		cc["required"] = []string{"operation", "id", "base_version", "current_version", "conflicts"}
		cc["description"] = "409 commit_conflict summary. conflicts replaces the former paths[] list (no compatibility shim)."
	}
	conflict409 := map[string]any{
		"description": "commit_conflict: overlapping divergent edits. conflict is the operation summary; conflicts is the field-level array (also nested under conflict.conflicts). Suggestions are advisory only.",
		"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
			"type": "object", "required": []string{"error", "applied", "conflict", "conflicts"}, "additionalProperties": false,
			"properties": map[string]any{
				"error":     map[string]any{"type": "string", "enum": []string{"commit_conflict"}},
				"applied":   map[string]any{"type": "boolean", "enum": []bool{false}},
				"conflict":  map[string]any{"$ref": "#/components/schemas/CommitConflict"},
				"conflicts": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/FieldConflict"}},
			},
		}}},
	}
	for _, path := range []string{"/catalog/commits", "/catalog/commits/preview"} {
		op := paths[path].(map[string]any)["post"].(map[string]any)
		op["description"] = "Structural and relation writes run concurrently under SERIALIZABLE, without a global graph lock. Only confirmed database rollbacks (40001/40P01/55P03) restart the whole transaction, up to 16 attempts within the deadline. Local release/identity/row coordination remains. Connection errors or uncertain COMMITs are never automatically replayed."
		op["responses"].(map[string]any)["409"] = conflict409
		op["responses"].(map[string]any)["503"] = map[string]any{
			"description": "transaction_busy: this request rolled back after bounded attempts; Retry-After: 1. Retain the same commit ID/payload. This response cannot resolve uncertainty about an earlier same-ID request; recover its receipt first.",
			"headers":     map[string]any{"Retry-After": map[string]any{"schema": map[string]any{"type": "integer", "minimum": 1}}},
			"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{
				"type": "object", "required": []string{"error", "applied"}, "properties": map[string]any{
					"error":   map[string]any{"type": "string", "enum": []string{"transaction_busy"}},
					"applied": map[string]any{"type": "boolean", "enum": []bool{false}},
				},
			}}},
		}
	}
	params := []any{}
	for _, p := range []struct{ name, desc string }{
		{"kind", "Single entity kind filter"},
		{"kinds", "Multi-value kind filter (repeat or comma-separated, OR)"},
		{"status", "Entity status filter"},

		{"field", "Attribute filter: a published field code (dotted path allowed only when every level is searchable and enabled); the seed definitions make top-level codes such as tags, duration, edition_date and barcode usable"},
		{"value", "Exact value compared with field (text equality via ->>)"},
		{"work_id", "Expressions/content_units of the work + releases declaring it"},
		{"content_unit_id", "Expressions under the content unit"},
		{"release_id", "Mediums under the release"},
		{"medium_id", "Tracks under the medium"},
		{"parent_id", "Child content_units/mediums/tracks"},
		{"tags", "Multi-value tag filter (repeat or comma-separated, container match @>); matching is controlled by tags_mode"},
		{"tags_mode", "Tag matching: any (default) includes entities with at least one selected tag; all requires every selected tag. Other values return 400 invalid_query_param. Applies to browsing and text search."},
		{"original_language", "Exact match on document original_language (all eight kinds share it, e.g. ja)"},
		{"has_pictures", "Only entities with a non-empty pictures array when 1; missing key or non-array counts as no cover"},
		{"sort", "Sort key, whitelisted: updated_at (column, default), created_at (entity ids are time-ordered UUIDv7, so id order is creation order), title (the displayed title: requested locale -> original_language -> en-US -> base title). An unknown key is 400 invalid_sort rather than being silently ignored"},
		{"order", "Sort direction, asc or desc; defaults to desc for the time keys and asc for title. Anything else is 400 invalid_order"},
		{"locale", "Locale used only by sort=title to pick which translation is compared (BCP-47-like, ignored when malformed)"},
		{"limit", "Page size, default 50, max 100. A non-integer, 0, a negative value or anything above 100 is 400 invalid_limit (an empty value counts as absent) — the old behaviour silently replaced it with the default, so a caller could not tell its page size never applied"},
		{"offset", "For q, offset+limit must not exceed 10000 (400 search_window_exceeded); follow next_cursor for deeper pages. Page offset, default 0; must be an integer >= 0, otherwise 400 invalid_offset. Mutually exclusive with page: passing both is 400 pagination_conflict rather than letting one of them quietly win"},
		{"page", "For q, the derived offset+limit must not exceed 10000. 1-based page number, sugar for offset=(page-1)*limit (limit defaults to 50); must be an integer >= 1, otherwise 400 invalid_page. A page past the end answers 200 with an empty items and the real total still filled in, so the caller can tell 'nothing left' from 'nothing found'"},
		{"q", "OpenSearch search across titles, translations, aliases, summaries, tags and external IDs. Matching and pagination use an index snapshot; PostgreSQL rechecks current visibility and exact filters and supplies entity content. No PostgreSQL text-search fallback: an unavailable or unready index returns 503 search_unavailable. Query text is limited to 256 bytes and must contain valid UTF-8 with no control characters other than TAB. Response total describes the index snapshot (total_relation=index_snapshot); items can be shorter after current visibility/filter rechecks. Follow next_cursor while has_more even if items is empty."},
		{"cursor", "Opaque one-minute PIT search cursor, only with q. Keep the query, filters, sort, locale and limit unchanged. Mutually exclusive with offset/page. Deep pagination continues beyond 10,000 hits. Malformed or mismatched cursor: 400 invalid_search_cursor; expired PIT: 400 search_cursor_expired. Restart the search after expiry."},
	} {
		params = append(params, map[string]any{"name": p.name, "in": "query", "description": p.desc, "schema": map[string]any{"type": "string"}})
	}
	paths["/catalog/entities"].(map[string]any)["get"].(map[string]any)["parameters"] = params
	paths["/catalog/compare"].(map[string]any)["get"].(map[string]any)["parameters"] = []any{map[string]any{"name": "ids", "in": "query", "required": true, "description": "Two to six comma-separated entity UUIDs of any kinds", "schema": map[string]any{"type": "string"}}}
	// 查询参数补齐（与 http.go 实际读取一致）：标签聚合与货架 feed。
	qp := func(name, desc string, required bool) map[string]any {
		return map[string]any{"name": name, "in": "query", "required": required, "description": desc, "schema": map[string]any{"type": "string"}}
	}

	paths["/notifications"].(map[string]any)["get"].(map[string]any)["parameters"] = []any{
		qp("limit", "Page size, default 20, max 100; out-of-range values are clamped (never rejected)", false),
		qp("offset", "Page offset, default 0; negative values are clamped to 0", false),
	}
	paths["/catalog/tags"].(map[string]any)["get"].(map[string]any)["parameters"] = []any{
		qp("q", "Substring filter on tag name", false),
		qp("limit", "Max tags, default 200, max 500", false),
	}
	paths["/catalog/shelves/feed"].(map[string]any)["get"].(map[string]any)["parameters"] = []any{
		qp("per_shelf", "Items per shelf, default 12, max 100", false),
	}
	paths["/catalog/entities/{id}/links"].(map[string]any)["get"].(map[string]any)["parameters"] = []any{
		map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
		qp("limit", "Page size, default 50, max 100; invalid values return 400", false),
		qp("offset", "Page offset, default 0; mutually exclusive with page", false),
		qp("page", "1-based page number; mutually exclusive with offset", false),
	}
	paths["/catalog/developer/request-logs"].(map[string]any)["get"].(map[string]any)["parameters"] = []any{
		qp("credential", "Filter by credential name (PAT name, exact); empty returns all", false),
		qp("limit", "Page size, default 50, max 100", false),
	}
	// 用户贡献视图：items 的形状随 tab 变化（创建项 vs 修订项），响应 schema 见 UserContributions。
	// tab 之外的取值（topics/comments/audits 属互动服务）返回 400 invalid_tab，不静默给空列表。
	add("/users/{id}/contributions", "get", "User contribution feed for the profile page: tab=all (default) mixes the entities the user created (works/releases/artists, judged by the version=1 revision actor snapshot) with their later edits (revision rows above version 1), revisions lists their entity revision rows with a field-level diff (diff keys follow the entity fields, attributes/translations drilled one level; ignored: id/version/created_by/created_at/updated_at), works/releases/artists list the entities the user created (artists maps to the agent kind); anonymous readable and filtered by the same visibility rules as entity lists (deleted/merged targets are invisible to everyone; unpublished targets only to their creator and catalog.lifecycle.manage holders), so items and stats never expose drafts; page defaults to 1 and page_size to 20 (both clamped, never rejected); an unknown tab is 400 invalid_tab, while an id that is not a UUID is 404 not_found — the same status and code the account profile (GET /users/{id}) and the community stats (GET /users/{id}/stats) answer with, so the profile page degrades all three sources alike instead of showing a parameter error; the directory never reads the account tables, so an unknown user and a user without contributions are both a 200 with zero counts; stats carry five counts derived from the revision actor snapshot columns without joining the account tables: works_created/releases_created/artists_created count first revisions (version 1) on visible targets, revisions_count counts revision rows on visible targets, while audit_actions counts durable lifecycle management facts (entity.deleted / entity.merged, stored independently of the outbox) the user performed regardless of the target's current state, because deleting or merging removes the target from the visible set", "", "UserContributions", false)
	paths["/users/{id}/contributions"].(map[string]any)["get"].(map[string]any)["parameters"] = []any{
		map[string]any{"name": "id", "in": "path", "required": true, "schema": map[string]any{"type": "string"}},
		qp("tab", "all (default) | revisions | works | releases | artists", false),
		qp("page", "Page number, default 1; values below 1 are clamped", false),
		qp("page_size", "Items per page, default 20, max 100; out-of-range values fall back to 20", false),
	}
	// 权限级别说明：OpenAPI security 只区分匿名/登录；管理端点的权限码在 summary 标注
	// （见各 admin/* 与 lifecycle 行），与 http.go required(<code>) 对应，不另加字段。
	// 老令牌（无 permissions 声明）按角色兜底（见 permission.go 的 User.Can）。
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "MetaFusion API",
			"description": "MetaFusion 元数据目录服务 API：固定实体骨架（Work / Expression / Release / Medium / Track / ContentUnit / Agent / Collection）、动态类型与属性扩展、多版本发行对比、外部库导入与实例间导入导出。账号（/auth、/oauth、/oidc）与收藏（/favorites）已由独立子系统提供，不在本服务。",
			"version":     "1.0.0",
		},
		// servers 保持相对路径 /api：网关/直连后端均同源，不硬编码域名；
		// OIDC discovery 的 issuer 由运行时 TokenIssuerURL/请求拼出，不在此写死。
		"servers": []any{
			map[string]any{"url": "/api", "description": "标准统一主干 API"},
		},
		"tags": []any{
			map[string]any{"name": "Catalog", "description": "核心实体编目与查询 (Work, Release, Medium, Track, ContentUnit, Agent, Collection)"},
			map[string]any{"name": "Definitions", "description": "无代码动态元数据类型、属性与关系定义管理"},
			map[string]any{"name": "Notifications", "description": "站内通知：本人收件箱、未读数角标、标记已读，以及服务间投递（互动服务 → 目录）"},
			map[string]any{"name": "ExternalDatabases", "description": "外部权威数据库与官方渠道配置"},
			map[string]any{"name": "RateLimits", "description": "API 限流策略：全局默认、按用户组预设与按账号覆盖（解除限制或指定每分钟额度）"},
		},
		"paths": paths,
		"components": map[string]any{
			"schemas": schemas,
			"securitySchemes": map[string]any{
				"session": map[string]any{"type": "apiKey", "in": "cookie", "name": "mf_session"},
				"bearer":  map[string]any{"type": "http", "scheme": "bearer"},
			},
		},
	}
}
