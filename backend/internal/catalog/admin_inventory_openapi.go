package catalog

func adminInventoryPaths() map[string]any {
	text := func() map[string]any { return map[string]any{"type": "string", "maxLength": listTextParamLimit} }
	param := func(name, description string, schema map[string]any) map[string]any {
		return map[string]any{"name": name, "in": "query", "description": description, "schema": schema}
	}
	page := func(item map[string]any) map[string]any {
		return map[string]any{"type": "object", "required": []string{"items", "total", "limit", "offset", "has_more"}, "properties": map[string]any{
			"items": map[string]any{"type": "array", "items": item}, "total": map[string]any{"type": "integer", "format": "int64"},
			"limit": map[string]any{"type": "integer"}, "offset": map[string]any{"type": "integer"}, "has_more": map[string]any{"type": "boolean"},
		}}
	}
	operation := func(summary string, output map[string]any, parameters []any) map[string]any {
		parameters = append(parameters,
			param("limit", "Page size; 1..100, default 50. Invalid values return 400.", map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 50}),
			param("offset", "Rows to skip; nonnegative, default 0. Results use stable timestamp/ID ordering.", map[string]any{"type": "integer", "minimum": 0, "default": 0}),
		)
		return map[string]any{
			"summary": summary, "parameters": parameters,
			"security": []any{map[string]any{"bearer": []string{}}, map[string]any{"session": []string{}}},
			"responses": map[string]any{
				"200": map[string]any{"description": "Exact PostgreSQL inventory and count from one read snapshot", "content": map[string]any{"application/json": map[string]any{"schema": output}}},
				"400": map[string]any{"description": "Invalid query or pagination parameter"},
				"401": map[string]any{"description": "Authentication required"},
				"403": map[string]any{"description": "Requires catalog.lifecycle.manage; third-party OAuth clients are denied"},
				"500": map[string]any{"description": "Database or internal failure; diagnostics are never returned"},
			},
		}
	}
	log := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "user_id", "at", "credential_type", "method", "route", "status", "ms"}, "properties": map[string]any{
		"id": map[string]any{"type": "string", "format": "uuid"}, "user_id": map[string]any{"type": "string", "format": "uuid"},
		"at": map[string]any{"type": "string", "format": "date-time"}, "credential_type": text(), "credential_name": text(),
		"method": text(), "route": text(), "status": map[string]any{"type": "integer"}, "ms": map[string]any{"type": "integer"},
	}}
	return map[string]any{
		"/admin/catalog-entities": map[string]any{"get": operation(
			"Read all catalog entity lifecycle states for management; requires catalog.lifecycle.manage. No OpenSearch dependency. Public entity browsing remains active-only.",
			page(map[string]any{"$ref": "#/components/schemas/Entity"}), []any{
				param("state", "Omit for all lifecycle states; an unknown value returns 400.", map[string]any{"type": "string", "enum": []string{"active", "deleted", "merged"}}),
				param("kind", "Optional fixed catalog entity kind.", map[string]any{"type": "string", "enum": Kinds}),
				param("q", "Literal case-insensitive substring of the original title, translated titles or UUID. SQL LIKE metacharacters are treated literally.", text()),
			})},
		"/admin/catalog-request-logs": map[string]any{"get": operation(
			"Read all users' catalog API request logs; requires catalog.lifecycle.manage. Only authenticated calls are recorded, with 30-day retention. Routes are templates without entity IDs or query strings. No secrets, headers or request bodies are returned. Self-service developer logs remain owner-only.",
			page(log), []any{
				param("user_id", "Optional exact actor UUID.", map[string]any{"type": "string", "format": "uuid"}),
				param("credential", "Optional exact credential display name, matching self-service log filtering; never a credential secret.", text()),
				param("status", "Optional exact HTTP response status.", map[string]any{"type": "integer", "minimum": 100, "maximum": 599}),
				param("path", "Optional literal case-insensitive substring of the stored route template.", text()),
			})},
	}
}
