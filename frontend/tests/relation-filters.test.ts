import { test } from "node:test";
import assert from "node:assert/strict";
import {
  UNGROUPED_RELATION_GROUP,
  buildRelationFacets,
  filterRelationRows,
  groupRelationSections,
  keepValidSelection,
  type RelationFacet,
  type RelationFacetId,
  type RelationFacetOption,
  type RelationFacetRow,
  type RelationFacetSelection,
  type RelationFacetVocabulary,
} from "../src/lib/relationFilters";

type TestRow = RelationFacetRow & { key?: string };

// definitions 口径：只有声明了分类且分类有本地化名才按分类展示，否则进兜底分类。
const GROUPS: Record<string, { key: string; label: string }> = {
  credits: { key: "credits", label: "署名" },
  creative: { key: "creative", label: "创作关系" },
};

const GROUP_MAP: Record<string, string | null> = {
  voiced_by: "credits",
  credit_for: "credits",
  adaptation_of: "creative",
  sequel_of: "creative",
  orphan_relation: null,
};

const KIND_LABELS: Record<string, string> = {
  agent: "主体",
  work: "作品",
};

const vocab: RelationFacetVocabulary = {
  labels: { kind: "关联对象", group: "关系分类", type: "关系类型" },
  ungroupedLabel: "其他关系",
  groupOf: (type: string) => {
    const key = GROUP_MAP[type];
    return key ? GROUPS[key] || null : null;
  },
  kindLabelOf: (kind: string) => KIND_LABELS[kind] || kind,
  typeLabelOf: (row: RelationFacetRow) => row.label || row.type,
  typeNeutralLabelOf: (type: string) => type,
};

const rows: TestRow[] = [
  { type: "voiced_by", kind: "agent", label: "配音者", key: "a" },
  { type: "voiced_by", kind: "agent", label: "配音者", key: "b" },
  { type: "credit_for", kind: "agent", label: "参与制作", key: "c" },
  { type: "adaptation_of", kind: "work", label: "改编自", key: "d" },
  { type: "sequel_of", kind: "work", label: "续作于", key: "e" },
  { type: "orphan_relation", kind: "work", label: "杂项", key: "f" },
];

function facet(facets: RelationFacet[], id: RelationFacetId): RelationFacet {
  const found = facets.find((f) => f.id === id);
  assert.ok(found);
  return found;
}

function option(facets: RelationFacet[], id: RelationFacetId, value: string): RelationFacetOption {
  const found = facet(facets, id).options.find((o) => o.value === value);
  assert.ok(found);
  return found;
}

test("维度取值与计数来自数据本身，顺序按首次出现", () => {
  const facets = buildRelationFacets(rows, {}, vocab);
  assert.deepEqual(
    facet(facets, "kind").options.map((o) => [o.value, o.label, o.count]),
    [
      ["agent", "主体", 3],
      ["work", "作品", 3],
    ],
  );
  assert.deepEqual(
    facet(facets, "group").options.map((o) => [o.value, o.count]),
    [
      ["credits", 3],
      ["creative", 2],
      [UNGROUPED_RELATION_GROUP, 1],
    ],
  );
  assert.deepEqual(facet(facets, "type").options.map((o) => o.value), [
    "voiced_by",
    "credit_for",
    "adaptation_of",
    "sequel_of",
    "orphan_relation",
  ]);
  assert.equal(facet(facets, "kind").total, 6);
});

test("其它维度已选时，本维度的选项与计数随之收窄", () => {
  const facets = buildRelationFacets(rows, { kind: "agent" }, vocab);
  assert.equal(facet(facets, "group").total, 3);
  assert.deepEqual(facet(facets, "group").options.map((o) => [o.value, o.count]), [["credits", 3]]);
  assert.deepEqual(facet(facets, "type").options.map((o) => o.value), ["voiced_by", "credit_for"]);
  assert.equal(facet(facets, "type").total, 3);
});

test("筛选按维度组合命中，空选择原样返回", () => {
  assert.equal(filterRelationRows(rows, {}, vocab.groupOf), rows);
  assert.deepEqual(
    filterRelationRows(rows, { kind: "work", group: UNGROUPED_RELATION_GROUP }, vocab.groupOf).map((r) => r.key),
    ["f"],
  );
  assert.deepEqual(
    filterRelationRows(rows, { group: "credits", type: "credit_for" }, vocab.groupOf).map((r) => r.key),
    ["c"],
  );
});

test("失效的维度值被清掉，无变化时返回原对象", () => {
  const facets = buildRelationFacets(rows, {}, vocab);
  const stale: RelationFacetSelection = { kind: "release", type: "voiced_by" };
  assert.deepEqual(keepValidSelection(stale, facets), { type: "voiced_by" });
  const kept: RelationFacetSelection = { kind: "agent" };
  assert.equal(keepValidSelection(kept, facets), kept);
});

test("分类归并：声明顺序优先、兜底分类最后、类型名按方向解析", () => {
  const sections = groupRelationSections(rows, { ...vocab, groupOrder: ["creative", "credits"] });
  assert.deepEqual(
    sections.map((s) => [s.key, s.label, s.count]),
    [
      ["creative", "创作关系", 2],
      ["credits", "署名", 3],
      [UNGROUPED_RELATION_GROUP, "其他关系", 1],
    ],
  );
  assert.deepEqual(
    sections[1].types.map((t) => [t.type, t.label]),
    [
      ["voiced_by", "配音者"],
      ["credit_for", "参与制作"],
    ],
  );
});

test("同一类型两个方向都在时用中性名，避免标题只代表一个方向", () => {
  const mixed: TestRow[] = [
    { type: "adaptation_of", kind: "work", label: "改编自" },
    { type: "adaptation_of", kind: "work", label: "被改编为" },
  ];
  const sections = groupRelationSections(mixed, { ...vocab, groupOrder: ["creative"] });
  assert.equal(sections[0].types[0].label, "adaptation_of");
  const facets = buildRelationFacets(mixed, {}, vocab);
  assert.equal(option(facets, "type", "adaptation_of").label, "adaptation_of");
});
