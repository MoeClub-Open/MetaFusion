package assistant

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/metafusion/metafusion-app/internal/catalog"
)

func TestDefinitionLookupKeepsLargeUIAndVocabulariesOutOfContext(t *testing.T) {
	config := catalog.DefinitionConfig{ETag: "live", Document: catalog.Definitions{
		Fields:       map[string]catalog.Field{"agent_type": {ApplicableKinds: []string{"agent"}}, "creation_form": {ApplicableKinds: []string{"work"}}},
		Templates:    map[string]catalog.Template{"large_ui": {Names: catalog.Names{"en-US": strings.Repeat("UI-only", 20000)}}},
		Vocabularies: map[string]catalog.Vocabulary{"forms": {Terms: map[string]catalog.Term{"tv_anime": {Enabled: true}}}},
	}}
	index, err := assistantDefinitionOutput(config, definitionQuery{Kind: "work"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(index)
	if len(raw) > 5000 || strings.Contains(string(raw), "UI-only") || strings.Contains(string(raw), "agent_type") || index["etag"] != "live" {
		t.Fatalf("index includes irrelevant or duplicated definition data: %s", raw)
	}
	if !strings.Contains(string(raw), "external_links") || !strings.Contains(string(raw), "translations") {
		t.Fatalf("index missing top-level entity schema hint: %s", raw)
	}
	extOut, err := assistantExternalDatabasesOutput([]catalog.ExternalDatabase{{Code: "official_website", Category: "all", URLPattern: "{id}"}}, []string{"official_website"})
	if err != nil || extOut["official_website"]["code"] != "official_website" {
		t.Fatalf("external database lookup failed: %v %+v", err, extOut)
	}
	metadata, err := assistantDefinitionOutput(config, definitionQuery{Section: "vocabularies", Codes: []string{"forms"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(metadata)
	if !strings.Contains(string(raw), "term_codes") || strings.Contains(string(raw), "enabled") {
		t.Fatal("vocabulary metadata expanded every term")
	}
	terms, err := assistantDefinitionOutput(config, definitionQuery{Section: "terms", Vocabulary: "forms", Codes: []string{"tv_anime"}})
	if err != nil || !terms["definitions"].(map[string]catalog.Term)["tv_anime"].Enabled {
		t.Fatalf("selected term lost the live rule: %v", err)
	}
	if _, err = assistantDefinitionOutput(config, definitionQuery{Section: "fields", Codes: []string{"invented"}}); err == nil {
		t.Fatal("unknown code became an invented definition")
	}
}

func TestChatCompletionInputBoundFits32KContextWindow(t *testing.T) {
	tools, err := completionTools()
	if err != nil {
		t.Fatal(err)
	}
	bound := completionInputBound(chatMessages(nil, "Use my own model"), tools)
	if bound+1280 > 32768 {
		t.Fatalf("chat tool schema + prompt bound %d leaves insufficient room in a 32768 context window", bound)
	}
}
