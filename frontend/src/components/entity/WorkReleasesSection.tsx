"use client";

import { searchInputLimitProps } from "@/lib/searchQuery";
// 作品详情的"发行目录"分节：从 WorkDetailPage 原样搬出（含数据装载、facet 过滤、分页与
// 表格/移动端卡片两套呈现），改成自包含组件由通用详情视图按 kind=work 渲染。
// 迁移动机：同一份发行数据此前有"作品模板版"与"通用版"两套实现，合并后只保留这一套。
//
// 列与可筛选字段都由发行版模板声明（columns / facet_fields），后台可改，代码不写死
// edition_type/format/country 这类字段码；关键词与 facet 过滤作用在完整候选集上再分页。
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  ArrowRightLeft,
  ArrowUpRight,
  ChevronLeft,
  ChevronRight,
  Layers,
  Search,
  X,
} from "lucide-react";
import { Card } from "@/components/ui/Card";
import { Entity, fetchAllPages, mapLimit, title as entityTitle } from "@/components/catalog/api";
import { FieldValue } from "@/components/catalog/TemplateAttributeSections";
import { useDefinitions, getFieldName, getTermName, templatesForEntity, resolveLocalizedName } from "@/lib/definitions";
import { useI18n } from "@/i18n/I18nProvider";
import { matchesEntityQuery } from "@/lib/entitySearch";
import { useCompareBasket } from "@/lib/compareBasket";

type Props = {
  /** 作品 id：发行版按 work_id 反查，介质格式按 release_id 聚合。 */
  workId: string;
};

