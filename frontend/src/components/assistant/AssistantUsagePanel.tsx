"use client";

import { useCallback, useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { assistantRequest, type AssistantModel, type AssistantPage, type AssistantUsage } from "@/lib/assistantApi";
import { describeAssistantError } from "@/lib/assistantUser";
import { Card } from "@/components/ui/Card";

const PAGE_SIZE = 20;

export function AssistantUsagePanel({ models }: { models: AssistantModel[] }) {
  const { t, locale } = useI18n();
  const [page, setPage] = useState(0);
  const [result, setResult] = useState<AssistantPage<AssistantUsage> | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const load = useCallback(async () => {
    setLoading(true); setError("");
    try { setResult(await assistantRequest(`/usage?limit=${PAGE_SIZE}&offset=${page * PAGE_SIZE}`)); }
    catch (cause) { setError(describeAssistantError(cause, t)); }
    finally { setLoading(false); }
  }, [page, t]);
  useEffect(() => { void load(); }, [load]);
  const number = new Intl.NumberFormat(locale);
  return <Card className="space-y-3">
    <div className="flex items-center justify-between gap-2"><h2 className="text-sm font-semibold">{t("assistant.user.usage")}</h2><button type="button" onClick={() => void load()} disabled={loading} aria-label={t("assistant.user.refresh")} className="rounded-control p-2 hover:bg-surfaceHover"><RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} /></button></div>
    {error && <p role="alert" className="text-xs text-danger">{error}</p>}
    {loading && <p role="status" className="text-xs text-text-muted">{t("common.loading")}</p>}
    {!loading && result?.items.length === 0 && <p className="text-xs text-text-muted">{t("assistant.user.noUsage")}</p>}
    {!!result?.items.length && <div className="overflow-x-auto"><table className="w-full text-xs text-left">
      <thead className="text-text-muted"><tr>{["time", "model", "tokenInput", "tokenOutput", "cachedTokens", "creditsSpent", "status"].map((key) => <th key={key} scope="col" className="px-2 py-2 font-medium whitespace-nowrap">{t(`assistant.user.${key}`)}</th>)}</tr></thead>
      <tbody className="divide-y divide-line-subtle">{result.items.map((item) => <tr key={item.id}>
        <td className="p-2 whitespace-nowrap">{new Date(item.created_at).toLocaleString(locale)}</td><td className="p-2">{models.find((model) => model.id === item.model_id)?.name || item.price_snapshot?.name || item.model_id}<span className="block text-[10px] text-text-muted">{item.byo_credential_id ? t("assistant.user.byo") : t(`assistant.user.purpose.${item.purpose}`)}</span></td>
        <td className="p-2 tabular-nums">{number.format(item.usage?.input_tokens || 0)}{item.usage_estimated && <span className="block text-[10px] text-text-muted">{t("assistant.user.estimated")}</span>}</td><td className="p-2 tabular-nums">{number.format(item.usage?.output_tokens || 0)}</td><td className="p-2 tabular-nums">{number.format(item.usage?.cached_input_tokens || 0)}</td><td className="p-2 tabular-nums">{number.format(item.credits || 0)}</td><td className="p-2">{t(`assistant.user.usageStatus.${item.status}`)}</td>
      </tr>)}</tbody>
    </table></div>}
    {result && result.total > PAGE_SIZE && <div className="flex items-center justify-end gap-3 text-xs"><button type="button" disabled={loading || page === 0} onClick={() => setPage(page - 1)} className="disabled:opacity-40">{t("pagination.prev")}</button><span>{t("common.pagination", { page: page + 1, total: Math.ceil(result.total / PAGE_SIZE) })}</span><button type="button" disabled={loading || (page + 1) * PAGE_SIZE >= result.total} onClick={() => setPage(page + 1)} className="disabled:opacity-40">{t("pagination.next")}</button></div>}
  </Card>;
}
