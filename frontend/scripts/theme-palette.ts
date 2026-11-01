// 主题配色的唯一来源：改这里 → 跑 `npm run theme:build` → 生成 CSS 变量块与前端目录。
//
// 只写"基准色 + 角色说明"，hover/light/对比色由生成器按色相明度推导，
// 保证每套配色在深色/浅色两种模式下取值口径一致，不会出现某一套按钮看不清字。

/** 各配色方案的基准色（取 600 档左右）与一个更亮的强调色（用于图表/标签的第二色）。
 *  everforest 用色板原色：深色主色是 medium 的 green，浅色主色在 light green 上略提亮，
 *  让森林墨色字保持 ≥4.5:1。其余配色仍由生成器按同一套明度规则推导。 */
export const ACCENTS = [
  { id: "everforest", labelKey: "theme.accent.everforest", base: "#a7c080", accent: "#83c092", lightBase: "#92a701", lightAccent: "#35a77c" },
  { id: "blue",       labelKey: "theme.accent.blue",       base: "#3b82f6", accent: "#22d3ee" },
  { id: "sky",        labelKey: "theme.accent.sky",        base: "#0ea5e9", accent: "#38bdf8" },
  { id: "cyan",       labelKey: "theme.accent.cyan",       base: "#06b6d4", accent: "#2dd4bf" },
  { id: "teal",       labelKey: "theme.accent.teal",       base: "#14b8a6", accent: "#22d3ee" },
  { id: "emerald",    labelKey: "theme.accent.emerald",    base: "#10b981", accent: "#34d399" },
  { id: "lime",       labelKey: "theme.accent.lime",       base: "#65a30d", accent: "#a3e635" },
  { id: "amber",      labelKey: "theme.accent.amber",      base: "#f59e0b", accent: "#fbbf24" },
  { id: "orange",     labelKey: "theme.accent.orange",     base: "#f97316", accent: "#fb923c" },
  { id: "red",        labelKey: "theme.accent.red",        base: "#ef4444", accent: "#f87171" },
  { id: "rose",       labelKey: "theme.accent.rose",       base: "#f43f5e", accent: "#fb7185" },
  { id: "fuchsia",    labelKey: "theme.accent.fuchsia",    base: "#d946ef", accent: "#e879f9" },
  { id: "purple",     labelKey: "theme.accent.purple",     base: "#a855f7", accent: "#c084fc" },
  { id: "violet",     labelKey: "theme.accent.violet",     base: "#8b5cf6", accent: "#a78bfa" },
  { id: "indigo",     labelKey: "theme.accent.indigo",     base: "#6366f1", accent: "#818cf8" },
  { id: "slate",      labelKey: "theme.accent.slate",      base: "#64748b", accent: "#94a3b8" },
  { id: "monochrome", labelKey: "theme.accent.monochrome", base: "#71717a", accent: "#a1a1aa" },
];

/**
 * 表面色调：只调"底色/描边/文字"这组中性色，不改主色，
 * 因此「配色方案 = 主色 × 表面色调 × 深色/浅色」三种选择可以自由组合。
 */
export const TONES = [
  { id: "neutral", labelKey: "theme.tone.neutral", hue: null,    contrast: 1.0 },
  { id: "cool",    labelKey: "theme.tone.cool",    hue: 220,     contrast: 1.0 },
  { id: "warm",    labelKey: "theme.tone.warm",    hue: 28,      contrast: 1.0 },
  { id: "deep",    labelKey: "theme.tone.deep",    hue: null,    contrast: 1.25 },
];

/** 背景图案独立于配色；由全局样式按同名属性选择矢量纹理。 */
export const PATTERNS = [
  { id: "catalog", labelKey: "theme.pattern.catalog" },
  { id: "dots", labelKey: "theme.pattern.dots" },
  { id: "none", labelKey: "theme.pattern.none" },
];

/** 深色 medium（Everforest）：neutral 色调直接用这组，冷/暖/深仍在它上面做色相或对比偏移。
 *  文字在 surface #343f44 上：strong 7.4:1、body 5.7:1、muted 4.6:1。faint 对齐旧档约 4.1:1。 */
export const BASE_DARK = {
  bg: "#2d353b", surface: "#343f44", surfaceHover: "#3d484d", surfaceSubtle: "rgba(211,198,170,0.05)",
  line: "rgba(211,198,170,0.14)", lineSubtle: "rgba(211,198,170,0.08)", lineStrong: "rgba(211,198,170,0.24)",
  textStrong: "#d3c6aa", textBody: "#c5bba3", textMuted: "#a2aca5", textFaint: "#99a197",
};

/** 浅色 medium。官方灰在纸色 #fdf6e3 上不足 4.5:1，muted/faint 沿同一色相压暗到达标。 */
export const BASE_LIGHT = {
  bg: "#fdf6e3", surface: "#f4f0d9", surfaceHover: "#efebd4", surfaceSubtle: "rgba(92,106,114,0.06)",
  line: "rgba(92,106,114,0.16)", lineSubtle: "rgba(92,106,114,0.09)", lineStrong: "rgba(92,106,114,0.28)",
  textStrong: "#5c6a72", textBody: "#5c6a72", textMuted: "#657364", textFaint: "#667466",
};
/** 默认配色与表面色调（首次访问、未做选择时的取值）。 */
export const DEFAULT_ACCENT = "everforest";
export const DEFAULT_TONE = "neutral";
export const DEFAULT_PATTERN = "catalog";
