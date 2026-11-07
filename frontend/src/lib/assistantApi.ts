import { fetchApi } from "./api";

/** Assistant requests share the site's Cookie session refresh and locale headers. */
export function assistantRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  return fetchApi<T>(`/assistant${path}`, options);
}

export interface AssistantPage<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
}

export const ASSISTANT_PROTOCOLS = ["openai_compatible", "openai_responses", "anthropic_messages", "gemini_generate_content"] as const;
export type AssistantProtocol = typeof ASSISTANT_PROTOCOLS[number];

export interface AssistantProvider {
  id: string; name: string; base_url: string; protocol: AssistantProtocol; enabled: boolean;
  has_key: boolean; key_hint: string; monthly_budget_microusd: number;
  created_at: string; updated_at: string;
}

export interface SearchProvider {
  id: string; name: string; kind: "duckduckgo" | "deepseek"; base_url: string;
  enabled: boolean; is_default: boolean; has_key: boolean; key_hint: string;
  credits_per_request: number; updated_at: string;
}

export interface SearchRequestRecord {
  id: string; user_id: string; provider_id: string; byo_provider_id?: string; purpose: string; operation: string;
  status: string; credits: number; upstream_cost_microcny: number; error: string; created_at: string;
}

export interface AssistantModel {
  id: string; provider_id: string; upstream_model: string; name: string; enabled: boolean;
  input_price_microusd_per_million: number; cached_input_price_microusd_per_million: number;
  output_price_microusd_per_million: number; input_credits_per_million: number;
  output_credits_per_million: number; max_output_tokens: number; context_window: number;
  max_tokens_parameter: "max_tokens" | "max_completion_tokens";
  created_at: string; updated_at: string;
}

export interface AssistantSettings {
  enabled: boolean; default_model_id: string; registration_credits: number;
  accepted_edit_credits: number; reward_effective_at: string; monthly_budget_microusd: number;
  max_tool_rounds: number; updated_at: string;
}

export interface AssistantBYOProvider {
  id: string; owner_id: string; name: string; base_url: string; protocol: AssistantProtocol; enabled: boolean;
  has_key: boolean; key_hint: string; created_at: string; updated_at: string;
}

export interface AssistantBYOModel {
  id: string; owner_id: string; provider_id: string; upstream_model: string; name: string; enabled: boolean;
  max_output_tokens: number; max_tokens_parameter: "max_tokens" | "max_completion_tokens"; context_window: number;
  created_at: string; updated_at: string;
}

export interface AssistantAccount {
  user_id: string; credits: number; reserved_credits: number; available_credits: number; updated_at: string;
}

export interface AssistantCreditEntry {
  id: string; user_id: string; kind: string; reference_id: string; credits: number; note: string; created_at: string;
}

export interface AssistantTokenUsage {
  input_tokens: number; output_tokens: number; cached_input_tokens: number;
}

export interface AssistantUsage {
  id: string; user_id: string; provider_id: string; model_id: string; byo_provider_id?: string;
  purpose: string; entity_id?: string; status: string; error?: string; usage: AssistantTokenUsage;
  usage_estimated: boolean; credits: number; cost_microusd: number; price_snapshot: AssistantModel; created_at: string; settled_at?: string;
}

export interface AssistantBudgetUsage {
  scope: string; month: string; spent_microusd: number; reserved_microusd: number;
}

export interface AssistantMessage {
  role: string; content: string; tool_call_id?: string;
  tool_calls?: { id: string; type: string; function: { name: string; arguments: string } }[];
  provider_context?: { binding: string; output: unknown };
}

export interface AssistantConversation {
  id: string; owner_id: string; title: string; model_id: string; active_request_id?: string;
  messages: AssistantMessage[]; created_at: string; updated_at: string;
}

export interface AssistantContextPreview {
  input_tokens_bound: number; context_window: number; max_output_tokens: number;
  message_count: number; tool_count: number; estimated: boolean;
}

export interface AssistantRequestRecord {
  id: string; owner_id: string; conversation_id?: string; status: "pending" | "completed" | "failed";
  created_at: string; updated_at: string; response?: unknown;
}

export interface GeneratedContent {
  id: string; entity_id: string; locale: string; context_hash: string; model_id: string;
  owner_id: string; content: unknown; dependencies: unknown; created_at: string; updated_at: string;
}

/** Blocks remain unknown until the bounded renderer validates their shape. */
export interface AssistantGeneratedOverview {
  id: string; entity_id: string; locale: string; model_id: string; owner_id: string;
  context_hash: string; created_at: string; updated_at: string; entity_version: number;
  definitions_etag: string; blocks: unknown;
  sources: { entity_id: string; title: string; url: string; version: number }[];
  partial: boolean; partial_reasons?: string[]; usage: AssistantTokenUsage;
}

export interface MissionPlanItem {
  id: string;
  content: string;
  status: "pending" | "in_progress" | "completed" | "cancelled";
}

export type MissionStatus = "running" | "paused" | "completed" | "stopped" | "failed";
export type MissionTaskRole = "recon" | "research" | "edit" | "review";
export type MissionTaskStatus = "queued" | "running" | "completed" | "failed" | "skipped";

export interface Mission {
  id: string;
  owner_id: string;
  scope: "user" | "curator";
  title: string;
  goal: string;
  steering?: string;
  pending_steer?: boolean;
  status: MissionStatus;
  model_id: string;
  worker_model_id?: string;
  search_provider_id?: string;
  search_byo_provider_id?: string;
  prefer_pull_request: boolean;
  budget_credits: number;
  spent_credits: number;
  max_workers: number;
  max_tasks: number;
  planner_rounds: number;
  max_planner_rounds: number;
  plan: MissionPlanItem[];
  summary?: string;
  error?: string;
  created_at: string;
  updated_at: string;
  finished_at?: string;
}

export interface MissionTask {
  id: string;
  mission_id: string;
  seq: number;
  role: MissionTaskRole;
  title: string;
  objective: string;
  depends_on: string[];
  anchor_entity_ids: string[];
  status: MissionTaskStatus;
  priority: number;
  result_summary?: string;
  discoveries: string[];
  sources: string[];
  workspace_id?: string;
  pull_request_id?: string;
  merged: boolean;
  model_id?: string;
  input_tokens: number;
  output_tokens: number;
  credits: number;
  messages?: AssistantMessage[];
  error?: string;
  started_at?: string;
  finished_at?: string;
  created_at: string;
  updated_at: string;
}

export interface MissionPlannerRun {
  id: string;
  mission_id: string;
  round: number;
  trigger: string;
  status: "running" | "completed" | "failed";
  summary?: string;
  model_id?: string;
  input_tokens: number;
  output_tokens: number;
  credits: number;
  messages?: AssistantMessage[];
  error?: string;
  started_at: string;
  finished_at?: string;
}

export interface MissionDetail {
  mission: Mission;
  tasks: MissionTask[];
  planner_runs: MissionPlannerRun[];
  balance?: AssistantAccount;
}
