// 将主站主题作为静态源码同步到独立账号应用；账号构建与运行不依赖主仓。
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import postcss from "postcss";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const target = path.resolve(root, "../../metafusion-auth/user");
const check = process.argv.includes("--check");
const stale: string[] = [];
const header = "由 MetaFusion/frontend/scripts/sync-account-theme.ts 同步，勿手改。";

function emit(relative: string, content: string) {
  const destination = path.join(target, relative);
  if (fs.existsSync(destination) && fs.readFileSync(destination, "utf8") === content) return;
  if (check) { stale.push(relative); return; }
  fs.mkdirSync(path.dirname(destination), { recursive: true });
  fs.writeFileSync(destination, content);
}

function copy(source: string, destination = source) {
  emit(destination, fs.readFileSync(path.join(root, source), "utf8"));
}

for (const name of ["ThemeControls", "ThemePicker", "ThemePopover", "ThemeModeControls", "ThemeModeSwitcher"]) {
  copy(`src/components/${name}.tsx`);
}
copy("src/lib/themeContext.tsx");
copy("src/lib/theme.generated.ts");
copy("src/app/theme.generated.css", "src/shared/theme/theme.generated.css");
copy("tailwind.config.ts", "src/shared/theme/tailwind.config.ts");
for (const name of ["catalog", "dots"]) copy(`public/patterns/${name}.svg`);
// 内联引导避免独立应用与主站的 public 路径发生路由冲突。
emit("src/shared/theme/boot.generated.ts", `// ${header}\nexport const themeBootScript = ${JSON.stringify(fs.readFileSync(path.join(root, "public/theme-boot.js"), "utf8"))};\n`);

// 只同步全局令牌、表面、底纹与原生表单样式，不带目录/文章等业务样式。
const stylesheet = postcss.parse(fs.readFileSync(path.join(root, "src/app/globals.css"), "utf8"));
const shared = postcss.root();
for (const node of stylesheet.nodes) {
  if (node.type === "comment" && node.text.includes("Typography utilities")) break;
  if (node.type === "comment" || (node.type === "atrule" && ["import", "config"].includes(node.name))) continue;
  if (node.type === "rule" && (node.selector.includes(".article-") || ["html.light body", "html.light .keep-white", "html.light .bg-primary.text-white:not(.keep-white)"].includes(node.selector))) continue;
  const block = node.clone();
  if ("walkComments" in block) block.walkComments((comment) => { comment.remove(); });
  shared.append(block);
}
stylesheet.walkRules(".mf-focus", (rule) => {
  shared.append(postcss.atRule({ name: "layer", params: "components" }).append(rule.clone()));
});
emit("src/shared/theme/globals.css", `/* ${header} */\n@import "./theme.generated.css";\n@import "tailwindcss";\n@config "./tailwind.config.ts";\n\n${shared.toString().trim()}\n\n@media (prefers-reduced-motion: reduce) {\n  html { scroll-behavior: auto; }\n  *, *::before, *::after { animation-duration: 0.01ms !important; transition-duration: 0.01ms !important; }\n}\n`);

for (const locale of ["zh-CN", "en-US", "zh-TW", "ja-JP"]) {
  const relative = `src/messages/${locale}.json`;
  const current = JSON.parse(fs.readFileSync(path.join(target, relative), "utf8"));
  const source = JSON.parse(fs.readFileSync(path.join(root, relative), "utf8"));
  for (const [key, value] of Object.entries(source)) {
    if (key.startsWith("theme.") || key === "revisions.close") current[key] = value;
  }
  emit(relative, JSON.stringify(current, null, 2) + "\n");
}

if (stale.length) {
  console.error("账号主题尚未同步：\n" + stale.join("\n"));
  process.exit(1);
}
console.log(check ? "账号主题与主站一致" : "已同步账号主题");
