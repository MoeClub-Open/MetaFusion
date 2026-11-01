import {
  ACCENT_IDS, TONE_IDS, PATTERN_IDS, DEFAULT_ACCENT, DEFAULT_TONE, DEFAULT_PATTERN,
  type ThemeAccent, type ThemeTone, type ThemePattern,
} from "./theme.generated.ts";

export type ThemeMode = "dark" | "light" | "system";
export interface ThemeSelection { mode: ThemeMode; accent: ThemeAccent; tone: ThemeTone; pattern: ThemePattern; }
export const MODE_STORAGE_KEY = "metafusion_theme_mode";
export const ACCENT_STORAGE_KEY = "metafusion_theme_accent";
export const TONE_STORAGE_KEY = "metafusion_theme_tone";
export const PATTERN_STORAGE_KEY = "metafusion_theme_pattern";
export const THEME_STORAGE_KEYS = [MODE_STORAGE_KEY, ACCENT_STORAGE_KEY, TONE_STORAGE_KEY, PATTERN_STORAGE_KEY];

export function readStorage(key: string): string | null {
  try { return localStorage.getItem(key); } catch { return null; }
}
export function writeStorage(key: string, value: string): void {
  try { localStorage.setItem(key, value); } catch { /* 本页仍可切换。 */ }
}
export function isThemeMode(value: unknown): value is ThemeMode { return value === "dark" || value === "light" || value === "system"; }
export function isThemeSelection(value: Record<string, unknown>): boolean {
  return isThemeMode(value.mode) && ACCENT_IDS.includes(value.accent as string) && TONE_IDS.includes(value.tone as string) && PATTERN_IDS.includes(value.pattern as ThemePattern);
}
export function readThemeSelection(): ThemeSelection {
  const mode = readStorage(MODE_STORAGE_KEY);
  const accent = readStorage(ACCENT_STORAGE_KEY);
  const tone = readStorage(TONE_STORAGE_KEY);
  const pattern = readStorage(PATTERN_STORAGE_KEY);
  return {
    mode: isThemeMode(mode) ? mode : "dark",
    accent: accent && ACCENT_IDS.includes(accent) ? accent : DEFAULT_ACCENT,
    tone: tone && TONE_IDS.includes(tone) ? tone : DEFAULT_TONE,
    pattern: pattern && PATTERN_IDS.includes(pattern as ThemePattern) ? pattern as ThemePattern : DEFAULT_PATTERN,
  };
}
export function applyTheme({ mode, accent, tone, pattern }: ThemeSelection): "dark" | "light" {
  const effectiveMode = mode === "system" ? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light") : mode;
  const root = document.documentElement;
  root.setAttribute("data-theme-mode", effectiveMode);
  root.setAttribute("data-theme-accent", accent);
  root.setAttribute("data-theme-tone", tone);
  root.setAttribute("data-theme-pattern", pattern);
  root.classList.toggle("dark", effectiveMode === "dark");
  root.classList.toggle("light", effectiveMode === "light");
  root.style.colorScheme = effectiveMode;
  const meta = document.querySelector('meta[name="theme-color"]');
  const background = getComputedStyle(root).getPropertyValue("--bg-color").trim();
  if (meta && background) meta.setAttribute("content", background);
  return effectiveMode;
}
