#!/usr/bin/env node
/**
 * 设计令牌守门（挂 frontend job）——用项目自己的 tailwind.config.ts 编译一次 CSS，
 * 再校验"源码里实际用到的 utility 是否都生成了 CSS"。为什么必须有它：
 *
 *   上一轮的事故是"令牌注册了没生成"——text-muted-foreground、border-border、bg-card 这些
 *   类名在源码里写了 37 处，产出 CSS 里一条都没有（Tailwind 对未注册的颜色直接跳过），
 *   页面上表现为描边消失、卡片变透明，而 tsc / next build 全绿。同类事故还有第二半：
 *   颜色注册成裸 var(--x)（没有 <alpha-value>）时，bg-primary/10 这类透明度变体整批不生成。
 *   这两件事只有"编译 + 查选择器"能守住，所以本脚本把上轮临时工具固化成 CI 步骤。
 *
 * 四项检查：
 *   ① 生成：源码里出现的语义令牌与静态布局、控件、动效 utility（含 hover:/dark:/placeholder: 等变体与 /NN、
 *      /[0.0x] 透明度档）都必须在产出 CSS 里有选择器；
 *   ② 令牌清单：config 仍注册这些语义色，且注册值支持 <alpha-value>；
 *   ③ 变量定义：这些令牌的三通道变量在 globals.css 的深浅两套模式块里都定义了
 *      （只定义一边 = 另一种模式看不见，属于同一类"改了但不生效"）；
 *   ④ 迁移不回退：无条件（不带 dark: 前缀）的字面白/调色板色不得回到源码里。
 *
 * 运行：bun scripts/check-design-tokens.ts   （工作目录 frontend/，只依赖本地依赖，无网络）
 */
