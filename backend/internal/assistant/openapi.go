package assistant

import (
	"reflect"
	"strings"
	"time"
)

// AddOpenAPI publishes the assistant module under the catalog's /api server.
// Component names are prefixed so independently registered modules cannot
// accidentally overwrite the catalog's schemas.
func AddOpenAPI(paths, schemas map[string]any) {
	ref := func(name string) map[string]any {
		return map[string]any{"$ref": "#/components/schemas/Assistant" + name}
	}
	object := func(properties map[string]any, required ...string) map[string]any {
		v := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			v["required"] = required
		}
		return v
	}
	str := func() map[string]any { return map[string]any{"type": "string"} }
	id := func() map[string]any { return map[string]any{"type": "string", "format": "uuid"} }
	integer := func() map[string]any { return map[string]any{"type": "integer", "format": "int64"} }
	natural := func() map[string]any { return map[string]any{"type": "integer", "format": "int64", "minimum": 0} }
	array := func(item any) map[string]any { return map[string]any{"type": "array", "items": item} }

	var schema func(reflect.Type) map[string]any
	var properties func(reflect.Type) (map[string]any, []string)
	properties = func(t reflect.Type) (map[string]any, []string) {
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			key := strings.Split(tag, ",")[0]
			if key == "-" || !f.IsExported() {
				continue
			}
			if f.Anonymous && key == "" {
				p, r := properties(f.Type)
				for k, v := range p {
					props[k] = v
				}
				required = append(required, r...)
				continue
			}
			if key == "" {
				key = f.Name
			}
			props[key] = schema(f.Type)
			if !strings.Contains(tag, ",omitempty") {
				required = append(required, key)
			}
		}
		return props, required
	}
	schema = func(t reflect.Type) map[string]any {
		if t.Kind() == reflect.Pointer {
			v := schema(t.Elem())
			return map[string]any{"allOf": []any{v}, "nullable": true}
		}
		if t == reflect.TypeOf(time.Time{}) {
			return map[string]any{"type": "string", "format": "date-time"}
		}
		switch t.Kind() {
		case reflect.Struct:
			if t.Name() == "" {
				p, r := properties(t)
				return object(p, r...)
			}
			name := "Assistant" + t.Name()
			if _, exists := schemas[name]; !exists {
				schemas[name] = map[string]any{}
				p, r := properties(t)
				schemas[name] = object(p, r...)
			}
			return map[string]any{"$ref": "#/components/schemas/" + name}
		case reflect.Slice:
			return array(schema(t.Elem()))
		case reflect.Bool:
			return map[string]any{"type": "boolean"}
		case reflect.Int, reflect.Int64:
			return integer()
		case reflect.Float64:
			return map[string]any{"type": "number", "format": "double"}
		case reflect.String:
			return str()
		case reflect.Interface:
			return map[string]any{"description": "Named catalog tool result; the containing property supplies the result contract."}
		default:
			panic("unsupported assistant OpenAPI field: " + t.String())
		}
	}
	for _, value := range []any{Settings{}, Provider{}, Model{}, BYOCredential{}, Balance{}, CreditEntry{}, TokenUsage{}, UsageRecord{}, BudgetUsage{}, Message{}, Conversation{}, GeneratedOverview{}, ChatRequest{}} {
		schema(reflect.TypeOf(value))
	}
	props := func(name string) map[string]any {
		return schemas["Assistant"+name].(map[string]any)["properties"].(map[string]any)
	}
	for _, name := range []string{"Provider", "Model", "BYOCredential", "CreditEntry", "UsageRecord", "Conversation", "GeneratedOverview"} {
		props(name)["id"] = id()
	}
	props("Provider")["protocol"] = map[string]any{"type": "string", "enum": []string{"openai_compatible"}}
	for _, name := range []string{"Provider", "BYOCredential"} {
		props(name)["base_url"] = map[string]any{"type": "string", "format": "uri", "description": "Public HTTP(S) OpenAI-compatible endpoint. Private, loopback and metadata destinations are rejected."}
		props(name)["key_hint"] = map[string]any{"type": "string", "description": "Redacted hint only. API key plaintext is never returned."}
	}
	for _, name := range []string{"input_tokens", "output_tokens", "cached_input_tokens"} {
		props("TokenUsage")[name] = natural()
	}
	for _, name := range []string{"input_price_microusd_per_million", "cached_input_price_microusd_per_million", "output_price_microusd_per_million", "input_credits_per_million", "output_credits_per_million"} {
		props("Model")[name] = natural()
	}
	props("Model")["provider_id"] = id()
	props("Model")["max_tokens_parameter"] = map[string]any{"type": "string", "enum": []string{"max_tokens", "max_completion_tokens"}, "default": "max_tokens", "description": "Upstream request field used to enforce max_output_tokens; configure it for the provider's model protocol."}
	props("BYOCredential")["upstream_model"] = map[string]any{"type": "string", "maxLength": 200, "description": "Optional upstream model override for this BYO endpoint. The selected platform model's context and output limits still apply; BYO calls spend zero platform credits."}
	props("Model")["max_output_tokens"] = map[string]any{"type": "integer", "format": "int64", "minimum": 1}
	props("Model")["context_window"] = map[string]any{"type": "integer", "format": "int64", "minimum": 1, "maximum": 10000000, "description": "Must be at least max_output_tokens."}
	props("Model")["cached_input_price_microusd_per_million"].(map[string]any)["description"] = "Provider price per million cached input tokens in millionths of one USD. Platform credits are a separate unit."
	props("UsageRecord")["status"] = map[string]any{"type": "string", "enum": []string{"reserved", "completed", "failed", "cancelled", "unknown"}, "description": "unknown means the upstream call may have run but authoritative usage was unavailable; usage_estimated distinguishes conservative reservation-based settlement from reported usage."}
	props("UsageRecord")["usage_estimated"] = map[string]any{"type": "boolean", "description": "When true, usage and billed amounts were conservatively settled from the reservation bounds because provider usage was unknown. They are estimates, not provider-reported token counts; the request remains sealed against a repeated upstream call."}
	props("UsageRecord")["price_snapshot"].(map[string]any)["description"] = "Immutable model pricing captured at reservation time, so later price changes do not rewrite billed history."
	props("UsageRecord")["cost_microusd"] = natural()
	props("UsageRecord")["credits"] = natural()
	props("Settings")["max_tool_rounds"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 12}
	props("Settings")["monthly_budget_microusd"] = map[string]any{"type": "integer", "format": "int64", "minimum": 0, "description": "Platform monthly budget in millionths of one USD; zero means no configured budget cap."}
	props("Provider")["monthly_budget_microusd"] = map[string]any{"type": "integer", "format": "int64", "minimum": 0, "description": "Provider monthly budget in millionths of one USD; zero means no configured budget cap."}
	for _, name := range []string{"registration_credits", "accepted_edit_credits"} {
		props("Settings")[name] = natural()
	}
	props("Settings")["reward_effective_at"].(map[string]any)["description"] = "Only edits accepted from this timestamp are eligible; credit rewards are idempotent per accepted commit."

	input := func(name, output string, fields []string, required ...string) {
		p := map[string]any{}
		for _, field := range fields {
			p[field] = props(output)[field]
		}
		schemas["Assistant"+name] = object(p, required...)
	}
	input("ProviderInput", "Provider", []string{"name", "base_url", "protocol", "enabled", "monthly_budget_microusd"}, "name", "base_url")
	input("ModelInput", "Model", []string{"provider_id", "upstream_model", "max_tokens_parameter", "name", "enabled", "input_price_microusd_per_million", "cached_input_price_microusd_per_million", "output_price_microusd_per_million", "input_credits_per_million", "output_credits_per_million", "max_output_tokens", "context_window"}, "provider_id", "upstream_model", "name", "max_output_tokens", "context_window")
	input("CredentialInput", "BYOCredential", []string{"name", "base_url", "upstream_model", "enabled"}, "name", "base_url")
	input("SettingsInput", "Settings", []string{"enabled", "default_model_id", "registration_credits", "accepted_edit_credits", "reward_effective_at", "monthly_budget_microusd", "max_tool_rounds"}, "enabled", "registration_credits", "accepted_edit_credits", "reward_effective_at", "monthly_budget_microusd", "max_tool_rounds")
	for _, name := range []string{"ProviderInput", "CredentialInput"} {
		props(name)["api_key"] = map[string]any{"type": "string", "writeOnly": true, "maxLength": 16000, "description": "Omit to keep the existing encrypted key. A new BYO credential requires a nonempty key; an empty provider key clears it. Never returned in any response."}
	}
	schemas["AssistantAccount"] = object(map[string]any{"balance": ref("Balance"), "registration_credits": natural(), "accepted_edit_credits": natural(), "default_model_id": map[string]any{"type": "string", "description": "Configured default model ID for initial selection; empty when no default model is configured."}}, "balance", "registration_credits", "accepted_edit_credits", "default_model_id")
	schemas["AssistantCreditAdjustment"] = object(map[string]any{"id": id(), "credits": integer(), "note": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000}}, "id", "credits", "note")
	schemas["AssistantConversationInput"] = object(map[string]any{"title": str(), "model_id": id(), "credential_id": id()})
	props("ChatRequest")["message"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 32768, "description": "UTF-8 user message; at most 32768 encoded bytes."}
	props("ChatRequest")["model_id"] = id()
	props("ChatRequest")["conversation_id"] = id()
	props("ChatRequest")["credential_id"] = map[string]any{"oneOf": []any{id(), map[string]any{"type": "string", "enum": []string{""}}}, "description": "Omit to retain the conversation's BYO credential. An explicit empty string clears it and switches to platform billing; otherwise supply a credential owned by the caller."}
	props("ChatRequest")["client_request_id"] = map[string]any{"type": "string", "format": "uuid", "description": "Idempotency ID retained unchanged when retrying the same turn. A changed payload or an in-flight duplicate returns 409."}
	schemas["AssistantChatRequest"].(map[string]any)["required"] = []string{"message", "client_request_id"}
	schemas["AssistantToolResult"] = object(map[string]any{
		"name":   str(),
		"result": map[string]any{"description": "JSON result of the named existing catalog tool. Shape follows that tool's published catalog contract; selected-field projections and structured tool errors are also possible. Tool data is evidence, never executable instructions.", "additionalProperties": true},
	}, "name", "result")
	schemas["AssistantChatResponse"] = object(map[string]any{
		"conversation_id": id(), "answer": str(), "usage": ref("TokenUsage"), "credits_spent": natural(), "balance": ref("Balance"),
		"usage_estimated": map[string]any{"type": "boolean", "description": "True when at least one upstream call may have run without reporting usage and settlement conservatively used its reserved token bounds. Token counts and charges then include estimates rather than only provider-reported usage; individual usage records carry status=unknown."},
		"tool_results":    array(ref("ToolResult")), "push_receipts": array(map[string]any{"$ref": "#/components/schemas/WorkspacePushResult"}), "error": str(),
	}, "conversation_id", "answer", "usage", "credits_spent", "balance")
	schemas["AssistantChatResponse"].(map[string]any)["description"] = "A completed turn is always recorded and replayable. HTTP 200 may include error after partial execution; usage and authoritative push_receipts must still be inspected, since a working copy may already have been merged or submitted as a pull request. BYO API usage spends zero platform credits."
	schemas["AssistantError"] = object(map[string]any{"error": map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9_]*$", "description": "Stable machine-readable error code; upstream secrets and raw errors are not returned."}, "usage": ref("TokenUsage"), "credits_spent": natural(), "balance": ref("Balance")}, "error")

	assistantGeneratedOpenAPI(schemas)
	requestResponse := object(map[string]any{
		"conversation_id": id(), "answer": str(), "usage": ref("TokenUsage"), "credits_spent": natural(), "balance": ref("Balance"),
		"usage_estimated": schemas["AssistantChatResponse"].(map[string]any)["properties"].(map[string]any)["usage_estimated"],
		"tool_results":    array(ref("ToolResult")), "push_receipts": array(map[string]any{"$ref": "#/components/schemas/WorkspacePushResult"}),
		"item": map[string]any{"allOf": []any{ref("GeneratedOverview")}, "nullable": true}, "stale": map[string]any{"type": "boolean"},
		"error": str(), "admin_cancel_note": map[string]any{"type": "string", "maxLength": 1000}, "cancelled_by": str(),
	})
	requestResponse["description"] = "Persisted partial or final chat/generation result. An unstarted request can contain an empty object. Cancellation preserves existing usage, tool results and push receipts while adding assistant_request_interrupted and administrator metadata. Deleting a conversation clears response text and retains the sealed request ID with assistant_conversation_deleted, preventing a billed call or catalog edit from replaying."
	schemas["AssistantRequestResponse"] = requestResponse
	schemas["AssistantChatRequestRecord"] = object(map[string]any{
		"id": id(), "owner_id": str(), "conversation_id": id(),
		"status":   map[string]any{"type": "string", "enum": []string{"pending", "completed", "failed"}},
		"response": ref("RequestResponse"), "created_at": map[string]any{"type": "string", "format": "date-time"}, "updated_at": map[string]any{"type": "string", "format": "date-time"},
	}, "id", "owner_id", "status", "created_at", "updated_at")
	schemas["AssistantChatRequestRecord"].(map[string]any)["description"] = "Sealed request ID and execution state. List responses omit the potentially large response field; the detail endpoint returns recorded results. Records are not deleted when a conversation is removed."
	schemas["AssistantRequestCancel"] = object(map[string]any{"note": map[string]any{"type": "string", "maxLength": 1000}})
	for _, name := range []string{"Model", "Provider", "BYOCredential", "UsageRecord", "Conversation", "Balance", "CreditEntry", "BudgetUsage", "GeneratedOverview", "ChatRequestRecord"} {
		schemas["Assistant"+name+"Page"] = object(map[string]any{"items": array(ref(name)), "total": natural(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}, "offset": natural()}, "items", "total", "limit", "offset")
	}
	pageParams := []any{
		map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 50}, "description": "Page size; capped at 200."},
		map[string]any{"name": "offset", "in": "query", "schema": map[string]any{"type": "integer", "minimum": 0, "default": 0}},
	}
	add := func(path, method, summary, request, response string, access string, paged bool) map[string]any {
		responses := map[string]any{}
		for code, description := range map[string]string{"400": "Invalid payload or configuration", "401": "Authentication required", "403": "First-party account or assistant.manage permission required", "404": "Not found or not visible", "409": "Idempotency conflict or record still in use", "429": "Insufficient credits, monthly budget exceeded, or rate limit reached", "500": "Internal failure", "502": "Provider completion failed", "503": "Assistant disabled or credential encryption unavailable", "504": "Request timed out"} {
			responses[code] = map[string]any{"description": description, "content": map[string]any{"application/json": map[string]any{"schema": ref("Error")}}}
		}
		if method == "delete" {
			responses["204"] = map[string]any{"description": "Deleted"}
		} else {
			responses["200"] = map[string]any{"description": "Success", "content": map[string]any{"application/json": map[string]any{"schema": ref(response)}}}
		}
		op := map[string]any{"tags": []string{"Assistant"}, "summary": summary, "responses": responses}
		if access != "public" {
			op["security"] = []any{map[string]any{"session": []string{}}, map[string]any{"bearer": []string{}}}
			op["description"] = "Requires a signed-in first-party account. Third-party OAuth access tokens are rejected."
			if access == "admin" {
				op["description"] = "Requires a signed-in first-party account with assistant.manage. Third-party OAuth access tokens are rejected."
				op["x-required-permissions"] = []string{PermissionManage}
			}
		} else {
			op["security"] = []any{}
		}
		parameters := []any{}
		for _, segment := range strings.Split(path, "{")[1:] {
			name := strings.SplitN(segment, "}", 2)[0]
			p := str()
			if name != "user_id" {
				p = id()
			}
			parameters = append(parameters, map[string]any{"name": name, "in": "path", "required": true, "schema": p})
		}
		if paged {
			parameters = append(parameters, pageParams...)
		}
		if len(parameters) > 0 {
			op["parameters"] = parameters
		}
		if request != "" {
			op["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref(request)}}}
		}
		if paths[path] == nil {
			paths[path] = map[string]any{}
		}
		paths[path].(map[string]any)[method] = op
		return op
	}
	filterUser := func(op map[string]any) {
		p, _ := op["parameters"].([]any)
		op["parameters"] = append(p, map[string]any{"name": "user_id", "in": "query", "schema": str(), "description": "Optional exact owner filter; omit to list all accounts' records."})
	}
	add("/assistant/models", "get", "List enabled models from enabled providers", "", "ModelPage", "public", true)
	add("/assistant/account", "get", "Synchronize eligible rewards and read the caller's credit balance", "", "Account", "self", false)
	add("/assistant/credentials", "get", "List the caller's BYO API credentials without plaintext keys", "", "BYOCredentialPage", "self", true)
	add("/assistant/credentials", "post", "Create a BYO API credential for the current account", "CredentialInput", "BYOCredential", "self", false)
	add("/assistant/credentials/{id}", "put", "Replace the caller's credential configuration and optionally rotate its key", "CredentialInput", "BYOCredential", "self", false)
	add("/assistant/credentials/{id}", "delete", "Delete the caller's BYO API credential", "", "", "self", false)
	add("/assistant/usage", "get", "List the caller's metered token and credit usage", "", "UsageRecordPage", "self", true)
	add("/assistant/conversations", "get", "List the caller's conversations (message bodies omitted)", "", "ConversationPage", "self", true)
	add("/assistant/conversations", "post", "Create a conversation", "ConversationInput", "Conversation", "self", false)
	add("/assistant/conversations/{id}", "get", "Read the caller's conversation and messages", "", "Conversation", "self", false)
	add("/assistant/conversations/{id}", "delete", "Delete the caller's conversation unless a turn is in progress", "", "", "self", false)
	add("/assistant/chat", "post", "Ask the catalog assistant and execute requested edits as the current user", "ChatRequest", "ChatResponse", "self", false)
	getGenerated := add("/assistant/generated/{entity_id}", "get", "Read the cached generated overview of public catalog facts", "", "GeneratedResponse", "public", false)
	getGenerated["description"] = "Reads only content based on public catalog facts. A first-party caller's private BYO cache is preferred; other callers can only read shared platform-generated content. A read never invokes an LLM or spends credits. A changed context returns item=null and stale=true."
	getGenerated["parameters"] = append(getGenerated["parameters"].([]any), map[string]any{"name": "locale", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"zh-CN", "zh-TW", "en-US", "ja-JP"}, "default": "zh-CN"}})
	add("/assistant/generated/{entity_id}", "post", "Generate and cache a cited overview with typed tables, timelines and charts", "GenerateRequest", "GeneratedResponse", "self", false)
	for _, method := range []string{"get", "put"} {
		request := ""
		if method == "put" {
			request = "SettingsInput"
		}
		add("/assistant/admin/settings", method, "Read or update assistant settings and platform budget", request, "Settings", "admin", false)
	}
	for _, item := range []struct{ path, singular, input string }{{"providers", "Provider", "ProviderInput"}, {"models", "Model", "ModelInput"}} {
		path := "/assistant/admin/" + item.path
		add(path, "get", "List all "+item.path+", including disabled entries", "", item.singular+"Page", "admin", true)
		add(path, "post", "Create "+strings.ToLower(item.singular), item.input, item.singular, "admin", false)
		add(path+"/{id}", "put", "Replace "+strings.ToLower(item.singular)+" configuration", item.input, item.singular, "admin", false)
		add(path+"/{id}", "delete", "Delete "+strings.ToLower(item.singular)+" unless still referenced", "", "", "admin", false)
	}
	for _, item := range []struct {
		path, response string
		ownerFilter    bool
	}{{"credentials", "BYOCredentialPage", true}, {"usage", "UsageRecordPage", true}, {"accounts", "BalancePage", false}, {"ledger", "CreditEntryPage", true}, {"conversations", "ConversationPage", true}, {"generated", "GeneratedOverviewPage", false}, {"budgets", "BudgetUsagePage", false}} {
		op := add("/assistant/admin/"+item.path, "get", "List assistant "+item.path, "", item.response, "admin", true)
		if item.ownerFilter {
			filterUser(op)
		}
		if item.path == "generated" {
			p := op["parameters"].([]any)
			op["parameters"] = append(p, map[string]any{"name": "entity_id", "in": "query", "schema": id()}, map[string]any{"name": "owner_id", "in": "query", "schema": str(), "description": "Optional exact BYO cache owner filter."})
		}
		if item.path == "budgets" {
			op["description"] = "Requires a signed-in first-party account with assistant.manage. Lists at most the latest 1000 persisted monthly budget buckets; limit/offset page within this window."
		}
	}
	add("/assistant/admin/credentials/{id}", "put", "Update a user's BYO credential while preserving its owner", "CredentialInput", "BYOCredential", "admin", false)
	add("/assistant/admin/credentials/{id}", "delete", "Delete a user's BYO credential", "", "", "admin", false)
	add("/assistant/admin/accounts/{user_id}/adjust", "post", "Apply an idempotent credit adjustment with an audit note", "CreditAdjustment", "Balance", "admin", false)
	add("/assistant/admin/conversations/{id}", "get", "Read a conversation and messages for administration", "", "Conversation", "admin", false)
	add("/assistant/admin/conversations/{id}", "delete", "Delete a conversation unless a turn is in progress", "", "", "admin", false)
	add("/assistant/admin/generated/{id}", "delete", "Delete generated cached content", "", "", "admin", false)
	requests := add("/assistant/admin/requests", "get", "List sealed chat and overview generation requests without response bodies", "", "ChatRequestRecordPage", "admin", true)
	filterUser(requests)
	requests["parameters"] = append(requests["parameters"].([]any), map[string]any{"name": "status", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"pending", "completed", "failed"}}, "description": "Optional exact execution-state filter."})
	add("/assistant/admin/requests/{id}", "get", "Inspect a sealed request and its persisted response or partial progress", "", "ChatRequestRecord", "admin", false)
	cancel := add("/assistant/admin/requests/{id}/cancel", "post", "Seal an abandoned pending request as failed while retaining recorded results", "RequestCancel", "ChatRequestRecord", "admin", false)
	cancel["description"] = "Requires a signed-in first-party account with assistant.manage. Only pending requests created more than ten minutes ago can be cancelled; other requests return 409. Existing usage, result data and the idempotency ID are retained. This does not undo completed catalog edits, resend an upstream call or grant a new retry."
}