export function WorkReleasesSection({ workId }: Props) {
  const { t, locale } = useI18n();
  const { definitions: defs } = useDefinitions();
  const { basket: compareSelected, toggle: toggleCompare } = useCompareBasket();

  const [releaseEntities, setReleaseEntities] = useState<Entity[]>([]);
  // 每个发行版的介质格式计数（按实际 Medium 聚合）：CD+BD 组合不再被"首个格式"吞掉。
  const [releaseFormatCounts, setReleaseFormatCounts] = useState<Record<string, Record<string, number>>>({});
  // 筛选条件：键为字段码，值选中项。字段集合由模板 facet_fields 声明。
  const [facetValues, setFacetValues] = useState<Record<string, string>>({});
  const [page, setPage] = useState(1);
  const pageSize = 10;
  const [q, setQ] = useState("");
  const [qInput, setQInput] = useState("");
  const [loadingReleases, setLoadingReleases] = useState(true);
  const [releasesFailed, setReleasesFailed] = useState(false);

  // 只负责拉全量发行与其介质格式汇总。关键词与 facet 过滤、分页都在渲染侧按完整候选集
  // 求值：否则"先分页后筛选"会漏掉其它页的命中，总数也不会随筛选变化。
  const loadReleases = async () => {
    setLoadingReleases(true);
    setReleasesFailed(false);
    try {
      const entities = await fetchAllPages<Entity>(`/catalog/entities?kind=release&work_id=${encodeURIComponent(workId)}`);
      // 载体格式从实际 Medium 全量聚合（受并发上限约束），不再截断在首屏 50 条。
      const counts = await mapLimit(entities, 8, async (e) => {
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
      entities.forEach((e, i) => { fmtMap[e.id!] = counts[i] || {}; });
      setReleaseEntities(entities);
      setReleaseFormatCounts(fmtMap);
    } catch {
      // 列表取数失败不能落成「暂无发行版。」：那是在替服务端断言"这部作品没有发行版"。
      setReleasesFailed(true);
    } finally {
      setLoadingReleases(false);
    }
  };

  useEffect(() => {
    if (!workId) return;
    void loadReleases();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workId]);

  // 发行版列表的列与可筛选字段：均由发行版模板声明（columns / facet_fields），后台可改。
  const releaseTemplates = useMemo(
    () => Array.from(new Set(releaseEntities.flatMap((entity) => templatesForEntity(defs, entity.kind, entity.attributes)))),
    [defs, releaseEntities],
  );
  const releaseColumns = useMemo(
    () => Array.from(new Set(releaseTemplates.flatMap((tpl: any) => tpl.columns || []))).filter((c) => !!defs?.fields?.[c]),
    [defs, releaseTemplates],
  );
  const releaseFacets = useMemo(
    () => Array.from(new Set(releaseTemplates.flatMap((tpl: any) => tpl.facet_fields || []))).filter((c) => !!defs?.fields?.[c]),
    [defs, releaseTemplates],
  );

  // facet 字段的候选值：先看发行版自身属性，format 再并入实际 Medium 聚合出的格式集合。
  // 多介质发行版（CD＋BD）应能被任一组成格式筛中，因此匹配按"候选列表包含"而不是全等。
  const facetCandidatesOf = (code: string, e: Entity): string[] => {
    const own = e.attributes?.[code];
    const ownList = own !== undefined && own !== null && own !== "" ? [String(own).trim()] : [];
    if (code !== "format") return ownList;
    const derived = Object.keys(releaseFormatCounts[e.id!] || {});
    return Array.from(new Set([...ownList, ...derived]));
  };
  // 某 facet 的全部候选值（来自全量发行版集合）。
  const facetOptionsOf = (code: string) =>
    Array.from(new Set(releaseEntities.flatMap((e) => facetCandidatesOf(code, e)).filter(Boolean)));

  // 介质格式汇总展示（"CD×1＋BD×1"）：仅当有实际 Medium 聚合结果时返回；
  // 无载体数据时返回空串，由调用方回退发行版自身 format 属性的正常渲染。
  const formatSummaryOf = (e: Entity): string => {
    const counts = releaseFormatCounts[e.id!] || {};
    const entries = Object.entries(counts);
    if (entries.length === 0) return "";
    const joiner = locale.startsWith("zh") ? "＋" : " + ";
    return entries
      .map(([code, n]) => {
        const label = getTermName(defs, "format", code, locale);
        return `${label !== code ? label : code}×${n}`;
      })
      .join(joiner);
  };

  // 关键词与 facet 都作用在完整候选集上，再对结果分页；关键词走本地过滤，不必每次输入都重拉全量。
  const filteredReleases = useMemo(() => {
    const kw = q.trim().toLowerCase();
    return releaseEntities.filter((e) => {
      if (!matchesEntityQuery(e, kw)) return false;
      return releaseFacets.every((code) => {
        const want = facetValues[code];
        if (!want) return true;
        return facetCandidatesOf(code, e).includes(want);
      });
    });
    // facetCandidatesOf/依赖项与作品页一致；releaseFormatCounts 参与 format 候选计算
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [releaseEntities, q, facetValues, releaseFacets, releaseFormatCounts]);

  const total = filteredReleases.length;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const pagedReleases = useMemo(
    () => filteredReleases.slice((page - 1) * pageSize, page * pageSize),
    [filteredReleases, page],
  );

  const onSearch = (e: React.FormEvent) => {
    e.preventDefault();
    setPage(1);
    setQ(qInput);
  };

  // 筛选/搜索后总数变小可能让当前页越界，回退到最后一页，避免停在空白页。
  useEffect(() => {
    if (page > totalPages) setPage(totalPages);
  }, [page, totalPages]);

  return (
    <section id="releases" className="space-y-0">
 <div className="px-3.5 sm:px-4 py-3 border-b border-line-subtle flex flex-col sm:flex-row sm:items-center justify-between gap-2.5">
 <div className="flex items-center gap-2">
 <span className="w-9 h-9 max-sm:min-h-[44px] grid place-items-center rounded-md bg-sky-500/10 border border-sky-500/20">
 <Layers className="w-4 h-4 text-sky-500" strokeWidth={1.5} />
 </span>
 <h2 className="font-display text-base font-bold tracking-tight text-text-strong">{t("work.detail.releaseCatalog")}</h2>
 <span className="text-sm text-text-faint">{t("work.detail.totalReleases", { count: total })}</span>
 </div>
 <form onSubmit={onSearch} className="relative w-full sm:w-auto">
 <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 w-4 h-4 text-text-muted" strokeWidth={1.5} />
 <input
 value={qInput}
 {...searchInputLimitProps(setQInput)}
 aria-label={t("work.detail.searchPlaceholder")}
 placeholder={t("work.detail.searchPlaceholder")}
 className="pl-11 pr-3.5 h-9 max-sm:min-h-[44px] w-full sm:w-48 bg-black/[0.03] dark:bg-white/[0.04] border border-line rounded-md text-sm text-text-strong placeholder:text-text-muted focus:outline-none focus:border-primary/50 font-mono"
 />
 </form>
 </div>

 {loadingReleases ? (
 <div className="p-8 text-center text-sm text-text-faint">{t("work.detail.loadingReleases")}</div>
 ) : releasesFailed ? (
 <div role="alert" className="p-8 text-center text-sm space-y-2">
 <p className="text-amber-700 dark:text-warn-soft">{t("catalog.listFailed")}</p>
 <button type="button" onClick={() => void loadReleases()} className="text-xs font-mono text-primary hover:underline cursor-pointer">
 {t("catalog.retry")}
 </button>
 </div>
 ) : releaseEntities.length === 0 ? (
 <Card padding="none" className="border-dashed">
   <div className="p-8 text-center text-sm text-text-faint">{t("work.detail.noReleases")}{q ? t("work.detail.noReleasesHint") : ""}</div>
 </Card>
 ) : (
 <>
 <div className="px-3.5 sm:px-4 py-2.5 border-b border-line-subtle flex flex-col lg:flex-row lg:items-center gap-2.5 bg-black/[0.01] dark:bg-white/[0.01]">
 <div className="flex flex-wrap items-center gap-2">
 {releaseFacets.map((code) => (
 <label key={code} className="inline-flex items-center gap-1.5 text-xs text-text-faint">
 <span className="font-mono">{getFieldName(defs, code, locale)}</span>
 <select value={facetValues[code] || ""} onChange={(e) => { setFacetValues((prev) => ({ ...prev, [code]: e.target.value })); setPage(1); }} className="h-9 max-sm:min-h-[44px] px-2 rounded-md bg-black/[0.03] dark:bg-white/[0.06] border border-line text-xs text-text-strong">
 <option value="">{t("common.all")}</option>
 {facetOptionsOf(code).map((o) => {
 const def: any = defs?.fields?.[code];
 const label = def?.type === "enum" && def?.vocabulary ? getTermName(defs, def.vocabulary, o, locale) : o;
 return <option key={o} value={o}>{label}</option>;
 })}
 </select>
 </label>
 ))}
 {Object.values(facetValues).some(Boolean) && (
 <button onClick={() => setFacetValues({})} className="inline-flex items-center gap-1 h-9 px-2.5 rounded-md text-xs text-text-faint hover:text-primary">
 <X className="w-3.5 h-3.5" strokeWidth={1.6} /><span>{t("work.detail.clearFilters")}</span>
 </button>
 )}
 </div>
 {compareSelected.length > 0 && (
 <Link href={`/compare?ids=${encodeURIComponent(compareSelected.join(","))}`} className="lg:ml-auto inline-flex items-center gap-1.5 h-9 max-sm:min-h-[44px] px-3 rounded-md bg-primary text-white text-xs font-semibold hover:opacity-90 w-fit">
 <ArrowRightLeft className="w-3.5 h-3.5" strokeWidth={1.6} /><span>{t("work.detail.compareOpenCount", { count: compareSelected.length })}</span>
 </Link>
 )}
 </div>
 {filteredReleases.length === 0 ? (
 <div className="p-8 text-center text-sm text-text-faint">{t("work.detail.noFilterResult")}</div>
 ) : (
 <>
 {/* 发行版列表的列由模板 columns 声明（后台可改），不再写死字段码 */}
 {releaseColumns.length > 0 && (
 <div className="hidden sm:block overflow-x-auto">
 <table className="w-full text-left text-sm">
 <thead className="bg-surfaceSubtle border-b border-line-subtle text-xs uppercase tracking-wider text-text-faint">
 <tr>
 <th className="py-2.5 px-2 font-medium w-10" aria-label={t("work.detail.compareSelect")} />
 <th className="py-2.5 px-3.5 font-medium">{t("work.detail.tableRelease")}</th>
 {releaseColumns.map((code) => (
 <th key={code} className="py-2.5 px-3.5 font-medium">
 {getFieldName(defs, code, locale)}
 </th>
 ))}
 </tr>
 </thead>
 <tbody className="divide-y divide-black/5 dark:divide-white/[0.06]">
 {pagedReleases.map((rel) => (
 <tr key={rel.id} className="hover:bg-surfaceSubtle transition-colors duration-fast ease-soft">
 <td className="py-2.5 px-2">
 <input type="checkbox" aria-label={t("work.detail.compareSelectName", { name: entityTitle(rel, locale) })} checked={compareSelected.includes(rel.id!)} onChange={() => toggleCompare(rel.id!)} className="w-4 h-4 rounded accent-primary cursor-pointer" />
 </td>
 <td className="py-2.5 px-3.5">
 <Link href={`/catalog/${rel.id}`} className="font-semibold text-text-strong hover:text-primary inline-flex items-center gap-1.5">
 {entityTitle(rel, locale)} <ArrowUpRight className="w-3.5 h-3.5 text-text-muted" strokeWidth={1.6} />
 </Link>
 </td>
 {releaseColumns.map((code) => (
 <td key={code} className="py-2.5 px-3.5 text-xs text-gray-600 dark:text-gray-400 whitespace-nowrap">
 {code === "format" && formatSummaryOf(rel) ? (
 <span className="font-mono">{formatSummaryOf(rel)}</span>
 ) : rel.attributes?.[code] ? (
 <FieldValue defs={defs} code={code} value={rel.attributes[code]} locale={locale} />
 ) : ("—")}
 </td>
 ))}
 </tr>
 ))}
 </tbody>
 </table>
 </div>
 )}
 <div className="sm:hidden divide-y divide-black/5 dark:divide-white/[0.06]">
 {pagedReleases.map((rel) => (
 <div key={rel.id} className="px-3.5 py-3 flex items-start gap-2.5">
 <input type="checkbox" aria-label={t("work.detail.compareSelectName", { name: entityTitle(rel, locale) })} checked={compareSelected.includes(rel.id!)} onChange={() => toggleCompare(rel.id!)} className="mt-1 w-5 h-5 rounded accent-primary cursor-pointer shrink-0" />
 <Link href={`/catalog/${rel.id}`} className="min-w-0 flex-1 space-y-1">
 <div className="font-semibold text-text-strong text-sm leading-tight line-clamp-2">{entityTitle(rel, locale)}</div>
                      <div className="text-xs text-text-faint truncate">
 {releaseColumns.map((code) => (code === "format" && formatSummaryOf(rel)) || attributeText(defs, code, rel.attributes?.[code], locale)).filter(Boolean).join(" · ") || t("work.detail.noEditionMeta")}
 </div>
 </Link>
 </div>
 ))}
 </div>
 {totalPages > 1 && (
 <div className="px-3.5 sm:px-4 py-3 border-t border-line-subtle flex items-center justify-end gap-2">
 <span className="font-mono text-[11px] text-text-faint">{t("common.pagination", { page, total: totalPages })}</span>
 <button type="button" disabled={page <= 1} onClick={() => setPage((p) => Math.max(1, p - 1))} aria-label={t("pagination.prev")} className="w-8 h-8 grid place-items-center rounded-full bg-black/[0.04] dark:bg-white/[0.06] border border-line disabled:opacity-40 hover:bg-black/[0.08] dark:hover:bg-white/[0.10]">
 <ChevronLeft className="w-3.5 h-3.5" strokeWidth={1.6} />
 </button>
 <button type="button" disabled={page >= totalPages} onClick={() => setPage((p) => Math.min(totalPages, p + 1))} aria-label={t("pagination.next")} className="w-8 h-8 grid place-items-center rounded-full bg-black/[0.04] dark:bg-white/[0.06] border border-line disabled:opacity-40 hover:bg-black/[0.08] dark:hover:bg-white/[0.10]">
 <ChevronRight className="w-3.5 h-3.5" strokeWidth={1.6} />
 </button>
 </div>
 )}
 </>
 )}
 </>
 )}
 </section>
  );
}

// attributeText：把某属性的值转成纯文本（枚举按词表本地化、实体取标题），供移动端摘要拼接。
// 枚举名必须按请求语言解析，不能写死 zh-CN——否则日/英界面会显示中文词条。
function attributeText(defs: any, code: string, value: any, locale: string): string {
  if (value === undefined || value === null || value === "") return "";
  const def: any = defs?.fields?.[code];
  if (def?.type === "enum" && def?.vocabulary) {
    const term = defs?.vocabularies?.[def.vocabulary]?.terms?.[String(value)];
    return resolveLocalizedName(term?.names, locale, String(value));
  }
  if (def?.type === "entity") {
    if (typeof value === "object" && value) return value.title || value.name || value.id || "";
    return String(value);
  }
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
