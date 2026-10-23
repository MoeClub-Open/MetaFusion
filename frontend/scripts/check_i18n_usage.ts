#!/usr/bin/env node
// i18n 使用面检查（CI 门禁 + 清理取证工具），方向有两个：
//
//   1) 悬空键（**失败**）：代码里 t()/tr() 的字面量键必须在四语字典里都存在。
//      只比四份字典的键集合（ci.yml 的键集比对）抓不到反向问题——键只在一边定义、
//      代码引用了字典里根本没有的键，界面上就会把裸 key 显示出来。
//      动态拼接键（t(\`catalog.\${k}\`)）无法静态判定取值集合，按前缀白名单放行，
//      同时要求该前缀下至少有一个键存在（否则前缀本身写错了）。
//
//   2) 死键（**默认只报告**，--strict-dead 才失败）：代码里既没有字面量出现、
//      也不在任何动态拼接前缀覆盖下的字典键。判据刻意保守（宁少报不误报）：
//        - 字面量扫描覆盖整个仓库（backend / deploy / docs / scripts / frontend），
//          所以测试夹具、文档、脚本里的键也算"有人用"；
//        - 动态前缀从**代码里所有模板字面量**取静态前缀（不只 t() 调用点），
//          因为 labelKey 之类的字段常是 \`theme.\${id}\` 形式在别处拼好后传进 t()。
//      剩余集合是清理候选，不是"已证明无引用"：真正删除前要跑 --list-dead 人工过一遍，
//      并用 --prune 按同一判据删除（会拒绝四语键集合不一致的字典）。
//
// 用法：
//   bun scripts/check_i18n_usage.ts            # CI：悬空键失败 + 死键计数报告
//   bun scripts/check_i18n_usage.ts --list-dead
//   bun scripts/check_i18n_usage.ts --strict-dead
//   bun scripts/check_i18n_usage.ts --prune    # 按判据删死键（四语保持一致）
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join, extname, dirname, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const SRC = join(ROOT, "frontend", "src");
const LOCALES = ["zh-CN", "en-US", "zh-TW", "ja-JP"];
const SKIP_DIRS = new Set(["node_modules", ".git", ".next", "dist", "build", "docs-local", "undefined", ".venv", "__pycache__"]);
const SCAN_EXT = new Set([".ts", ".tsx", ".js", ".json", ".py", ".go", ".yml", ".yaml", ".sh", ".sql", ".conf", ".md"]);

const argv = process.argv.slice(2);
const flag = (n) => argv.includes(n);

function walk(dir, out = []) {
  let entries = [];
  try { entries = readdirSync(dir, { withFileTypes: true }); } catch { return out; }
  for (const e of entries) {
    if (e.isDirectory()) { if (!SKIP_DIRS.has(e.name)) walk(join(dir, e.name), out); }
    else if (SCAN_EXT.has(extname(e.name))) out.push(join(dir, e.name));
  }
  return out;
}

// ---- 字典 ----
const dicts = {};
for (const l of LOCALES) {
  const p = join(SRC, "messages", `${l}.json`);
  let raw;
  try { raw = JSON.parse(readFileSync(p, "utf8")); }
  catch (e) { console.error(`失败：${relative(ROOT, p)} 无法解析：${e.message}`); process.exit(1); }
  dicts[l] = raw;
}
const baseKeys = new Set(Object.keys(dicts[LOCALES[0]]));
let parityBroken = false;
for (const l of LOCALES.slice(1)) {
  const cur = new Set(Object.keys(dicts[l]));
  const missing = [...baseKeys].filter((k) => !cur.has(k));
  const extra = [...cur].filter((k) => !baseKeys.has(k));
  if (missing.length || extra.length) {
    parityBroken = true;
    console.error(`失败：四语不一致 ${LOCALES[0]} ↔ ${l}：缺 ${missing.length}、多 ${extra.length}`);
    for (const k of [...missing, ...extra].slice(0, 10)) console.error(`  ${k}`);
  }
}

// ---- 代码扫描 ----
const files = walk(ROOT).filter((f) => !f.startsWith(join(SRC, "messages")));
// 前缀只从界面代码取：仓库里还有 vendored 的第三方 js（backend/internal/catalog/docsassets/），
// 它的模板字面量会拼出 "The " 这种假前缀，把白名单撑坏。
const uiFiles = files.filter((f) => (f.startsWith(SRC) || f.includes(join("frontend", "scripts"))) && [".ts", ".tsx", ".js"].includes(extname(f)));
const blob = files.map((f) => { try { return readFileSync(f, "utf8"); } catch { return ""; } }).join("\n");

