package assistant

import (
	"encoding/json"
	"errors"
	"sync"
)

const catalogEditingPrompt = `Catalog editing & review rules: Work titles name the work; Release titles preserve the edition title; Event entities cover standalone concerts/tours/tournaments/conventions/awards while minor dates stay in embedded events. Record Chinese titles only from official/authorized localization; community names must use translations.<locale>.nonofficial=true or nonofficial_aliases (never append markers to title text). Read catalog_definitions before setting dynamic fields/relations/terms, and set primary subtypes accurately: agent.agent_type, collection.collection_type (franchise/series/anthology/event_catalog), event.event_type, work.creation_form (song for single tracks, never original_album; single/ep/original_album/soundtrack/compilation_album for multi-track works), content_unit.entry_role, release.edition_type, medium.format. Relation & temporal boundaries: created_by is strictly for original creators; produced_by covers studios, production companies, producers, circles, organizers, and game makers (exact role in credit_role); prefer specific codes (directed_by, written_by, composed_by, lyricist_of, arranged_by, performed_by, voiced_by, character_in, member_of, subsidiary_of, succeeded_by, recorded_at, released_at, includes, participated_in) with credit_for only as fallback without duplicating specific edges; record temporal spans via begin_date/end_date/ended/date_precision and renames without identity split in agent.former_names. Always call catalog_candidates and catalog_get on close matches (reuse|review|distinct) before creating any entity. Edit via working copy: create/reuse -> checkout -> stage -> commit (UUID + message) -> push (expected_version + expected_head). Staging/committing never changes main; only applied=true or merged_at proves merge. Sources are optional. Preserve authorship, resolve conflicts via rebase without overwriting others' changes, recover uncertain writes from workspace/commit/PR state before retrying, and end with a concise summary of inspected/modified /catalog/<id> entities, field/relation changes, and merge/PR status.`

var (
	chatCoreToolNames = []string{
		"catalog_search", "catalog_get", "catalog_relationships", "catalog_definitions",
		"external_sources", "external_source_query", "public_fetch", "public_wiki",
		"web_search", "web_fetch", "assistant_mission", "assistant_load_tools",
	}
	chatToolsetOrder  = []string{"edit", "pr", "version"}
	chatToolsetGroups = map[string][]string{
		"edit": {
			"catalog_candidates", "catalog_workspace_create", "catalog_workspace_list",
			"catalog_workspace_get", "catalog_workspace_checkout", "catalog_workspace_stage",
			"catalog_workspace_commit", "catalog_workspace_push", "catalog_commit_get",
		},
		"pr": {
			"catalog_pull_request_list", "catalog_pull_request_get", "catalog_pull_request_create",
			"catalog_pull_request_edit", "catalog_pull_request_merge", "catalog_workspace_edit",
			"catalog_workspace_members", "catalog_workspace_commits", "catalog_workspace_rebase",
		},
		"version": {
			"catalog_version_read", "catalog_version_fork", "catalog_version_apply",
			"catalog_version_tag", "catalog_version_stash",
		},
	}
	chatGroupByTool = func() map[string]string {
		out := map[string]string{}
		for group, names := range chatToolsetGroups {
			for _, name := range names {
				out[name] = group
			}
		}
		return out
	}()
	cachedAllTools = sync.OnceValues(func() ([]Tool, error) {
		b, err := json.Marshal(assistantToolDefinitions())
		if err != nil {
			return nil, err
		}
		var tools []Tool
		if err = json.Unmarshal(b, &tools); err != nil {
			return nil, err
		}
		tools = append(tools, searchTools()...)
		tools = append(tools, missionChatTools()...)
		tools = append(tools, chatToolsetTools()...)
		return tools, nil
	})
)

func chatToolsetTools() []Tool {
	return []Tool{
		NewTool(
			"assistant_load_tools",
			"Unlock additional toolsets for this chat (call alongside read tools when needed): edit (candidates + working-copy create/checkout/stage/commit/push + editing rules), pr (pull requests, collaborators, branch commits, rebase), version (history/revisions/diffs/snapshots/blame + fork/rewrite/tag/stash).",
			`{"type":"object","properties":{"groups":{"type":"array","items":{"type":"string","enum":["edit","pr","version"]},"minItems":1,"maxItems":3}},"required":["groups"],"additionalProperties":false}`,
		),
	}
}

func activeChatToolGroups(messages []Message) map[string]bool {
	active := map[string]bool{}
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			if call.Function.Name == "assistant_load_tools" {
				var in struct {
					Groups []string `json:"groups"`
				}
				if decodeAssistantToolArgs(json.RawMessage(call.Function.Arguments), &in) == nil {
					for _, g := range in.Groups {
						if _, ok := chatToolsetGroups[g]; ok {
							active[g] = true
						}
					}
				}
			} else if g, ok := chatGroupByTool[call.Function.Name]; ok {
				active[g] = true
			}
		}
	}
	return active
}

func activeChatToolNames(messages []Message) []string {
	active := activeChatToolGroups(messages)
	out := make([]string, 0, len(chatCoreToolNames)+23)
	out = append(out, chatCoreToolNames...)
	for _, g := range chatToolsetOrder {
		if active[g] {
			out = append(out, chatToolsetGroups[g]...)
		}
	}
	return out
}

func chatCompletionTools(messages []Message) ([]Tool, error) {
	return completionToolsFiltered(activeChatToolNames(messages))
}

func chatSystemPrompt(messages []Message) string {
	active := activeChatToolGroups(messages)
	if active["edit"] || active["pr"] {
		return catalogAssistantPrompt + "\n\n" + catalogEditingPrompt
	}
	return catalogAssistantPrompt
}

func runLoadTools(raw json.RawMessage) (any, error) {
	var in struct {
		Groups []string `json:"groups"`
	}
	if err := decodeAssistantToolArgs(raw, &in); err != nil {
		return nil, err
	}
	if len(in.Groups) == 0 || len(in.Groups) > 3 {
		return nil, errors.New("invalid_tool_arguments")
	}
	want := map[string]bool{}
	for _, g := range in.Groups {
		if _, ok := chatToolsetGroups[g]; !ok {
			return nil, errors.New("invalid_tool_arguments")
		}
		want[g] = true
	}
	enabled := make([]string, 0, len(want))
	names := []string{}
	for _, g := range chatToolsetOrder {
		if want[g] {
			enabled = append(enabled, g)
			names = append(names, chatToolsetGroups[g]...)
		}
	}
	return map[string]any{"enabled": enabled, "tools": names}, nil
}

func isLoadToolsOnlyRound(calls []ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, c := range calls {
		if c.Function.Name != "assistant_load_tools" {
			return false
		}
	}
	return true
}
