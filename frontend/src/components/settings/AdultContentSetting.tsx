"use client";

import { useEffect, useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { useAdultContentMode, type AdultContentMode } from "@/lib/adultContent";
import { AlertCircle, Loader2, RotateCw, ShieldAlert } from "lucide-react";

const OPTIONS: ReadonlyArray<{ value: AdultContentMode; labelKey: string }> = [
  { value: "hide", labelKey: "settings.adultContentHide" },
  { value: "blur", labelKey: "settings.adultContentBlur" },
  { value: "show", labelKey: "settings.adultContentShow" },
];

export function AdultContentSetting() {
  const { t } = useI18n();
  const { mode, loading, loadFailed, refresh, updateMode } = useAdultContentMode();
  const [saving, setSaving] = useState(false);
  const [saveFailed, setSaveFailed] = useState(false);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!saved) return;
    const timer = window.setTimeout(() => setSaved(false), 3000);
    return () => window.clearTimeout(timer);
  }, [saved]);

  const select = async (next: AdultContentMode) => {
    if (saving || loading || loadFailed || next === mode) return;
    setSaving(true);
    setSaveFailed(false);
    setSaved(false);
    try {
      await updateMode(next);
      setSaved(true);
    } catch {
      setSaveFailed(true);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="p-3 rounded-md bg-background border border-line-subtle space-y-2.5" data-mf-shell>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
        <div className="space-y-0.5 min-w-0">
          <div className="text-xs sm:text-sm font-medium text-text-strong flex items-center gap-1.5">
            <ShieldAlert className="w-3.5 h-3.5 text-rose-500" strokeWidth={1.8} />
            <span>{t("settings.adultContentLabel")}</span>
          </div>
          <div className="text-[11px] text-text-faint leading-relaxed">{t("settings.adultContentDesc")}</div>
        </div>

        {loadFailed ? (
          <button
            type="button"
            onClick={() => void refresh()}
            className="shrink-0 inline-flex items-center gap-1.5 px-2.5 h-8 rounded-md border border-line bg-surface hover:bg-surfaceHover text-[11px] font-medium text-text-strong transition-colors duration-fast ease-soft"
          >
            <RotateCw className="w-3 h-3" />
            {t("settings.dmStrangerRetry")}
          </button>
        ) : loading ? (
          <span className="shrink-0 inline-flex items-center gap-1.5 text-[11px] text-text-faint">
            <Loader2 className="w-3.5 h-3.5 animate-spin" />
            {t("settings.privacyLoading")}
          </span>
        ) : (
          <div role="radiogroup" aria-label={t("settings.adultContentLabel")} className="shrink-0 inline-flex rounded-lg border border-line bg-surface p-0.5">
            {OPTIONS.map((opt) => {
              const active = mode === opt.value;
              return (
                <button
                  key={opt.value}
                  type="button"
                  role="radio"
                  aria-checked={active}
                  disabled={saving}
                  onClick={() => void select(opt.value)}
                  className={
                    "px-2.5 py-1 rounded-md text-[11px] font-medium transition-colors duration-fast ease-soft cursor-pointer disabled:cursor-wait disabled:opacity-60 " +
                    (active ? "bg-primary text-primary-foreground shadow-2xs" : "text-text-muted hover:text-text-strong hover:bg-surfaceHover")
                  }
                >
                  {t(opt.labelKey)}
                </button>
              );
            })}
          </div>
        )}
      </div>

      {loadFailed && (
        <div className="text-[11px] text-danger-soft flex items-center gap-1.5">
          <AlertCircle className="w-3 h-3 shrink-0" />
          <span>{t("settings.privacyLoadFailed")}</span>
        </div>
      )}
      {saveFailed && (
        <div className="text-[11px] text-danger-soft flex items-center gap-1.5">
          <AlertCircle className="w-3 h-3 shrink-0" />
          <span>{t("settings.privacySaveFailed")}</span>
        </div>
      )}
      {saved && <div role="status" className="text-[11px] text-success">{t("settings.privacySaved")}</div>}
      {saving && !saveFailed && <div role="status" className="text-[11px] text-text-faint">{t("settings.privacySaving")}</div>}
    </div>
  );
}
