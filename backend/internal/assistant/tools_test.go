package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/metafusion/metafusion-app/internal/catalog"
)

func TestAssistantToolsRefuseCallerImpersonation(t *testing.T) {
	for _, name := range []string{"catalog_search", "catalog_get", "catalog_relationships", "catalog_candidates", "catalog_workspace_create", "catalog_workspace_list", "catalog_workspace_get", "catalog_workspace_edit", "catalog_workspace_checkout", "catalog_workspace_stage", "catalog_workspace_members", "catalog_workspace_commit", "catalog_workspace_commits", "catalog_workspace_push", "catalog_workspace_rebase", "catalog_pull_request_create", "catalog_pull_request_list", "catalog_pull_request_get", "catalog_pull_request_edit", "catalog_pull_request_merge", "catalog_commit_get", "catalog_definitions", "external_sources", "external_source_query", "public_fetch", "public_wiki"} {
		t.Run(name, func(t *testing.T) {
			_, err := runCatalogTool(context.Background(), &catalog.Store{}, catalog.User{ID: "caller"}, name, json.RawMessage(`{"actor_id":"other","permissions":["*"]}`))
			if err == nil || err.Error() != "invalid_tool_arguments" {
				t.Fatalf("impersonation was not rejected before storage access: %v", err)
			}
		})
	}
}

func TestAssistantWorkspaceWritesDoNotElevateOAuthOrAnonymous(t *testing.T) {
	for _, user := range []catalog.User{{}, {ID: "oauth", Permissions: []string{"*"}, IsThirdParty: true}} {
		for _, name := range []string{"catalog_version_fork", "catalog_version_apply", "catalog_version_tag", "catalog_version_stash", "catalog_workspace_create", "catalog_workspace_stage", "catalog_workspace_commit", "catalog_workspace_push", "catalog_workspace_rebase", "catalog_pull_request_create", "catalog_pull_request_merge"} {
			_, err := runCatalogTool(context.Background(), &catalog.Store{}, user, name, json.RawMessage(`{}`))
			if err == nil || err.Error() != "forbidden" {
				t.Fatalf("%s elevated %+v: %v", name, user, err)
			}
		}
	}
}

func TestAssistantRemovedDirectCommitToolCannotBeInvoked(t *testing.T) {
	_, err := runCatalogTool(context.Background(), &catalog.Store{}, catalog.User{ID: "caller"}, "catalog_commit", json.RawMessage(`{}`))
	if err == nil || err.Error() != "unknown_tool" {
		t.Fatalf("old direct mutation path remains: %v", err)
	}
}

