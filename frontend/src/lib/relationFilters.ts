/**
 * 关系筛选的纯逻辑层：维度、计数、排序与过滤口径只在这里定义一次，
 * 作品详情的关系列表、实体详情的卡片视图与关系图谱共用同一套口径。
 *
 * 三个维度全部来自数据本身与服务端 definitions，前端不另维护关系名单：
 * - kind  对端实体 kind（agent / work / release…）
 * - group 关系定义声明的展示分类（credits / creative / membership…）
 * - type  关系类型码
 * 关系没有声明分类、或分类没有本地化名时统一归入兜底分类：
 * 既保证不丢关系，也不会出现两个同名分类。
 *
 * 本文件不引入任何运行时依赖（只有类型导入），便于用纯函数单测钉住口径。
 */

/** 兜底分类键：没有可展示分类的关系都归到这里。 */
export const UNGROUPED_RELATION_GROUP = "__ungrouped__";

export type RelationFacetId = "kind" | "group" | "type";

/** 参与筛选的一行只需关系类型与对端 kind；label 是该行按方向解析出的关系名。 */
export interface RelationFacetRow {
  type: string;
  kind: string;
  label?: string;
}

/** 关系类型 → 展示分类；返回 null 表示该类型没有可展示的分类（进兜底分类）。 */
export type RelationGroupOf = (type: string) => { key: string; label: string } | null;

export interface RelationFacetOption {
  /** 该维度的取值（kind 码 / 分类键 / 关系类型码）。 */
  value: string;
  label: string;
  count: number;
}

export interface RelationFacet {
  id: RelationFacetId;
  label: string;
  /** 其它维度生效时本维度的总行数（"全部"项的计数）。 */
  total: number;
  options: RelationFacetOption[];
}

/** 维度选择：未设置表示该维度不筛。 */
export type RelationFacetSelection = Partial<Record<RelationFacetId, string>>;

/** 渲染层注入的本地化与分类解析：纯逻辑层不碰 dictionaries、definitions 与 locale。 */
export interface RelationFacetVocabulary {
  labels: Record<RelationFacetId, string>;
  /** 关系没有可展示分类时的分类名。 */
  ungroupedLabel: string;
  groupOf: RelationGroupOf;
  kindLabelOf: (kind: string) => string;
  typeLabelOf: (row: RelationFacetRow) => string;
  /** 同一类型两个方向都在时用的中性名（正向关系名）。 */
  typeNeutralLabelOf?: (type: string) => string;
  /** 分类展示顺序（模板 relation_groups 声明）：声明过的排前，其余按出现顺序，兜底永远最后。 */
  groupOrder?: string[];
}

const DIMENSIONS: RelationFacetId[] = ["kind", "group", "type"];

/** 展示分类键：没有可展示分类的一律归入兜底分类。 */
export function relationGroupKeyOf(type: string, groupOf: RelationGroupOf): string {
  return groupOf(type)?.key || UNGROUPED_RELATION_GROUP;
}

function relationGroupLabelOf(type: string, vocab: RelationFacetVocabulary): string {
  return vocab.groupOf(type)?.label || vocab.ungroupedLabel;
}

/** 类型展示名：同一类型各方向名一致时用它，混向时退回中性名，最后退类型码。 */
export function relationTypeLabelOf<R extends RelationFacetRow>(
  rows: R[],
  type: string,
  vocab: RelationFacetVocabulary
): string {
  const labels = Array.from(new Set(rows.map((r) => (r.label || "").trim()).filter(Boolean)));
  if (labels.length === 1) return labels[0];
  return vocab.typeNeutralLabelOf?.(type) || labels[0] || type;
}

function orderGroupKeys(keys: string[], groupOrder?: string[]): string[] {
  const declared = (groupOrder || []).filter((k) => keys.includes(k));
  const rest = keys.filter((k) => !declared.includes(k));
  return [
    ...declared,
    ...rest.filter((k) => k !== UNGROUPED_RELATION_GROUP),
    ...rest.filter((k) => k === UNGROUPED_RELATION_GROUP),
  ];
}

/** 单行是否命中当前选择；空选择命中全部。 */
export function matchesRelationSelection(
  row: RelationFacetRow,
  selection: RelationFacetSelection,
  groupOf: RelationGroupOf
): boolean {
  if (selection.kind && row.kind !== selection.kind) return false;
  if (selection.group && relationGroupKeyOf(row.type, groupOf) !== selection.group) return false;
  if (selection.type && row.type !== selection.type) return false;
  return true;
}

