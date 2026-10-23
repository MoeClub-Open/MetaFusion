"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import {
  getKindName,
  getRelationName,
  resolveLocalizedName,
  useDefinitions,
} from "@/lib/definitions";
import {
  RelationFacet,
  RelationFacetRow,
  RelationFacetSelection,
  RelationFacetVocabulary,
  RelationGroupOf,
  RelationGroupSection,
  buildRelationFacets,
  filterRelationRows,
  groupRelationSections,
  keepValidSelection,
} from "@/lib/relationFilters";

/**
 * 关系筛选状态：维度、选项、计数与"分类 → 类型"归并全部由数据与服务端 definitions
 * 推导，调用方只提供已按方向解析好关系名的行。
 *
 * groupOrder 传实体模板声明的 relation_groups：声明过的分类排前，兜底分类永远最后。
 */
export function useRelationFilter<R extends RelationFacetRow>(rows: R[], groupOrder?: string[]) {
  const { t, tr, locale } = useI18n();
  const { definitions: defs, kinds, relationshipRules } = useDefinitions();
  const [selection, setSelection] = useState<RelationFacetSelection>({});

  // 分类解析：只有声明了 group 且该分组有本地化名才按分类展示——否则两个无名分组
  // 会显示成两个同名分类，不如统一归入兜底分类。
  const groupOf = useMemo<RelationGroupOf>(
    () => (type: string) => {
      const rel = defs?.relations?.[type];
      const key = rel?.group || "";
      if (!key) return null;
      const label = resolveLocalizedName(rel?.group_names, locale, "");
      return label ? { key, label } : null;
    },
    [defs, locale]
  );

  const vocab = useMemo<RelationFacetVocabulary>(
    () => ({
      labels: {
        kind: t("relations.facetKind"),
        group: t("relations.facetGroup"),
        type: t("relations.facetType"),
      },
      ungroupedLabel: t("entity.page.relationsGroupOther"),
      groupOf,
      kindLabelOf: (kind: string) => getKindName(kinds, kind, locale, tr(`catalog.kind.${kind}`, kind)),
      typeLabelOf: (row: RelationFacetRow) =>
        (row.label || "").trim() || getRelationName(defs, row.type, true, locale, relationshipRules),
      typeNeutralLabelOf: (type: string) => getRelationName(defs, type, true, locale, relationshipRules),
      groupOrder,
    }),
    [t, tr, locale, defs, kinds, relationshipRules, groupOf, groupOrder]
  );

  const facets = useMemo(
    () => buildRelationFacets(rows, selection, vocab),
    [rows, selection, vocab]
  );

  // 数据或定义变化后选项可能消失（例如切了分类导致类型选项没了），清掉失效值，
  // 避免停在筛不出任何关系的空列表。
  useEffect(() => {
    const next = keepValidSelection(selection, facets);
    if (next !== selection) setSelection(next);
  }, [facets, selection]);

  const visible = useMemo(() => filterRelationRows(rows, selection, groupOf), [rows, selection, groupOf]);
  const sections = useMemo<RelationGroupSection<R>[]>(
    () => groupRelationSections(visible, vocab),
    [visible, vocab]
  );
  // 没有任何维度可筛时不渲染筛选条，调用方也据此避免留出一条空边框。
  const filterable = useMemo(
    () => displayFacets(facets, selection).length > 0,
    [facets, selection]
  );

  const toggle = useCallback((id: keyof RelationFacetSelection, value: string) => {
    setSelection((prev) => {
      const next = { ...prev };
      if (prev[id] === value) delete next[id];
      else next[id] = value;
      return next;
    });
  }, []);

  return {
    selection,
    setSelection,
    toggle,
    facets,
    filterable,
    sections,
    visible,
    total: rows.length,
    groupOf,
    vocab,
  };
}

/** 只有一个取值、且当前没被选中的维度没有可筛的东西，不占位置。 */
function displayFacets(facets: RelationFacet[], selection: RelationFacetSelection): RelationFacet[] {
  return facets.filter((f) => f.options.length >= 2 || !!selection[f.id]);
}

function FilterChip({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count?: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={count === undefined ? label : `${label} (${count})`}
      className={`inline-flex items-center gap-1 px-2.5 py-1 rounded-control border text-xs transition-all cursor-pointer ${
        active
          ? "bg-primary text-white border-primary font-medium shadow-xs"
          : "border-line bg-black/[0.03] dark:bg-white/[0.04] text-gray-600 dark:text-gray-300 hover:border-primary/40 hover:text-gray-900 dark:hover:text-white"
      }`}
    >
      <span className="truncate max-w-[12rem]">{label}</span>
      {count !== undefined && (
        <span className="font-mono text-[10px] opacity-70">({count})</span>
      )}
    </button>
  );
}

/**
 * 关系筛选条：每个维度一行，首项是"全部"。筛选维度来自关系数据与 definitions，
 * 数据里没有的维度不出现；再点一次已选中的项即取消该维度。
 */
export function RelationFilterBar({
  facets,
  selection,
  onToggle,
  className = "",
}: {
  facets: RelationFacet[];
  selection: RelationFacetSelection;
  onToggle: (id: RelationFacet["id"], value: string) => void;
  className?: string;
}) {
  const { t } = useI18n();
  const shown = displayFacets(facets, selection);
  if (shown.length === 0) return null;
  return (
    <div className={`space-y-1.5 ${className}`}>
      {shown.map((facet) => (
        <div key={facet.id} className="flex flex-wrap items-center gap-1.5">
          <span className="shrink-0 font-mono text-[11px] uppercase tracking-wider text-text-muted">
            {facet.label}
          </span>
          <FilterChip
            label={t("common.all")}
            count={facet.total}
            active={!selection[facet.id]}
            onClick={() => selection[facet.id] && onToggle(facet.id, selection[facet.id]!)}
          />
          {facet.options.map((option) => (
            <FilterChip
              key={option.value}
              label={option.label}
              count={option.count}
              active={selection[facet.id] === option.value}
              onClick={() => onToggle(facet.id, option.value)}
            />
          ))}
        </div>
      ))}
    </div>
  );
}
