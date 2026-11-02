"use client";

// 管理台与目录编辑页共用的呈现原语：区块标题 + 失败/状态反馈。
//
// 这几个块原先住在 app/admin/components/tabs/accountAccess/shared.tsx——一个账号域的目录下，
// 而它的消费者全是目录域（ExchangeTab、DefinitionHistory），账号标签本身早已不可达。
// 账号标签删除后，仍被使用的部分迁到通用位置，账号专用的取数器/多语言编辑器随之删除。
//
// 取数失败一律用 ErrorNotice 如实显示并给重试：空列表会被读成"这里真的没有数据"。

import React from "react";
import { AlertTriangle } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";

export function SectionHeader({
  icon,
  title,
  desc,
  actions,
}: {
  icon: React.ReactNode;
  title: string;
  desc?: string;
  actions?: React.ReactNode;
}) {
  return (
    <div className="flex min-h-8 flex-wrap items-center justify-between gap-2">
      <div className="min-w-0">
        <h3 className="text-sm font-semibold text-text-strong flex items-center gap-2">
          {icon}
          <span>{title}</span>
        </h3>
        {desc ? <p className="mt-1 text-xs text-text-muted">{desc}</p> : null}
      </div>
      {actions ? <div className="flex items-center gap-2 shrink-0">{actions}</div> : null}
    </div>
  );
}

export function ErrorNotice({
  message,
  onRetry,
  permissionHint,
}: {
  message: string;
  onRetry?: () => void;
  /** 中立的"本块需要什么权限"说明：无论当前是 403 还是上游不可用都成立。 */
  permissionHint?: string;
}) {
  const { t } = useI18n();
  return (
    <div className="p-3 rounded-xl bg-rose-500/10 border border-rose-500/30 space-y-2">
      <div className="flex items-start gap-2 text-danger-soft text-xs leading-relaxed">
        <AlertTriangle className="w-3.5 h-3.5 shrink-0 mt-0.5" />
        <span>{message}</span>
      </div>
      {permissionHint ? (
        <p className="text-[11px] text-danger-soft/70 leading-relaxed">{permissionHint}</p>
      ) : null}
      {onRetry ? (
        <button
          type="button"
          onClick={onRetry}
          className="px-2.5 py-1 rounded-lg bg-rose-500/20 hover:bg-rose-500/30 text-danger-soft text-[11px] font-medium transition-colors duration-fast ease-soft cursor-pointer"
        >
          {t("common.retry")}
        </button>
      ) : null}
    </div>
  );
}

export function StatusMessage({ kind, text }: { kind: "ok" | "err"; text: string }) {
  return (
    <div
      role="status"
      className={`p-2.5 rounded-lg text-[11px] font-mono leading-relaxed ${
        kind === "ok" ? "bg-emerald-500/15 text-success-soft" : "bg-rose-500/20 text-danger-soft"
      }`}
    >
      {text}
    </div>
  );
}
