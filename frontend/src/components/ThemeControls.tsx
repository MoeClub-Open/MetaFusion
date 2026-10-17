"use client";

// 外观控件：配色方案、背景和表面色调、矢量背景图案。
// 弹出选择器与设置页共用这一份，避免两处实现逐渐漂移。

import React from "react";
import { Check, Grid2X2, ListTree, Square, type LucideIcon } from "lucide-react";
import { useTheme, accentLabel, toneLabel, patternLabel, type ThemePattern } from "@/lib/themeContext";
import { useI18n } from "@/i18n/I18nProvider";

interface Props {
  /** compact：弹出层里用（窄、两行配色）；full：设置页里用（铺开、带名称）。 */
  layout?: "compact" | "full";
}

const patternIcons: Record<ThemePattern, LucideIcon> = { catalog: ListTree, dots: Grid2X2, none: Square };

export const ThemeControls: React.FC<Props> = ({ layout = "full" }) => {
  const { accent, tone, pattern, setAccent, setTone, setPattern, accents, tones, patterns } = useTheme();
  const { t } = useI18n();

  return (
    <div className={layout === "compact" ? "space-y-3" : "space-y-4"}>
      {/* 配色方案 */}
      <div className="space-y-1.5">
        <div className="flex items-center justify-between font-mono text-[11px] text-text-muted">
          <span>{t("theme.accentLabel")}</span>
          <span className="font-semibold text-text-strong">{accentLabel(accent, t)}</span>
        </div>
        <div className={`grid gap-1 ${layout === "compact" ? "grid-cols-4" : "grid-cols-4 sm:grid-cols-8 xl:grid-cols-[repeat(16,minmax(0,1fr))]"}`}>
          {accents.map((item) => {
            const active = accent === item.id;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => setAccent(item.id)}
                title={t(item.labelKey)}
                aria-label={t(item.labelKey)}
                aria-pressed={active}
                className={`mf-focus group relative min-h-11 min-w-0 rounded-chip grid place-items-center border transition-colors duration-fast ease-soft cursor-pointer ${
                  active ? "border-primary/60 ring-1 ring-primary/40" : "border-line-subtle hover:border-line"
                }`}
              >
                <span className="w-4 h-4 rounded-full shadow-2xs" style={{ backgroundColor: item.swatch }} />
                {active && <Check className="absolute -bottom-0.5 -right-0.5 w-3 h-3 p-[1px] rounded-full bg-primary text-[color:var(--primary-contrast-color)] stroke-[3]" />}
              </button>
            );
          })}
        </div>
      </div>

      {/* 表面色调 */}
      <div className="space-y-1.5">
        <div className="flex items-center justify-between font-mono text-[11px] text-text-muted">
          <span>{t("theme.toneLabel")}</span>
          <span className="font-semibold text-text-strong">{toneLabel(tone, t)}</span>
        </div>
        <div className="grid grid-cols-4 gap-1">
          {tones.map((item) => {
            const active = tone === item.id;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => setTone(item.id)}
                aria-pressed={active}
                className={`mf-focus min-h-11 min-w-0 break-words px-1 py-1.5 rounded-control text-[11px] border transition-colors duration-fast ease-soft cursor-pointer ${
                  active
                    ? "bg-primary/10 border-primary/50 text-text-strong font-semibold"
                    : "bg-surfaceSubtle border-line-subtle text-text-muted hover:text-text-strong"
                }`}
              >
                {t(item.labelKey)}
              </button>
            );
          })}
        </div>
      </div>

      <div className="space-y-1.5">
        <div className="flex items-center justify-between font-mono text-[11px] text-text-muted">
          <span>{t("theme.patternLabel")}</span>
          <span className="font-semibold text-text-strong">{patternLabel(pattern, t)}</span>
        </div>
        <div className="grid grid-cols-3 gap-1">
          {patterns.map((item) => {
            const active = pattern === item.id;
            const Icon = patternIcons[item.id];
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => setPattern(item.id)}
                aria-pressed={active}
                className={`mf-focus flex min-h-11 min-w-0 items-center justify-center gap-1.5 break-words rounded-control border px-1 py-1.5 text-[11px] transition-colors duration-fast ease-soft cursor-pointer ${
                  active
                    ? "bg-primary/10 border-primary/50 text-text-strong font-semibold"
                    : "bg-surfaceSubtle border-line-subtle text-text-muted hover:text-text-strong"
                }`}
              >
                <Icon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                <span>{t(item.labelKey)}</span>
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
};
