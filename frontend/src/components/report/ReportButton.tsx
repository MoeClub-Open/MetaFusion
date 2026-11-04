"use client";

import { useState } from "react";
import { Flag } from "lucide-react";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";
import type { ReportTargetType } from "@/lib/api/reports";
import ReportDialog from "./ReportDialog";

interface ReportButtonProps {
  targetType: ReportTargetType;
  targetId: string;
  className?: string;
  /** 列表行里用：更小的字号与内边距，跟同一行的元信息同高。 */
  compact?: boolean;
}

/**
 * 一行小按钮：点击打开举报弹窗。
 * 未登录**不弹窗**：用户填完一屏才被 401 拒掉没有意义，直接走既有登录跳转
 * （与 components/FavoriteButton.tsx 同一路径：/login?redirect=<当前页>）。
 */
export default function ReportButton({
  targetType,
  targetId,
  className = "",
  compact = false,
}: ReportButtonProps) {
  const { user } = useAuth();
  const { t } = useI18n();
  const [open, setOpen] = useState(false);

  if (!targetId) return null;

  const handleClick = () => {
    if (!user) {
      const back = window.location.pathname + window.location.search;
      window.location.href = `/login?redirect=${encodeURIComponent(back)}`;
      return;
    }
    setOpen(true);
  };

  return (
    <>
      <button
        type="button"
        onClick={handleClick}
        title={t("report.action")}
        aria-label={t("report.action")}
        className={`inline-flex shrink-0 items-center gap-1 whitespace-nowrap transition-colors duration-fast ease-soft text-text-faint hover:text-danger ${
          compact ? "text-[11px]" : "text-xs px-2.5 h-8 max-sm:min-h-[44px] rounded-md border border-line hover:border-danger/40"
        } ${className}`}
      >
        <Flag className={compact ? "w-3 h-3 shrink-0" : "w-3.5 h-3.5 shrink-0"} strokeWidth={1.6} />
        <span>{t("report.action")}</span>
      </button>

      <ReportDialog
        open={open}
        onClose={() => setOpen(false)}
        targetType={targetType}
        targetId={targetId}
      />
    </>
  );
}