func TestAssistantToolArgumentsAreBoundedAndStrict(t *testing.T) {
	for _, raw := range []json.RawMessage{
		nil, json.RawMessage(`null`), json.RawMessage(`[]`), json.RawMessage(`{} {}`), json.RawMessage(`{"unknown":true}`),
		json.RawMessage("{\"value\":\"" + strings.Repeat("a", maxAssistantToolArguments) + "\"}"),
		{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
	} {
		if err := decodeAssistantToolArgs(raw, &struct{}{}); err == nil {
			t.Fatalf("invalid arguments were accepted: %.80s", raw)
		}
	}
	if err := decodeAssistantToolArgs(json.RawMessage(" {} \n\t"), &struct{}{}); err != nil {
		t.Fatalf("empty argument object rejected: %v", err)
	}
}

func TestAssistantToolBoundsDoNotSilentlyClamp(t *testing.T) {
	for _, test := range []struct{ name, args, want string }{
		{"catalog_search", `{"q":"title","limit":0}`, "invalid_limit"},
		{"catalog_search", `{"q":"title","limit":26}`, "invalid_limit"},
		{"catalog_search", `{"q":"title","cursor":"next","offset":1}`, "invalid_pagination"},
		{"catalog_search", `{"q":"title\nextra"}`, "invalid_query_param"},
		{"catalog_search", `{"cursor":"next"}`, "invalid_search_cursor"},
		{"catalog_relationships", `{"ids":[],"limit":26}`, "invalid_limit"},
		{"catalog_candidates", `{"kind":"work","titles":["x"],"limit":1001}`, "invalid_limit"},
		{"external_source_query", `{"source":"bangumi","op":"search","q":"x","limit":26}`, "invalid_limit"},
		{"public_fetch", `{"url":"https://example.com","max_chars":60001}`, "invalid_source_projection"},
		{"public_wiki", `{"site":"wikipedia","query":"title","limit":21}`, "invalid_limit"},
	} {
		_, err := runCatalogTool(context.Background(), &catalog.Store{}, catalog.User{ID: "caller"}, test.name, json.RawMessage(test.args))
		if err == nil || err.Error() != test.want {
			t.Fatalf("%s %s: want %s, got %v", test.name, test.args, test.want, err)
		}
	}
}

func TestAssistantToolOversizeOutputCannotBecomeEditableSnapshot(t *testing.T) {
	value := map[string]any{"title": "work", "body": strings.Repeat("x", maxAssistantToolOutput)}
	result, err := boundedAssistantToolOutput(value)
	if err != nil {
		t.Fatal(err)
	}
	object, ok := result.(map[string]any)
	if !ok || object["error"] != "tool_output_too_large" || object["complete"] != false || object["body"] != nil {
		t.Fatalf("oversize response looked like a complete snapshot: %#v", result)
	}
}

func TestAssistantLargeMutationRetainsAuthoritativeOutcome(t *testing.T) {
	large := json.RawMessage(`{"body":"` + strings.Repeat("x", maxAssistantToolOutput) + `"}`)
	workspace := catalog.Workspace{ID: "copy", Version: 7, Objects: []catalog.WorkspaceObject{{Document: large}}}
	commit := catalog.CollaborationCommit{ID: "commit", Changes: []catalog.CommitObject{{After: large}}}
	value, err := boundedAssistantToolOutput(catalog.WorkspacePushResult{Applied: true, Workspace: &workspace, Commits: []catalog.CollaborationCommit{commit}})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := value.(map[string]any)
	receipt := wrapped["receipt"].(catalog.WorkspacePushResult)
	if !receipt.Applied || receipt.Workspace.ID != "copy" || receipt.Workspace.Version != 7 || receipt.Commits[0].ID != "commit" || wrapped["editable_snapshot"] != false {
		t.Fatalf("large push lost its merge receipt: %#v", value)
	}
	if len(receipt.Workspace.Objects) != 0 || len(receipt.Commits[0].Changes) != 0 {
		t.Fatal("large snapshots were not omitted")
	}
	value, err = boundedAssistantToolOutput(catalog.WorkspacePushResult{Applied: true, Commits: []catalog.CollaborationCommit{commit}})
	if err != nil || value.(map[string]any)["receipt"].(catalog.WorkspacePushResult).Workspace != nil {
		t.Fatalf("reviewer receipt gained a private workspace: %#v %v", value, err)
	}
	value, err = boundedAssistantToolOutput(catalog.WorkspaceChangesResult{Workspace: workspace, Refs: map[string]string{"new": "stable-id"}})
	if err != nil || value.(map[string]any)["workspace"].(catalog.Workspace).Version != 7 || value.(map[string]any)["refs"].(map[string]string)["new"] != "stable-id" {
		t.Fatalf("large staging reply lost CAS or stable IDs: %#v %v", value, err)
	}
}

func TestAssistantSearchSummariesNeverIncludeArticleOrOwner(t *testing.T) {
	result := assistantEntitySummaries([]catalog.Entity{{ID: "entity", Kind: "work", Title: "Original", CreatedBy: "owner", Translations: map[string]catalog.Translation{"ja": {Title: "Original", Body: "large article", Aliases: []string{"alias"}}}}})
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "large article") || strings.Contains(string(raw), "owner") {
		t.Fatalf("summary leaked unrelated data: %s", raw)
	}
	if len(result) != 1 || result[0]["title"] != "Original" {
		t.Fatalf("summary lost the source title: %#v", result)
	}
}

func TestAssistantCandidatesPreserveAllIdentityIDsWhenDetailsAreLarge(t *testing.T) {
	input := catalog.IdentityCandidateResponse{Total: 26, Complete: true, Basis: "postgres_repeatable_read"}
	for i := 0; i < 26; i++ {
		candidate := catalog.IdentityCandidateEntity{ID: "candidate-" + string(rune('a'+i)), Kind: "work", Version: 1, Attributes: map[string]any{"data": strings.Repeat("x", maxAssistantToolOutput/20)}}
		input.Items = append(input.Items, catalog.IdentityCandidateMatch{Matched: candidate, Canonical: &candidate})
	}
	result := assistantCandidateOutput(input).(map[string]any)
	if result["complete"] != true || result["details_complete"] != false {
		t.Fatalf("identity-set and detail completeness were confused: %#v", result)
	}
	items := result["items"].([]map[string]any)
	if len(items) != 26 || items[25]["canonical"].(map[string]any)["id"] != "candidate-z" {
		t.Fatalf("candidate identities were silently truncated: %#v", result)
	}
	bounded, err := boundedAssistantToolOutput(result)
	if err != nil || bounded.(map[string]any)["items"] == nil {
		t.Fatalf("full identity set did not fit the output boundary: %v %#v", err, bounded)
	}
}

