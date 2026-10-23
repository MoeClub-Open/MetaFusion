// tag 反查键序确定性回归测试
//
//   cd frontend && bun scripts/tag-i18n-order.test.ts
//
// 锁三件事：
//   1) 同一 raw tag 的解析只取决于 term code 的**字典序**，与 definitions JSON 的键序无关：
//      同一份词表按正序与反序写键，解析结果（命中的 term code 与展示名）完全一致；
//   2) 同名碰撞时取字典序最小的 term code（含 `Light novel`、`映画`、`交響詩` 等真实碰撞键）；
//   3) 112 个真实 raw tag × 4 locale 的展示名与 id29 基线逐值一致（零回归）。
//
// 夹具 tag-i18n-order-baseline.json 来自 live definitions id=29（base_version=28）的
// tags 词表与线上 112 个真实标签，基线由修复前的 getTagName 生成（见夹具 provenance）。

import { readFileSync } from "node:fs";
import assert from "node:assert/strict";

const { getTagName, getTagNames, resolveTagTermCode, tagCode, resolveLocalizedName } = await import(
  "../src/lib/definitions.ts"
);

const fixture = JSON.parse(readFileSync(new URL("./tag-i18n-order-baseline.json", import.meta.url), "utf8"));
const { terms, rawTags, baseline, provenance } = fixture;
const locales = provenance.locales;

/** 夹具里每个词条只存了 names，这里补成与 definitions 词表同形的对象。 */
const termDefs = (namesMap) =>
  Object.fromEntries(Object.entries(namesMap).map(([code, names]) => [code, { names, enabled: true }]));

const forward = { vocabularies: { tags: { names: { "zh-CN": "标签" }, terms: termDefs(terms) } } };
const reversedCodes = [...Object.keys(terms)].reverse();
const reversed = { vocabularies: { tags: { names: { "zh-CN": "标签" }, terms: termDefs(
  Object.fromEntries(reversedCodes.map((code) => [code, terms[code]])),
) } } };

const localeKeys = [...new Set(Object.values(terms).flatMap((names) => Object.keys(names)))];

/** 每个名字（按语种）被哪些 term code 声明。 */
const ownersByLocaleName = new Map(); // `${locale}\u0000${name}` -> Set<code>
const ownersByName = new Map(); // name -> Set<code>（跨语种合并，用于碰撞样例）
for (const [code, names] of Object.entries(terms)) {
  for (const [loc, name] of Object.entries(names)) {
    if (typeof name !== "string" || !name.trim()) continue;
    const byLocale = `${loc}\u0000${name.trim()}`;
    if (!ownersByLocaleName.has(byLocale)) ownersByLocaleName.set(byLocale, new Set());
    ownersByLocaleName.get(byLocale).add(code);
    if (!ownersByName.has(name.trim())) ownersByName.set(name.trim(), new Set());
    ownersByName.get(name.trim()).add(code);
  }
}
const collisionRows = [...ownersByLocaleName.entries()]
  .filter(([, owners]) => owners.size > 1)
  .map(([key, owners]) => {
    const [locale, name] = key.split("\u0000");
    return { locale, name, owners: [...owners].sort() };
  });

/** 测试用的独立期望实现：与文档化的优先级逐条对应（code 精确 → 精确大小写 → 折叠大小写）。 */
const expectedCode = (namesMap, raw) => {
  const key = String(raw).trim();
  if (!key) return "";
  if (namesMap[key]) return key;
  const exactOwners = new Set();
  const foldedOwners = new Set();
  for (const [code, names] of Object.entries(namesMap)) {
    for (const name of Object.values(names)) {
      if (typeof name !== "string") continue;
      if (name.trim() === key) exactOwners.add(code);
      if (name.trim().toLowerCase() === key.toLowerCase()) foldedOwners.add(code);
    }
  }
  const pool = exactOwners.size > 0 ? exactOwners : foldedOwners;
  return [...pool].sort()[0] || "";
};

let failed = 0;
let checks = 0;
const check = (label, fn) => {
  try {
    fn();
    checks++;
    console.log(`PASS ${label}`);
  } catch (err) {
    failed++;
    console.log(`FAIL ${label}\n     ${String(err?.message || err).split("\n").join("\n     ")}`);
  }
};
const countTrue = (list, pred, label) => {
  const bad = list.filter((x) => !pred(x));
  assert.equal(bad.length, 0, `${label}：${bad.length} 项不符，例如 ${JSON.stringify(bad.slice(0, 3))}`);
  return list.length;
};

console.log(
  `夹具：live definitions id=${provenance.published_id}（base_version=${provenance.base_version}）` +
    ` · ${provenance.term_count} term · ${provenance.raw_tag_count} raw tag · ${locales.length} locale` +
    ` · 基线生成于 ${provenance.baseline_generated_at}`,
);
console.log(`碰撞键（同名被多个 term 声明，含语种）：${collisionRows.length} 行\n`);

const pairs = locales.flatMap((loc) => rawTags.map((raw) => ({ loc, raw })));

check("夹具自检：反序词表确实与正序键序相反（否则本测试无意义）", () => {
  assert.deepEqual(Object.keys(reversed.vocabularies.tags.terms), reversedCodes);
  assert.notDeepEqual(Object.keys(reversed.vocabularies.tags.terms), Object.keys(terms));
});
check(`夹具自检：存在同名碰撞键（实测 ${collisionRows.length} 行）`, () => {
  assert.ok(collisionRows.length >= 20, `碰撞键过少：${collisionRows.length}`);
});