// assistantGeneratedOpenAPI keeps generated rich content explicit: the renderer
// never accepts arbitrary HTML, JavaScript, chart specifications or components.
func assistantGeneratedOpenAPI(schemas map[string]any) {
	ref := func(name string) map[string]any {
		return map[string]any{"$ref": "#/components/schemas/Assistant" + name}
	}
	str := func(max int) map[string]any { return map[string]any{"type": "string", "maxLength": max} }
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
	}
	array := func(items any, max int) map[string]any {
		return map[string]any{"type": "array", "items": items, "maxItems": max}
	}
	typed := func(kind string, fields map[string]any, required ...string) map[string]any {
		fields["type"] = map[string]any{"type": "string", "enum": []string{kind}}
		fields["title"] = str(200)
		return object(fields, append([]string{"type"}, required...)...)
	}
	schemas["AssistantOverviewBlock"] = map[string]any{"oneOf": []any{
		typed("markdown", map[string]any{"markdown": str(40000)}, "markdown"),
		typed("table", map[string]any{"columns": array(str(200), 16), "rows": array(array(str(1000), 16), 200)}, "columns", "rows"),
		typed("timeline", map[string]any{"events": array(object(map[string]any{"date": str(64), "title": str(200), "description": str(2000), "entity_id": map[string]any{"type": "string", "format": "uuid"}}, "date", "title"), 100)}, "events"),
		typed("chart", map[string]any{"chart_type": map[string]any{"type": "string", "enum": []string{"bar", "line"}}, "labels": array(str(200), 100), "series": array(object(map[string]any{"name": str(200), "values": array(map[string]any{"type": "number"}, 100)}, "name", "values"), 8)}, "chart_type", "labels", "series"),
	}}
	schemas["AssistantGenerateRequest"] = object(map[string]any{"model_id": map[string]any{"type": "string", "format": "uuid", "description": "Omit to use the configured default model."}, "credential_id": map[string]any{"type": "string", "format": "uuid"}, "locale": map[string]any{"type": "string", "enum": []string{"zh-CN", "zh-TW", "en-US", "ja-JP"}, "default": "zh-CN"}, "regenerate": map[string]any{"type": "boolean"}, "client_request_id": map[string]any{"type": "string", "format": "uuid", "description": "Optional idempotency key retained when retrying; a duplicate sealed generation request returns 409 and never sends a second completion upstream."}})
	schemas["AssistantGeneratedResponse"] = object(map[string]any{"item": map[string]any{"allOf": []any{ref("GeneratedOverview")}, "nullable": true}, "stale": map[string]any{"type": "boolean"}, "usage": ref("TokenUsage"), "credits_spent": map[string]any{"type": "integer", "format": "int64", "minimum": 0}, "balance": ref("Balance")}, "item", "stale")
}
