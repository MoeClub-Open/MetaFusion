#!/usr/bin/env node
// 页面外壳自检：页面/组件不得自写「容器宽度」与「页面级内边距」。
//
// 背景：页面各自写 max-w-* / px-* 时，同一份内容在首页、详情页、管理台会落在不同的
// 左边界上（同一实体的 /works/[id] 与 /catalog/[id] 观感不一致就是这么来的）。
// 容器宽度与水平内边距的唯一出处是 frontend/src/components/ui/PageShell.tsx，
// 页签面板（重挂载 + 进入动画）的唯一出处是 frontend/src/components/ui/TabPanel.tsx。
//
// 规则：
//   R1 宽自己写   —— 同一 className 里同时出现 mx-auto 与 max-w-*
//   R2 main 内边距 —— <main> 上直接写 p-/px-/py-/pl-/pr-/pt-/pb-
//   R3 外壳令牌泄漏 —— max-w-page / max-w-narrow 出现在 PageShell.tsx 之外
//   R4 手写页签面板 —— .mf-tabpanel 出现在 TabPanel.tsx 之外
//   R5 页面局部样式 —— app/**/*.module.css 里声明 max-width / padding
//
// 用法：bun scripts/check_page_shell.ts   （仓库根目录；CI 与本地同命令）
// 退出码：0 = 无违规；1 = 有违规。
import fs from "node:fs";
import path from "node:path";
import process from "node:process";

const ROOT = path.resolve(path.dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1")), "..");
const SRC = path.join(ROOT, "frontend", "src");
const SHELL = "frontend/src/components/ui/PageShell.tsx";
const TABPANEL = "frontend/src/components/ui/TabPanel.tsx";

// 例外白名单：每条都要写清"为什么它不是页面级容器"。
const ALLOW_FILES = {
  [SHELL]: { rules: ["R1", "R2", "R3"], reason: "外壳组件本身，是全站 max-w/px 的唯一出处" },
  [TABPANEL]: { rules: ["R4"], reason: "页签面板组件本身，是 .mf-tabpanel 的唯一出处" },
  "frontend/src/components/graph/InteractiveRelationGraph.tsx": {
    rules: ["R1", "R2"],
    reason: "全屏关系图视图：按视口铺满并覆盖导航栏，宽度不由页面外壳决定",
  },
  "frontend/src/app/error.tsx": { rules: ["R2"], reason: "Next 全局错误边界，不参与页面外壳（没有 Navbar 与外壳上下文）" },
  "frontend/src/app/global-error.tsx": { rules: ["R2"], reason: "Next 全局错误边界（根布局失败时渲染，外壳不可用）" },
  "frontend/src/app/not-found.tsx": { rules: ["R2"], reason: "404 兜底页，独立于页面外壳" },
};
// 内容级例外：明确到"这段类名为什么可以自己限宽"。
const ALLOW_CLASS = [
  {
    file: "frontend/src/app/setup/page.tsx",
    match: /max-w-(md|lg) mx-auto/,
    rule: "R1",
    reason: "整屏初始化向导里的居中短文本（副标题），属于阅读列内的排版限宽，不是页面容器",
  },
];

const CLASS_RE = /className=(?:"([^"]*)"|\{`([^`]*)`\})/g;

function walk(dir, filter, out = []) {
  if (!fs.existsSync(dir)) return out;
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(p, filter, out);
    else if (filter(entry.name)) out.push(p);
  }
  return out;
}

const rel = (p) => path.relative(ROOT, p).split(path.sep).join("/");
const allowed = (file, rule, text) => {
  const f = ALLOW_FILES[file];
  if (f && f.rules.includes(rule)) return true;
  return ALLOW_CLASS.some((a) => a.file === file && a.rule === rule && a.match.test(text));
};

const violations = [];
const report = (file, line, rule, detail, text) => {
  if (allowed(file, rule, text)) return;
  violations.push({ file, line, rule, detail });
};

const tsxFiles = [
  ...walk(path.join(SRC, "app"), (n) => n.endsWith(".tsx")),
  ...walk(path.join(SRC, "components"), (n) => n.endsWith(".tsx")),
];

for (const abs of tsxFiles) {
  const file = rel(abs);
  const lines = fs.readFileSync(abs, "utf8").split("\n");
  lines.forEach((line, i) => {
    const n = i + 1;
    for (const m of line.matchAll(CLASS_RE)) {
      const cls = m[1] ?? m[2] ?? "";
      if (/\bmx-auto\b/.test(cls) && /\bmax-w-[^\s"']+/.test(cls)) {
        report(file, n, "R1", "className 里同时写了 mx-auto 与 max-w-*：容器宽度应交给 PageShell/PageContainer", cls);
      }
    }
    if (/<main\b/.test(line) && /\b(p|px|py|pl|pr|pt|pb)-[0-9]/.test(line)) {
      report(file, n, "R2", "<main> 上自写内边距：页面级内边距应交给 PageShell", line);
    }
    if (/\bmax-w-(page|narrow)\b/.test(line)) {
      report(file, n, "R3", "max-w-page / max-w-narrow 只允许出现在 PageShell.tsx", line);
    }
    // R4 只看 className（注释里提到 .mf-tabpanel 不算手写面板）。
    for (const m of line.matchAll(CLASS_RE)) {
      const cls = m[1] ?? m[2] ?? "";
      if (/\bmf-tabpanel\b/.test(cls)) {
        report(file, n, "R4", "手写 .mf-tabpanel：页签面板请用 <TabPanel activeKey=...>（key 重放由组件保证）", cls);
      }
    }
  });
}

for (const abs of walk(path.join(SRC, "app"), (n) => n.endsWith(".module.css"))) {
  const file = rel(abs);
  fs.readFileSync(abs, "utf8").split("\n").forEach((line, i) => {
    if (/^\s*(max-width|padding(-(left|right|top|bottom))?)\s*:/.test(line)) {
      report(file, i + 1, "R5", "页面局部样式里声明了容器宽度/内边距：应交给 PageShell", line);
    }
  });
}

violations.sort((a, b) => (a.file === b.file ? a.line - b.line : a.file < b.file ? -1 : 1));
if (violations.length === 0) {
  console.log("页面外壳自检通过：0 违规（" + tsxFiles.length + " 个 tsx + app 下 .module.css 已扫）");
  process.exit(0);
}
for (const v of violations) console.log(v.file + ":" + v.line + "  [" + v.rule + "] " + v.detail);
console.log("\n共 " + violations.length + " 处违规：页面容器宽度/内边距必须来自 PageShell（页签面板来自 TabPanel）。");
process.exit(1);
