package assistant

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

func TestOpenAPIRegisteredRoutes(t *testing.T) {
	doc := catalog.OpenAPI()
	paths := doc["paths"].(map[string]any)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	AddOpenAPI(paths, schemas)
	if _, err := json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	HTTP{Catalog: &catalog.Store{}}.Register(router.Group("/api"))
	routes := map[string]bool{}
	for _, route := range router.Routes() {
		segments := strings.Split(strings.TrimPrefix(route.Path, "/api"), "/")
		for i, segment := range segments {
			if strings.HasPrefix(segment, ":") {
				segments[i] = "{" + segment[1:] + "}"
			}
		}
		path, method := strings.Join(segments, "/"), strings.ToLower(route.Method)
		routes[method+" "+path] = true
		item, ok := paths[path].(map[string]any)
		if !ok {
			t.Errorf("missing documented path: %s", path)
			continue
		}
		op, ok := item[method].(map[string]any)
		if !ok {
			t.Errorf("missing documented method: %s %s", method, path)
			continue
		}
		if strings.HasPrefix(path, "/assistant/admin/") {
			permission, ok := op["x-required-permissions"].([]string)
			if !ok || len(permission) != 1 || permission[0] != PermissionManage {
				t.Errorf("missing management permission: %s %s", method, path)
			}
		}
		parameters, _ := op["parameters"].([]any)
		for _, segment := range segments {
			if !strings.HasPrefix(segment, "{") {
				continue
			}
			name := strings.Trim(segment, "{}")
			found := false
			for _, p := range parameters {
				param := p.(map[string]any)
				found = found || param["name"] == name && param["in"] == "path" && param["required"] == true
			}
			if !found {
				t.Errorf("undeclared path parameter %s: %s", name, path)
			}
		}
	}
	for path, value := range paths {
		if !strings.HasPrefix(path, "/assistant/") {
			continue
		}
		for method := range value.(map[string]any) {
			if !routes[method+" "+path] {
				t.Errorf("documented operation without registered handler: %s %s", method, path)
			}
		}
	}
	var visit func(any)
	visit = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if raw, ok := v["$ref"].(string); ok {
				name := strings.TrimPrefix(raw, "#/components/schemas/")
				if _, exists := schemas[name]; !exists {
					t.Errorf("dangling schema reference: %s", raw)
				}
			}
			for _, item := range v {
				visit(item)
			}
		case []any:
			for _, item := range v {
				visit(item)
			}
		}
	}
	visit(doc)
}

func TestOpenAPISecretsAndPagination(t *testing.T) {
	paths, schemas := map[string]any{}, map[string]any{}
	AddOpenAPI(paths, schemas)
	account := schemas["AssistantAccount"].(map[string]any)
	if account["properties"].(map[string]any)["default_model_id"].(map[string]any)["type"] != "string" {
		t.Error("account response must expose the configured default model as a string, allowing an empty value")
	}
	if !strings.Contains(","+strings.Join(account["required"].([]string), ",")+",", ",default_model_id,") {
		t.Error("account response always includes default_model_id")
	}
	for _, name := range []string{"AssistantProviderInput", "AssistantCredentialInput"} {
		props := schemas[name].(map[string]any)["properties"].(map[string]any)
		if props["api_key"].(map[string]any)["writeOnly"] != true {
			t.Errorf("credential plaintext must be write-only: %s", name)
		}
	}
	for _, name := range []string{"AssistantProvider", "AssistantBYOCredential", "AssistantModel", "AssistantGeneratedOverview"} {
		props := schemas[name].(map[string]any)["properties"].(map[string]any)
		for _, key := range []string{"api_key", "encrypted_key", "content", "dependencies"} {
			if _, exists := props[key]; exists {
				t.Errorf("response exposes secret or internal generated storage: %s.%s", name, key)
			}
		}
	}
	for path, value := range paths {
		for method, raw := range value.(map[string]any) {
			op := raw.(map[string]any)
			responses := op["responses"].(map[string]any)
			if method == "delete" {
				if _, ok := responses["204"]; !ok {
					t.Errorf("delete must document 204: %s", path)
				}
				continue
			}
			success := responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
			name := strings.TrimPrefix(success["$ref"].(string), "#/components/schemas/")
			if !strings.HasSuffix(name, "Page") {
				continue
			}
			props := schemas[name].(map[string]any)["properties"].(map[string]any)
			for _, field := range []string{"items", "total", "limit", "offset"} {
				if _, ok := props[field]; !ok {
					t.Errorf("page schema missing %s: %s", field, name)
				}
			}
			parameters, _ := op["parameters"].([]any)
			for _, field := range []string{"limit", "offset"} {
				found := false
				for _, item := range parameters {
					param := item.(map[string]any)
					found = found || param["name"] == field && param["in"] == "query"
				}
				if !found {
					t.Errorf("page operation missing query %s: %s", field, path)
				}
			}
		}
	}
}

