"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Calendar, Clock, GitCompare, History, ExternalLink, X } from "lucide-react";
import { Card } from "@/components/ui/Card";
import { SectionTitle } from "@/components/ui/SectionTitle";
import { Select } from "@/components/ui/Select";
import {
  TemporalAsOfSection,
  TemporalCompareSection,
  eventAnchorDate,
} from "@/components/entity/EntityTemporalSnapshot";
import type { Entity } from "@/components/catalog/api";
import { title as entityTitle } from "@/components/catalog/api";
import {
  getTermName,
  resolveLocalizedName,
  type DynamicDefinitions,
} from "@/lib/definitions";
import { useI18n } from "@/i18n/I18nProvider";
import { localizeCatalogError } from "@/lib/catalogErrors";
import {
  fetchEntityAsOf,
  fetchEntityTemporalCompare,
  fetchEntityTimeline,
  type EntityAsOfResponse,
  type EntityTemporalCompareResponse,
  type RelationshipEntitySummary,
  type TimelineEvent,
} from "@/lib/api/temporal";

type TemporalMode = "timeline" | "as_of" | "compare";

function splitTokens(raw?: string | null): string[] {
  if (!raw) return [];
  return raw
    .split(/[,/、;|]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

export function EntityTemporalWorkspace({
  entity,
  releases = [],
  defs,
  blurAdultImages = false,
  activeAsOf,
  onAsOfChange,
  onEventCountChange,
}: {
  entity: Entity;
  releases?: Entity[];
  defs: DynamicDefinitions | null | undefined;
  blurAdultImages?: boolean;
  activeAsOf?: EntityAsOfResponse | null;
  onAsOfChange?: (snapshot: EntityAsOfResponse | null) => void;
  onEventCountChange?: (count: number) => void;
}) {
  const { t, locale } = useI18n();
  const entityId = entity.id || "";

  const [mode, setMode] = useState<TemporalMode>("timeline");
  const [regionFilter, setRegionFilter] = useState("");
  const [platformFilter, setPlatformFilter] = useState("");
  const [releaseFilter, setReleaseFilter] = useState("");
  const [typeFilter, setTypeFilter] = useState("");
  const [rangeFrom, setRangeFrom] = useState("");
  const [rangeTo, setRangeTo] = useState("");

  const [timelineItems, setTimelineItems] = useState<TimelineEvent[]>([]);
  const [timelineEntities, setTimelineEntities] = useState<Record<string, RelationshipEntitySummary>>({});
  const [timelineLoading, setTimelineLoading] = useState(true);
  const [timelineError, setTimelineError] = useState("");

  const [asOfInput, setAsOfInput] = useState(activeAsOf?.at || "");
  const [asOfData, setAsOfData] = useState<EntityAsOfResponse | null>(activeAsOf || null);
  const [asOfLoading, setAsOfLoading] = useState(false);
  const [asOfError, setAsOfError] = useState("");

  const [compareFrom, setCompareFrom] = useState("");
  const [compareTo, setCompareTo] = useState("");
  const [compareData, setCompareData] = useState<EntityTemporalCompareResponse | null>(null);
  const [compareLoading, setCompareLoading] = useState(false);
  const [compareError, setCompareError] = useState("");

  useEffect(() => {
    if (activeAsOf === null) {
      setAsOfData(null);
    } else if (activeAsOf) {
      setAsOfData(activeAsOf);
      setAsOfInput(activeAsOf.at);
    }
  }, [activeAsOf]);

  const loadTimeline = useCallback(() => {
    if (!entityId) return () => {};
    const ctrl = new AbortController();
    setTimelineLoading(true);
    setTimelineError("");
    fetchEntityTimeline(
      entityId,
      {
        from: rangeFrom,
        to: rangeTo,
        type: typeFilter,
        region: regionFilter,
        platform: platformFilter,
        release_id: releaseFilter,
      },
      ctrl.signal,
    )
      .then((res) => {
        const items = Array.isArray(res.items) ? res.items : [];
        setTimelineItems(items);
        setTimelineEntities(res.entities || {});
        if (!rangeFrom && !rangeTo && !typeFilter && !regionFilter && !platformFilter && !releaseFilter) {
          onEventCountChange?.(items.length);
        }
      })
      .catch((err) => {
        if (ctrl.signal.aborted) return;
        setTimelineError(localizeCatalogError((err as Error)?.message || "load_failed", t));
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setTimelineLoading(false);
      });
    return () => ctrl.abort();
  }, [entityId, rangeFrom, rangeTo, typeFilter, regionFilter, platformFilter, releaseFilter, onEventCountChange, t]);

  useEffect(() => {
    return loadTimeline();
  }, [loadTimeline, entity.version]);

  const discoveredRegions = useMemo(() => {
    const set = new Set<string>();
    for (const r of releases) {
      for (const tok of splitTokens(String(r.attributes?.country || r.attributes?.region || ""))) {
        set.add(tok);
      }
    }
    for (const ev of timelineItems) {
      for (const tok of splitTokens(ev.scope?.region)) set.add(tok);
    }
    for (const p of entity.pictures || []) {
      for (const tok of splitTokens(p.region)) set.add(tok);
    }
    return Array.from(set).sort();
  }, [releases, timelineItems, entity.pictures]);

  const discoveredPlatforms = useMemo(() => {
    const set = new Set<string>();
    for (const r of releases) {
      for (const tok of splitTokens(String(r.attributes?.platform || ""))) set.add(tok);
    }
    for (const ev of timelineItems) {
      for (const tok of splitTokens(ev.scope?.platform)) set.add(tok);
    }
    for (const p of entity.pictures || []) {
      for (const tok of splitTokens(p.platform)) set.add(tok);
    }
    return Array.from(set).sort();
  }, [releases, timelineItems, entity.pictures]);

  const eventTypeOptions = useMemo(() => {
    const vocab = defs?.vocabularies?.event_type?.terms || defs?.vocabularies?.embedded_event_kind?.terms || {};
    return Object.entries(vocab)
      .filter(([, term]) => term?.enabled !== false)
      .map(([code, term]) => ({
        code,
        label: resolveLocalizedName(term.names, locale, code),
      }));
  }, [defs, locale]);

  const quickDates = useMemo(() => {
    const seen = new Set<string>();
    const out: { date: string; label: string }[] = [];
    for (const ev of timelineItems) {
      const d = eventAnchorDate(ev);
      if (!d || seen.has(d)) continue;
      seen.add(d);
      const evTitle = resolveLocalizedName(ev.title_names, locale, ev.title);
      out.push({ date: d, label: evTitle ? d + " · " + evTitle : d });
      if (out.length >= 10) break;
    }
    return out;
  }, [timelineItems, locale]);

  const runAsOf = useCallback(
    async (targetDate: string) => {
      const trimmed = targetDate.trim();
      if (!trimmed || !entityId) return;
      setAsOfInput(trimmed);
      setMode("as_of");
      setAsOfLoading(true);
      setAsOfError("");
      try {
        const snap = await fetchEntityAsOf(entityId, {
          at: trimmed,
          region: regionFilter,
          platform: platformFilter,
          release_id: releaseFilter,
        });
        setAsOfData(snap);
        onAsOfChange?.(snap);
      } catch (err) {
        setAsOfError(localizeCatalogError((err as Error)?.message || "load_failed", t));
      } finally {
        setAsOfLoading(false);
      }
    },
    [entityId, regionFilter, platformFilter, releaseFilter, onAsOfChange, t],
  );

  useEffect(() => {
    if (mode === "as_of" && asOfData?.at) {
      void runAsOf(asOfData.at);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [regionFilter, platformFilter, releaseFilter]);

  const runCompare = useCallback(
    async (fromVal: string, toVal: string) => {
      const f = fromVal.trim();
      const to = toVal.trim();
      if (!f || !to || !entityId) return;
      setMode("compare");
      setCompareLoading(true);
      setCompareError("");
      try {
        const res = await fetchEntityTemporalCompare(entityId, {
          from: f,
          to,
          region: regionFilter,
          platform: platformFilter,
          release_id: releaseFilter,
        });
        setCompareData(res);
      } catch (err) {
        setCompareError(localizeCatalogError((err as Error)?.message || "load_failed", t));
      } finally {
        setCompareLoading(false);
      }
    },
    [entityId, regionFilter, platformFilter, releaseFilter, t],
  );

  const eventTypeLabel = (code: string) => {
    const fromEvent = getTermName(defs, "event_type", code, locale);
    if (fromEvent !== code) return fromEvent;
    return getTermName(defs, "embedded_event_kind", code, locale);
  };

  const hasScopeFilters = Boolean(regionFilter || platformFilter || releaseFilter || typeFilter || rangeFrom || rangeTo);

  return (
    <Card id="timeline" padding="section" className="space-y-4 shadow-soft">
      <SectionTitle
        icon={<Clock className="w-4 h-4 text-primary" strokeWidth={1.5} />}
        actions={
          <div className="flex flex-wrap items-center gap-1 rounded-lg bg-surfaceSubtle border border-line-subtle p-0.5 text-xs font-mono">
            {(
              [
                { id: "timeline", label: t("temporal.modeTimeline"), icon: Calendar },
                { id: "as_of", label: t("temporal.modeAsOf"), icon: History },
                { id: "compare", label: t("temporal.modeCompare"), icon: GitCompare },
              ] as const
            ).map((item) => {
              const Icon = item.icon;
              const active = mode === item.id;
              return (
                <button
                  key={item.id}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setMode(item.id)}
                  className={
                    "inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md transition-colors cursor-pointer " +
                    (active
                      ? "bg-surface text-primary font-semibold shadow-2xs"
                      : "text-text-muted hover:text-text-strong")
                  }
                >
                  <Icon className="w-3.5 h-3.5" strokeWidth={1.6} />
                  <span>{item.label}</span>
                </button>
              );
            })}
          </div>
        }
      >
        {t("temporal.sectionTitle")}
      </SectionTitle>

      {/* 统一范围筛选栏：地区 / 平台 / 关联发行版 / 事件类型 */}
      <div className="flex flex-wrap items-center gap-2 rounded-lg border border-line-subtle bg-surfaceSubtle p-2.5 text-xs">
        {entity.kind === "work" && releases.length > 0 && (
          <div className="inline-flex items-center gap-1.5 text-text-muted">
            <span className="font-mono text-[11px]">{t("temporal.filterRelease")}</span>
            <Select
              fullWidth={false}
              aria-label={t("temporal.filterRelease")}
              value={releaseFilter}
              onChange={setReleaseFilter}
              options={[
                { value: "", label: t("common.all") },
                ...releases.filter((r) => r.id).map((r) => ({ value: r.id!, label: entityTitle(r, locale) })),
              ]}
              className="!h-8 min-w-[7rem] !px-2.5 !text-xs !bg-surface"
            />
          </div>
        )}

        <label className="inline-flex items-center gap-1.5 text-text-muted">
          <span className="font-mono text-[11px]">{t("temporal.filterRegion")}</span>
          <input
            list={entityId + "-regions"}
            value={regionFilter}
            onChange={(e) => setRegionFilter(e.target.value)}
            placeholder="JP / CN / GLOBAL"
            className="h-8 w-28 px-2 rounded-md bg-surface border border-line text-xs text-text-strong font-mono placeholder:text-text-faint"
          />
          <datalist id={entityId + "-regions"}>
            {discoveredRegions.map((reg) => (
              <option key={reg} value={reg} />
            ))}
          </datalist>
        </label>

        <label className="inline-flex items-center gap-1.5 text-text-muted">
          <span className="font-mono text-[11px]">{t("temporal.filterPlatform")}</span>
          <input
            list={entityId + "-platforms"}
            value={platformFilter}
            onChange={(e) => setPlatformFilter(e.target.value)}
            placeholder="ios / android / pc"
            className="h-8 w-28 px-2 rounded-md bg-surface border border-line text-xs text-text-strong font-mono placeholder:text-text-faint"
          />
          <datalist id={entityId + "-platforms"}>
            {discoveredPlatforms.map((plat) => (
              <option key={plat} value={plat} />
            ))}
          </datalist>
        </label>

        {mode === "timeline" && (
          <>
            <div className="inline-flex items-center gap-1.5 text-text-muted">
              <span className="font-mono text-[11px]">{t("temporal.filterEventType")}</span>
              <Select
                fullWidth={false}
                aria-label={t("temporal.filterEventType")}
                value={typeFilter}
                onChange={setTypeFilter}
                options={[
                  { value: "", label: t("common.all") },
                  ...eventTypeOptions.map((opt) => ({ value: opt.code, label: opt.label })),
                ]}
                className="!h-8 min-w-[7rem] !px-2.5 !text-xs !bg-surface"
              />
            </div>
            <label className="inline-flex items-center gap-1 text-text-muted">
              <span className="font-mono text-[11px]">{t("temporal.rangeFrom")}</span>
              <input
                value={rangeFrom}
                onChange={(e) => setRangeFrom(e.target.value)}
                placeholder="YYYY-MM-DD"
                className="h-8 w-28 px-2 rounded-md bg-surface border border-line text-xs text-text-strong font-mono placeholder:text-text-faint"
              />
            </label>
            <label className="inline-flex items-center gap-1 text-text-muted">
              <span className="font-mono text-[11px]">{t("temporal.rangeTo")}</span>
              <input
                value={rangeTo}
                onChange={(e) => setRangeTo(e.target.value)}
                placeholder="YYYY-MM-DD"
                className="h-8 w-28 px-2 rounded-md bg-surface border border-line text-xs text-text-strong font-mono placeholder:text-text-faint"
              />
            </label>
          </>
        )}

        {hasScopeFilters && (
          <button
            type="button"
            onClick={() => {
              setRegionFilter("");
              setPlatformFilter("");
              setReleaseFilter("");
              setTypeFilter("");
              setRangeFrom("");
              setRangeTo("");
            }}
            className="inline-flex items-center gap-1 h-8 px-2 rounded-md text-xs text-text-muted hover:text-primary cursor-pointer"
          >
            <X className="w-3.5 h-3.5" />
            <span>{t("catalog.clearFilters")}</span>
          </button>
        )}
      </div>

      {/* 模式一：统一事件时间线 */}
      {mode === "timeline" && (
        <div className="space-y-3">
          {timelineLoading ? (
            <div className="space-y-2" aria-busy="true">
              <div className="h-14 rounded-lg bg-emphasis/[0.04] animate-pulse" />
              <div className="h-14 rounded-lg bg-emphasis/[0.04] animate-pulse" />
            </div>
          ) : timelineError ? (
            <div role="alert" className="rounded-lg border border-warn/30 bg-warn/10 p-3 text-xs text-warn flex items-center justify-between gap-2">
              <span>{timelineError}</span>
              <button type="button" onClick={() => loadTimeline()} className="text-primary hover:underline cursor-pointer">
                {t("catalog.retry")}
              </button>
            </div>
          ) : timelineItems.length === 0 ? (
            <Card tone="subtle" padding="card" className="text-center text-xs text-text-faint">
              {t("temporal.noEvents")}
            </Card>
          ) : (
            <ol className="relative border-l border-line pl-4 ml-2 space-y-3">
              {timelineItems.map((ev) => {
                const anchor = eventAnchorDate(ev);
                const evTitle = resolveLocalizedName(ev.title_names, locale, ev.title) || eventTypeLabel(ev.event_type);
                const relSummary = ev.scope?.release_id ? timelineEntities[ev.scope.release_id] : undefined;
                const peerTargets = (ev.target_ids || []).filter(
                  (tid) => tid !== entityId && tid !== ev.event_entity_id && tid !== ev.scope?.release_id,
                );
                return (
                  <li key={ev.id} className="relative">
                    <span className="absolute -left-[21px] top-2 w-2.5 h-2.5 rounded-full bg-primary ring-4 ring-surface" aria-hidden />
                    <Card tone="subtle" padding="card" className="space-y-2">
                      <div className="flex flex-wrap items-start justify-between gap-2">
                        <div className="space-y-1 min-w-0">
                          <div className="flex flex-wrap items-center gap-1.5 font-mono text-[11px]">
                            <span className="font-semibold text-primary">
                              {ev.occurred_at || ev.begin_date || t("temporal.dateUndated")}
                              {ev.end_date && ev.end_date !== (ev.occurred_at || ev.begin_date) ? " — " + ev.end_date : ""}
                            </span>
                            {ev.effective_at && ev.effective_at !== ev.occurred_at && (
                              <span className="text-text-muted">
                                ({t("temporal.effectiveAtShort", { date: ev.effective_at })})
                              </span>
                            )}
                            <span className="px-1.5 py-0.5 rounded bg-primary/10 text-primary text-[10px] font-semibold">
                              {eventTypeLabel(ev.event_type)}
                            </span>
                            {ev.scope?.region && (
                              <span className="px-1.5 py-0.5 rounded bg-emphasis/[0.06] text-text-body text-[10px]">
                                {ev.scope.region}
                              </span>
                            )}
                            {ev.scope?.platform && (
                              <span className="px-1.5 py-0.5 rounded bg-emphasis/[0.06] text-text-body text-[10px]">
                                {ev.scope.platform}
                              </span>
                            )}
                            {ev.scope?.channel && (
                              <span className="px-1.5 py-0.5 rounded bg-emphasis/[0.06] text-text-muted text-[10px]">
                                {ev.scope.channel}
                              </span>
                            )}
                            {ev.date_precision && ev.date_precision !== "day" && (
                              <span className="px-1.5 py-0.5 rounded bg-warn/15 text-warn text-[10px]">
                                {getTermName(defs, "date_precision", ev.date_precision, locale)}
                              </span>
                            )}
                          </div>
                          <div className="text-sm font-semibold text-text-strong break-words">
                            {ev.event_entity_id ? (
                              <Link href={"/catalog/" + ev.event_entity_id} className="hover:text-primary hover:underline">
                                {evTitle}
                              </Link>
                            ) : (
                              evTitle
                            )}
                          </div>
                        </div>

                        {anchor && (
                          <div className="flex flex-wrap items-center gap-1 shrink-0">
                            <button
                              type="button"
                              onClick={() => void runAsOf(anchor)}
                              className="px-2 py-1 rounded-md border border-primary/30 bg-primary/10 hover:bg-primary/20 text-primary font-mono text-[11px] font-medium cursor-pointer"
                            >
                              {t("temporal.jumpAsOf")}
                            </button>
                            <button
                              type="button"
                              onClick={() => {
                                setCompareFrom(anchor);
                                if (compareTo) void runCompare(anchor, compareTo);
                                else setMode("compare");
                              }}
                              className="px-2 py-1 rounded-md border border-line bg-surface hover:bg-surfaceHover text-text-muted hover:text-text-strong font-mono text-[10px] cursor-pointer"
                            >
                              {t("temporal.setCompareFrom")}
                            </button>
                            <button
                              type="button"
                              onClick={() => {
                                setCompareTo(anchor);
                                if (compareFrom) void runCompare(compareFrom, anchor);
                                else setMode("compare");
                              }}
                              className="px-2 py-1 rounded-md border border-line bg-surface hover:bg-surfaceHover text-text-muted hover:text-text-strong font-mono text-[10px] cursor-pointer"
                            >
                              {t("temporal.setCompareTo")}
                            </button>
                          </div>
                        )}
                      </div>

                      {(relSummary || peerTargets.length > 0 || (ev.source_refs && ev.source_refs.length > 0)) && (
                        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 pt-1 border-t border-line-subtle text-xs text-text-muted">
                          {relSummary && (
                            <Link href={"/catalog/" + relSummary.id} className="text-primary hover:underline font-medium">
                              {relSummary.title}
                            </Link>
                          )}
                          {peerTargets.map((tid) => {
                            const peer = timelineEntities[tid];
                            return (
                              <Link key={tid} href={"/catalog/" + tid} className="hover:text-primary hover:underline">
                                {peer?.title || tid.slice(0, 8)}
                              </Link>
                            );
                          })}
                          {(ev.source_refs || []).map((src, si) =>
                            src.url ? (
                              <a
                                key={si}
                                href={src.url}
                                target="_blank"
                                rel="noreferrer"
                                className="inline-flex items-center gap-1 font-mono text-[11px] text-text-faint hover:text-primary"
                              >
                                <ExternalLink className="w-3 h-3" />
                                <span>{src.citation || t("catalog.pictureSourceLink")}</span>
                              </a>
                            ) : null,
                          )}
                        </div>
                      )}
                    </Card>
                  </li>
                );
              })}
            </ol>
          )}
        </div>
      )}

      {/* 模式二：按时间点重建历史状态 */}
      {mode === "as_of" && (
        <TemporalAsOfSection
          entityId={entityId}
          defs={defs}
          blurAdultImages={blurAdultImages}
          asOfInput={asOfInput}
          onAsOfInputChange={setAsOfInput}
          asOfData={asOfData}
          asOfLoading={asOfLoading}
          asOfError={asOfError}
          quickDates={quickDates}
          onRunAsOf={(d) => void runAsOf(d)}
          onClearAsOf={() => {
            setAsOfData(null);
            onAsOfChange?.(null);
          }}
        />
      )}

      {/* 模式三：两日期对比 */}
      {mode === "compare" && (
        <TemporalCompareSection
          entityId={entityId}
          defs={defs}
          blurAdultImages={blurAdultImages}
          compareFrom={compareFrom}
          compareTo={compareTo}
          onCompareFromChange={setCompareFrom}
          onCompareToChange={setCompareTo}
          compareData={compareData}
          compareLoading={compareLoading}
          compareError={compareError}
          quickDates={quickDates}
          onRunCompare={(f, to) => void runCompare(f, to)}
          onJumpAsOf={(d) => void runAsOf(d)}
        />
      )}
    </Card>
  );
}
