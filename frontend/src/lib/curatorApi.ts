import { fetchApi } from "./api";
import type { AssistantModel, AssistantMessage } from "./assistantApi";

export function curatorRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  return fetchApi<T>(`/curator${path}`, options);
}

export interface CuratorSettings {
  execution: { child_model_id: string; billing_owner_id: string; search_provider_id: string; search_byo_provider_id: string; max_children: number; allow_reviews: boolean; auto_merge_reviews: boolean };
  enabled: boolean;
  model_id: string;
  interval_seconds: number;
  max_runs_per_hour: number;
  monthly_credit_allowance: number;
  auto_merge_low_risk: boolean;
  allow_structures: boolean;
  allow_sources: boolean;
  allow_names: boolean;
  allow_dedupe: boolean;
  system_user_id: string;
  reviewer_user_id: string;
  paused: boolean;
  paused_reason: string;
  consecutive_failures: number;
  sweep_detector: number;
  updated_at: string;
}

export interface CuratorFinding {
  review_pr_id?: string;
  id: string;
  entity_id: string;
  entity_kind: string;
  entity_title?: string;
  code: string;
  severity: "error" | "warning" | "info";
  priority: number;
  signature: string;
  details: Record<string, unknown>;
  status: "open" | "running" | "done" | "failed" | "ignored" | "resolved";
  decision: string;
  reason: string;
  sources: string[];
  attempts: number;
  next_attempt_at: string;
  last_error: string;
  last_run_id?: string;
  workspace_id?: string;
  pull_request_id?: string;
  merged: boolean;
  auto_merged: boolean;
  last_seen_at: string;
  resolved_at?: string;
  created_at: string;
  updated_at: string;
}

export interface CuratorRun {
  parent_run_id?: string; role: string; messages?: AssistantMessage[];
  id: string;
  finding_id?: string;
  entity_id?: string;
  kind: "manager" | "agent" | "sweep" | "quality";
  trigger: "schedule" | "manual" | "sweep";
  status: "running" | "completed" | "failed" | "skipped";
  model_id?: string;
  decision: string;
  reason: string;
  sources: string[];
  tool_trace: unknown[];
  detail: Record<string, unknown>;
  workspace_id?: string;
  pull_request_id?: string;
  merged: boolean;
  auto_merged: boolean;
  error: string;
  input_tokens: number;
  output_tokens: number;
  credits: number;
  started_at: string;
  finished_at?: string;
  created_at: string;
}

export interface CuratorBalance {
  user_id: string;
  credits: number;
  reserved_credits: number;
  available_credits: number;
  updated_at: string;
}

export interface CuratorSummary {
  settings: CuratorSettings;
  assistant_enabled: boolean;
  model?: AssistantModel;
  balance: CuratorBalance;
  spent_credits: number;
  remaining_credits: number;
  counts: Record<string, number>;
  runs_24h: number;
  lease_held: boolean;
}

export interface CuratorPage<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
}
