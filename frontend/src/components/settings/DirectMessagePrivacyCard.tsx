"use client";

import { useCallback, useEffect, useState } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { fetchMessageSettings, updateMessageSettings } from "@/lib/api";
import { AlertCircle, Loader2, Mail, RotateCw } from "lucide-react";

/**
 * 「接收陌生人私信」开关（收件人侧，写的是互动服务的 /api/messages/settings）。
 *
 * 三态严格分开，不许互相伪装：
 *   - 读取中：转圈，不渲染开关（也**不**先画成"接收"）；
 *   - 读取失败：显示失败文案 + 重试，**不渲染开关**——把"取不到"画成"默认接收"就是在编造一个
 *     不存在的设置状态（与收件箱页"失败态/空态分开"同一口径）；
 *   - 读到值：渲染真开关，保存中禁用，保存失败**回滚到旧值**并提示（绝不显示没写进去的状态）。
 */
export function DirectMessagePrivacyCard() {
  const { t } = useI18n();
  // accept === null 表示"还没有值"（读取中或读取失败），而不是 false。
  const [accept, setAccept] = useState<boolean | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveFailed, setSaveFailed] = useState(false);
  const [saved, setSaved] = useState(false);

  const load = useCallback(() => {
    setLoadFailed(false);
    setSaveFailed(false);
    setAccept(null);
    fetchMessageSettings()
      .then((v) => setAccept(v))
      .catch(() => setLoadFailed(true));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  // "已保存"提示是瞬时的：3 秒后自己消失，不需要用户手动关掉。
  useEffect(() => {
    if (!saved) return;
    const timer = window.setTimeout(() => setSaved(false), 3000);
    return () => window.clearTimeout(timer);
  }, [saved]);

  const toggle = async () => {
    if (accept === null || saving) return;
    const previous = accept;
    const next = !accept;
    setSaving(true);
    setSaveFailed(false);
    setSaved(false);
    setAccept(next); // 乐观更新：开关要跟手
    try {
      const got = await updateMessageSettings(next);
      setAccept(got); // 以服务端回读为准（不信任本地那个值）
      setSaved(true);
    } catch {
      setAccept(previous); // 回滚
      setSaveFailed(true);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="p-3 rounded-md bg-background border border-line-subtle" data-mf-shell>
      <div className="flex items-center justify-between gap-3">
        <div className="space-y-0.5 min-w-0">
          <div className="text-xs sm:text-sm font-medium text-text-strong flex items-center gap-1.5">
            <Mail className="w-3.5 h-3.5 text-primary" strokeWidth={1.8} />
            <span>{t("settings.dmStrangerLabel")}</span>
          </div>
          <div className="text-[11px] text-text-faint leading-relaxed">{t("settings.dmStrangerDesc")}</div>
        </div>

        {loadFailed ? (
          <button
            type="button"
            onClick={load}
            className="shrink-0 inline-flex items-center gap-1.5 px-2.5 h-8 rounded-md border border-line bg-surface hover:bg-surfaceHover text-[11px] font-medium text-text-strong transition-colors duration-fast ease-soft"
          >
            <RotateCw className="w-3 h-3" />
            {t("settings.dmStrangerRetry")}
          </button>
        ) : accept === null ? (
          <span className="shrink-0 inline-flex items-center gap-1.5 text-[11px] text-text-faint">
            <Loader2 className="w-3.5 h-3.5 animate-spin" />
            {t("settings.dmStrangerLoading")}
          </span>
        ) : (
          <button
            type="button"
            role="switch"
            aria-checked={accept}
            aria-label={t("settings.dmStrangerLabel")}
            disabled={saving}
            onClick={toggle}
            className={
              "relative shrink-0 w-9 h-5 rounded-full border transition-colors duration-fast ease-soft cursor-pointer disabled:cursor-wait disabled:opacity-60 " +
              (accept ? "bg-primary border-primary" : "bg-emphasis/15 border-line")
            }
          >
            <span
              aria-hidden="true"
              className={
                "absolute left-0 top-[3px] w-3.5 h-3.5 rounded-full bg-[color:var(--primary-contrast-color)] transition-transform duration-fast ease-soft " +
                (accept ? "translate-x-[18px]" : "translate-x-[3px]")
              }
            />
          </button>
        )}
      </div>

      {loadFailed && (
        <div className="mt-2 text-[11px] text-danger-soft flex items-center gap-1.5">
          <AlertCircle className="w-3 h-3 shrink-0" />
          <span>{t("settings.dmStrangerLoadFailed")}</span>
        </div>
      )}
      {saveFailed && (
        <div className="mt-2 text-[11px] text-danger-soft flex items-center gap-1.5">
          <AlertCircle className="w-3 h-3 shrink-0" />
          <span>{t("settings.dmStrangerSaveFailed")}</span>
        </div>
      )}
      {saved && <div className="mt-2 text-[11px] text-success">{t("settings.dmStrangerSaved")}</div>}
      {saving && !saveFailed && (
        <div className="mt-2 text-[11px] text-text-faint">{t("settings.dmStrangerSaving")}</div>
      )}
    </div>
  );
}