func TestOpenAPIRequestAdministration(t *testing.T) {
	paths, schemas := map[string]any{}, map[string]any{}
	AddOpenAPI(paths, schemas)
	record := schemas["AssistantChatRequestRecord"].(map[string]any)
	props := record["properties"].(map[string]any)
	dto := reflect.TypeOf(ChatRequestRecord{})
	if len(props) != dto.NumField() {
		t.Fatalf("request record fields drifted from DTO: schema=%d DTO=%d", len(props), dto.NumField())
	}
	required := map[string]bool{}
	for _, name := range record["required"].([]string) {
		required[name] = true
	}
	for i := 0; i < dto.NumField(); i++ {
		field := dto.Field(i)
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if _, exists := props[name]; !exists {
			t.Errorf("missing request record field: %s", name)
		}
		if required[name] == strings.Contains(tag, ",omitempty") {
			t.Errorf("request field required/optional drift: %s", name)
		}
	}
	if props["response"].(map[string]any)["$ref"] != "#/components/schemas/AssistantRequestResponse" {
		t.Error("persisted JSON response must use the explicit result schema")
	}
	list := paths["/assistant/admin/requests"].(map[string]any)["get"].(map[string]any)
	params := map[string]map[string]any{}
	for _, raw := range list["parameters"].([]any) {
		param := raw.(map[string]any)
		params[param["name"].(string)] = param
	}
	for _, name := range []string{"limit", "offset", "user_id", "status"} {
		if param, ok := params[name]; !ok || param["in"] != "query" {
			t.Errorf("missing request list filter: %s", name)
		}
	}
	statuses := params["status"]["schema"].(map[string]any)["enum"].([]string)
	if strings.Join(statuses, ",") != "pending,completed,failed" {
		t.Errorf("unexpected request status filter: %v", statuses)
	}
	cancel := paths["/assistant/admin/requests/{id}/cancel"].(map[string]any)["post"].(map[string]any)
	responses := cancel["responses"].(map[string]any)
	if _, ok := responses["409"]; !ok {
		t.Error("cancelling an active or already sealed request must document 409")
	}
	responseRef := responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["$ref"]
	if responseRef != "#/components/schemas/AssistantChatRequestRecord" {
		t.Errorf("cancel response must retain the sealed record, got %v", responseRef)
	}
	requestResponse := schemas["AssistantRequestResponse"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"tool_results", "push_receipts", "usage", "error", "admin_cancel_note", "cancelled_by"} {
		if _, exists := requestResponse[name]; !exists {
			t.Errorf("persisted partial response or cancellation metadata missing: %s", name)
		}
	}
	if _, exists := paths["/assistant/admin/requests/{id}"].(map[string]any)["delete"]; exists {
		t.Error("sealed request IDs must not expose a delete operation")
	}
}

func TestOpenAPIModelOverridesAndEstimatedUsage(t *testing.T) {
	paths, schemas := map[string]any{}, map[string]any{}
	AddOpenAPI(paths, schemas)
	props := func(name string) map[string]any {
		return schemas[name].(map[string]any)["properties"].(map[string]any)
	}
	for _, name := range []string{"AssistantModel", "AssistantModelInput"} {
		field := props(name)["max_tokens_parameter"].(map[string]any)
		if field["default"] != "max_tokens" || strings.Join(field["enum"].([]string), ",") != "max_tokens,max_completion_tokens" {
			t.Errorf("model token-limit parameter contract drift: %s", name)
		}
	}
	for _, name := range []string{"AssistantBYOCredential", "AssistantCredentialInput"} {
		if _, ok := props(name)["upstream_model"]; !ok {
			t.Errorf("missing BYO upstream override: %s", name)
		}
	}
	usage := props("AssistantUsageRecord")
	if usage["usage_estimated"].(map[string]any)["type"] != "boolean" {
		t.Error("estimated provider usage must be explicitly distinguishable")
	}
	statuses := usage["status"].(map[string]any)["enum"].([]string)
	if !strings.Contains(","+strings.Join(statuses, ",")+",", ",unknown,") {
		t.Error("usage contract must include unknown upstream outcome")
	}
	for _, name := range []string{"AssistantChatResponse", "AssistantRequestResponse"} {
		if props(name)["usage_estimated"].(map[string]any)["type"] != "boolean" {
			t.Errorf("chat and persisted responses must mark estimated usage: %s", name)
		}
	}
	for _, field := range schemas["AssistantChatResponse"].(map[string]any)["required"].([]string) {
		if field == "usage_estimated" {
			t.Error("chat usage_estimated is omitted when no provider usage was estimated")
		}
	}
	credential := props("AssistantChatRequest")["credential_id"].(map[string]any)
	variants := credential["oneOf"].([]any)
	if len(variants) != 2 || variants[1].(map[string]any)["enum"].([]string)[0] != "" {
		t.Error("explicit empty credential_id must allow clearing a conversation's BYO selection")
	}
}
