#!/usr/bin/env node
// JSX 裸文案检查：ci.yml 的 i18n keys sync check 只比对四个字典的键集合，
// 抓不到写死在 JSX 里的文案——landing 与管理页的加载提示曾因此漏过检查。
//
// 用 TypeScript 自己的解析器取真正的 JsxText 节点：用正则（>…<）会把泛型参数、比较运算、
// 类型断言全当成文本节点，误报会淹掉信号。
//
// 判据（白名单式，宁少报不误报）：
//   1. 只报**多词**（>=2 个含字母的词）或**带省略号**的拉丁串——词级标签（"Deps:"、"TOTAL"、
//      "UUID:"）与纯标识另有口径，不在本检查里；
//   2. 中日韩裸串不报：JSX 里"共 {n} 项"这类混排很多，且中文裸串另有审查口径；
//   3. ALLOWED 是"不是文案"的标识/品牌，BASELINE 是检查上线时**已存在**的裸文案（欠账清单，
//      不是许可）——新出现的一条会被拦下，由人决定"进字典"还是"进白名单并写明理由"。
// 用法：bun scripts/check_jsx_literals.ts [--selftest]
import { readFileSync, readdirSync, statSync, writeFileSync, unlinkSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript-api";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

// 不是文案：品牌、域名、仓库地址、技术名。
const ALLOWED = new Set([
  "MetaFusion Forum",
  "MetaFusion", // 站名：与四语字典里的同名值一致，改名时两边一起改
  "MetaBrainz", // 上游项目署名
  "MusicBrainz",
  "Bangumi",
  "findverse.cc", // 站点域名
  "github.com/MoeclubM/MetaFusion",
  "Open API", // /docs 入口的技术叫法
]);

// 检查上线时已存在的裸文案：止住新增用，不是允许。修一条删一条（脚本会提示）。
const BASELINE = new Set([
  "APPLICATION ERROR", // error.tsx 装饰字（与 not-found 的装饰字同一形态）
  "NOT FOUND", // not-found.tsx 装饰字
]);

/** 收集 JSX 文本节点（TS 解析器给出的真节点，不是正则猜的）。 */
function jsxTextNodes(file) {
  const src = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const out = [];
  const visit = (node) => {
    if (ts.isJsxText(node)) {
      const text = node.text.replace(/\s+/g, " ").trim();
      if (text) out.push({ text, line: src.getLineAndCharacterOfPosition(node.getStart(src)).line + 1 });
    }
    ts.forEachChild(node, visit);
  };
  visit(src);
  return out;
}

export function isSuspect(text) {
  if (ALLOWED.has(text)) return false;
  // 只有标签/实体/标点，没有字母：不是文案。
  if (!/[A-Za-z]/.test(text)) return false;
  const words = text.split(/\s+/).filter((w) => /[A-Za-z]/.test(w));
  if (words.length >= 2) return true;
  return /[…]$|\.\.\.$/.test(text) && words.length >= 1;
}

function walk(dir, out = []) {
  for (const name of readdirSync(dir)) {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (name.endsWith(".tsx")) out.push(full);
  }
  return out;
}

function scan() {
  const fresh = [];
  const stale = new Set(BASELINE);
  for (const file of walk(join(root, "src"))) {
    for (const node of jsxTextNodes(file)) {
      if (!isSuspect(node.text)) continue;
      if (BASELINE.has(node.text)) {
        stale.delete(node.text);
        continue;
      }
      fresh.push({ file: relative(root, file), line: node.line, text: node.text });
    }
  }
  return { fresh, stale };
}

if (process.argv.includes("--selftest")) {
  // 自检：必须抓得到当初漏掉的那类裸文案，也必须放过白名单、基线与非拉丁文本。
  const sample = join(root, ".selftest.tsx");
  const cases = [
    ["<div>Loading…</div>", true],
    ["<div>Loading Admin...</div>", true],
    ["<span>Something went wrong</span>", true],
    ["<span>{t(\"common.loading\")}</span>", false],
    ["<i>MetaFusion</i>", false],
    ["<b>共 {n} 项</b>", false],
    ["<u>UUID:</u>", false],
    ["<s>NOT FOUND</s>", false], // 已在 BASELINE：基线条目不算新增
  ];
  for (const [jsx, want] of cases) {
    writeFileSync(sample, "export const X = () => (" + jsx + ");\n");
    const got = jsxTextNodes(sample).some((n) => isSuspect(n.text) && !BASELINE.has(n.text));
    unlinkSync(sample);
    if (got !== want) {
      console.error("selftest failed:", jsx, "expected", want, "got", got);
      process.exit(1);
    }
  }
  console.log("selftest ok");
  process.exit(0);
}

const { fresh, stale } = scan();
for (const key of stale) console.log("BASELINE 里的这条已不存在，请从 BASELINE 删掉：" + key);
if (fresh.length > 0) {
  console.error("JSX 里出现了疑似写死的文案（请走 useI18n() + 四语字典；若确是标识，加进 ALLOWED 并写明理由）：");
  for (const h of fresh) console.error(`  ${h.file}:${h.line}  ${h.text}`);
  process.exit(1);
}
console.log("jsx literals in sync");
