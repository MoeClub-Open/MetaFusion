package assistant

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/metafusion/metafusion-app/internal/catalog"
)

func TestInitialChatContextAndProgressiveToolsets(t *testing.T) {
	msgs := chatMessages(nil, "")
	coreTools, err := chatCompletionTools(msgs)
	if err != nil {
		t.Fatal(err)
	}
	allTools, err := completionTools()
	if err != nil {
		t.Fatal(err)
	}
	coreBound := completionInputBound(msgs, coreTools)
	fullBound := completionInputBound(msgs, allTools)
	msgBytes, _ := json.Marshal(msgs)
	coreToolBytes, _ := json.Marshal(coreTools)
	allToolBytes, _ := json.Marshal(allTools)
	t.Logf("CORE: prompt=%d msgs_json=%d tools=%d tools_json=%d bound=%d",
		len(catalogAssistantPrompt), len(msgBytes), len(coreTools), len(coreToolBytes), coreBound)
	t.Logf("FULL: edit_prompt=%d all_tools=%d all_tools_json=%d full_bound=%d",
		len(catalogEditingPrompt), len(allTools), len(allToolBytes), fullBound)
	type row struct {
		name   string
		total  int
		desc   int
		params int
	}
	rows := make([]row, 0, len(allTools))
	for _, tl := range allTools {
		b, _ := json.Marshal(tl)
		rows = append(rows, row{tl.Function.Name, len(b), len(tl.Function.Description), len(tl.Function.Parameters)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].total > rows[j].total })
	for _, r := range rows {
		t.Logf("TOOL %-32s total=%5d desc=%4d params=%5d", r.name, r.total, r.desc, r.params)
	}
	if len(coreTools) != 12 {
		t.Fatalf("expected 12 core chat tools, got %d", len(coreTools))
	}
	if coreBound > 3200 {
		t.Fatalf("initial chat context bound %d exceeds compact budget 3200", coreBound)
	}
	if len(allTools) != 35 || len(allToolBytes) > 22500 {
		t.Fatalf("unexpected full tool registry: count=%d bytes=%d", len(allTools), len(allToolBytes))
	}

	// Invalid or unknown group arguments are strictly rejected.
	for _, bad := range []string{`{}`, `{"groups":[]}`, `{"groups":["unknown"]}`, `{"groups":["edit"],"actor_id":"x"}`} {
		if _, err := runLoadTools(json.RawMessage(bad)); err == nil || err.Error() != "invalid_tool_arguments" {
			t.Fatalf("expected invalid_tool_arguments for %s, got %v", bad, err)
		}
	}

	// Unlocking edit in a conversation expands tools + editing prompt without counting against max_tool_rounds.
	loadCall := ToolCall{ID: "load-1", Type: "function"}
	loadCall.Function.Name, loadCall.Function.Arguments = "assistant_load_tools", `{"groups":["edit"]}`
	searchCall := ToolCall{ID: "read-1", Type: "function"}
	searchCall.Function.Name, searchCall.Function.Arguments = "catalog_definitions", `{}`
	f := &runtimeFixture{responses: []CompletionResponse{
		{Message: Message{ToolCalls: []ToolCall{loadCall}}},
		{Message: Message{ToolCalls: []ToolCall{searchCall}}},
		{Message: Message{Content: "done"}},
	}}
	// runtimeFixture has MaxToolRounds=2; load-only round 0 grants +1 round so round 1 tool + round 2 answer succeed.
	initial := chatMessages(nil, "Edit record")
	run, err := runCompletion(context.Background(), f, nil, catalog.User{ID: "owner"}, ChatRequest{
		ClientRequestID: uuid.NewString(),
		beforeCompletion: func() error {
			return nil
		},
	}, initial, true, "chat", "")
	if err != nil || run.Answer != "done" {
		t.Fatalf("progressive load completion failed: err=%v answer=%q", err, run.Answer)
	}
	if !strings.Contains(run.Messages[0].Content, "Catalog editing & review rules:") {
		t.Fatalf("system prompt did not activate editing rules after load_tools")
	}
	seenRound2Tools := []string{}
	nextTurnTools, err := chatCompletionTools(withoutSystemMessages(run.Messages))
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range nextTurnTools {
		seenRound2Tools = append(seenRound2Tools, tl.Function.Name)
	}
	if !slices.Contains(seenRound2Tools, "catalog_workspace_stage") || !slices.Contains(seenRound2Tools, "catalog_candidates") {
		t.Fatalf("edit tools missing in subsequent turn: %v", seenRound2Tools)
	}
	if slices.Contains(seenRound2Tools, "catalog_version_apply") {
		t.Fatalf("unrelated version tools should stay unloaded: %v", seenRound2Tools)
	}

	// Historical conversations with direct workspace/PR/version calls automatically retain their toolsets.
	directHistory := []Message{{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Type: "function"}}}}
	directHistory[0].ToolCalls[0].Function.Name = "catalog_pull_request_get"
	prMsgs := chatMessages(directHistory, "check PR")
	prTools, err := chatCompletionTools(prMsgs)
	if err != nil || len(prTools) != 21 || !strings.Contains(prMsgs[0].Content, "Catalog editing & review rules:") {
		t.Fatalf("historical PR conversation did not restore PR toolset and rules: tools=%d err=%v", len(prTools), err)
	}
}
