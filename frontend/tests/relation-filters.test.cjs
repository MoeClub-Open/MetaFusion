const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const ts = require('typescript-api');

// relationFilters.ts 不引入运行时依赖（只有类型导入），因此可以直接转译执行。
const source = fs.readFileSync(path.join(__dirname, '../src/lib/relationFilters.ts'), 'utf8');
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } });
const helpers = {};
new Function('exports', compiled.outputText)(helpers);
const {
  UNGROUPED_RELATION_GROUP,
  buildRelationFacets,
  filterRelationRows,
  groupRelationSections,
  keepValidSelection,
} = helpers;

// definitions 口径：只有声明了分类且分类有本地化名才按分类展示，否则进兜底分类。
const GROUPS = {
  credits: { key: 'credits', label: '署名' },
  creative: { key: 'creative', label: '创作关系' },
};
const vocab = {
  labels: { kind: '关联对象', group: '关系分类', type: '关系类型' },
  ungroupedLabel: '其他关系',
  groupOf: (type) => GROUPS[{
    voiced_by: 'credits',
    credit_for: 'credits',
    adaptation_of: 'creative',
    sequel_of: 'creative',
    // nameless 类型所在分组没有本地化名：应归入兜底分类
    orphan_relation: null,
  }[type]] || null,
  kindLabelOf: (kind) => ({ agent: '主体', work: '作品' }[kind] || kind),
  typeLabelOf: (row) => row.label || row.type,
  typeNeutralLabelOf: (type) => type,
};

const rows = [
  { type: 'voiced_by', kind: 'agent', label: '配音者', key: 'a' },
  { type: 'voiced_by', kind: 'agent', label: '配音者', key: 'b' },
  { type: 'credit_for', kind: 'agent', label: '参与制作', key: 'c' },
  { type: 'adaptation_of', kind: 'work', label: '改编自', key: 'd' },
  { type: 'sequel_of', kind: 'work', label: '续作于', key: 'e' },
  { type: 'orphan_relation', kind: 'work', label: '杂项', key: 'f' },
];

const facet = (facets, id) => facets.find((f) => f.id === id);
const option = (facets, id, value) => facet(facets, id).options.find((o) => o.value === value);

test('维度取值与计数来自数据本身，顺序按首次出现', () => {
  const facets = buildRelationFacets(rows, {}, vocab);
  assert.deepEqual(facet(facets, 'kind').options.map((o) => [o.value, o.label, o.count]), [
    ['agent', '主体', 3],
    ['work', '作品', 3],
  ]);
  assert.deepEqual(facet(facets, 'group').options.map((o) => [o.value, o.count]), [
    ['credits', 3],
    ['creative', 2],
    [UNGROUPED_RELATION_GROUP, 1],
  ]);
  assert.deepEqual(facet(facets, 'type').options.map((o) => o.value), [
    'voiced_by', 'credit_for', 'adaptation_of', 'sequel_of', 'orphan_relation',
  ]);
  assert.equal(facet(facets, 'kind').total, 6);
});

test('其它维度已选时，本维度的选项与计数随之收窄', () => {
  const facets = buildRelationFacets(rows, { kind: 'agent' }, vocab);
  assert.equal(facet(facets, 'group').total, 3);
  assert.deepEqual(facet(facets, 'group').options.map((o) => [o.value, o.count]), [['credits', 3]]);
  assert.deepEqual(facet(facets, 'type').options.map((o) => o.value), ['voiced_by', 'credit_for']);
  // 本维度自己的选择不影响本维度候选集，否则选项会在点下去之后消失
  assert.equal(facet(facets, 'type').total, 3);
});

test('筛选按维度组合命中，空选择原样返回', () => {
  assert.equal(filterRelationRows(rows, {}, vocab.groupOf), rows);
  assert.deepEqual(
    filterRelationRows(rows, { kind: 'work', group: UNGROUPED_RELATION_GROUP }, vocab.groupOf).map((r) => r.key),
    ['f']
  );
  assert.deepEqual(
    filterRelationRows(rows, { group: 'credits', type: 'credit_for' }, vocab.groupOf).map((r) => r.key),
    ['c']
  );
});

test('失效的维度值被清掉，无变化时返回原对象', () => {
  const facets = buildRelationFacets(rows, {}, vocab);
  const stale = { kind: 'release', type: 'voiced_by' };
  assert.deepEqual(keepValidSelection(stale, facets), { type: 'voiced_by' });
  const kept = { kind: 'agent' };
  assert.equal(keepValidSelection(kept, facets), kept);
});

test('分类归并：声明顺序优先、兜底分类最后、类型名按方向解析', () => {
  const sections = groupRelationSections(rows, { ...vocab, groupOrder: ['creative', 'credits'] });
  assert.deepEqual(sections.map((s) => [s.key, s.label, s.count]), [
    ['creative', '创作关系', 2],
    ['credits', '署名', 3],
    [UNGROUPED_RELATION_GROUP, '其他关系', 1],
  ]);
  assert.deepEqual(sections[1].types.map((t) => [t.type, t.label]), [
    ['voiced_by', '配音者'],
    ['credit_for', '参与制作'],
  ]);
});

test('同一类型两个方向都在时用中性名，避免标题只代表一个方向', () => {
  const mixed = [
    { type: 'adaptation_of', kind: 'work', label: '改编自' },
    { type: 'adaptation_of', kind: 'work', label: '被改编为' },
  ];
  const sections = groupRelationSections(mixed, { ...vocab, groupOrder: ['creative'] });
  assert.equal(sections[0].types[0].label, 'adaptation_of');
  const facets = buildRelationFacets(mixed, {}, vocab);
  assert.equal(option(facets, 'type', 'adaptation_of').label, 'adaptation_of');
});
