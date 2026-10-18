"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { ArrowUpRight, Copy } from "lucide-react";
import { Modal } from "@/components/ui/Modal";
import { useI18n } from "@/i18n/I18nProvider";
import { activityChanges, activityTargetHref, activityTargetName } from "@/lib/activity";
import type { AuditLogEntry } from "@/lib/api/audit";
import { fetchApi } from "@/lib/api/client";
import { copyText } from "@/lib/clipboard";
import { isoTimestamp } from "@/lib/datetime";

export function ActivityResult({ result }: { result?: string }) {
  const { t } = useI18n();
  const color = result === "success" ? "bg-success/10 text-success" : result === "failure" ? "bg-danger/10 text-danger" : "bg-surfaceSubtle text-text-muted";
  return <span className={`inline-flex rounded-chip px-1.5 py-0.5 text-xs ${color}`}>{t(result === "success" ? "settings.activity.resultSuccess" : result === "failure" ? "settings.activity.resultFailure" : "settings.activity.unknownResult")}</span>;
}

const NAME_ENDPOINTS: Record<string, string> = {
  entity: "/catalog/entities/", workspace: "/catalog/workspaces/",
  commit: "/catalog/commits/", pull_request: "/catalog/pull-requests/",
};

export function ActivityDetail({ entry, onClose }: { entry: AuditLogEntry; onClose: () => void }) {
  const { t, tr } = useI18n();
  const capturedName = activityTargetName(entry);
  const href = activityTargetHref(entry);
  const [currentName, setCurrentName] = useState("");
  const [unavailable, setUnavailable] = useState(false);
  const [copyState, setCopyState] = useState("");
  const changes = activityChanges(entry.changes);
  const hasBefore = changes.some((change) => change.hasBefore);
  const targetLabel = tr(`settings.activity.target.${entry.target_type}`, entry.target_type || t("settings.activity.unknownTarget"));

  useEffect(() => {
    setCurrentName("");
    setUnavailable(false);
    setCopyState("");
    // 旧行只补读当前可见对象，不回写或冒充操作当时的标题。
    const type = entry.target_type ?? "";
    const endpoint = Object.hasOwn(NAME_ENDPOINTS, type) ? NAME_ENDPOINTS[type] : undefined;
    if (capturedName || !href || !endpoint) return;
    const controller = new AbortController();
    fetchApi<Record<string, unknown>>(endpoint + entry.target_id, { signal: controller.signal }).then((object) => {
      if (controller.signal.aborted) return;
      const name = [object.title, object.name, object.message].find((value) => typeof value === "string" && value.trim());
      if (typeof name === "string") setCurrentName(name);
    }).catch(() => {
      if (!controller.signal.aborted) setUnavailable(true);
    });
    return () => controller.abort();
  }, [entry, capturedName, href]);

  function valueText(value: unknown): string {
    if (value === null) return t("settings.activity.nullValue");
    if (value === undefined) return "—";
    if (value === true) return t("settings.activity.yes");
    if (value === false) return t("settings.activity.no");
    if (Array.isArray(value)) return value.length ? value.map(valueText).join(" · ") : t("settings.activity.emptyValue");
    if (typeof value === "object") return Object.entries(value).map(([key, item]) => `${tr(`settings.activity.field.${key}`, key)}: ${valueText(item)}`).join("; ") || t("settings.activity.emptyValue");
    if (value === "[redacted]") return t("settings.activity.hidden");
    return typeof value === "string" ? tr(`settings.activity.value.${value}`, value || t("settings.activity.emptyValue")) : String(value);
  }

  const technical: [string, unknown][] = [
    [t("settings.activity.actionCode"), entry.action],
    [t("settings.activity.colTarget"), [entry.target_type, entry.target_id].filter(Boolean).join(": ")],
    [t("settings.activity.actor"), [entry.actor_username, entry.actor_user_id].filter(Boolean).join(" · ")],
    [t("settings.activity.colCredential"), entry.credential_type && tr(`settings.activity.credential.${entry.credential_type}`, entry.credential_type)],
    [t("settings.activity.colIp"), entry.actor_ip], [t("settings.activity.userAgent"), entry.actor_user_agent],
    [t("settings.activity.method"), entry.request_method], [t("settings.activity.httpStatus"), entry.http_status],
    [t("settings.activity.route"), entry.route], [t("settings.activity.colError"), entry.error_code],
  ];

  return <Modal open onClose={onClose} maxWidth="max-w-3xl" title={t("settings.activity.details")}>
    <div className="space-y-5 text-sm text-text-body">
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-2"><h4 className="font-semibold text-text-strong">{tr(`settings.activity.action.${entry.action}`, t("settings.activity.unknownAction"))}</h4><ActivityResult result={entry.result} /></div>
        <p className="break-words text-base text-text-strong">{capturedName || currentName || targetLabel}</p>
        {currentName && <p className="text-xs text-text-muted">{t("settings.activity.currentTargetName")}</p>}
        {unavailable && <p className="text-xs text-text-muted">{t("settings.activity.targetUnavailable")}</p>}
        <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-text-muted">
          <span>{tr(`settings.activity.service.${entry.service}`, entry.service)}</span>
          <time dateTime={entry.occurred_at}>{isoTimestamp(entry.occurred_at)}</time>
        </p>
        {href && <Link href={href} className="inline-flex items-center gap-1 text-primary hover:underline">{t("settings.activity.openTarget")}<ArrowUpRight className="size-3.5" /></Link>}
      </div>

      <section className="space-y-2">
        <h4 className="font-medium text-text-strong">{t("settings.activity.changes")}</h4>
        {changes.length ? <div className="overflow-hidden rounded-control border border-line-subtle">
          {changes.map((change) => <div key={change.field} className="grid gap-1 border-b border-line-subtle p-3 last:border-b-0 sm:grid-cols-[minmax(7rem,1fr)_minmax(0,3fr)] sm:gap-3">
            <div className="break-words text-xs text-text-muted">{tr(`settings.activity.field.${change.field}`, change.field)}</div>
            {hasBefore ? <div className="grid min-w-0 gap-2 sm:grid-cols-2">
              <div className="min-w-0 rounded-control bg-danger/5 p-2"><span className="mb-1 block text-xs text-text-muted">{t("settings.activity.before")}</span><p className="whitespace-pre-wrap break-words">{change.hasBefore ? valueText(change.before) : "—"}</p></div>
              <div className="min-w-0 rounded-control bg-success/5 p-2"><span className="mb-1 block text-xs text-text-muted">{t("settings.activity.after")}</span><p className="whitespace-pre-wrap break-words">{change.hasAfter ? valueText(change.after) : "—"}</p></div>
            </div> : <p className="min-w-0 whitespace-pre-wrap break-words">{change.hasAfter ? valueText(change.after) : "—"}</p>}
          </div>)}
        </div> : <p className="text-sm text-text-muted">{t("settings.activity.noSummary")}</p>}
        {entry.changes?._truncated === true && <p className="text-xs text-text-muted">{t("settings.activity.truncated")}</p>}
      </section>

      <details className="border-t border-line-subtle pt-3">
        <summary className="w-fit cursor-pointer text-text-muted">{t("settings.activity.technicalDetails")}</summary>
        <dl className="mt-3 space-y-3 text-xs">
          {technical.filter(([, value]) => value !== undefined && value !== "").map(([key, value]) => <div key={key} className="grid gap-1 sm:grid-cols-[8rem_minmax(0,1fr)]"><dt className="text-text-muted">{key}</dt><dd className="whitespace-pre-wrap break-all font-mono">{String(value)}</dd></div>)}
          {entry.request_id && <div className="grid gap-1 sm:grid-cols-[8rem_minmax(0,1fr)]">
            <dt className="text-text-muted">{t("settings.activity.requestIdLabel")}</dt>
            <dd className="min-w-0"><span className="break-all font-mono">{entry.request_id}</span><button type="button" className="ml-2 inline-flex items-center gap-1 rounded-control border border-line px-2 py-1 text-text-muted hover:text-primary" onClick={async () => setCopyState(t(await copyText(entry.request_id!) ? "settings.activity.copied" : "settings.activity.copyFailed"))}><Copy className="size-3" />{t("settings.activity.copyRequestId")}</button><span role="status" className="ml-2">{copyState}</span></dd>
          </div>}
        </dl>
      </details>
      <details className="border-t border-line-subtle pt-3">
        <summary className="w-fit cursor-pointer text-text-muted">{t("settings.activity.raw")}</summary>
        <pre className="mt-3 max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-control bg-surfaceSubtle p-3 text-xs">{JSON.stringify(entry, null, 2)}</pre>
      </details>
    </div>
  </Modal>;
}
