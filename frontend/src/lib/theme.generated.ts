// 由 scripts/gen-theme.mjs 生成，勿手改：新增配色请看 scripts/theme-palette.mjs 后重跑 npm run theme:build。

export interface AccentEntry { id: string; labelKey: string; swatch: string; }
export interface ToneEntry { id: string; labelKey: string; }
export interface PatternEntry { id: string; labelKey: string; }

export const ACCENTS: AccentEntry[] = [
  { id: "blue", labelKey: "theme.accent.blue", swatch: "#3b82f6" },
  { id: "sky", labelKey: "theme.accent.sky", swatch: "#0ea5e9" },
  { id: "cyan", labelKey: "theme.accent.cyan", swatch: "#06b6d4" },
  { id: "teal", labelKey: "theme.accent.teal", swatch: "#14b8a6" },
  { id: "emerald", labelKey: "theme.accent.emerald", swatch: "#10b981" },
  { id: "lime", labelKey: "theme.accent.lime", swatch: "#65a30d" },
  { id: "amber", labelKey: "theme.accent.amber", swatch: "#f59e0b" },
  { id: "orange", labelKey: "theme.accent.orange", swatch: "#f97316" },
  { id: "red", labelKey: "theme.accent.red", swatch: "#ef4444" },
  { id: "rose", labelKey: "theme.accent.rose", swatch: "#f43f5e" },
  { id: "fuchsia", labelKey: "theme.accent.fuchsia", swatch: "#d946ef" },
  { id: "purple", labelKey: "theme.accent.purple", swatch: "#a855f7" },
  { id: "violet", labelKey: "theme.accent.violet", swatch: "#8b5cf6" },
  { id: "indigo", labelKey: "theme.accent.indigo", swatch: "#6366f1" },
  { id: "slate", labelKey: "theme.accent.slate", swatch: "#64748b" },
  { id: "monochrome", labelKey: "theme.accent.monochrome", swatch: "#71717a" },
];

export const TONES: ToneEntry[] = [
  { id: "neutral", labelKey: "theme.tone.neutral" },
  { id: "cool", labelKey: "theme.tone.cool" },
  { id: "warm", labelKey: "theme.tone.warm" },
  { id: "deep", labelKey: "theme.tone.deep" },
];

export const PATTERNS = [
  { id: "catalog", labelKey: "theme.pattern.catalog" },
  { id: "dots", labelKey: "theme.pattern.dots" },
  { id: "none", labelKey: "theme.pattern.none" },
] as const satisfies readonly PatternEntry[];

export const ACCENT_IDS = ACCENTS.map((a) => a.id);
export const TONE_IDS = TONES.map((t) => t.id);
export const PATTERN_IDS = PATTERNS.map((p) => p.id);
export type ThemeAccent = (typeof ACCENT_IDS)[number];
export type ThemeTone = (typeof TONE_IDS)[number];
export type ThemePattern = (typeof PATTERN_IDS)[number];
export const DEFAULT_ACCENT: ThemeAccent = "blue";
export const DEFAULT_TONE: ThemeTone = "neutral";
export const DEFAULT_PATTERN: ThemePattern = "catalog";
