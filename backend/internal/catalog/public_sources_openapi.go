package catalog

// publicSourcePaths uses the same /api server base as the catalog contract.
func publicSourcePaths() map[string]any {
	stringField := func() map[string]any { return map[string]any{"type": "string"} }
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
	}
	site := object(map[string]any{
		"id": stringField(), "name": stringField(), "api_url": stringField(), "article_url": stringField(),
		"enabled": map[string]any{"type": "boolean"}, "license": stringField(), "attribution": stringField(),
	}, "id", "name", "api_url", "article_url", "enabled", "license", "attribution")
	settings := object(map[string]any{
		"enabled":    map[string]any{"type": "boolean"},
		"wiki_sites": map[string]any{"type": "array", "maxItems": 32, "items": site},
		"updated_at": map[string]any{"type": "string", "format": "date-time"},
	}, "enabled", "wiki_sites", "updated_at")
	response := object(map[string]any{
		"url": stringField(), "fetched_at": map[string]any{"type": "string", "format": "date-time"},
		"content_hash": stringField(), "content_type": stringField(), "site": stringField(), "title": stringField(),
		"text": stringField(), "data": map[string]any{}, "attribution": stringField(), "license": stringField(),
	}, "url", "fetched_at", "content_hash", "content_type")
	wikiInput := object(map[string]any{
		"site": stringField(), "language": map[string]any{"type": "string", "pattern": wikiLanguagePattern.String(), "default": "en"},
		"title": map[string]any{"type": "string", "minLength": 1, "maxLength": 500}, "query": map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
		"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "default": 5},
	}, "site")
	wikiInput["oneOf"] = []any{map[string]any{"required": []string{"title"}}, map[string]any{"required": []string{"query"}}}
	op := func(description string, input, output map[string]any) map[string]any {
		v := map[string]any{
			"summary":  description,
			"security": []any{map[string]any{"bearer": []string{}}, map[string]any{"session": []string{}}},
			"responses": map[string]any{
				"200": map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": output}}},
				"400": map[string]any{"description": "Invalid URL, wiki query or site configuration"},
				"401": map[string]any{"description": "Authentication required"},
				"403": map[string]any{"description": "Insufficient administrator permission"},
				"409": map[string]any{"description": "version_conflict for stale source configuration"},
				"413": map[string]any{"description": "public_source_too_large (2 MiB limit)"},
				"415": map[string]any{"description": "public_source_content_type_unsupported"},
				"429": map[string]any{"description": "public_source_rate_limited or platform rate limit"},
				"502": map[string]any{"description": "Upstream denied, unreachable, invalid or exceeded redirect limit; access restrictions are never bypassed"},
				"503": map[string]any{"description": "public_sources_disabled or wiki_site_disabled"},
			},
		}
		if input != nil {
			v["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": input}}}
		}
		return v
	}
	return map[string]any{
		"/external-sources/fetch": map[string]any{"post": op("Read public HTTP(S) text or JSON with source hash and timestamp. GET only; no caller credentials, private addresses or unlimited responses. Redirects validate public destinations. Requires authentication; does not modify catalog records.", object(map[string]any{
			"url":    map[string]any{"type": "string", "format": "uri", "maxLength": 8000},
			"format": map[string]any{"type": "string", "enum": []string{"text", "json"}},
		}, "url"), response)},
		"/external-sources/wiki": map[string]any{"post": op("Search or read configured public MediaWiki APIs. Supply exactly one of title or query. Defaults: site wikipedia or moegirl, language en, limit 5. Returns original wikitext/JSON and configured attribution/license; never writes or translates catalog facts.", wikiInput, response)},
		"/admin/public-sources": map[string]any{
			"get": op("Read public network and MediaWiki configuration; requires catalog.definitions.manage.", nil, settings),
			"put": op("Replace public source configuration; requires catalog.definitions.manage. expected_updated_at protects concurrent edits. Site URLs accept {language}; article URLs also accept {title}. Only public HTTP(S) endpoints are allowed; API credentials are not supported.", object(map[string]any{
				"settings": settings, "expected_updated_at": map[string]any{"type": "string", "format": "date-time"},
			}, "settings", "expected_updated_at"), settings),
		},
	}
}