// 1) t()/tr() 里的字面量键
const KEY_RE = /^[a-z][A-Za-z0-9_]*(\.[A-Za-z0-9_-]+)+$/;
// 前缀形状：小写命名空间段，以点结尾（catalog.kind. / theme.），挡住模板字面量里的散文串。
const PREFIX_RE = /^[a-z][A-Za-z0-9_]*(\.[A-Za-z0-9_-]+)*\.$/;
const used = new Map(); // key -> 首个出现位置
const dynamicPrefixes = new Map(); // prefix -> 首个出现位置
for (const f of uiFiles) {
  const text = readFileSync(f, "utf8");
  const rel = relative(ROOT, f);
  for (const m of text.matchAll(/(^|[^\w.$])(t|tr)\(\s*["']([^"'\n]+)["']/g)) {
    if (KEY_RE.test(m[3])) if (!used.has(m[3])) used.set(m[3], rel);
  }
  for (const m of text.matchAll(/(^|[^\w.$])(t|tr)\(\s*`([^`\\]*)\$\{/g)) {
    if (m[3] && PREFIX_RE.test(m[3]) && !dynamicPrefixes.has(m[3])) dynamicPrefixes.set(m[3], rel);
  }
}
// 2) 任意模板字面量的静态前缀（labelKey: \`theme.\${id}\` 这类在别处拼好后传进 t()）
const allPrefixes = new Set(dynamicPrefixes.keys());
for (const f of uiFiles) {
  const text = readFileSync(f, "utf8");
  for (const m of text.matchAll(/`([^`\\]*)\$\{/g)) {
    if (m[1] && PREFIX_RE.test(m[1])) allPrefixes.add(m[1]);
  }
}

// ---- 方向 1：悬空键 ----
const dangling = [...used.keys()].filter((k) => !baseKeys.has(k));
const prefixWithNoKey = [...dynamicPrefixes.keys()].filter((p) => ![...baseKeys].some((k) => k.startsWith(p) && k.length > p.length));

// ---- 方向 2：死键 ----
const dead = [];
for (const k of baseKeys) {
  if (used.has(k)) continue;
  if (blob.includes(k)) continue;
  if ([...allPrefixes].some((p) => k.startsWith(p) && k.length > p.length)) continue;
  dead.push(k);
}
dead.sort();

// ---- 输出 ----
const dynamicNote = `${dynamicPrefixes.size} 个 t() 动态前缀`;
if (dangling.length) {
  console.error(`失败：代码引用但四语字典里没有的键 ${dangling.length} 个`);
  for (const k of dangling) console.error(`  ${k}  （首次出现：${used.get(k)}）`);
}
if (prefixWithNoKey.length) {
  console.error(`失败：动态前缀下没有任何字典键（前缀或字典写错了）${prefixWithNoKey.length} 个`);
  for (const p of prefixWithNoKey) console.error(`  ${p}  （首次出现：${dynamicPrefixes.get(p)}）`);
}
if (parityBroken) process.exit(1);

if (flag("--list-dead")) {
  for (const k of dead) console.log(`${k}\t${dicts[LOCALES[0]][k]}`);
}
const byNs = {};
for (const k of dead) { const ns = k.split(".")[0]; byNs[ns] = (byNs[ns] || 0) + 1; }
const topNs = Object.entries(byNs).sort((a, b) => b[1] - a[1]).slice(0, 8).map(([n, c]) => `${n}=${c}`).join(" ");

if (flag("--prune")) {
  if (parityBroken) { console.error("拒绝删除：四语键集合不一致，先修 parity"); process.exit(1); }
  if (!dead.length) { console.log("没有死键，未改动字典"); process.exit(0); }
  const drop = new Set(dead);
  for (const l of LOCALES) {
    const kept = {};
    for (const [k, v] of Object.entries(dicts[l])) if (!drop.has(k)) kept[k] = v;
    const p = join(SRC, "messages", `${l}.json`);
    writeFileSync(p, JSON.stringify(kept, null, 2) + "\n", "utf8");
    console.log(`${l}: 删除 ${Object.keys(dicts[l]).length - Object.keys(kept).length} 键`);
  }
  console.log(`已按判据删除 ${dead.length} 个死键（四语一致）`);
  process.exit(0);
}

console.log(`i18n usage: 字典键 ${baseKeys.size}、代码字面量引用 ${used.size}、${dynamicNote}；悬空键 ${dangling.length}、死键 ${dead.length}（${topNs}）`);
if (dead.length) console.log("死键是清理候选，不是已证明无引用：bun scripts/check_i18n_usage.ts --list-dead 查看，--prune 按同一判据删除");
if (flag("--strict-dead") && dead.length) { console.error(`失败：存在 ${dead.length} 个死键（--strict-dead）`); process.exit(1); }
process.exit(dangling.length || prefixWithNoKey.length || parityBroken ? 1 : 0);
