// 主题实现与静态资产由主站维护；各应用提交同步结果，独立构建、独立运行。
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const check = process.argv.includes("--check");
const stale: string[] = [];
const header = "由 MetaFusion/frontend/scripts/sync-ui-theme.ts 同步，勿手改。";
const read = (file: string) => fs.readFileSync(file, "utf8").replace(/\r\n/g, "\n");
const source = (relative: string) => read(path.join(root, relative));
const apps = [
  { name: "auth-user", dir: "metafusion-auth/user", i18n: "@/i18n/I18nProvider", config: "src/shared/theme/tailwind.config.ts", base: "" },
  { name: "auth-admin", dir: "metafusion-auth/admin", i18n: "@/i18n/I18nProvider", config: "src/shared/theme/tailwind.config.ts", base: "/admin/account" },
  { name: "community-admin", dir: "metafusion-community/admin", i18n: "@/lib/i18n/provider", config: "tailwind.config.ts", base: "/admin/community" },
  { name: "storage-admin", dir: "metafusion-storage/admin", i18n: "@/shared/i18n/I18nProvider", config: "tailwind.config.ts", base: "/admin/storage" },
];
function emit(target: string, relative: string, content: string) {
  const destination = path.join(target, relative);
  if (fs.existsSync(destination) && read(destination) === content) return;
  if (check) { stale.push(path.relative(path.resolve(root, "../.."), destination)); return; }
  fs.mkdirSync(path.dirname(destination), { recursive: true });
  fs.writeFileSync(destination, content);
}
const boot = source("public/theme-boot.js");
for (const app of apps) {
  const target = path.resolve(root, "../..", app.dir);
  for (const name of ["ThemeControls", "ThemePicker", "ThemePopover", "ThemeModeControls", "ThemeModeSwitcher"]) {
    emit(target, `src/components/${name}.tsx`, source(`src/components/${name}.tsx`).replaceAll("@/i18n/I18nProvider", app.i18n));
  }
  for (const name of ["themeContext.tsx", "theme.ts", "theme.generated.ts"]) emit(target, `src/lib/${name}`, source(`src/lib/${name}`));
  for (const name of ["theme.generated.css", "theme.tokens.css", "theme.base.css"]) {
    emit(target, `src/shared/theme/${name}`, source(`src/app/${name}`).replaceAll('url("/patterns/', `url("${app.base}/patterns/`));
  }
  emit(target, app.config, source("tailwind.config.ts"));
  if (app.name.endsWith("-admin")) emit(target, "src/shared/theme/admin.css", source("src/app/admin/admin.css"));
  const config = path.relative("src/shared/theme", app.config).replaceAll("\\", "/");
  emit(target, "src/shared/theme/globals.css", `/* ${header} */\n@import "./theme.generated.css";\n@import "tailwindcss";\n@import "./theme.tokens.css";\n@import "./theme.base.css";\n${app.name.endsWith("-admin") ? '@import "./admin.css";\n' : ""}@config "./${config}";\n`);
  for (const name of ["catalog", "dots"]) emit(target, `public/patterns/${name}.svg`, source(`public/patterns/${name}.svg`));
  emit(target, "src/shared/theme/boot.generated.ts", `// ${header}\nexport const themeBootScript = ${JSON.stringify(boot)};\n`);
  for (const locale of ["zh-CN", "en-US", "zh-TW", "ja-JP"]) {
    const relative = `src/messages/${locale}.json`;
    const current = JSON.parse(read(path.join(target, relative)));
    const messages = JSON.parse(source(relative));
    for (const key of Object.keys(current)) if (key.startsWith("admin.theme.") || (key.startsWith("theme.") && !(key in messages))) delete current[key];
    for (const [key, value] of Object.entries(messages)) if (key.startsWith("theme.") || key === "revisions.close") current[key] = value;
    emit(target, relative, JSON.stringify(current, null, 2) + "\n");
  }
}
const docs = path.resolve(root, "../../metafusion-docs");
for (const name of ["catalog", "dots"]) emit(docs, `docs/public/patterns/${name}.svg`, source(`public/patterns/${name}.svg`));
for (const name of ["theme.ts", "theme.generated.ts"]) emit(docs, `docs/.vitepress/theme/shared/${name}`, source(`src/lib/${name}`));
for (const name of ["theme.tokens.css", "theme.generated.css"]) emit(docs, `docs/.vitepress/theme/shared/${name}`, source(`src/app/${name}`));
// 兼容 VitePress 自带外观按钮使用的 auto 值，不改变站点共享的 system 设置。
const docsBoot = 'try { var mode = localStorage.getItem("metafusion_theme_mode") || "dark"; localStorage.setItem("vitepress-theme-appearance", mode === "system" ? "auto" : mode); } catch (_) {}\n' + boot;
emit(docs, "docs/.vitepress/theme/shared/boot.generated.ts", `// ${header}\nexport const themeBootScript = ${JSON.stringify(docsBoot)};\n`);
if (stale.length) { console.error("主题资产未同步：\n" + stale.join("\n")); process.exit(1); }
console.log(check ? "全部应用的共享主题一致" : "已同步全部应用的主题");
