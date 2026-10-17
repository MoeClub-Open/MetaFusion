// 由 scripts/theme-palette.mjs 生成：
//   src/app/theme.generated.css  —— 每套配色在深/浅两种模式下的 CSS 变量块（静态，无 JS 也能生效）
//   src/lib/theme.generated.ts   —— 前端需要的配色目录（id / 名称键 / 色卡）
// 生成物一并提交：主题是静态资产，构建期不依赖脚本再次运行。
import fs from "fs";
import path from "path";
import { ACCENTS, TONES, PATTERNS, BASE_DARK, BASE_LIGHT, DEFAULT_ACCENT, DEFAULT_TONE, DEFAULT_PATTERN } from "./theme-palette.mjs";

const ROOT = process.cwd();
const CSS_OUT = path.join(ROOT, "src/app/theme.generated.css");
const TS_OUT = path.join(ROOT, "src/lib/theme.generated.ts");

// —— 颜色工具：按色相/明度推导，保证 16 套配色的派生规则一致 ——
function hexToRgb(hex) {
  const h = hex.replace("#", "");
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
}
function rgbToHsl([r, g, b]) {
  r /= 255; g /= 255; b /= 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b);
  const l = (max + min) / 2;
  let h = 0, s = 0;
  if (max !== min) {
    const d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    if (max === r) h = ((g - b) / d + (g < b ? 6 : 0)) / 6;
    else if (max === g) h = ((b - r) / d + 2) / 6;
    else h = ((r - g) / d + 4) / 6;
  }
  return [h * 360, s * 100, l * 100];
}
function hslToHex(h, s, l) {
  h = ((h % 360) + 360) % 360; s = Math.max(0, Math.min(100, s)) / 100; l = Math.max(0, Math.min(100, l)) / 100;
  const c = (1 - Math.abs(2 * l - 1)) * s, x = c * (1 - Math.abs(((h / 60) % 2) - 1)), m = l - c / 2;
  let rgb = [0, 0, 0];
  if (h < 60) rgb = [c, x, 0]; else if (h < 120) rgb = [x, c, 0]; else if (h < 180) rgb = [0, c, x];
  else if (h < 240) rgb = [0, x, c]; else if (h < 300) rgb = [x, 0, c]; else rgb = [c, 0, x];
  return "#" + rgb.map((v) => Math.round((v + m) * 255).toString(16).padStart(2, "0")).join("");
}
function shift(hex, { dh = 0, ds = 0, dl = 0 }) {
  const [h, s, l] = rgbToHsl(hexToRgb(hex));
  return hslToHex(h + dh, s + ds, l + dl);
}
// 三通道值（"r g b"）：Tailwind 的 <alpha-value> 只能替换 rgb() 的斜杠通道，
// 所以主色这类要出透明度变体的令牌必须以三通道形式额外给一份。
const rgbTriplet = (hex) => hexToRgb(hex).join(" ");
// 把半透明描边压到表面上得到不透明等价值：border-border 与 border-border/50 共用一个变量，
// 而 <alpha-value> 在裸类名下取默认档 1，只能用不透明值表达描边的"本色"。
const blendOver = (rgba, surfaceHex) => {
  const parts = (rgba.match(/\(([^)]+)\)/) || [null, "0,0,0,1"])[1].split(",").map((v) => Number(v.trim()));
  const [r, g, b] = parts;
  const a = parts.length > 3 ? parts[3] : 1;
  const s = hexToRgb(surfaceHex);
  return [r, g, b].map((c, i) => Math.round(c * a + s[i] * (1 - a))).join(" ");
};

// 相对亮度决定"主色上的文字"用什么颜色：亮主色（黄/柠檬）配深字，其余配白字。
function luminance(hex) {
  const [r, g, b] = hexToRgb(hex).map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
const contrastOn = (hex) => (luminance(hex) > 0.45 ? "#0b0f17" : "#ffffff");

// 每个配色在两套模式下的五个变量：主色 / 悬停 / 亮色 / 主色上的文字 / 第二强调色
function accentVars(a) {
  const dark = {
    primary: shift(a.base, { dl: 4 }),
    hover: shift(a.base, { dl: -6 }),
    light: shift(a.base, { dl: 16 }),
    accent: a.accent,
  };
  const light = {
    primary: shift(a.base, { dl: -6 }),
    hover: shift(a.base, { dl: -14 }),
    light: shift(a.base, { dl: 10 }),
    accent: a.accent,
  };
  return {
    dark: { ...dark, contrast: contrastOn(dark.primary) },
    light: { ...light, contrast: contrastOn(light.primary) },
  };
}

// 表面色调：在中性基准上做色相偏移（暖/冷）与对比缩放（deep 提升线与文字对比）
function toneVars(tone, base, mode) {
  const out = { ...base };
  const hueTint = (hex) => {
    if (tone.hue === null) return hex;
    const [, , l] = rgbToHsl(hexToRgb(hex));
    // 直接换到目标色相并给一档低饱和：暖/冷要"看得见"，但仍然是灰底不是彩色底。
    const sat = mode === "dark" ? 13 : 22;
    return hslToHex(tone.hue, sat, l);
  };
  for (const key of ["bg", "surface", "surfaceHover"]) out[key] = hueTint(base[key]);
  if (tone.contrast !== 1) {
    const bump = (rgba, factor) => rgba.replace(/([0-9.]+)\)$/, (m, n) => `${Math.min(1, Number(n) * factor).toFixed(3)})`);
    out.line = bump(base.line, tone.contrast);
    out.lineSubtle = bump(base.lineSubtle, tone.contrast);
    out.lineStrong = bump(base.lineStrong, tone.contrast);
    out.surfaceSubtle = bump(base.surfaceSubtle, 1);
  }
  return out;
}

