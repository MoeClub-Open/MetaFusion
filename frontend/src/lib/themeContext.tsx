"use client";

// 主题系统：显示模式 × 配色方案 × 表面色调 × 背景图案，四组选择彼此独立。
//
// 颜色不在这里计算：配色目录由 scripts/gen-theme.mjs 从 scripts/theme-palette.mjs 生成，
// CSS 变量块是静态的，因此首帧不会闪错色，也不需要把色板塞进运行时。
// 这里只负责把四个属性写到 <html> 上并持久化。

import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import {
  ACCENTS,
  TONES,
  PATTERNS,
  ACCENT_IDS,
  TONE_IDS,
  PATTERN_IDS,
  DEFAULT_ACCENT,
  DEFAULT_TONE,
  DEFAULT_PATTERN,
  type AccentEntry,
  type ThemeAccent,
  type ThemeTone,
  type ThemePattern,
  type ToneEntry,
  type PatternEntry,
} from "@/lib/theme.generated";

export type ThemeMode = "dark" | "light" | "system";
export type { ThemeAccent, ThemeTone, ThemePattern, AccentEntry, ToneEntry, PatternEntry };

export const MODE_STORAGE_KEY = "metafusion_theme_mode";
export const ACCENT_STORAGE_KEY = "metafusion_theme_accent";
export const TONE_STORAGE_KEY = "metafusion_theme_tone";
export const PATTERN_STORAGE_KEY = "metafusion_theme_pattern";

/** 配色方案的展示名：以 i18n 键为权威来源，缺键时回落到 id。 */
export function accentLabel(id: ThemeAccent, t: (key: string) => string): string {
  const found = ACCENTS.find((a) => a.id === id);
  if (!found) return id;
  const v = t(found.labelKey);
  return v && v !== found.labelKey ? v : id;
}

/** 表面色调的展示名，同上。 */
export function toneLabel(id: ThemeTone, t: (key: string) => string): string {
  const found = TONES.find((x) => x.id === id);
  if (!found) return id;
  const v = t(found.labelKey);
  return v && v !== found.labelKey ? v : id;
}

export function patternLabel(id: ThemePattern, t: (key: string) => string): string {
  const found = PATTERNS.find((x) => x.id === id);
  if (!found) return id;
  const v = t(found.labelKey);
  return v && v !== found.labelKey ? v : id;
}

interface ThemeSelection {
  mode: ThemeMode;
  accent: ThemeAccent;
  tone: ThemeTone;
  pattern: ThemePattern;
}

interface ThemeContextType {
  mode: ThemeMode;
  accent: ThemeAccent;
  tone: ThemeTone;
  pattern: ThemePattern;
  resolvedMode: "dark" | "light";
  setMode: (mode: ThemeMode) => void;
  setAccent: (accent: ThemeAccent) => void;
  setTone: (tone: ThemeTone) => void;
  setPattern: (pattern: ThemePattern) => void;
  accents: AccentEntry[];
  tones: ToneEntry[];
  patterns: ReadonlyArray<(typeof PATTERNS)[number]>;
}

const ThemeContext = createContext<ThemeContextType>({
  mode: "dark",
  accent: DEFAULT_ACCENT,
  tone: DEFAULT_TONE,
  pattern: DEFAULT_PATTERN,
  resolvedMode: "dark",
  setMode: () => {},
  setAccent: () => {},
  setTone: () => {},
  setPattern: () => {},
  accents: ACCENTS,
  tones: TONES,
  patterns: PATTERNS,
});

const isAccent = (v: string | null): v is ThemeAccent => !!v && (ACCENT_IDS as string[]).includes(v);
const isTone = (v: string | null): v is ThemeTone => !!v && (TONE_IDS as string[]).includes(v);
const isPattern = (v: string | null): v is ThemePattern => !!v && (PATTERN_IDS as string[]).includes(v);
const isMode = (v: string | null): v is ThemeMode => v === "dark" || v === "light" || v === "system";
function readStorage(key: string): string | null {
  try { return localStorage.getItem(key); } catch { return null; }
}

function writeStorage(key: string, value: string): void {
  try { localStorage.setItem(key, value); } catch { /* 本页仍可切换主题。 */ }
}

