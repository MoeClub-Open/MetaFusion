"use client";

import { useEffect, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import { useCatalog } from "@/components/catalog/CatalogProvider";
import { fetchApi } from "@/lib/api";
import { can, CATALOG_LIFECYCLE_MANAGE } from "@/lib/permissions";

type Counts = { pending: number | null; active: number | null; tombstones: number | null; external: number | null };
const EMPTY: Counts = { pending: null, active: null, tombstones: null, external: null };
const count = (value: unknown) => typeof value === "number" && Number.isFinite(value) ? value : null;

export function CatalogOverview() {
  const { user } = useAuth();
  const { t, tr, locale } = useI18n();
  const { modules, refresh } = useCatalog();
  const manage = can(user, CATALOG_LIFECYCLE_MANAGE);
  const [counts, setCounts] = useState(EMPTY);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setFailed(false);
    setCounts(EMPTY);
    void Promise.allSettled([
      manage ? fetchApi<{ states: Record<string, number>; pending_pull_requests: number }>("/catalog/entities/stats") : Promise.resolve(null),
      fetchApi<{ items: unknown[] }>("/catalog/external-databases"),
    ]).then(([entities, external]) => {
      if (!alive) return;
      const next = { ...EMPTY };
      let invalid = entities.status === "rejected" || external.status === "rejected";
      if (entities.status === "fulfilled" && entities.value) {
        next.active = count(entities.value.states?.active);
        next.pending = count(entities.value.pending_pull_requests);
        const deleted = count(entities.value.states?.deleted);
        const merged = count(entities.value.states?.merged);
        next.tombstones = deleted !== null && merged !== null ? deleted + merged : null;
        invalid ||= [next.active, next.pending, next.tombstones].includes(null);
      }
      if (external.status === "fulfilled" && Array.isArray(external.value.items)) next.external = external.value.items.length;
      else invalid = true;
      setCounts(next);
      setFailed(invalid);
      setLoading(false);
    });
    return () => { alive = false; };
  }, [manage, user?.id, nonce]);

  const items = [
    ...(manage ? [
      { label: "admin.console.pendingReviews", value: counts.pending },
      { label: "admin.console.activeEntities", value: counts.active },
      { label: "admin.console.tombstones", value: counts.tombstones },
    ] : []),
    { label: "admin.console.extDatabases", value: counts.external },
  ];
  return <div className="space-y-3">
    <div className="flex items-center justify-between gap-3">
      <h2 className="min-w-0 text-base font-semibold text-text-strong">{t("admin.console.sysOverview")}</h2>
      <button type="button" disabled={loading} onClick={() => { setNonce(value => value + 1); void refresh(); }} className="shrink-0 whitespace-nowrap inline-flex items-center gap-2 rounded-control border border-line px-3 py-2 text-xs disabled:opacity-50">
        <RefreshCw className={`size-4 shrink-0 ${loading ? "animate-spin" : ""}`} />{t("common.refresh")}
      </button>
    </div>
    {failed && <p role="alert" className="text-xs text-danger">{t("admin.console.overviewFailed")}</p>}
    <dl aria-busy={loading} className="grid grid-cols-2 gap-3 border-y border-line-subtle py-3 sm:grid-cols-4">
      {items.map(item => <div key={item.label}>
        <dt className="text-xs text-text-muted">{t(item.label)}</dt>
        <dd className="mt-1 text-xl font-semibold text-text-strong">{item.value === null ? "—" : item.value.toLocaleString(locale)}</dd>
      </div>)}
    </dl>
    <section aria-labelledby="catalog-services-title">
      <h3 id="catalog-services-title" className="text-sm font-semibold text-text-strong">{t("admin.console.modulesTitle")}</h3>
      <dl className="mt-3 divide-y divide-line-subtle">
        {modules.map(module => <div key={module.id} className="flex items-center justify-between gap-3 py-2.5 text-sm">
          <dt>{tr(`admin.capability.${module.id}`, module.id)}</dt>
          <dd className={module.enabled ? "text-success" : "text-text-muted"}>{t(module.enabled ? "admin.console.deployed" : "admin.console.notDeployed")}</dd>
        </div>)}
      </dl>
    </section>
  </div>;
}
