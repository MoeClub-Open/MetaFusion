// 主题配色的唯一来源：改这里 → 跑 `npm run theme:build` → 生成 CSS 变量块与前端目录。
//
// 只写"基准色 + 角色说明"，hover/light/对比色由生成器按色相明度推导，
// 保证 16 套配色在深色/浅色两种模式下取值口径一致，不会出现某一套按钮看不清字。

/** 各配色方案的基准色（取 600 档左右）与一个更亮的强调色（用于图表/标签的第二色）。 */
export const ACCENTS = [
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

/** 深色模式的中性基准（neutral 色调）：其余色调在它的基础上做色相偏移与对比缩放。 */
export const BASE_DARK = {
  bg: "#0b0f17", surface: "#121826", surfaceHover: "#1a2336", surfaceSubtle: "rgba(255,255,255,0.03)",
  line: "rgba(255,255,255,0.10)", lineSubtle: "rgba(255,255,255,0.06)", lineStrong: "rgba(255,255,255,0.18)",
  textStrong: "#f0f3f6", textBody: "#cbd5e1", textMuted: "#94a3b8", textFaint: "#64748b",
};

export const BASE_LIGHT = {
  bg: "#f8fafc", surface: "#ffffff", surfaceHover: "#f1f5f9", surfaceSubtle: "rgba(15,23,42,0.035)",
  line: "rgba(15,23,42,0.12)", lineSubtle: "rgba(15,23,42,0.07)", lineStrong: "rgba(15,23,42,0.22)",
  // 对比度：次要文字在最浅的浅色表面（surfaceHover #f1f5f9）上也要 ≥4.5:1 ——
  // muted #55606f = 5.83:1、faint #636e80 = 4.71:1（改前 #64748b = 4.34、#94a3b8 = 2.34）。
  textStrong: "#0f172a", textBody: "#334155", textMuted: "#55606f", textFaint: "#636e80",
};
/** 默认配色与表面色调（首次访问、未做选择时的取值）。 */
export const DEFAULT_ACCENT = "blue";
export const DEFAULT_TONE = "neutral";
export const DEFAULT_PATTERN = "catalog";
