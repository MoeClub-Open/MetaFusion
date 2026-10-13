"use client";

import { searchInputLimitProps } from "@/lib/searchQuery";
// /compare 的「变更对比」模式。两条入口，同一套渲染：
//   1) 深链 ?revisions=<实体>:<版本>,<实体>:<版本> —— 实体历史页勾选两版进来，可跨实体；
//   2) 对比页自带的选取器 —— 直接选实体 + 任意两版看字段级 diff，不必先去历史页。
// 数据源只有 GET /catalog/entities/{id}/revisions 的快照（RevisionItem.snapshot），
// 选取器不再另发一次取版请求；渲染复用历史页签同一套 RevisionDiffInspector。

import { useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { api, Entity, title } from "./api";
import { RevisionDiffInspector, type DiffSnapshot } from "./RevisionDiffInspector";
import type { RevisionItem } from "./EntityRevisions";
import { ArrowLeftRight, Search, X } from "lucide-react";

interface RevRef {
  entityId: string;
  version: number;
}

function parseRefs(query: string): RevRef[] {
  return (query || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((part) => {
      const i = part.lastIndexOf(":");
      if (i <= 0) return null;
      const version = Number(part.slice(i + 1));
      if (!Number.isFinite(version)) return null;
      return { entityId: part.slice(0, i), version };
    })
    .filter((x): x is RevRef => !!x);
}

export function RevisionsCompare({ query }: { query: string }) {
  const refs = useMemo(() => parseRefs(query), [query]);
  // 跨实体深链没有"一个所属实体"可作为选取器锚点，只给结果；
  // 其余情况（0 / 1 个引用，或同实体的两版）交给选取器，改任一版即时重算。
  const crossEntity = refs.length === 2 && refs[0].entityId !== refs[1].entityId;
  if (crossEntity) return <CrossEntityDiff refs={refs} />;
  return <RevisionWorkbench refs={refs} />;
}

/** 跨实体两版：按深链原样取两个快照渲染，不提供选取器。 */
function CrossEntityDiff({ refs }: { refs: RevRef[] }) {
  const { t } = useI18n();
  const [snaps, setSnaps] = useState<DiffSnapshot[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    Promise.all(
      refs.map((r) =>
        api<{ items: RevisionItem[] }>(`/catalog/entities/${encodeURIComponent(r.entityId)}/revisions`).then(
          (res) => {
            const hit = (Array.isArray(res.items) ? res.items : []).find((v) => Number(v.version) === r.version);
            if (!hit) throw new Error("not_found:" + r.entityId + ":" + r.version);
            return hit as DiffSnapshot;
          },
        ),
      ),
    )
      .then((found) => {
        if (!cancelled) setSnaps(found);
      })
      .catch((e: unknown) => {
        if (cancelled) return;
        setSnaps([]);
        setError(e instanceof Error ? e.message : t("compare.revisions.notFound"));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [refs.map((r) => `${r.entityId}:${r.version}`).join(",")]);

  if (loading) return <p className="text-sm text-text-faint font-mono">{t("common.loading")}</p>;
  if (error || snaps.length !== 2) {
    return <p className="text-sm text-red-500 font-mono">{error || t("compare.revisions.notFound")}</p>;
  }
  return <RevisionDiffInspector base={snaps[0]} current={snaps[1]} />;
}

/** 单实体版本选取器 + diff：URL 始终反映当前两版，链接可直接分享。 */
function RevisionWorkbench({ refs }: { refs: RevRef[] }) {
  const { t, tr, locale } = useI18n();
  const searchInputRef = useRef<HTMLInputElement>(null);
  const [entityId, setEntityId] = useState(refs[0]?.entityId || "");
  const [entity, setEntity] = useState<Entity | null>(null);
  const [revisions, setRevisions] = useState<RevisionItem[]>([]);
  const [base, setBase] = useState<number>(refs.length === 2 ? refs[0].version : 0);
  const [current, setCurrent] = useState<number>(refs.length === 2 ? refs[1].version : 0);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<Entity[]>([]);
  const [searching, setSearching] = useState(false);

  // 按版本号取该版修订行；缺行即数据变了（版本被裁掉/未取到），不当 0 号位用。
  const byVersion = (v: number) => revisions.find((r) => Number(r.version) === v) || null;
  const baseSnap = byVersion(base);
  const currentSnap = byVersion(current);

  useEffect(() => {
    if (!entityId) {
      setEntity(null);
      setRevisions([]);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError("");
    (async () => {
      try {
        const [resolved, log] = await Promise.all([
          api<Entity>(`/catalog/entities/${encodeURIComponent(entityId)}/resolve`),
          api<{ items: RevisionItem[] }>(`/catalog/entities/${encodeURIComponent(entityId)}/revisions`),
        ]);
        if (cancelled) return;
        const list = (Array.isArray(log.items) ? log.items : []).slice().sort((a, b) => a.version - b.version);
        setEntity(resolved || null);
        setRevisions(list);
        // 深链没给版本号时取最近两版；只有一版时给不出差异，交给下面的提示。
        if (list.length >= 2) {
          setBase((v) => (list.some((r) => r.version === v) ? v : list[list.length - 2].version));
          setCurrent((v) => (list.some((r) => r.version === v) ? v : list[list.length - 1].version));
        }
      } catch (e: unknown) {
        if (cancelled) return;
        setEntity(null);
        setRevisions([]);
        setError(e instanceof Error ? e.message : t("compare.revisions.notFound"));
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [entityId]);

  // 搜索与直接粘 UUID 共用一个输入框：像 UUID 的输入回车即定位，不逼用户先认路径。
  useEffect(() => {
    const q = query.trim();
    if (!q) {
      setResults([]);
      return;
    }
    const timer = setTimeout(() => {
      setSearching(true);
      api<{ items: Entity[] }>(`/catalog/entities?q=${encodeURIComponent(q)}&limit=8`)
        .then((r) => setResults(Array.isArray(r.items) ? r.items : []))
        .catch(() => setResults([]))
        .finally(() => setSearching(false));
    }, 250);
    return () => clearTimeout(timer);
  }, [query]);

  const pickFromInput = () => {
    const q = query.trim();
    if (!q) return;
    if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(q)) {
      setEntityId(q);
      setQuery("");
      setResults([]);
      searchInputRef.current?.blur();
    }
  };

  // 选择结果写回 URL：同一链接能复现同一组版本，后退键在两版之间往返。
  useEffect(() => {
    if (typeof window === "undefined") return;
    if (!entityId || !base || !current || base === current) return;
    window.history.replaceState(null, "", `/compare?revisions=${entityId}:${base},${entityId}:${current}`);
  }, [entityId, base, current]);

  const onlyOneRevision = !!entity && revisions.length < 2 && !loading;

  return (
    <div className="space-y-5">
      <section className="bg-card border border-border rounded-card p-4 shadow-sm space-y-4">
        {!entityId && (
          <div>
            <h2 className="text-sm font-semibold text-foreground mb-2">{t("compare.revisions.pickEntity")}</h2>
            <div className="relative">
              <Search className="w-4 h-4 text-muted-foreground absolute left-3.5 top-1/2 -translate-y-1/2 pointer-events-none" />
              <input
                ref={searchInputRef}
                type="text"
                placeholder={t("catalog.compareSearchPlaceholder")}
                value={query}
                {...searchInputLimitProps(setQuery)}
                onKeyDown={(e) => e.key === "Enter" && pickFromInput()}
                className="w-full pl-10 pr-10 py-2.5 bg-background border border-border rounded-xl text-sm text-foreground placeholder:text-muted-foreground focus:outline-hidden focus:ring-2 focus:ring-primary/30 focus:border-primary transition-all duration-base ease-soft"
              />
              {query && (
                <button
                  type="button"
                  onClick={() => setQuery("")}
                  aria-label={t("catalog.clear")}
                  className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground p-1 cursor-pointer"
                >
                  <X className="w-3.5 h-3.5" />
                </button>
              )}
            </div>
            {searching && (
              <div className="flex items-center gap-2 text-xs text-muted-foreground mt-3">
                <span className="w-3.5 h-3.5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
                <span>{t("catalog.loading")}</span>
              </div>
            )}
            {results.length > 0 && (
              <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-2 mt-3">
                {results.map((r) => (
                  <button
                    key={r.id}
                    type="button"
                    onClick={() => {
                      setEntityId(r.id!);
                      setQuery("");
                      setResults([]);
                    }}
                    className="text-left rounded-xl p-3 border border-border bg-surface hover:border-primary/50 transition-colors duration-fast ease-soft cursor-pointer"
                  >
                    <span className="block text-xs font-semibold text-foreground truncate">{title(r, locale)}</span>
                    <span className="block text-[11px] text-muted-foreground truncate mt-0.5">
                      {r.kind ? tr(`catalog.kind.${r.kind}`, r.kind) : r.id}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </div>
        )}

        {entityId && (
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="min-w-0">
              <div className="flex items-center gap-2 min-w-0">
                <span className="text-sm font-semibold text-foreground truncate">{entity ? title(entity, locale) : entityId}</span>
                {entity?.kind && (
                  <span className="shrink-0 text-[11px] px-1.5 py-0.5 rounded bg-primary/10 text-primary font-mono">
                    {tr(`catalog.kind.${entity.kind}`, entity.kind)}
                  </span>
                )}
              </div>
              <p className="text-[11px] font-mono text-muted-foreground mt-1 break-all">{entityId}</p>
            </div>
            <button
              type="button"
              onClick={() => {
                setEntityId("");
                setEntity(null);
                setRevisions([]);
                setBase(0);
                setCurrent(0);
              }}
              className="px-3 py-1.5 rounded-lg border border-border text-xs text-muted-foreground hover:text-foreground hover:border-primary/40 transition-colors duration-fast ease-soft cursor-pointer shrink-0"
            >
              {t("compare.revisions.pickEntity")}
            </button>
          </div>
        )}

        {loading && (
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <span className="w-3.5 h-3.5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
            <span>{t("catalog.loading")}</span>
          </div>
        )}

        {error && <p className="text-sm text-red-500 font-mono m-0">{error}</p>}

        {entity && revisions.length >= 2 && (
          <div className="flex flex-wrap items-end gap-3 pt-1">
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              <span>{t("compare.revisions.base")}</span>
              <VersionSelect revisions={revisions} value={base} onChange={setBase} />
            </label>
            <button
              type="button"
              onClick={() => {
                setBase(current);
                setCurrent(base);
              }}
              title={t("compare.revisions.swap")}
              aria-label={t("compare.revisions.swap")}
              className="h-9 w-9 rounded-lg border border-border text-muted-foreground hover:text-primary hover:border-primary/40 inline-flex items-center justify-center transition-colors duration-fast ease-soft cursor-pointer shrink-0"
            >
              <ArrowLeftRight className="w-4 h-4" />
            </button>
            <label className="flex flex-col gap-1 text-xs text-muted-foreground">
              <span>{t("compare.revisions.current")}</span>
              <VersionSelect revisions={revisions} value={current} onChange={setCurrent} />
            </label>
          </div>
        )}

        {onlyOneRevision && (
          <p className="text-sm text-muted-foreground m-0">{t("compare.revisions.needTwo")}</p>
        )}
      </section>

      {baseSnap && currentSnap && base !== current && (
        <RevisionDiffInspector base={baseSnap} current={currentSnap} />
      )}
    </div>
  );
}

/** 版本下拉：v + 版本号是 locale 中立的，不占字典键；说明缺时退回编辑者，再缺退回日期。 */
function VersionSelect({
  revisions,
  value,
  onChange,
}: {
  revisions: RevisionItem[];
  value: number;
  onChange: (v: number) => void;
}) {
  const { locale } = useI18n();
  return (
    <select
      value={value || ""}
      onChange={(e) => onChange(Number(e.target.value))}
      className="h-9 px-2.5 rounded-lg bg-background border border-border text-sm text-foreground focus:outline-hidden focus:border-primary transition-colors duration-fast ease-soft cursor-pointer min-w-[180px]"
    >
      {!value && <option value="" disabled />}
      {revisions.map((r) => {
        const note = r.edit_note || r.actor_name || formatRevisionDate(r.created_at, locale);
        return (
          <option key={r.version} value={r.version}>
            {note ? `v${r.version} · ${note}` : `v${r.version}`}
          </option>
        );
      })}
    </select>
  );
}

function formatRevisionDate(iso: string, locale: string): string {
  if (!iso) return "";
  try {
    return new Date(iso).toLocaleDateString(locale);
  } catch {
    return iso.slice(0, 10);
  }
}
