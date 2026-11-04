"use client";

import React, { useEffect, useId, useState } from "react";
import { AlertCircle, Check, Flag } from "lucide-react";
import { Modal } from "@/components/ui/Modal";
import { useI18n } from "@/i18n/I18nProvider";
import { REPORT_REASONS, submitReport, type ReportReason, type ReportTargetType } from "@/lib/api/reports";
import { localizeReportError } from "@/lib/reportErrors";

interface ReportDialogProps {
  open: boolean;
  onClose: () => void;
  targetType: ReportTargetType;
  targetId: string;
  /** 提交成功后回调（列表页可据此刷新），失败不回调。 */
  onSubmitted?: () => void;
}

/**
 * 举报弹窗：受控（open/onClose 由调用方持有），外壳复用 components/ui/Modal，
 * 焦点陷阱、Esc 关闭与滚动锁都由它负责。
 * 失败一律按机器码翻成四语文案（lib/reportErrors.ts），不把裸码显示给用户。
 */
export default function ReportDialog({
  open,
  onClose,
  targetType,
  targetId,
  onSubmitted,
}: ReportDialogProps) {
  const { t } = useI18n();
  const uid = useId();
  const reasonId = `${uid}-reason`;
  const detailId = `${uid}-detail`;
  const evidenceId = `${uid}-evidence`;

  const [reason, setReason] = useState<ReportReason>(REPORT_REASONS[0]);
  const [detail, setDetail] = useState("");
  const [evidenceUrl, setEvidenceUrl] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [errorText, setErrorText] = useState("");
  const [done, setDone] = useState(false);

  // 每次打开（或换举报对象）都回到初始态：上一次的错误与已提交成功的横幅不能留给下一次。
  useEffect(() => {
    if (!open) return;
    setReason(REPORT_REASONS[0]);
    setDetail("");
    setEvidenceUrl("");
    setSubmitting(false);
    setErrorText("");
    setDone(false);
  }, [open, targetType, targetId]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    setErrorText("");
    try {
      await submitReport({
        target_type: targetType,
        target_id: targetId,
        reason,
        detail,
        evidence_url: evidenceUrl,
      });
      setDone(true);
      onSubmitted?.();
    } catch (err) {
      setErrorText(localizeReportError(err, t));
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Modal open={open} onClose={onClose} title={t("report.title")} icon={<Flag className="w-4 h-4 text-primary" />}>
      {done ? (
        <div className="space-y-4">
          <div className="rounded-control border border-emerald-500/25 bg-emerald-500/10 p-3 text-emerald-600 dark:text-success text-sm flex items-start gap-2">
            <Check className="w-4 h-4 shrink-0 mt-0.5" />
            <span className="min-w-0">{t("report.submitted")}</span>
          </div>
          <div className="flex justify-end">
            <button
              type="button"
              onClick={onClose}
              className="shrink-0 whitespace-nowrap px-3.5 h-9 max-sm:min-h-[44px] rounded-md bg-primary text-primary-foreground text-sm font-semibold hover:opacity-90"
            >
              {t("report.close")}
            </button>
          </div>
        </div>
      ) : (
        <form onSubmit={handleSubmit} className="space-y-4">
          <div className="min-w-0 text-xs font-mono text-text-faint flex items-center gap-2">
            <span className="shrink-0 whitespace-nowrap">{t(`report.target.${targetType}`)}</span>
            <span className="truncate text-text-muted">{targetId}</span>
          </div>

          <div className="space-y-1.5">
            <label htmlFor={reasonId} className="text-xs font-mono text-text-faint">
              {t("report.reasonLabel")}
            </label>
            <select
              id={reasonId}
              value={reason}
              onChange={(e) => setReason(e.target.value as ReportReason)}
              disabled={submitting}
              className="w-full h-9 max-sm:min-h-[44px] px-2 rounded-md bg-black/[0.03] dark:bg-white/[0.06] border border-line text-sm text-text-strong disabled:opacity-50"
            >
              {REPORT_REASONS.map((code) => (
                <option key={code} value={code}>
                  {t(`report.reason.${code}`)}
                </option>
              ))}
            </select>
          </div>

          <div className="space-y-1.5">
            <label htmlFor={detailId} className="text-xs font-mono text-text-faint">
              {t("report.detailLabel")}
            </label>
            <textarea
              id={detailId}
              value={detail}
              onChange={(e) => setDetail(e.target.value)}
              disabled={submitting}
              rows={4}
              placeholder={t("report.detailPlaceholder")}
              className="w-full px-3 py-2 rounded-md bg-black/[0.03] dark:bg-white/[0.06] border border-line text-sm text-text-strong placeholder:text-text-muted focus:outline-none focus:border-primary/50 disabled:opacity-50"
            />
          </div>

          <div className="space-y-1.5">
            <label htmlFor={evidenceId} className="text-xs font-mono text-text-faint">
              {t("report.evidenceLabel")}
            </label>
            <input
              id={evidenceId}
              type="url"
              inputMode="url"
              value={evidenceUrl}
              onChange={(e) => setEvidenceUrl(e.target.value)}
              disabled={submitting}
              placeholder={t("report.evidencePlaceholder")}
              className="w-full h-9 max-sm:min-h-[44px] px-3 rounded-md bg-black/[0.03] dark:bg-white/[0.06] border border-line text-sm text-text-strong placeholder:text-text-muted focus:outline-none focus:border-primary/50 disabled:opacity-50"
            />
          </div>

          {errorText && (
            <div
              role="alert"
              className="rounded-control border border-red-500/20 bg-red-500/10 p-3 text-red-500 dark:text-danger-soft font-mono text-xs flex items-start gap-2"
            >
              <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
              <span className="min-w-0 break-words">{errorText}</span>
            </div>
          )}

          <div className="flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              disabled={submitting}
              className="shrink-0 whitespace-nowrap px-3.5 h-9 max-sm:min-h-[44px] rounded-md border border-line text-sm text-text-body hover:text-emphasis disabled:opacity-50"
            >
              {t("report.cancel")}
            </button>
            <button
              type="submit"
              disabled={submitting}
              className="shrink-0 whitespace-nowrap px-3.5 h-9 max-sm:min-h-[44px] rounded-md bg-primary text-primary-foreground text-sm font-semibold hover:opacity-90 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {submitting ? t("report.submitting") : t("report.submit")}
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}
