"use client";

import { searchInputLimitProps } from "@/lib/searchQuery";
import React, { useEffect, useMemo, useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { fetchApi } from "@/lib/api";
import { Entity, fetchAllPages, mapLimit, title as entityTitle } from "@/components/catalog/api";
import { useDefinitions, getTermName } from "@/lib/definitions";
import { useI18n } from "@/i18n/I18nProvider";
import { ArrowLeft, Search, ChevronLeft, ChevronRight, ArrowUpRight, X } from "lucide-react";
import { PageShell } from "@/components/ui/PageShell";

// vocabLabel：枚举字段值按 definitions 声明的词表本地化（不写死字段码清单），
// 缺词表命中时原样返回。用于版本类别/批次/包装等所有枚举属性列。
function vocabLabel(definitions: any, code: string, value: unknown, locale: string): string {
  const v = String(value ?? "").trim();
  if (!v) return "";
  const vocab = definitions?.fields?.[code]?.vocabulary;
  if (!vocab) return v;
  const label = getTermName(definitions, vocab, v, locale);
  return label || v;
}

export default function WorkReleasesPage() {
  const params = useParams();
  const workId = params.id as string;
  const { t, locale } = useI18n();
  const { definitions } = useDefinitions();
  const [work, setWork] = useState<Entity | null>(null);
  const [entities, setEntities] = useState<Entity[]>([]);
  // 每个发行版的介质格式计数（按实际 Medium 聚合），供筛选与规格列展示。
  const [formatCounts, setFormatCounts] = useState<Record<string, Record<string, number>>>({});
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const pageSize = 20;
  const [q, setQ] = useState("");
  const [qInput, setQInput] = useState("");
  const [editionFilter, setEditionFilter] = useState("");
  const [batchFilter, setBatchFilter] = useState("");
  const [formatFilter, setFormatFilter] = useState("");
  const [countryFilter, setCountryFilter] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!workId) return;
    fetchApi<Entity>(`/catalog/entities/${workId}`).then(setWork).catch(()=>{});
  }, [workId]);

  const load = async (keyword: string) => {
    setLoading(true);
    try {
      const fetched = await fetchAllPages<Entity>(`/catalog/entities?kind=release&work_id=${encodeURIComponent(workId)}`);
      let items = fetched;
      if (keyword.trim()) {
        const kw = keyword.trim().toLowerCase();
        items = items.filter((e) => (e.title || "").toLowerCase().includes(kw) || JSON.stringify(e.attributes || {}).toLowerCase().includes(kw));
      }
      // 介质格式按实际 Medium 全量聚合（并发受控），不再截断首屏/首格式。
      const counts = await mapLimit(items, 8, async (e) => {
        try {
          const ms = await fetchAllPages<Entity>(`/catalog/entities?kind=medium&release_id=${encodeURIComponent(e.id!)}`);
          const c: Record<string, number> = {};
          for (const m of ms) {
            const f = String(m.attributes?.format || "").trim();
            if (f) c[f] = (c[f] || 0) + 1;
          }
          return c;
        } catch { return {}; }
      });
      const fmtMap: Record<string, Record<string, number>> = {};
      items.forEach((e, i) => { fmtMap[e.id!] = counts[i] || {}; });
      setEntities(items);
      setFormatCounts(fmtMap);
      setTotal(items.length);
    } catch (e) { console.error(e); } finally { setLoading(false); }
  };

  useEffect(() => { if (!workId) return; load(q); }, [workId, q]);

  const onSearch = (e: React.FormEvent) => { e.preventDefault(); setPage(1); setQ(qInput); };
  // format 候选来自实际 Medium 聚合：多介质发行版（CD＋BD）可被任一组成格式筛中。
  const formatCodesOf = (e: Entity): string[] => Object.keys(formatCounts[e.id!] || {});
  const formatSummaryOf = (e: Entity): string => {
    const entries = Object.entries(formatCounts[e.id!] || {});
    if (entries.length === 0) return "";
    const joiner = locale.startsWith("zh") ? "＋" : " + ";
    return entries
      .map(([code, n]) => {
        const label = getTermName(definitions, "format", code, locale);
        return `${label !== code ? label : code}×${n}`;
      })
      .join(joiner);
  };
  const filtered = useMemo(() => entities.filter((e) => {
    const edition = String(e.attributes?.edition_type || "").trim();
    const country = String(e.attributes?.country || "").trim();
    if (editionFilter && edition !== editionFilter) return false;
    if (batchFilter && String(e.attributes?.edition_batch || "").trim() !== batchFilter) return false;
    if (formatFilter && !formatCodesOf(e).includes(formatFilter)) return false;
    if (countryFilter && country !== countryFilter) return false;
    return true;
  }), [entities, formatCounts, editionFilter, batchFilter, formatFilter, countryFilter]);
  const totalPages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const pageItems = filtered.slice((page - 1) * pageSize, page * pageSize);
  const editionOptions = useMemo(() => Array.from(new Set(entities.map((e) => String(e.attributes?.edition_type || "").trim()).filter(Boolean))), [entities]);
  const batchOptions = useMemo(() => Array.from(new Set(entities.map((e) => String(e.attributes?.edition_batch || "").trim()).filter(Boolean))), [entities]);
  const formatOptions = useMemo(() => Array.from(new Set(entities.flatMap(formatCodesOf).filter(Boolean))), [entities, formatCounts]);
  const countryOptions = useMemo(() => Array.from(new Set(entities.map((e) => String(e.attributes?.country || "").trim()).filter(Boolean))), [entities]);

  return (
    <div className="min-h-screen flex flex-col">
      <PageShell
        width="page"
        header={
        <div className="flex items-center gap-2 font-mono text-[11px] text-text-faint">
          <Link href={`/catalog/${workId}`} className="hover:text-emphasis inline-flex items-center gap-1">
            <ArrowLeft className="w-3 h-3" strokeWidth={1.6} />
            {work?.title || t("work.releases.backToWork")}
          </Link>
          <span className="text-text-faint">/</span>
          <span className="text-emphasis">{t("work.releases.allReleases")}</span>
        </div>
        }
      >
        <div className="rounded-card border border-line-subtle bg-surface/70 backdrop-blur overflow-hidden">
          <div className="px-4 md:px-5 py-4 border-b border-line-subtle flex flex-col sm:flex-row sm:items-center justify-between gap-3">
            <h1 className="font-display text-xl tracking-tight text-emphasis">{t("work.releases.releaseCount", { count: total })}</h1>
            <form onSubmit={onSearch} className="relative w-full sm:w-auto">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-text-faint" strokeWidth={1.5} />
              <input value={qInput} {...searchInputLimitProps(setQInput)} placeholder={t("work.detail.searchPlaceholder")} className="pl-9 pr-3 h-10 sm:h-9 w-full sm:w-56 bg-emphasis/[0.04] border border-line rounded-control text-xs text-emphasis placeholder:text-text-faint focus:outline-none focus:border-primary/40" />
            </form>
          </div>

          <div className="px-4 md:px-5 py-2.5 border-b border-line-subtle flex flex-wrap items-center gap-2">
            <label className="inline-flex items-center gap-1.5 text-xs text-text-muted">
              <span className="font-mono">{t("work.detail.filterEdition")}</span>
              <select value={editionFilter} onChange={(e) => { setEditionFilter(e.target.value); setPage(1); }} className="h-9 px-2 rounded-md bg-emphasis/[0.04] border border-line text-xs text-emphasis">
                <option value="">{t("common.all")}</option>
                {editionOptions.map((o) => <option key={o} value={o}>{getTermName(definitions, "edition_type", o, locale) !== o ? getTermName(definitions, "edition_type", o, locale) : o}</option>)}
              </select>
            </label>
            <label className="inline-flex items-center gap-1.5 text-xs text-text-muted">
              <span className="font-mono">{t("release.detail.batchLabel")}</span>
              <select value={batchFilter} onChange={(e) => { setBatchFilter(e.target.value); setPage(1); }} className="h-9 px-2 rounded-md bg-emphasis/[0.04] border border-line text-xs text-emphasis">
                <option value="">{t("common.all")}</option>
                {batchOptions.map((o) => <option key={o} value={o}>{getTermName(definitions, "edition_batch", o, locale) !== o ? getTermName(definitions, "edition_batch", o, locale) : o}</option>)}
              </select>
            </label>
            <label className="inline-flex items-center gap-1.5 text-xs text-text-muted">
              <span className="font-mono">{t("work.detail.filterFormat")}</span>
              <select value={formatFilter} onChange={(e) => { setFormatFilter(e.target.value); setPage(1); }} className="h-9 px-2 rounded-md bg-emphasis/[0.04] border border-line text-xs text-emphasis">
                <option value="">{t("common.all")}</option>
                {formatOptions.map((o) => <option key={o} value={o}>{getTermName(definitions, "format", o, locale) !== o ? getTermName(definitions, "format", o, locale) : o}</option>)}
              </select>
            </label>
            <label className="inline-flex items-center gap-1.5 text-xs text-text-muted">
              <span className="font-mono">{t("work.detail.filterCountry")}</span>
              <select value={countryFilter} onChange={(e) => { setCountryFilter(e.target.value); setPage(1); }} className="h-9 px-2 rounded-md bg-emphasis/[0.04] border border-line text-xs text-emphasis">
                <option value="">{t("common.all")}</option>
                {countryOptions.map((o) => <option key={o} value={o}>{o}</option>)}
              </select>
            </label>
            {(editionFilter || batchFilter || formatFilter || countryFilter) && (
              <button onClick={() => { setEditionFilter(""); setBatchFilter(""); setFormatFilter(""); setCountryFilter(""); setPage(1); }} className="inline-flex items-center gap-1 h-9 px-2.5 rounded-md text-xs text-text-muted hover:text-primary">
                <X className="w-3.5 h-3.5" strokeWidth={1.6} /><span>{t("work.detail.clearFilters")}</span>
              </button>
            )}
          </div>

          {loading ? (
            <div className="p-5 text-center font-mono text-xs text-text-faint">{t("common.loading")}</div>
          ) : pageItems.length === 0 ? (
            <div className="p-5 text-center font-mono text-xs text-text-faint">{entities.length === 0 ? t("work.releases.noReleases") : t("work.detail.noFilterResult")}</div>
          ) : (
            <>
              <div className="hidden sm:block overflow-x-auto">
                <table className="w-full text-left text-xs">
                  <thead className="bg-emphasis/[0.03] border-b border-line-subtle font-mono text-[11px] tracking-wide text-text-faint">
                    <tr>
                      <th className="py-3 px-4 font-medium">{t("work.detail.tableRelease")}</th>
                      <th className="py-3 px-4 font-medium">{t("work.detail.tableEdition")}</th>
                      <th className="py-3 px-4 font-medium">{t("work.detail.tableRegion")}</th>
                      <th className="py-3 px-4 font-medium">{t("work.detail.tablePackaging")}</th>
                      <th className="py-3 px-4 font-medium">{t("work.detail.tableSpec")}</th>
                      <th className="py-3 px-4 text-right font-medium">{t("work.detail.tableDate")}</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line-subtle">
                    {pageItems.map((rel)=>{
                      const edition = String(rel.attributes?.edition_type || "").trim();
                      const country = String(rel.attributes?.country || "").trim();
                      const packaging = String(rel.attributes?.packaging || "").trim();
                      const catalogNo = String(rel.attributes?.catalog_number || "").trim();
                      const editionDate = String(rel.attributes?.edition_date || "").trim();
                      const fmt = formatSummaryOf(rel);
                      return (
                      <tr key={rel.id} className="hover:bg-emphasis/[0.03] transition-colors duration-fast ease-soft">
                        <td className="py-3 px-4"><Link href={`/catalog/${rel.id}`} className="font-semibold text-emphasis hover:text-info-soft inline-flex items-center gap-1">{entityTitle(rel, locale)} <ArrowUpRight className="w-3 h-3 text-text-faint" strokeWidth={1.5} /></Link></td>
                        <td className="py-3 px-4 text-text-muted">{vocabLabel(definitions, "edition_type", edition, locale) || "—"}</td>
                        <td className="py-3 px-4 text-text-muted">{country || "—"}</td>
                        <td className="py-3 px-4 text-text-muted">{vocabLabel(definitions, "packaging", packaging, locale) || "—"}</td>
                        <td className="py-3 px-4 font-mono text-text-faint">{[fmt, catalogNo].filter(Boolean).join(" · ") || "—"}</td>
                        <td className="py-3 px-4 font-mono text-text-muted text-right whitespace-nowrap">{editionDate || "—"}</td>
                      </tr>
                    );})}
                  </tbody>
                </table>
              </div>
              <div className="sm:hidden divide-y divide-line-subtle">
                {pageItems.map((rel)=>{
                  const edition = String(rel.attributes?.edition_type || "").trim();
                  const country = String(rel.attributes?.country || "").trim();
                  const packaging = String(rel.attributes?.packaging || "").trim();
                  const catalogNo = String(rel.attributes?.catalog_number || "").trim();
                  const editionDate = String(rel.attributes?.edition_date || "").trim();
                  const fmt = formatSummaryOf(rel);
                  return (
                  <div key={rel.id} className="px-4 py-3.5 flex items-start gap-2.5">
                    <Link href={`/catalog/${rel.id}`} className="min-w-0 flex-1 space-y-1">
                      <div className="font-semibold text-emphasis text-sm line-clamp-2">{entityTitle(rel, locale)}</div>
                      <div className="font-mono text-[11px] text-text-muted truncate">{[vocabLabel(definitions, "edition_type", edition, locale), country, vocabLabel(definitions, "packaging", packaging, locale)].filter(Boolean).join(" · ") || t("work.detail.noEditionMeta")}</div>
                      <div className="font-mono text-[11px] text-text-faint truncate">{[fmt, catalogNo, editionDate].filter(Boolean).join(" · ") || "—"}</div>
                    </Link>
                  </div>
                );})}
              </div>
              {/* 单页数据不渲染翻页器：页数没有变化时"第 1/1 页"加两个禁用按钮只是噪音
                  （与 works/[id] 的 totalPages > 1 守卫一致）。 */}
              {totalPages > 1 && (
                <div className="px-4 py-3 border-t border-line-subtle bg-emphasis/[0.02] flex items-center justify-between">
                  <span className="font-mono text-[11px] text-text-faint">{t("common.pagination", { page, total: totalPages })}</span>
                  <div className="flex items-center gap-2">
                    <button disabled={page<=1} onClick={()=>setPage(p=>Math.max(1,p-1))} className="w-8 h-8 grid place-items-center rounded-control bg-emphasis/[0.06] border border-line disabled:opacity-40 hover:bg-emphasis/[0.10]"><ChevronLeft className="w-3.5 h-3.5" strokeWidth={1.6} /></button>
                    <button disabled={page>=totalPages} onClick={()=>setPage(p=>Math.min(totalPages,p+1))} className="w-8 h-8 grid place-items-center rounded-control bg-emphasis/[0.06] border border-line disabled:opacity-40 hover:bg-emphasis/[0.10]"><ChevronRight className="w-3.5 h-3.5" strokeWidth={1.6} /></button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </PageShell>
    </div>
  );
}