/** 按选择过滤；未选择任何维度时原样返回，避免无谓重算。 */
export function filterRelationRows<R extends RelationFacetRow>(
  rows: R[],
  selection: RelationFacetSelection,
  groupOf: RelationGroupOf
): R[] {
  if (!DIMENSIONS.some((id) => selection[id])) return rows;
  return rows.filter((row) => matchesRelationSelection(row, selection, groupOf));
}

/** 某一维度上计入的取值（kind 维度忽略空 kind，避免出现空白筛选项）。 */
function valueOf(row: RelationFacetRow, id: RelationFacetId, vocab: RelationFacetVocabulary): string {
  if (id === "kind") return row.kind || "";
  if (id === "group") return relationGroupKeyOf(row.type, vocab.groupOf);
  return row.type;
}

function optionLabelOf(
  id: RelationFacetId,
  value: string,
  rows: RelationFacetRow[],
  vocab: RelationFacetVocabulary
): string {
  if (id === "kind") return vocab.kindLabelOf(value);
  if (id === "group") {
    // 分类名以组内任一条关系声明的 group_names 为准，不因首条缺声明而整组降级。
    for (const row of rows) {
      if (relationGroupKeyOf(row.type, vocab.groupOf) !== value) continue;
      const label = relationGroupLabelOf(row.type, vocab);
      if (label) return label;
    }
    return vocab.ungroupedLabel;
  }
  return relationTypeLabelOf(rows.filter((r) => r.type === value), value, vocab);
}

/**
 * 构建维度与选项：每个维度的选项与计数按**其它维度**已生效的选择计算
 * （分面检索口径），因此任一维度上显示的计数都等于点它之后会看到的行数。
 */
export function buildRelationFacets<R extends RelationFacetRow>(
  rows: R[],
  selection: RelationFacetSelection,
  vocab: RelationFacetVocabulary
): RelationFacet[] {
  const facets: RelationFacet[] = [];
  for (const id of DIMENSIONS) {
    // 本维度的候选集：其它维度生效、本维度不生效。
    const scope: RelationFacetSelection = { ...selection };
    delete scope[id];
    const scoped = filterRelationRows(rows as RelationFacetRow[], scope, vocab.groupOf);
    const counts = new Map<string, number>();
    for (const row of scoped) {
      const value = valueOf(row, id, vocab);
      if (!value) continue;
      counts.set(value, (counts.get(value) || 0) + 1);
    }
    const values =
      id === "group"
        ? orderGroupKeys(Array.from(counts.keys()), vocab.groupOrder)
        : Array.from(counts.keys());
    facets.push({
      id,
      label: vocab.labels[id],
      total: scoped.filter((row) => valueOf(row, id, vocab)).length,
      options: values.map((value) => ({
        value,
        label: optionLabelOf(id, value, scoped, vocab),
        count: counts.get(value) || 0,
      })),
    });
  }
  return facets;
}

/** 清掉选项已不存在的维度值；没有变化时返回原对象，供 React 依赖比较。 */
export function keepValidSelection(
  selection: RelationFacetSelection,
  facets: RelationFacet[]
): RelationFacetSelection {
  let next: RelationFacetSelection | null = null;
  for (const facet of facets) {
    const value = selection[facet.id];
    if (!value) continue;
    if (facet.options.some((o) => o.value === value)) continue;
    next = next || { ...selection };
    delete next[facet.id];
  }
  return next || selection;
}

export interface RelationTypeSection<R extends RelationFacetRow> {
  type: string;
  label: string;
  rows: R[];
}

export interface RelationGroupSection<R extends RelationFacetRow> {
  key: string;
  label: string;
  count: number;
  types: RelationTypeSection<R>[];
}

/**
 * 按"分类 → 关系类型"二级归并：同一分类下的关系不再混排成一片，
 * 类型名按方向解析（同一类型两个方向都在时用中性名）。
 */
export function groupRelationSections<R extends RelationFacetRow>(
  rows: R[],
  vocab: RelationFacetVocabulary
): RelationGroupSection<R>[] {
  const groups = new Map<string, Map<string, R[]>>();
  for (const row of rows) {
    const key = relationGroupKeyOf(row.type, vocab.groupOf);
    let types = groups.get(key);
    if (!types) {
      types = new Map();
      groups.set(key, types);
    }
    const list = types.get(row.type) || [];
    list.push(row);
    types.set(row.type, list);
  }
  return orderGroupKeys(Array.from(groups.keys()), vocab.groupOrder).map((key) => {
    const types = groups.get(key)!;
    const typeSections = Array.from(types.entries()).map(([type, list]) => ({
      type,
      label: relationTypeLabelOf(list, type, vocab),
      rows: list,
    }));
    const groupRows = typeSections.flatMap((t) => t.rows);
    return {
      key,
      label: optionLabelOf("group", key, groupRows, vocab),
      count: groupRows.length,
      types: typeSections,
    };
  });
}
