package curator

// AddOpenAPI publishes the curator admin surface next to the assistant module.
// The schema names are prefixed so separately registered modules stay isolated.
func AddOpenAPI(paths, schemas map[string]any) {
	ref := func(name string) map[string]any {
		return map[string]any{"$ref": "#/components/schemas/Curator" + name}
	}
	str := func() map[string]any { return map[string]any{"type": "string"} }
	integer := func() map[string]any { return map[string]any{"type": "integer", "format": "int64"} }
	boolean := func() map[string]any { return map[string]any{"type": "boolean"} }
	object := func(properties map[string]any, required ...string) map[string]any {
		v := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
		if len(required) > 0 {
			v["required"] = required
		}
		return v
	}
	page := func(name string) map[string]any {
		return object(map[string]any{
			"items": map[string]any{"type": "array", "items": ref(name)}, "total": integer(),
			"limit": integer(), "offset": integer(),
		}, "items", "total", "limit", "offset")
	}
	reasoningEffortProp := map[string]any{"type": "string", "enum": []string{"", "none", "minimal", "low", "medium", "high", "xhigh"}, "default": ""}
	schemas["CuratorExecutionConfig"] = object(map[string]any{
		"child_model_id": str(), "reasoning_effort": reasoningEffortProp, "child_reasoning_effort": reasoningEffortProp, "billing_owner_id": map[string]any{"type": "string", "readOnly": true}, "search_provider_id": str(), "search_byo_provider_id": str(), "max_children": integer(), "allow_reviews": boolean(), "auto_merge_reviews": boolean(),
	})
	schemas["CuratorTaskInput"] = object(map[string]any{"id": str(), "message": str(), "entity_id": str()}, "id", "message")
	schemas["CuratorMissionInput"] = object(map[string]any{"client_request_id": map[string]any{"type": "string", "format": "uuid"}, "title": str(), "goal": str(), "model_id": str(), "worker_model_id": str(), "reasoning_effort": reasoningEffortProp, "worker_reasoning_effort": reasoningEffortProp, "budget_credits": integer(), "max_workers": integer(), "max_tasks": integer(), "max_planner_rounds": integer()}, "client_request_id", "goal", "budget_credits")
	schemas["CuratorSettings"] = object(map[string]any{
		"execution": ref("ExecutionConfig"), "enabled": boolean(), "model_id": str(), "interval_seconds": integer(), "max_runs_per_hour": integer(),
		"monthly_credit_allowance": integer(), "auto_merge_low_risk": boolean(),
		"allow_structures": boolean(), "allow_sources": boolean(), "allow_names": boolean(), "allow_dedupe": boolean(),
		"system_user_id": str(), "reviewer_user_id": str(), "paused": boolean(), "paused_reason": str(),
		"consecutive_failures": integer(), "sweep_detector": integer(), "updated_at": map[string]any{"type": "string", "format": "date-time"},
		"sweep_cursor_id": str(), "sweep_cursor_updated_at": map[string]any{"type": "string", "format": "date-time"},
		"last_sweep_at": map[string]any{"type": "string", "format": "date-time"},
		"last_run_at":   map[string]any{"type": "string", "format": "date-time"},
	})
	settingsProperties := schemas["CuratorSettings"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"system_user_id", "reviewer_user_id", "paused", "paused_reason", "consecutive_failures", "sweep_detector", "sweep_cursor_id", "sweep_cursor_updated_at", "last_sweep_at", "last_run_at", "updated_at"} {
		settingsProperties[field].(map[string]any)["readOnly"] = true
	}
	schemas["CuratorFinding"] = object(map[string]any{
		"review_pr_id": str(), "id": str(), "entity_id": str(), "entity_kind": str(), "entity_title": str(), "code": str(),
		"severity": str(), "priority": integer(), "signature": str(), "details": map[string]any{},
		"status": str(), "decision": str(), "reason": str(), "sources": map[string]any{}, "attempts": integer(),
		"next_attempt_at": map[string]any{"type": "string", "format": "date-time"}, "last_error": str(),
		"last_run_id": str(), "resolved_at": map[string]any{"type": "string", "format": "date-time"},
		"workspace_id": str(), "pull_request_id": str(), "merged": boolean(), "auto_merged": boolean(),
		"last_seen_at": map[string]any{"type": "string", "format": "date-time"},
		"created_at":   map[string]any{"type": "string", "format": "date-time"},
		"updated_at":   map[string]any{"type": "string", "format": "date-time"},
	})
	schemas["CuratorRun"] = object(map[string]any{
		"parent_run_id": str(), "role": str(), "messages": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/AssistantMessage"}}, "id": str(), "finding_id": str(), "entity_id": str(), "kind": str(), "trigger": str(), "status": str(),
		"model_id": str(), "decision": str(), "reason": str(), "sources": map[string]any{}, "tool_trace": map[string]any{},
		"detail": map[string]any{}, "workspace_id": str(), "pull_request_id": str(), "merged": boolean(),
		"auto_merged": boolean(), "error": str(), "input_tokens": integer(), "output_tokens": integer(),
		"credits": integer(), "started_at": map[string]any{"type": "string", "format": "date-time"},
		"finished_at": map[string]any{"type": "string", "format": "date-time"},
		"created_at":  map[string]any{"type": "string", "format": "date-time"},
	})
	schemas["CuratorSummary"] = object(map[string]any{
		"settings": ref("Settings"), "assistant_enabled": boolean(), "model": map[string]any{},
		"balance": map[string]any{}, "spent_credits": integer(), "remaining_credits": integer(),
		"counts":   map[string]any{"type": "object", "additionalProperties": integer()},
		"runs_24h": integer(), "lease_held": boolean(),
	})
	schemas["CuratorFindingPage"] = page("Finding")
	schemas["CuratorRunPage"] = page("Run")

	op := func(summary, id string, schema any, adminOnly bool) map[string]any {
		v := map[string]any{"summary": summary, "operationId": id, "responses": map[string]any{"200": map[string]any{"description": "OK"}}}
		if schema != nil {
			v["responses"].(map[string]any)["200"] = map[string]any{"description": "OK", "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
		}
		if adminOnly {
			v["description"] = "Requires assistant.manage for reads; mutating requests additionally require catalog.review.manage."
		}
		return v
	}
	paths["/curator/summary"] = map[string]any{"get": op("Read curator status, queue counts, budget and lease state", "curatorSummary", ref("Summary"), true)}
	paths["/curator/settings"] = map[string]any{
		"get": op("Read curator settings", "curatorGetSettings", ref("Settings"), true),
		"put": op("Update curator settings", "curatorSaveSettings", ref("Settings"), true),
	}
	paths["/curator/settings"].(map[string]any)["put"].(map[string]any)["requestBody"] = map[string]any{
		"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref("Settings")}},
	}
	findingsOp := op("List audit findings", "curatorListFindings", ref("FindingPage"), true)
	findingsOp["parameters"] = []any{
		map[string]any{"name": "status", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"open", "running", "done", "failed", "ignored", "resolved"}}},
		map[string]any{"name": "code", "in": "query", "schema": str()},
		map[string]any{"name": "kind", "in": "query", "schema": str()},
		map[string]any{"name": "limit", "in": "query", "schema": integer()},
		map[string]any{"name": "offset", "in": "query", "schema": integer()},
	}
	paths["/curator/findings"] = map[string]any{"get": findingsOp}
	runsOp := op("List agent and sweep runs", "curatorListRuns", ref("RunPage"), true)
	runsOp["parameters"] = []any{
		map[string]any{"name": "kind", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"manager", "agent", "quality", "sweep"}}},
		map[string]any{"name": "limit", "in": "query", "schema": integer()},
		map[string]any{"name": "offset", "in": "query", "schema": integer()},
	}
	runsOp["parameters"] = append(runsOp["parameters"].([]any), map[string]any{"name": "parent_run_id", "in": "query", "schema": str()})
	paths["/curator/runs"] = map[string]any{"get": runsOp}
	paths["/curator/runs/{id}"] = map[string]any{"get": withID(op("Read one run with its tool trace", "curatorGetRun", ref("Run"), true))}
	paths["/curator/runs/{id}/children"] = map[string]any{"get": withID(op("Read child agent conversations", "curatorChildren", map[string]any{"type": "array", "items": ref("Run")}, true))}
	taskOp := op("Queue a maintenance instruction without calling a model", "curatorTask", ref("Finding"), true)
	taskOp["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref("TaskInput")}}}
	paths["/curator/tasks"] = map[string]any{"post": taskOp}
	paths["/curator/pause"] = map[string]any{"post": op("Pause the background curator", "curatorPause", ref("Settings"), true)}
	paths["/curator/pause"].(map[string]any)["post"].(map[string]any)["requestBody"] = map[string]any{
		"content": map[string]any{"application/json": map[string]any{"schema": object(map[string]any{"reason": str()})}},
	}
	paths["/curator/resume"] = map[string]any{"post": op("Resume the background curator", "curatorResume", ref("Settings"), true)}
	paths["/curator/sweep"] = map[string]any{"post": op("Request an immediate deterministic sweep", "curatorSweep", object(map[string]any{"ok": boolean()}, "ok"), true)}
	paths["/curator/findings/{id}/reopen"] = map[string]any{"post": withID(op("Reopen a finding for another agent run", "curatorReopenFinding", ref("Finding"), true))}
	paths["/curator/findings/{id}/ignore"] = map[string]any{"post": withID(op("Ignore a finding until an administrator reopens it", "curatorIgnoreFinding", ref("Finding"), true))}
	aRef := func(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/Assistant" + name} }
	missionsOp := op("List autonomous curator scope missions", "curatorListMissions", aRef("MissionPage"), true)
	missionsOp["parameters"] = []any{
		map[string]any{"name": "status", "in": "query", "schema": map[string]any{"type": "string", "enum": []string{"running", "paused", "completed", "stopped", "failed"}}},
		map[string]any{"name": "limit", "in": "query", "schema": integer()},
		map[string]any{"name": "offset", "in": "query", "schema": integer()},
	}
	createMissionOp := op("Create and start an autonomous curator scope mission", "curatorCreateMission", aRef("MissionDetail"), true)
	createMissionOp["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": ref("MissionInput")}}}
	paths["/curator/missions"] = map[string]any{"get": missionsOp, "post": createMissionOp}
	paths["/curator/missions/{id}"] = map[string]any{"get": withID(op("Read curator mission progress, plan and Frontier tasks", "curatorGetMission", aRef("MissionDetail"), true))}
	paths["/curator/missions/{id}/tasks/{task_id}"] = map[string]any{"get": withTwoIDs(op("Read curator mission Worker trace", "curatorGetMissionTask", aRef("MissionTask"), true), "task_id")}
	paths["/curator/missions/{id}/planner-runs/{run_id}"] = map[string]any{"get": withTwoIDs(op("Read curator mission Planner round trace", "curatorGetMissionPlannerRun", aRef("MissionPlannerRun"), true), "run_id")}
	steerOp := withID(op("Steer curator mission or adjust budget", "curatorSteerMission", aRef("Mission"), true))
	steerOp["requestBody"] = map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": aRef("MissionSteerRequest")}}}
	paths["/curator/missions/{id}/steer"] = map[string]any{"post": steerOp}
	paths["/curator/missions/{id}/pause"] = map[string]any{"post": withID(op("Pause curator mission", "curatorPauseMission", aRef("Mission"), true))}
	paths["/curator/missions/{id}/resume"] = map[string]any{"post": withID(op("Resume curator mission", "curatorResumeMission", aRef("Mission"), true))}
	paths["/curator/missions/{id}/stop"] = map[string]any{"post": withID(op("Stop curator mission", "curatorStopMission", aRef("Mission"), true))}
}

func withID(operation map[string]any) map[string]any {
	params, _ := operation["parameters"].([]any)
	operation["parameters"] = append(params, map[string]any{
		"name": "id", "in": "path", "required": true,
		"schema": map[string]any{"type": "string", "format": "uuid"},
	})
	return operation
}

func withTwoIDs(operation map[string]any, second string) map[string]any {
	operation = withID(operation)
	params, _ := operation["parameters"].([]any)
	operation["parameters"] = append(params, map[string]any{
		"name": second, "in": "path", "required": true,
		"schema": map[string]any{"type": "string", "format": "uuid"},
	})
	return operation
}
