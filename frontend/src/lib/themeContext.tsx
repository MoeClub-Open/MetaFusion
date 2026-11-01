"use client";

// 主题系统：显示模式 × 配色方案 × 表面色调 × 背景图案，四组选择彼此独立。
//
// 颜色不在这里计算：配色目录由 scripts/gen-theme.ts 从 scripts/theme-palette.ts 生成，
// CSS 变量块是静态的，因此首帧不会闪错色，也不需要把色板塞进运行时。
// 这里只负责把四个属性写到 <html> 上并持久化。

import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import {
  ACCENTS,
  TONES,
  PATTERNS,
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

import { applyTheme, readThemeSelection, writeStorage, MODE_STORAGE_KEY, ACCENT_STORAGE_KEY, TONE_STORAGE_KEY, PATTERN_STORAGE_KEY, THEME_STORAGE_KEYS, type ThemeMode, type ThemeSelection } from "./theme";
export type { ThemeMode, ThemeSelection } from "./theme";
export { MODE_STORAGE_KEY, ACCENT_STORAGE_KEY, TONE_STORAGE_KEY, PATTERN_STORAGE_KEY } from "./theme";
export type { ThemeAccent, ThemeTone, ThemePattern, AccentEntry, ToneEntry, PatternEntry };

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

interface ThemeContextType {
  mode: ThemeMode;
  accent: ThemeAccent;
  tone: ThemeTone;
  pattern: ThemePattern;
  resolvedMode: "dark" | "light";
  setTheme: (selection: ThemeSelection) => void;
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
  setTheme: () => {},
  setMode: () => {},
  setAccent: () => {},
  setTone: () => {},
  setPattern: () => {},
  accents: ACCENTS,
  tones: TONES,
  patterns: PATTERNS,
});

export const ThemeProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [mode, setModeState] = useState<ThemeMode>("dark");
  const [accent, setAccentState] = useState<ThemeAccent>(DEFAULT_ACCENT);
  const [tone, setToneState] = useState<ThemeTone>(DEFAULT_TONE);
  const [pattern, setPatternState] = useState<ThemePattern>(DEFAULT_PATTERN);
  const [resolvedMode, setResolvedMode] = useState<"dark" | "light">("dark");
  const selection = useRef<ThemeSelection>({ mode: "dark", accent: DEFAULT_ACCENT, tone: DEFAULT_TONE, pattern: DEFAULT_PATTERN });

  useEffect(() => {
    const sync = () => {
      const saved = readThemeSelection();
      selection.current = saved;
      setModeState(saved.mode);
      setAccentState(saved.accent);
      setToneState(saved.tone);
      setPatternState(saved.pattern);
      setResolvedMode(applyTheme(saved));
    };
    sync();
    const onStorage = (event: StorageEvent) => {
      if (event.key === null || THEME_STORAGE_KEYS.includes(event.key)) sync();
    };
    window.addEventListener("storage", onStorage);

    // 跟随系统时，系统主题变化要即时生效；换配色/色调不需要重挂监听。
    const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => {
      if (selection.current.mode !== "system") return;
      setResolvedMode(applyTheme(selection.current));
    };
    mediaQuery.addEventListener("change", handler);
    return () => { mediaQuery.removeEventListener("change", handler); window.removeEventListener("storage", onStorage); };
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

  const setTheme = useCallback((next: ThemeSelection) => {
    writeStorage(MODE_STORAGE_KEY, next.mode);
    writeStorage(ACCENT_STORAGE_KEY, next.accent);
    writeStorage(TONE_STORAGE_KEY, next.tone);
    writeStorage(PATTERN_STORAGE_KEY, next.pattern);
    updateSelection(next);
  }, [updateSelection]);

  return (
    <ThemeContext.Provider value={{ mode, accent, tone, pattern, resolvedMode, setTheme, setMode, setAccent, setTone, setPattern, accents: ACCENTS, tones: TONES, patterns: PATTERNS }}>
      {children}
    </ThemeContext.Provider>
  );
};

export const useTheme = () => useContext(ThemeContext);
