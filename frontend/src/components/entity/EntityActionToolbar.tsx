"use client";

import React from "react";
import { Edit3, GitMerge } from "lucide-react";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";

interface Props {
  onEdit: () => void;
  onMerge?: () => void;
  entityTypeLabel?: string;
  className?: string;
  children?: React.ReactNode;
}

export function EntityActionToolbar({
  onEdit,
  onMerge,
  entityTypeLabel,
  className = "",
  children,
}: Props) {
  const { user } = useAuth();
  const { t } = useI18n();

  const label = entityTypeLabel || t("entity.toolbar.defaultLabel");

  return (
    <div className={`flex flex-wrap items-center gap-1.5 font-mono text-xs ${className}`}>
      {/* 仅登录用户展示编辑与合并操作按钮 */}
      {user && (
        <>
          <button
            type="button"
            onClick={onEdit}
            className="inline-flex items-center gap-1.5 px-3 h-7 rounded-md bg-amber-400 hover:bg-amber-300 text-black font-semibold transition-all shadow-xs active:scale-95"
          >
            <Edit3 className="w-3.5 h-3.5" />
            {t("entity.toolbar.edit", { entityType: label })}
          </button>

          {onMerge && (
            <button
              type="button"
              onClick={onMerge}
              className="inline-flex items-center gap-1.5 px-3 h-7 rounded-md bg-purple-500/10 border border-purple-500/30 hover:bg-purple-500/20 text-alt-soft hover:text-alt-soft transition-all active:scale-95"
            >
              <GitMerge className="w-3.5 h-3.5" />
              {t("entity.toolbar.merge", { entityType: label })}
            </button>
          )}
        </>
      )}

      {children}
    </div>
  );
}