import { createRequire } from "node:module";
import { readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const postcss = require("postcss");
const tailwindcss = require("@tailwindcss/postcss");
const ts = require("typescript-api");
const { default: config } = await import("../tailwind.config.ts");

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const srcDir = path.join(root, "src");

// —— 语义色清单：utility 的前缀 + 色名 ——
// 前缀是 Tailwind 里会吃颜色名的那些；色名按"最长优先"匹配（line-subtle 先于 line）。
const COLOR_UTILITY_PREFIX = /^(?:text|bg|border|divide|outline|ring|from|via|to|fill|stroke|shadow|accent|caret|decoration|placeholder)$/;
const SEMANTIC_COLORS = [
  "surfaceHover", "surfaceSubtle", "surfaceBorder",
  "emphasis", "warn", "success", "danger", "info", "alt",
  "line-subtle", "line-strong", "line",
  "text-strong", "text-body", "text-muted", "text-faint",
  "background", "foreground", "card", "secondary", "destructive", "primary", "muted",
].sort((a, b) => b.length - a.length);
// 注册值必须带 <alpha-value>（否则 /10 这类透明度变体整批不生成）；surfaceBorder 是整值描边，例外。
const ALPHA_REQUIRED = ["foreground", "card", "muted", "secondary", "border", "theme", "destructive", "primary",
  "emphasis", "warn", "success", "danger", "info", "alt"];
// 令牌的三通道变量：globals.css 的深色块与浅色块都要有一份。
const REQUIRED_VARS = ["--emphasis-rgb", "--state-warn-rgb", "--state-warn-soft-rgb", "--state-success-rgb",
  "--state-success-soft-rgb", "--state-danger-rgb", "--state-danger-soft-rgb", "--state-info-rgb",
  "--state-info-soft-rgb", "--state-alt-rgb", "--state-alt-soft-rgb", "--card-rgb", "--muted-rgb",
  "--muted-foreground-rgb", "--secondary-rgb", "--border-rgb", "--destructive-rgb",
  "--destructive-foreground-rgb", "--foreground-rgb", "--primary-rgb", "--primary-contrast-rgb"];

const fail = [];
const note = (ok, msg) => console.log((ok ? "ok   " : "FAIL ") + msg);
const bad = (msg) => { fail.push(msg); console.log("FAIL " + msg); };

// —— ① 编译一次 ——
const cssPath = path.join(srcDir, "app", "globals.css");
const css = (await postcss([tailwindcss({ base: root, optimize: false })])
  .process(readFileSync(cssPath, "utf8"), { from: cssPath })).css;

const escapeClass = (cls) => "." + cls.replace(/[^a-zA-Z0-9-]/g, (c) => "\\" + c);
const selectorParser = require("postcss-selector-parser");
const generatedClasses = new Set();
postcss.parse(css).walkRules((rule) => {
  selectorParser((selectors) => selectors.walkClasses((node) => generatedClasses.add(node.value))).processSync(rule.selector);
});
const hasSelector = (cls) => generatedClasses.has(cls);

// —— 收集源码里的候选 token（含变体前缀；逐文件行号便于定位）——
const files = [];
(function walk(dir) {
  for (const e of readdirSync(dir)) {
    const p = path.join(dir, e);
    if (statSync(p).isDirectory()) walk(p);
    else if (/\.(ts|tsx)$/.test(p)) files.push(p);
  }
})(srcDir);
// 左边界保证不会从长 token 中间切一刀（hover:text-white 整体作为一个 token 命中的前提），
// 右边界排除 - / [ 让 text-white 不吞掉 text-white/60。
const TOKEN_RE = /(^|[\s"'\u0060({])((?:[a-zA-Z0-9-]+:)*)((?:[a-z][a-zA-Z0-9-]*)(?:\/[^\s"'\u0060)}]+)?)/g;
const STATUS_COLORS = "amber|yellow|orange|emerald|green|teal|rose|red|sky|blue|purple|violet|fuchsia|indigo|pink|cyan|lime";
const STATUS_RE = new RegExp("^(" + STATUS_COLORS + ")-(\\d{2,3})$");
const GRAY_RE = /^gray-(100|200|300|400|500)$/;

let tokensSeen = 0, tokensOk = 0;
const migrationLeft = [];
for (const file of files) {
  const src = readFileSync(file, "utf8");
  const rel = path.relative(root, file).replace(/\\/g, "/");
  const lineOf = (idx) => src.slice(0, idx).split("\n").length;
  let m;
  TOKEN_RE.lastIndex = 0;
  while ((m = TOKEN_RE.exec(src))) {
    const [, lead, variants, body] = m;
    // dark: 是"深色专用"的语义（可以带更多变体，如 dark:hover:），按变体段判断而不是整串匹配。
    const isDark = variants.split(":").includes("dark");
    const slash = body.indexOf("/");
    const name = slash < 0 ? body : body.slice(0, slash);
    const dash = name.indexOf("-");
    const prefix = dash < 0 ? name : name.slice(0, dash);
    const color = dash < 0 ? "" : name.slice(dash + 1);
    const hit = SEMANTIC_COLORS.find((c) => color === c || color.startsWith(c + "-") || color.startsWith(c + "["));
    if (COLOR_UTILITY_PREFIX.test(prefix) && hit) {
      tokensSeen++;
      if (hasSelector(variants + body)) tokensOk++;
      else bad("令牌类名没有产出 CSS：" + variants + body + "  (" + rel + ":" + lineOf(m.index) + ")");
    }
    // —— ④ 迁移不回退 ——
    const where = (why) => migrationLeft.push(rel + ":" + lineOf(m.index) + "  " + variants + body + "  → " + why);
    if (!isDark) {
      if ((prefix === "text" || prefix === "bg" || prefix === "border" || prefix === "divide" || prefix === "ring") && color === "white") {
        const lit = src.slice(Math.max(0, m.index - 240), m.index + 240);
        if (/(^|:)selection:$/.test(variants)) { /* 与 selection:bg-primary 成对：浅色下白字叠主色达标，保留 */ }
        else if (name === "bg-white" && slash < 0) { /* 刻意的白色按钮（配 text-black），保留 */ }
        else if (/(^|[\s"'\u0060])(hover:)?bg-primary(?![\w/-])/.test(lit)) { /* 主色底上的字色由每套配色的对照色决定，保留 */ }
        else where(prefix === "text" ? "用 text-emphasis" : "用 line / emphasis 令牌");
      }
      if (prefix === "text" && GRAY_RE.test(color)) where("用 text-text-strong/body/muted/faint");
      const st = STATUS_RE.exec(color);
      if (prefix === "text" && st && Number(st[2]) >= 100 && Number(st[2]) <= 400) where("用 text-warn/success/danger/info/alt（-soft 为浅一档）");
    }
    if (isDark && prefix === "text") {
      const st = STATUS_RE.exec(color);
      if (st && (Number(st[2]) === 300 || Number(st[2]) === 400)) where("dark:text-{family}-{300,400} 用 dark:text-warn 等");
    }
  }
}
note(true, "① 生成：源码语义令牌 utility " + tokensOk + "/" + tokensSeen + " 有产出 CSS");

// 尺寸、布局、圆角与动效同样会因版本差异静默失效。用 TS AST 读静态类名片段，
// 排除注释；项目 CSS 自定义的类也算有效。模板边界中的半个类名交给调用方补全。
const globals = readFileSync(path.join(srcDir, "app", "globals.css"), "utf8");
const utilityPattern = /^(?:(?:[\w/-]+|\[[^\]]+\]):)*!?-?(?:size|fade|zoom-in|slide-in-from|w|h|min-w|min-h|max-w|max-h|p[xytrblse]?|m[xytrblse]?|gap(?:-[xy])?|space-[xy]|rounded(?:-[trblse]{1,2})?|animate|duration|ease|opacity|z|grid-cols|grid-rows|col-span|row-span|bg|text|border(?:-[trblsexy])?|shadow|from|via|to|ring|outline|divide-[xy]|translate-[xy]|scale|transition|font|tracking|leading|blur|backdrop-blur)-[^\s{}]+$/;
const checked = new Set();
for (const file of files) {
  const source = ts.createSourceFile(file, readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true);
  const visit = (node) => {
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node) || ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node)) {
      const templatePart = ts.isTemplateHead(node) || ts.isTemplateMiddle(node) || ts.isTemplateTail(node);
      const parts = node.text.split(/\s+/);
      parts.forEach((token, index) => {
        if (templatePart && ((index === 0 && !/^\s/.test(node.text)) || (index === parts.length - 1 && !/\s$/.test(node.text)))) return;
        if (!utilityPattern.test(token) || checked.has(token)) return;
        checked.add(token);
        if (hasSelector(token) || globals.includes(escapeClass(token))) return;
        const line = source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1;
        bad("布局/控件类名没有产出 CSS：" + token + " (" + path.relative(root, file).replace(/\\/g, "/") + ":" + line + ")");
      });
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
}
note(true, "① 生成：另核验 " + checked.size + " 个静态布局/控件/动效类名");

// —— ② 令牌清单 ——
const colors = (config.theme && config.theme.extend && config.theme.extend.colors) || {};
const valueOf = (v) => (v && typeof v === "object" ? Object.entries(v).map(([k, x]) => k + "=" + x).join(" ") : String(v));
for (const name of ALPHA_REQUIRED) {
  const entry = colors[name];
  if (!entry) bad("② 令牌清单：tailwind.config.ts 里没有注册颜色 \"" + name + "\"");
  else if (!valueOf(entry).includes("<alpha-value>")) bad("② 令牌清单：" + name + " 的注册值没有 <alpha-value>，透明度变体不会生成（" + valueOf(entry) + "）");
}
const softs = ["warn", "success", "danger", "info", "alt"];
for (const s of softs) if (!colors[s] || colors[s].soft === undefined) bad("② 令牌清单：状态色 " + s + " 缺少 soft 档");
for (const s of softs) {
  if (colors[s] && !String(colors[s].DEFAULT || "").includes("<alpha-value>")) bad("② 令牌清单：" + s + ".DEFAULT 不支持透明度");
}
note(true, "② 令牌清单：" + ALPHA_REQUIRED.length + " 个语义色 + 5 组状态色 base/soft 已注册且支持透明度变体");

// —— ③ 变量定义（两套模式都要有）——
for (const v of REQUIRED_VARS) {
  const n = globals.split(new RegExp(v + ":", "g")).length - 1;
  if (n < 2) bad("③ 变量定义：" + v + " 在 globals.css 里只出现 " + n + " 次（深/浅两套模式块各要一次）");
}
note(true, "③ 变量定义：" + REQUIRED_VARS.length + " 个三通道变量在深浅两套模式块里都定义了");

// —— ④ 迁移不回退 ——
if (migrationLeft.length) {
  for (const line of migrationLeft.slice(0, 25)) console.log("     " + line);
  bad("④ 迁移不回退：仍有 " + migrationLeft.length + " 处无条件调色板/字面白工具类（上一轮已迁走，别再写回来）");
} else {
  note(true, "④ 迁移不回退：源码里没有无条件字面白 / 调色板 100-400 档 / 灰阶 100-500");
}

console.log("");
if (fail.length) {
  console.error("设计令牌守门失败：" + fail.length + " 项");
  process.exit(1);
}
console.log("设计令牌守门通过");