func TestAssistantToolDefinitionsHaveStrictTopLevelArguments(t *testing.T) {
	seen := map[string]bool{}
	for _, definition := range assistantToolDefinitions() {
		function, ok := definition["function"].(map[string]any)
		if !ok || definition["type"] != "function" {
			t.Fatalf("invalid provider tool definition: %#v", definition)
		}
		name, _ := function["name"].(string)
		if name == "" || seen[name] {
			t.Fatalf("duplicate or empty tool: %s", name)
		}
		seen[name] = true
		schema := function["parameters"].(map[string]any)
		if schema["additionalProperties"] != false {
			t.Fatalf("%s accepts caller-controlled identity fields", name)
		}
		if _, err := json.Marshal(definition); err != nil {
			t.Fatalf("%s is not serializable: %v", name, err)
		}
	}
	for _, name := range []string{"catalog_search", "catalog_get", "catalog_relationships", "catalog_candidates", "catalog_workspace_create", "catalog_workspace_list", "catalog_workspace_get", "catalog_workspace_edit", "catalog_workspace_checkout", "catalog_workspace_stage", "catalog_workspace_members", "catalog_workspace_commit", "catalog_workspace_commits", "catalog_workspace_push", "catalog_workspace_rebase", "catalog_pull_request_create", "catalog_pull_request_list", "catalog_pull_request_get", "catalog_pull_request_edit", "catalog_pull_request_merge", "catalog_commit_get", "catalog_definitions", "external_sources", "external_source_query", "public_fetch", "public_wiki"} {
		if !seen[name] {
			t.Fatalf("missing required tool: %s", name)
		}
	}
	if seen["catalog_commit"] {
		t.Fatal("old direct commit tool remains")
	}
	var stage map[string]any
	for _, definition := range assistantToolDefinitions() {
		fn := definition["function"].(map[string]any)
		if fn["name"] == "catalog_workspace_stage" {
			stage = fn["parameters"].(map[string]any)
		}
	}
	properties := stage["properties"].(map[string]any)
	operation := properties["operations"].(map[string]any)["items"].(map[string]any)
	opProperties := operation["properties"].(map[string]any)
	actions := opProperties["action"].(map[string]any)["enum"].([]string)
	if strings.Join(actions, ",") != "create,update,delete,content" {
		t.Fatalf("invalid operation vocabulary: %v", actions)
	}
	if opProperties["source_refs"] != nil || opProperties["reviewed_candidate_ids"] != nil {
		t.Fatal("staging still requires an evidence workflow")
	}

}

func TestAssistantPublicSourceExcerptsPreserveProvenance(t *testing.T) {
	source := catalog.PublicSourceResponse{URL: "https://en.wikipedia.org/w/api.php?titles=Example", ContentHash: "sha256:original", License: "CC BY-SA", Attribution: "Contributors", Text: "一二三四五六", Data: map[string]any{"duplicate": "unabridged source"}}
	first := assistantPublicSourceOutput(source, 0, 3)
	second := assistantPublicSourceOutput(source, 3, 3)
	if first["text"] != "一二三" || first["complete"] != false || first["next_text_offset"] != 3 || first["has_more_text"] != true {
		t.Fatalf("incorrect Unicode excerpt: %#v", first)
	}
	if second["text"] != "四五六" || second["has_more_text"] != false || second["complete"] != false {
		t.Fatalf("continuation claimed to contain the full article: %#v", second)
	}
	for _, result := range []map[string]any{first, second} {
		if result["content_hash"] != source.ContentHash || result["license"] != source.License || result["attribution"] != source.Attribution || result["data"] != nil {
			t.Fatalf("excerpt lost provenance or included the unbounded duplicate: %#v", result)
		}
	}
	jsonSource := catalog.PublicSourceResponse{URL: "https://example.com/api", Data: map[string]any{"key": strings.Repeat("x", 100)}}
	projection := assistantPublicSourceOutput(jsonSource, 0, 10)
	if projection["projection"] != "json_excerpt" || projection["complete"] != false || projection["next_text_offset"] != 10 {
		t.Fatalf("oversize JSON claimed to be complete: %#v", projection)
	}
}

func TestAssistantExternalSourcesReuseCatalogRegistry(t *testing.T) {
	store := &catalog.Store{}
	actual, err := runCatalogTool(context.Background(), store, catalog.User{}, "external_sources", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want, err := store.ExternalSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	actualJSON, _ := json.Marshal(actual)
	wantJSON, _ := json.Marshal(want)
	if string(actualJSON) != string(wantJSON) {
		t.Fatalf("tool source metadata diverges from registry: %s != %s", actualJSON, wantJSON)
	}
}