/** 把四组选择写到 <html> 上；同时同步浏览器地址栏/移动端状态栏配色。 */
function applyTheme({ mode, accent, tone, pattern }: ThemeSelection): "dark" | "light" {
  const effectiveMode: "dark" | "light" =
    mode === "system"
      ? window.matchMedia("(prefers-color-scheme: dark)").matches
        ? "dark"
        : "light"
      : mode;

  const root = document.documentElement;
  root.setAttribute("data-theme-mode", effectiveMode);
  root.setAttribute("data-theme-accent", accent);
  root.setAttribute("data-theme-tone", tone);
  root.setAttribute("data-theme-pattern", pattern);
  root.classList.toggle("dark", effectiveMode === "dark");
  root.classList.toggle("light", effectiveMode === "light");
  root.style.colorScheme = effectiveMode;

  // 移动端地址栏/状态栏跟随底色，避免深色站点配白色状态栏。
  const meta = document.querySelector('meta[name="theme-color"]');
  const bg = getComputedStyle(root).getPropertyValue("--bg-color").trim();
  if (meta && bg) meta.setAttribute("content", bg);

  return effectiveMode;
}

export const ThemeProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [mode, setModeState] = useState<ThemeMode>("dark");
  const [accent, setAccentState] = useState<ThemeAccent>(DEFAULT_ACCENT);
  const [tone, setToneState] = useState<ThemeTone>(DEFAULT_TONE);
  const [pattern, setPatternState] = useState<ThemePattern>(DEFAULT_PATTERN);
  const [resolvedMode, setResolvedMode] = useState<"dark" | "light">("dark");
  const selection = useRef<ThemeSelection>({ mode: "dark", accent: DEFAULT_ACCENT, tone: DEFAULT_TONE, pattern: DEFAULT_PATTERN });

  useEffect(() => {
    const savedModeRaw = readStorage(MODE_STORAGE_KEY);
    const savedMode = isMode(savedModeRaw) ? savedModeRaw : "dark";
    const savedAccentRaw = readStorage(ACCENT_STORAGE_KEY);
    const savedToneRaw = readStorage(TONE_STORAGE_KEY);
    const savedPatternRaw = readStorage(PATTERN_STORAGE_KEY);
    const savedAccent = isAccent(savedAccentRaw) ? savedAccentRaw : DEFAULT_ACCENT;
    const savedTone = isTone(savedToneRaw) ? savedToneRaw : DEFAULT_TONE;
    const savedPattern = isPattern(savedPatternRaw) ? savedPatternRaw : DEFAULT_PATTERN;
    selection.current = { mode: savedMode, accent: savedAccent, tone: savedTone, pattern: savedPattern };

    setModeState(savedMode);
    setAccentState(savedAccent);
    setToneState(savedTone);
    setPatternState(savedPattern);
    setResolvedMode(applyTheme(selection.current));

    // 跟随系统时，系统主题变化要即时生效；换配色/色调不需要重挂监听。
    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => {
      if (selection.current.mode !== "system") return;
      setResolvedMode(applyTheme(selection.current));
    };
    mediaQuery.addEventListener("change", handler);
    return () => mediaQuery.removeEventListener("change", handler);
  }, []);

  // 以内存中的当前选择合并更新，避免存储被禁用时切换某项重置其他项。
  const updateSelection = useCallback((next: Partial<ThemeSelection>) => {
    const current = { ...selection.current, ...next };
    selection.current = current;
    setModeState(current.mode);
    setAccentState(current.accent);
    setToneState(current.tone);
    setPatternState(current.pattern);
    setResolvedMode(applyTheme(current));
  }, []);

  const setMode = useCallback((nextMode: ThemeMode) => {
    writeStorage(MODE_STORAGE_KEY, nextMode);
    updateSelection({ mode: nextMode });
  }, [updateSelection]);

  const setAccent = useCallback((nextAccent: ThemeAccent) => {
    writeStorage(ACCENT_STORAGE_KEY, nextAccent);
    updateSelection({ accent: nextAccent });
  }, [updateSelection]);

  const setTone = useCallback((nextTone: ThemeTone) => {
    writeStorage(TONE_STORAGE_KEY, nextTone);
    updateSelection({ tone: nextTone });
  }, [updateSelection]);

  const setPattern = useCallback((nextPattern: ThemePattern) => {
    writeStorage(PATTERN_STORAGE_KEY, nextPattern);
    updateSelection({ pattern: nextPattern });
  }, [updateSelection]);

  return (
    <ThemeContext.Provider value={{ mode, accent, tone, pattern, resolvedMode, setMode, setAccent, setTone, setPattern, accents: ACCENTS, tones: TONES, patterns: PATTERNS }}>
      {children}
    </ThemeContext.Provider>
  );
};

export const useTheme = () => useContext(ThemeContext);
