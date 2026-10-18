"use client";

import { useEffect, useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { assistantRequest, type AssistantContextPreview } from "@/lib/assistantApi";

export function AssistantContextMeter({ conversationID, modelID, message, revision, disabled }: {
  conversationID: string; modelID: string; message: string; revision: number; disabled: boolean;
}) {
  const { t, locale } = useI18n();
  const key = JSON.stringify([conversationID, modelID, message, revision]);
  const [state, setState] = useState<{ key: string; value?: AssistantContextPreview; failed?: boolean }>();
  useEffect(() => {
    if (!modelID || disabled) return;
    const controller = new AbortController();
    const timer = setTimeout(() => {
      assistantRequest<AssistantContextPreview>("/context", {
        method: "POST", signal: controller.signal,
        body: JSON.stringify({ model_id: modelID, ...(conversationID ? { conversation_id: conversationID } : {}), message: message.trim() }),
      }).then(value => { if (!controller.signal.aborted) setState({ key, value }); })
        .catch(() => { if (!controller.signal.aborted) setState({ key, failed: true }); });
    }, 500);
    return () => { clearTimeout(timer); controller.abort(); };
  }, [conversationID, modelID, message, revision, disabled, key]);
  if (!modelID) return null;
  const current = state?.key === key ? state : undefined;
  if (!current?.value) return <span className="text-xs text-text-faint">{t(current?.failed ? "assistantUI.contextUnavailable" : "assistantUI.contextLoading")}</span>;
  const value = current.value;
  const number = new Intl.NumberFormat(locale);
  const percent = Math.min(100, Math.round(value.input_tokens_bound / value.context_window * 100));
  const nearLimit = value.input_tokens_bound + value.max_output_tokens >= value.context_window;
  return <details className="group min-w-0 text-xs text-text-muted">
    <summary className={`flex cursor-pointer list-none flex-wrap items-center gap-2 ${nearLimit ? "text-warn" : ""}`}>
      <span>{t("assistantUI.context", { used: number.format(value.input_tokens_bound), limit: number.format(value.context_window) })}</span>
      <span aria-hidden className="h-1 w-12 overflow-hidden rounded-control bg-surfaceSubtle"><span className={`block h-full ${nearLimit ? "bg-warn" : "bg-primary"}`} style={{ width: `${percent}%` }} /></span>
    </summary>
    <p className="mt-2 max-w-xl text-[11px] leading-relaxed">{t("assistantUI.contextHint", { messages: number.format(value.message_count), tools: number.format(value.tool_count), output: number.format(value.max_output_tokens) })}</p>
  </details>;
}
