import { ApiError } from "./api";
import { assistantRequest, type AssistantAccount, type AssistantMessage, type AssistantModel, type AssistantPage } from "./assistantApi";
import type { TranslateFn } from "@/i18n/I18nProvider";

export interface AssistantAccountResponse {
  balance: AssistantAccount;
  registration_credits: number;
  accepted_edit_credits: number;
  default_model_id: string;
}

export interface AssistantPushReceipt {
  applied: boolean;
  workspace: { id: string; head_commit_id: string | null } | null;
  commits: { id: string; merged_at: string | null; changes: { id: string; target: string }[] }[];
  pull_request: { id: string; status: "open" | "merged" | "closed" } | null;
}
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);
export const pushReceiptKey = (receipt: AssistantPushReceipt) => `${receipt.workspace?.id || receipt.pull_request?.id || ""}:${receipt.workspace?.head_commit_id || receipt.commits.map((commit) => commit.id).join(",")}:${receipt.applied}`;

/** Only authoritative push/merge tool replies become outcomes, never staging or user text. */
export function conversationReceipts(messages: AssistantMessage[]): AssistantPushReceipt[] {
  const calls = new Set<string>();
  const receipts = new Map<string, AssistantPushReceipt>();
  for (const message of messages) {
    if (message.role === "assistant") {
      for (const call of message.tool_calls || []) {
        if (call.function.name === "catalog_workspace_push" || call.function.name === "catalog_pull_request_merge") calls.add(call.id);
      }
    }
    if (message.role !== "tool" || !message.tool_call_id || !calls.has(message.tool_call_id)) continue;
    try {
      const value: unknown = JSON.parse(message.content);
      const receipt = record(value) && record(value.receipt) ? value.receipt : value;
      if (!record(receipt) || typeof receipt.applied !== "boolean" || !Array.isArray(receipt.commits)) continue;
      const workspace = receipt.workspace;
      if (workspace !== null && (!record(workspace) || typeof workspace.id !== "string" || !UUID.test(workspace.id) ||
        !(workspace.head_commit_id === null || typeof workspace.head_commit_id === "string" && UUID.test(workspace.head_commit_id)))) continue;
      const commits = receipt.commits.flatMap((commit) => record(commit) && typeof commit.id === "string" && UUID.test(commit.id) &&
        (commit.merged_at === null || typeof commit.merged_at === "string" && Number.isFinite(Date.parse(commit.merged_at)))
        ? [{ id: commit.id, merged_at: commit.merged_at, changes: Array.isArray(commit.changes) ? commit.changes.flatMap((item) => record(item) &&
          typeof item.id === "string" && UUID.test(item.id) && (item.target === "entity" || item.target === "relation") ? [{ id: item.id, target: item.target }] : []) : [] }] : []);
      const pr = receipt.pull_request;
      if (pr !== null && (!record(pr) || typeof pr.id !== "string" || !UUID.test(pr.id) || !["open", "closed", "merged"].includes(String(pr.status)))) continue;
      const outcome: AssistantPushReceipt = {
        applied: receipt.applied, workspace: record(workspace) ? { id: workspace.id as string, head_commit_id: workspace.head_commit_id as string | null } : null, commits,
        pull_request: record(pr) ? { id: pr.id as string, status: pr.status as "open" | "merged" | "closed" } : null,
      };
      receipts.set(pushReceiptKey(outcome), outcome);
    } catch { /* A failed tool is not a successful push. */ }
  }
  return [...receipts.values()];
}

export function describeAssistantError(error: unknown, t: TranslateFn): string {
  if (error instanceof ApiError) {
    const key = ({
      assistant_insufficient_credits: "creditsLow",
      assistant_budget_exceeded: "budgetReached",
      assistant_disabled: "unavailable",
      assistant_encryption_unavailable: "unavailable",
      assistant_request_conflict: "requestConflict",
      assistant_context_limit: "contextLimit",
    } as Record<string, string>)[error.message];
    if (key) return t(`assistant.user.error.${key}`);
    if (error.status === 401) return t("assistant.user.loginRequired");
    if (error.status === 403) return t("assistant.user.error.forbidden");
    if (error.status === 429) return t("assistant.user.error.rateLimited");
  }
  return t("assistant.user.error.requestFailed");
}

/** Lists use the server's total; they never pretend the first page is complete. */
export async function assistantAllPages<T>(path: string): Promise<T[]> {
  const items: T[] = [];
  let offset = 0;
  for (;;) {
    const separator = path.includes("?") ? "&" : "?";
    const page = await assistantRequest<AssistantPage<T>>(`${path}${separator}limit=100&offset=${offset}`);
    const rows = page.items || [];
    items.push(...rows);
    offset += rows.length;
    if (offset >= page.total) return items;
    if (!rows.length) throw new Error("assistant_incomplete_page");
  }
}

export function modelCreditRate(model: AssistantModel, locale: string): string {
  const number = new Intl.NumberFormat(locale);
  return `${number.format(model.input_credits_per_million)} / ${number.format(model.output_credits_per_million)}`;
}
