import { ApiError } from "./api";
import { assistantRequest, type AssistantAccount, type AssistantMessage, type AssistantModel, type AssistantPage } from "./assistantApi";
import type { TranslateFn } from "@/i18n/I18nProvider";

export interface AssistantAccountResponse {
  balance: AssistantAccount;
  registration_credits: number;
  accepted_edit_credits: number;
  default_model_id: string;
}

export interface AssistantCommitReceipt {
  id: string; applied: boolean; review_status: string;
  items: { id: string; target: string; version: number }[];
}
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);

/** Only actual catalog_commit tool results become receipts; dry runs are not submissions. */
export function conversationReceipts(messages: AssistantMessage[]): AssistantCommitReceipt[] {
  const calls = new Set<string>();
  const receipts = new Map<string, AssistantCommitReceipt>();
  for (const message of messages) {
    if (message.role === "assistant") {
      for (const call of message.tool_calls || []) {
        if (call.function.name !== "catalog_commit") continue;
        try {
          const args: unknown = JSON.parse(call.function.arguments);
          if (record(args) && args.preview !== true) calls.add(call.id);
        } catch { /* An invalid tool request is not an applied edit. */ }
      }
    }
    if (message.role !== "tool" || !message.tool_call_id || !calls.has(message.tool_call_id)) continue;
    try {
      const value: unknown = JSON.parse(message.content);
      const receipt = record(value) && record(value.receipt) ? value.receipt : value;
      if (!record(receipt) || typeof receipt.id !== "string" || !UUID.test(receipt.id) || typeof receipt.applied !== "boolean" ||
        typeof receipt.review_status !== "string" || !Array.isArray(receipt.items)) continue;
      const items = receipt.items.flatMap((item) => record(item) && typeof item.id === "string" && UUID.test(item.id) &&
        typeof item.target === "string" && typeof item.version === "number" && Number.isSafeInteger(item.version)
        ? [{ id: item.id, target: item.target, version: item.version }] : []);
      receipts.set(receipt.id, { id: receipt.id, applied: receipt.applied, review_status: receipt.review_status, items });
    } catch { /* Tools may return non-receipt errors; their raw data is not displayed. */ }
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
