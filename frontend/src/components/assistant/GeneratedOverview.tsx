"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { ChevronDown, Loader2, RefreshCw, Sparkles } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { ApiError } from "@/lib/api";
import { assistantRequest, type AssistantAccount, type AssistantCredential, type AssistantGeneratedOverview, type AssistantModel, type AssistantTokenUsage } from "@/lib/assistantApi";
import { assistantAllPages, describeAssistantError, type AssistantAccountResponse } from "@/lib/assistantUser";
import { parseGeneratedBlocks } from "@/lib/generatedBlocks";
import { AssistantModelPicker } from "./AssistantModelPicker";
import GeneratedBlocks from "./GeneratedBlocks";

interface OverviewResponse {
  item: AssistantGeneratedOverview | null; stale: boolean; usage?: AssistantTokenUsage; credits_spent?: number; balance?: AssistantAccount;
}
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** A cache read is free. A paid generation only starts from the explicit button. */
export function GeneratedOverview({ entityID, version }: { entityID: string; version?: number }) {
  const { t, locale } = useI18n();
  const { user } = useAuth();
  const [result, setResult] = useState<OverviewResponse | null>(null);
  const [reading, setReading] = useState(true);
  const [refresh, setRefresh] = useState(0);
  const [expanded, setExpanded] = useState(false);
  const [models, setModels] = useState<AssistantModel[]>([]);
  const [credentials, setCredentials] = useState<AssistantCredential[]>([]);
  const [account, setAccount] = useState<AssistantAccountResponse | null>(null);
  const [modelID, setModelID] = useState("");
  const [credentialID, setCredentialID] = useState("");
  const [optionsLoading, setOptionsLoading] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [error, setError] = useState("");
  const [failedUsage, setFailedUsage] = useState<{ usage: AssistantTokenUsage; credits_spent: number } | null>(null);
  const readKey = `${entityID}:${version || 0}:${locale}:${user?.id || ""}`;
  const currentKey = useRef(readKey);
  currentKey.current = readKey;
  const requestID = useRef<string | null>(null);
  const optionsKey = useRef("");

  useEffect(() => {
    setModels([]); setCredentials([]); setAccount(null); setModelID(""); setCredentialID("");
    optionsKey.current = "";
  }, [user?.id]);

  useEffect(() => {
    let alive = true;
    setReading(true); setResult(null); setExpanded(false); setError(""); setFailedUsage(null); setGenerating(false); setOptionsLoading(false);
    requestID.current = null;
    assistantRequest<OverviewResponse>(`/generated/${entityID}?locale=${encodeURIComponent(locale)}`)
      .then((next) => {
        if (!alive) return;
        setResult(next);
        if (next.item) void assistantAllPages<AssistantModel>("/models").then((nextModels) => { if (alive) setModels(nextModels); }).catch(() => {});
      })
      .catch((cause) => { if (alive) setError(describeAssistantError(cause, t)); })
      .finally(() => { if (alive) setReading(false); });
    return () => { alive = false; };
  }, [entityID, version, locale, user?.id, refresh, t]);

  const openOptions = async () => {
    setExpanded((previous) => !previous);
    if (!user || optionsKey.current === user.id) return;
    const key = readKey;
    setOptionsLoading(true); setError("");
    try {
      const [nextModels, nextCredentials, nextAccount] = await Promise.all([
        assistantAllPages<AssistantModel>("/models"), assistantAllPages<AssistantCredential>("/credentials"), assistantRequest<AssistantAccountResponse>("/account"),
      ]);
      if (currentKey.current !== key) return;
      setModels(nextModels); setCredentials(nextCredentials); setAccount(nextAccount);
      setModelID(nextModels.find((model) => model.id === nextAccount.default_model_id)?.id || nextModels[0]?.id || ""); setCredentialID(""); optionsKey.current = user.id;
    } catch (cause) { if (currentKey.current === key) setError(describeAssistantError(cause, t)); }
    finally { if (currentKey.current === key) setOptionsLoading(false); }
  };

  const generate = async () => {
    if (generating || !modelID) return;
    const key = readKey;
    requestID.current ||= crypto.randomUUID();
    setGenerating(true); setError(""); setFailedUsage(null);
    try {
      const next = await assistantRequest<OverviewResponse>(`/generated/${entityID}`, { method: "POST", body: JSON.stringify({ model_id: modelID, ...(credentialID ? { credential_id: credentialID } : {}), locale, regenerate: !!result?.item, client_request_id: requestID.current }) });
      if (currentKey.current !== key) return;
      setResult(next); setExpanded(false); requestID.current = null;
      if (next.balance?.user_id === user?.id) setAccount((previous) => previous ? { ...previous, balance: next.balance! } : previous);
    } catch (cause) {
      if (currentKey.current === key) {
        setError(describeAssistantError(cause, t));
        if (cause instanceof ApiError && cause.details && typeof cause.details === "object") {
          const detail = cause.details as { usage?: AssistantTokenUsage; credits_spent?: number; balance?: AssistantAccount };
          if (detail.usage && typeof detail.usage.input_tokens === "number" && typeof detail.usage.output_tokens === "number") {
            setFailedUsage({ usage: detail.usage, credits_spent: detail.credits_spent || 0 });
          }
          if (detail.balance?.user_id === user?.id) setAccount((previous) => previous ? { ...previous, balance: detail.balance! } : previous);
        }
      }
    }
    finally { if (currentKey.current === key) setGenerating(false); }
  };

  const item = result?.item;
  const usage = result?.usage || item?.usage;
  const blocks = parseGeneratedBlocks(item?.blocks);
  const sources = (item?.sources || []).filter((source) => source && typeof source.entity_id === "string" && UUID.test(source.entity_id) && typeof source.title === "string");
  const number = new Intl.NumberFormat(locale);
  return <section className="border-t border-line-subtle pt-4 space-y-3" aria-label={t("assistant.generated.title")}>
    <div className="flex items-start justify-between gap-3 flex-wrap">
      <div className="min-w-0"><h3 className="flex items-center gap-1.5 text-sm font-semibold text-text-strong"><Sparkles className="w-4 h-4 text-primary" />{t("assistant.generated.title")}{item && <span className="text-[10px] font-normal text-text-muted">{t("assistant.generated.cached")}</span>}{result?.stale && <span className="text-[10px] font-normal text-warn">{t("assistant.generated.stale")}</span>}</h3>
        <p className="text-[11px] text-text-muted mt-1">{t("assistant.generated.hint")}</p>
      </div>
      <button type="button" disabled={generating || reading} onClick={() => void openOptions()} aria-expanded={expanded} className="inline-flex items-center gap-1.5 rounded-control border border-line px-2.5 py-1.5 text-xs text-text-body hover:bg-surfaceHover disabled:opacity-50"><span>{t(item ? "assistant.generated.update" : "assistant.generated.choose")}</span><ChevronDown className={`w-3 h-3 ${expanded ? "rotate-180" : ""}`} /></button>
    </div>
    {reading && <p role="status" className="text-xs text-text-muted">{t("assistant.generated.loading")}</p>}
    {error && <div role="alert" className="flex items-center gap-2 text-xs text-danger"><span>{error}</span>{!generating && <button type="button" onClick={() => setRefresh((previous) => previous + 1)} aria-label={t("assistant.user.refresh")} className="p-1"><RefreshCw className="w-3.5 h-3.5" /></button>}</div>}
    {failedUsage && <p className="text-[11px] text-text-muted">{t("assistant.user.consumed", { input: number.format(failedUsage.usage.input_tokens), output: number.format(failedUsage.usage.output_tokens), credits: number.format(failedUsage.credits_spent) })}</p>}
    {expanded && <div className="rounded-card border border-line-subtle bg-surfaceSubtle p-3 space-y-3">
      {!user ? <Link href={`/login?redirect=${encodeURIComponent(`/catalog/${entityID}#overview`)}`} className="text-xs text-primary hover:underline">{t("assistant.generated.login")}</Link> : <>
        {optionsLoading ? <p role="status" className="text-xs text-text-muted">{t("common.loading")}</p> : <AssistantModelPicker models={models} credentials={credentials} modelID={modelID} credentialID={credentialID} onModel={(id) => { setModelID(id); requestID.current = null; }} onCredential={(id) => { setCredentialID(id); requestID.current = null; }} disabled={generating} />}
        <div className="flex flex-wrap items-center justify-between gap-3"><p className="text-[11px] text-text-muted">{account ? t("assistant.user.balance", { credits: number.format(account.balance.available_credits) }) : t("assistant.generated.paidHint")}</p>
          <button type="button" onClick={() => void generate()} disabled={generating || optionsLoading || !modelID} className="inline-flex items-center gap-1.5 rounded-control bg-primary px-3 py-2 text-xs font-semibold text-white disabled:opacity-40">{generating ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Sparkles className="w-3.5 h-3.5" />}{t(generating ? "assistant.generated.generating" : "assistant.generated.generate")}</button></div>
        <p className="text-[10px] text-text-muted">{t("assistant.generated.paidHint")}</p>
      </>}
    </div>}
    {!!blocks.length && <GeneratedBlocks blocks={blocks} chartLabel={t("assistant.generated.chart")} tableLabel={t("assistant.generated.table")} timelineLabel={t("assistant.generated.timeline")} />}
    {item?.partial && <p className="text-xs text-warn">{t("assistant.generated.partial")}</p>}
    {item && !blocks.length && <p className="text-xs text-text-muted">{t("assistant.generated.invalid")}</p>}
    {item && <footer className="space-y-2 text-[10px] text-text-muted">
      <p>{t("assistant.generated.metadata", { model: models.find((model) => model.id === item.model_id)?.name || item.model_id, date: new Date(item.updated_at || item.created_at).toLocaleString(locale), version: item.entity_version })}</p>
      {usage && <p>{result?.credits_spent === undefined
        ? t("assistant.generated.tokenUsage", { input: number.format(usage.input_tokens), output: number.format(usage.output_tokens) })
        : t("assistant.user.consumed", { input: number.format(usage.input_tokens), output: number.format(usage.output_tokens), credits: number.format(result.credits_spent) })}</p>}
      {!!sources.length && <div className="max-h-24 overflow-y-auto flex flex-wrap items-center gap-x-3 gap-y-1"><span>{t("assistant.generated.sources")}</span>{sources.map((source) => <Link key={source.entity_id} href={`/catalog/${source.entity_id}`} className="text-primary hover:underline">{source.title}</Link>)}</div>}
    </footer>}
  </section>;
}