// —— 生成 CSS ——
const lines = [];
lines.push("/* 由 scripts/gen-theme.mjs 生成，勿手改：改配色请看 scripts/theme-palette.mjs。 */");
lines.push("");
for (const a of ACCENTS) {
  const v = accentVars(a);
  // 两个属性一起写，特异性(0,2,0)高于 html.dark / [data-theme-mode="dark"] 的默认值块，
  // 否则深色模式下默认主色会盖住所选配色。
  lines.push(`[data-theme-accent="${a.id}"][data-theme-mode="dark"] {`);
  lines.push(`  --primary-color: ${v.dark.primary};`);
  lines.push(`  --primary-hover-color: ${v.dark.hover};`);
  lines.push(`  --primary-light-color: ${v.dark.light};`);
  lines.push(`  --primary-contrast-color: ${v.dark.contrast};`);
  lines.push(`  --primary-rgb: ${rgbTriplet(v.dark.primary)};`);
  lines.push(`  --primary-contrast-rgb: ${rgbTriplet(v.dark.contrast)};`);
  lines.push(`  --accent-color: ${v.dark.accent};`);
  lines.push(`  --accent-rgb: ${rgbTriplet(v.dark.accent)};`);
  lines.push("}");
  lines.push(`[data-theme-accent="${a.id}"][data-theme-mode="light"] {`);
  lines.push(`  --primary-color: ${v.light.primary};`);
  lines.push(`  --primary-hover-color: ${v.light.hover};`);
  lines.push(`  --primary-light-color: ${v.light.light};`);
  lines.push(`  --primary-contrast-color: ${v.light.contrast};`);
  lines.push(`  --primary-rgb: ${rgbTriplet(v.light.primary)};`);
  lines.push(`  --primary-contrast-rgb: ${rgbTriplet(v.light.contrast)};`);
  lines.push(`  --accent-color: ${v.light.accent};`);
  lines.push(`  --accent-rgb: ${rgbTriplet(v.light.accent)};`);
  lines.push("}");
  lines.push("");
}
for (const tone of TONES) {
  for (const [mode, base] of [["dark", BASE_DARK], ["light", BASE_LIGHT]]) {
    const t = toneVars(tone, base, mode);
    lines.push(`[data-theme-tone="${tone.id}"][data-theme-mode="${mode}"] {`);
    lines.push(`  --bg-color: ${t.bg};`);
    lines.push(`  --surface-color: ${t.surface};`);
    lines.push(`  --surface-hover-color: ${t.surfaceHover};`);
    // Tailwind 透明度令牌与 CSS 原色必须来自同一套表面值。
    lines.push(`  --bg-rgb: ${rgbTriplet(t.bg)};`);
    lines.push(`  --surface-rgb: ${rgbTriplet(t.surface)};`);
    lines.push(`  --surface-hover-rgb: ${rgbTriplet(t.surfaceHover)};`);
    lines.push(`  --card-rgb: ${rgbTriplet(t.surface)};`);
    lines.push(`  --muted-rgb: ${rgbTriplet(t.surfaceHover)};`);
    lines.push(`  --secondary-rgb: ${rgbTriplet(t.surfaceHover)};`);
    lines.push(`  --muted-foreground-rgb: ${rgbTriplet(t.textMuted)};`);
    lines.push(`  --surface-subtle-color: ${t.surfaceSubtle};`);
    lines.push(`  --line-color: ${t.line};`);
    lines.push(`  --line-subtle-color: ${t.lineSubtle};`);
    lines.push(`  --line-strong-color: ${t.lineStrong};`);
    lines.push(`  --text-strong-color: ${t.textStrong};`);
    lines.push(`  --text-body-color: ${t.textBody};`);
    lines.push(`  --text-muted-color: ${t.textMuted};`);
    lines.push(`  --text-faint-color: ${t.textFaint};`);
    // 描边令牌的三通道值跟着色调/对比一起走，避免 border-border 与 border-line 在暖/冷/深色调下分家。
    lines.push(`  --border-rgb: ${blendOver(t.line, t.surface)};`);
    lines.push("}");
    lines.push("");
  }
}
fs.writeFileSync(CSS_OUT, lines.join("\n"));

