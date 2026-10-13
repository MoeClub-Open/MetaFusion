"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import type { Entity } from "./api";
import { fetchApi } from "@/lib/api";
import { useI18n } from "@/i18n/I18nProvider";
import { getKindName, getRelationName, useDefinitions } from "@/lib/definitions";
import { componentEntries, type RelationRow } from "@/components/work/WorkContentDirectory";

type RelationData = { items: RelationRow[]; entities: Record<string, Entity> };

/** Any of the eight entity kinds can participate in a declared aggregate relation. */
export function AggregateRelationList({ entityId, onCount }: { entityId: string; onCount?: (n: number) => void }) {
  const { t, tr, locale } = useI18n();
  const { definitions, kinds } = useDefinitions();
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [data, setData] = useState<RelationData>({ items: [], entities: {} });
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!entityId) return;
    let active = true;
    setState("loading");
    fetchApi<RelationData>(`/catalog/entities/${encodeURIComponent(entityId)}/relations`)
      .then((response) => {
        if (!active) return;
        setData(response);
        setState("ready");
      })
      .catch(() => { if (active) setState("error"); });
    return () => { active = false; };
  }, [entityId, attempt]);

  const groups = useMemo(() => {
    const entries = componentEntries(
      data.items || [], data.entities || {}, entityId, locale,
      (code) => definitions?.relations?.[code]?.aggregate === true,
    );
    const out: { key: string; label: string; entries: typeof entries.includes }[] = [];
    for (const [direction, rows] of [[true, entries.includes], [false, entries.includedIn]] as const) {
      const byCode = new Map<string, typeof rows>();
      for (const row of rows) {
        const code = row.relationCode || "";
        byCode.set(code, [...(byCode.get(code) || []), row]);
      }
      for (const [code, grouped] of Array.from(byCode)) {
        out.push({ key: `${direction}:${code}`, label: getRelationName(definitions, code, direction, locale), entries: grouped });
      }
    }
    return out;
  }, [data, entityId, locale, definitions]);

  const count = groups.reduce((n, g) => n + g.entries.length, 0);
  useEffect(() => {
    if (state === "ready") onCount?.(count);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state, count]);

  if (state === "loading" || (state === "ready" && groups.length === 0)) return null;
  if (state === "error") return (
    <div role="alert" className="rounded-lg border border-line p-4 text-sm text-text-muted">
      {t("catalog.listFailed")} <button type="button" className="text-primary hover:underline" onClick={() => setAttempt((n) => n + 1)}>{t("catalog.retry")}</button>
    </div>
  );
  return (
    <section className="rounded-lg border border-line bg-surface p-4 space-y-4">
      <h2 className="font-display text-base font-bold text-text-strong">{t("entity.page.navContents")}</h2>
      {groups.map((group) => (
        <div key={group.key} className="space-y-2">
          <h3 className="text-sm font-semibold text-text-body">{group.label}</h3>
          <ul className="grid gap-2 sm:grid-cols-2">
            {group.entries.map((entry) => (
              <li key={`${group.key}:${entry.id}`} className="rounded border border-line-subtle px-3 py-2 text-sm">
                <Link href={`/catalog/${entry.id}`} className="text-text-strong hover:text-primary">{entry.title}</Link>
                <span className="ml-2 text-xs text-text-faint">{getKindName(kinds, entry.kind, locale, tr(`catalog.kind.${entry.kind}`, entry.kind))}</span>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </section>
  );
}
