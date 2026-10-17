"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { AlertCircle, Heart, Loader2, Mail, RotateCw } from "lucide-react";
import { fetchPrivacySettings, updatePrivacySettings, type PrivacySettings } from "@/lib/api";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";

type PrivacyField = keyof PrivacySettings;

/** 只在服务端保存成功后切换；缺值、加载失败和关闭是不同状态。 */
export function ProfilePrivacyCards() {
  const { user, syncPrivacySettings } = useAuth();
  const { t } = useI18n();
  const [settings, setSettings] = useState<PrivacySettings | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [saving, setSaving] = useState<PrivacyField | null>(null);
  const [saveFailed, setSaveFailed] = useState<PrivacyField | null>(null);
  const [saved, setSaved] = useState<PrivacyField | null>(null);
  const request = useRef(0);
  const savingRequest = useRef(false);
  const userId = user?.id;

  const load = useCallback(async () => {
    const current = ++request.current;
    setSettings(null);
    setLoadFailed(false);
    setSaveFailed(null);
    setSaved(null);
    setSaving(null);
    savingRequest.current = false;
    if (!userId) return;
    try {
      const result = await fetchPrivacySettings();
      if (current !== request.current) return;
      setSettings(result);
      syncPrivacySettings(userId, result);
    } catch {
      if (current === request.current) setLoadFailed(true);
    }
  }, [userId, syncPrivacySettings]);

  useEffect(() => {
    void load();
    return () => { ++request.current; };
  }, [load]);

  useEffect(() => {
    if (!saved) return;
    const timer = window.setTimeout(() => setSaved(null), 3000);
    return () => window.clearTimeout(timer);
  }, [saved]);

  const toggle = async (field: PrivacyField) => {
    if (!settings || !userId || savingRequest.current) return;
    savingRequest.current = true;
    const current = request.current;
    setSaving(field);
    setSaveFailed(null);
    setSaved(null);
    try {
      const result = await updatePrivacySettings({ [field]: !settings[field] });
      if (current !== request.current) return;
      setSettings(result);
      syncPrivacySettings(userId, result);
      setSaved(field);
    } catch {
      if (current === request.current) setSaveFailed(field);
    } finally {
      if (current === request.current) {
        savingRequest.current = false;
        setSaving(null);
      }
    }
  };

  const cards = [
    { field: "favorites_public", label: "settings.privacyFavorites", description: "settings.privacyFavoritesDesc", icon: Heart, color: "text-rose-500" },
    { field: "email_public", label: "settings.privacyEmail", description: "settings.privacyEmailDesc", icon: Mail, color: "text-sky-500" },
  ] as const;

  return <>
    {cards.map(({ field, label, description, icon: Icon, color }) => (
      <div key={field} className="p-3 rounded-md bg-background border border-line-subtle" data-mf-shell>
        <div className="flex items-center justify-between gap-3">
          <div className="space-y-0.5 min-w-0">
            <div className="text-xs sm:text-sm font-medium text-text-strong flex items-center gap-1.5">
              <Icon className={`w-3.5 h-3.5 ${color}`} strokeWidth={1.8} />
              <span>{t(label)}</span>
            </div>
            <div className="text-[11px] text-text-faint leading-relaxed">{t(description)}</div>
          </div>
          {loadFailed ? (
            <button type="button" onClick={() => void load()} className="shrink-0 inline-flex items-center gap-1.5 px-2.5 h-8 rounded-md border border-line bg-surface hover:bg-surfaceHover text-[11px] font-medium text-text-strong">
              <RotateCw className="w-3 h-3" />{t("common.retry")}
            </button>
          ) : settings === null ? (
            <span className="shrink-0 inline-flex items-center gap-1.5 text-[11px] text-text-faint" role="status">
              <Loader2 className="w-3.5 h-3.5 animate-spin" />{t("settings.privacyLoading")}
            </span>
          ) : (
            <button
              type="button" role="switch" aria-checked={settings[field]} aria-label={t(label)}
              disabled={saving !== null} onClick={() => void toggle(field)}
              className={"relative shrink-0 w-9 h-5 rounded-full border transition-colors duration-fast ease-soft cursor-pointer disabled:cursor-wait disabled:opacity-60 " + (settings[field] ? "bg-primary border-primary" : "bg-emphasis/15 border-line")}
            >
              <span aria-hidden="true" className={"absolute left-0 top-[3px] w-3.5 h-3.5 rounded-full bg-[color:var(--primary-contrast-color)] transition-transform duration-fast ease-soft " + (settings[field] ? "translate-x-[18px]" : "translate-x-[3px]")} />
            </button>
          )}
        </div>
        {(loadFailed || saveFailed === field) && <div role="alert" className="mt-2 text-[11px] text-danger-soft flex items-center gap-1.5">
          <AlertCircle className="w-3 h-3 shrink-0" /><span>{t(loadFailed ? "settings.privacyLoadFailed" : "settings.privacySaveFailed")}</span>
        </div>}
        {saving === field && <div role="status" className="mt-2 text-[11px] text-text-faint">{t("settings.privacySaving")}</div>}
        {saved === field && <div role="status" className="mt-2 text-[11px] text-success">{t("settings.privacySaved")}</div>}
      </div>
    ))}
  </>;
}