// —— 生成前端目录 ——
const ts = [
  "// 由 scripts/gen-theme.mjs 生成，勿手改：新增配色请看 scripts/theme-palette.mjs 后重跑 npm run theme:build。",
  "",
  "export interface AccentEntry { id: string; labelKey: string; swatch: string; }",
  "export interface ToneEntry { id: string; labelKey: string; }",
  "export interface PatternEntry { id: string; labelKey: string; }",
  "",
  "export const ACCENTS: AccentEntry[] = [",
  ...ACCENTS.map((a) => `  { id: ${JSON.stringify(a.id)}, labelKey: ${JSON.stringify(a.labelKey)}, swatch: ${JSON.stringify(a.base)} },`),
  "];",
  "",
  "export const TONES: ToneEntry[] = [",
  ...TONES.map((t) => `  { id: ${JSON.stringify(t.id)}, labelKey: ${JSON.stringify(t.labelKey)} },`),
  "];",
  "",
  "export const PATTERNS = [",
  ...PATTERNS.map((p) => `  { id: ${JSON.stringify(p.id)}, labelKey: ${JSON.stringify(p.labelKey)} },`),
  "] as const satisfies readonly PatternEntry[];",
  "",
  "export const ACCENT_IDS = ACCENTS.map((a) => a.id);",
  "export const TONE_IDS = TONES.map((t) => t.id);",
  "export const PATTERN_IDS = PATTERNS.map((p) => p.id);",
  "export type ThemeAccent = (typeof ACCENT_IDS)[number];",
  "export type ThemeTone = (typeof TONE_IDS)[number];",
  "export type ThemePattern = (typeof PATTERN_IDS)[number];",
  `export const DEFAULT_ACCENT: ThemeAccent = ${JSON.stringify(DEFAULT_ACCENT)};`,
  `export const DEFAULT_TONE: ThemeTone = ${JSON.stringify(DEFAULT_TONE)};`,
  `export const DEFAULT_PATTERN: ThemePattern = ${JSON.stringify(DEFAULT_PATTERN)};`,
  "",
].join("\n");
fs.writeFileSync(TS_OUT, ts);
console.log("已生成 " + ACCENTS.length + " 套配色 × " + TONES.length + " 种表面色调：");
console.log("  " + path.relative(ROOT, CSS_OUT));
console.log("  " + path.relative(ROOT, TS_OUT));
// —— 生成首帧引导脚本（public/theme-boot.js）——
// 为什么生成而不是手写：文件里要用到配色 id 列表与默认值，手写就等于把它们抄了第二遍，
// 新增配色时容易忘记同步（表现是"选了新配色，首帧又闪回默认"）。
const BOOT_OUT = path.join(ROOT, "public/theme-boot.js");
const bootJs = `// 由 scripts/gen-theme.mjs 生成，勿手改：改配色请看 scripts/theme-palette.mjs。
// 首帧同步执行：把 localStorage 里的主题选择写到 <html> 上，避免加载瞬间闪默认配色。
(function () {
  function read(key) {
    try { return localStorage.getItem(key); } catch (_) { return null; }
  }
  var m = read("metafusion_theme_mode") || "dark";
  var a = read("metafusion_theme_accent");
  var t = read("metafusion_theme_tone");
  var p = read("metafusion_theme_pattern");
  if (${JSON.stringify(ACCENTS.map((x) => x.id))}.indexOf(a) < 0) a = "${DEFAULT_ACCENT}";
  if (${JSON.stringify(TONES.map((x) => x.id))}.indexOf(t) < 0) t = "${DEFAULT_TONE}";
  if (${JSON.stringify(PATTERNS.map((x) => x.id))}.indexOf(p) < 0) p = "${DEFAULT_PATTERN}";
  var e = m === "system"
    ? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light")
    : (m === "light" ? "light" : "dark");
  var r = document.documentElement;
  r.setAttribute("data-theme-mode", e);
  r.setAttribute("data-theme-accent", a);
  r.setAttribute("data-theme-tone", t);
  r.setAttribute("data-theme-pattern", p);
  r.classList.remove("dark", "light");
  r.classList.add(e);
  r.style.colorScheme = e;
})();
`;
fs.writeFileSync(BOOT_OUT, bootJs);
console.log("  " + path.relative(ROOT, BOOT_OUT));