check(`112×${locales.length} 展示名与 id29 基线逐值一致（正序键）`, () => {
  const n = countTrue(pairs, ({ loc, raw }) => getTagName(forward, raw, loc) === baseline[loc][raw], "基线回归");
  assert.equal(n, rawTags.length * locales.length);
});
check(`112×${locales.length} 展示名与 id29 基线逐值一致（反序键）`, () => {
  countTrue(pairs, ({ loc, raw }) => getTagName(reversed, raw, loc) === baseline[loc][raw], "反序键基线回归");
});
check("正序键与反序键命中同一 term（112 raw tag）", () => {
  countTrue(rawTags, (raw) => resolveTagTermCode(forward, raw) === resolveTagTermCode(reversed, raw), "命中 term");
});
check("正序键与反序键展示名一致（112×4）", () => {
  countTrue(pairs, ({ loc, raw }) => getTagName(forward, raw, loc) === getTagName(reversed, raw, loc), "展示名");
});
check("命中 term = 字典序最小的同名 owner（全部碰撞键 × 正/反序）", () => {
  countTrue(
    collisionRows,
    ({ name, owners }) => {
      const want = terms[name] ? name : owners[0]; // ① code 精确优先，② 否则同名取最小 code
      return resolveTagTermCode(forward, name) === want && resolveTagTermCode(reversed, name) === want;
    },
    "碰撞键取最小 code",
  );
});
check("折叠大小写碰撞（无精确命中）同样取字典序最小 code", () => {
  const cases = [...ownersByName.entries()].filter(([name, owners]) => owners.size > 1 && !terms[name]);
  assert.ok(cases.length > 0, "夹具里没有折叠大小写碰撞样例");
  const probes = cases.flatMap(([name, owners]) => {
    const upper = name.toUpperCase();
    if (upper === name.trim()) return []; // 无大小写差异（CJK）：跳过构造探针
    const minCode = [...owners].sort()[0];
    return [{ raw: upper, minCode, name }];
  });
  assert.ok(probes.length > 0, "没有可用于构造大小写探针的碰撞名");
  countTrue(
    probes,
    ({ raw, minCode }) =>
      resolveTagTermCode(forward, raw) === minCode && resolveTagTermCode(reversed, raw) === minCode,
    "折叠命中取最小 code",
  );
});
check("测试敏感性：反序键下“插入顺序首发”与“字典序最小”确实不同（该测试能抓住键序依赖）", () => {
  const cf = (name) => {
    for (const [code, names] of Object.entries(reversed.vocabularies.tags.terms)) {
      if (Object.values(names.names).some((v) => typeof v === "string" && v.trim() === name)) return code;
    }
    return "";
  };
  const sensitive = collisionRows.filter(({ name }) => !terms[name] && cf(name) !== resolveTagTermCode(reversed, name));
  assert.ok(sensitive.length > 0, "反序键下没有任何碰撞键会改变结果——测试不具敏感性");
});
check("点名碰撞样例：Light novel / 映画 / 交響詩 / symphonic poem", () => {
  const cases = [
    // en-US 名被 light_novel 与 light_novel_jp 同时声明 → 取最小 code
    { raw: "Light novel", code: "light_novel", ja: terms.light_novel["ja-JP"] },
    { raw: "映画", code: "film", ja: terms.film["ja-JP"] },
    { raw: "交響詩", code: "symphonic_poem", ja: terms.symphonic_poem["ja-JP"] },
    // 精确大小写唯一命中：symphonic_poem 的 en-US 是 "Symphonic poem"，大写 S 区分
    { raw: "symphonic poem", code: "symphonic_poem_english", ja: terms.symphonic_poem_english["ja-JP"] },
  ];
  for (const c of cases) {
    for (const defs of [forward, reversed]) {
      assert.equal(resolveTagTermCode(defs, c.raw), c.code, `${c.raw} 命中 code`);
      assert.equal(getTagName(defs, c.raw, "ja-JP"), c.ja, `${c.raw} ja-JP 展示名`);
    }
  }
});
check("展示名与命中 term 一致（112×4，正/反序）", () => {
  countTrue(
    pairs.flatMap(({ loc, raw }) => [
      { defs: forward, loc, raw },
      { defs: reversed, loc, raw },
    ]),
    ({ defs, loc, raw }) => {
      const code = resolveTagTermCode(defs, raw);
      const want = code ? resolveLocalizedName(terms[code], loc, raw) : raw;
      return getTagName(defs, raw, loc) === want;
    },
    "展示名与命中 term",
  );
});
check("未收录 tag 回退原值，且不随键序变化", () => {
  const unknown = `${rawTags.length}个未收录标签-${provenance.published_id}`;
  assert.equal(getTagName(forward, unknown, "zh-CN"), unknown);
  assert.equal(getTagName(reversed, unknown, "zh-CN"), unknown);
  assert.equal(resolveTagTermCode(forward, unknown), "");
  assert.equal(resolveTagTermCode(reversed, unknown), "");
});
check("getTagNames 批量结果与逐项一致（含空值剔除）", () => {
  const sample = [rawTags[0], "", rawTags[1], "未收录-测试"];
  assert.deepEqual(getTagNames(forward, sample, "zh-CN"), getTagNames(reversed, sample, "zh-CN"));
  assert.deepEqual(
    getTagNames(forward, sample, "zh-CN"),
    sample.map((t) => getTagName(forward, t, "zh-CN")).filter(Boolean),
  );
});
check("tagCode 对 112 个真实标签幂等", () => {
  countTrue(rawTags, (raw) => tagCode(raw) === raw, "tagCode 幂等");
});

console.log(`\n${failed === 0 ? "ALL PASS" : `${failed} FAILED`}（${checks} 组断言，${pairs.length * 2} 次解析比对）`);
process.exit(failed === 0 ? 0 : 1);
