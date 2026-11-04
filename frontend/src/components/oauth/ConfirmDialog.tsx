"use client";

import React from "react";
import { AlertTriangle, Loader2 } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { Modal } from "@/components/ui/Modal";

/**
 * 破坏性动作的二次确认。
 * 不用原生 confirm()：那东西不可本地化、不可样式化，也只测得到个布尔。
 */
export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel,
  busy = false,
  onClose,
  onConfirm,
}: {
  open: boolean;
  title: React.ReactNode;
  message: React.ReactNode;
  confirmLabel: string;
  busy?: boolean;
  onClose: () => void;
  onConfirm: () => void;
}) {
  const { t } = useI18n();
  return (
    <Modal open={open} onClose={onClose} title={title} icon={<AlertTriangle className="w-4 h-4 text-danger" />}>
      <div className="space-y-3 text-xs">
        <div className="p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 text-danger-soft leading-relaxed">
          {message}
        </div>
        <div className="flex flex-wrap justify-end gap-2 pt-1">
          <button
            type="button"
            onClick={onClose}
            disabled={busy}
            className="shrink-0 whitespace-nowrap px-3 py-1.5 rounded-lg bg-surfaceSubtle hover:bg-surfaceHover border border-line text-text-body text-xs transition-colors duration-fast ease-soft disabled:opacity-50 cursor-pointer"
          >
            {t("common.cancel")}
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={busy}
            className="shrink-0 whitespace-nowrap px-4 py-1.5 rounded-lg bg-rose-500/90 hover:bg-rose-500 text-emphasis text-xs font-semibold transition-colors duration-fast ease-soft disabled:opacity-50 cursor-pointer inline-flex items-center gap-1.5"
          >
            {busy ? <Loader2 className="w-3.5 h-3.5 shrink-0 animate-spin" /> : null}
            <span>{confirmLabel}</span>
          </button>
        </div>
      </div>
    </Modal>
  );
}
