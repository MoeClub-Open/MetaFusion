"use client";

import { useEffect, useId, useRef, useState } from "react";
import { AlertTriangle, Check, HelpCircle } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import {
  fetchEntityQuality,
  qualityIssueTab,
  type QualityIssue,
  type QualityReport,
  type QualityStatus,
} from "@/lib/quality";

type Props = {
  entityId: string;
  /** 点击诊断行时跳到对应页签；发行页等可滚动到目录区。 */
  onNavigate?: (tabId: string, issue: QualityIssue) => void;
  className?: string;
};

/**
 * 详情页质量状态标签：紧挨 PUBLISHED / 标题旁。
 * 按需请求 /quality；加载显示灰占位；接口失败整颗隐藏，不影响页面。
 * 匿名可读（与实体详情同可见性），不设登录墙。
 */
export function QualityBadge({ entityId, onNavigate, className = "" }: Props) {
  const { t } = useI18n();
  const [report, setReport] = useState<QualityReport | null>(null);
  const [phase, setPhase] = useState<"loading" | "ready" | "hidden">("loading");
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const panelId = useId();

  useEffect(() => {
    let alive = true;
    setPhase("loading");
    setReport(null);
    setOpen(false);
    fetchEntityQuality(entityId)
      .then((r) => {
        if (!alive) return;
        setReport(r);
        setPhase("ready");
      })
      .catch(() => {
        if (!alive) return;
        setReport(null);
        setPhase("hidden");
      });
    return () => {
      alive = false;
    };
  }, [entityId]);

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: MouseEvent) => {
      if (!containerRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        buttonRef.current?.focus();
      }
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  if (phase === "hidden") return null;

  if (phase === "loading") {
    return (
      <span
        className={`inline-flex h-6 w-16 animate-pulse rounded-md bg-surfaceSubtle border border-line-subtle ${className}`}
        aria-hidden
      />
    );
  }

  if (!report) return null;

  const status = report.status as QualityStatus;
  const label = statusLabel(status, t);
  const tone = statusTone(status);
  const Icon = statusIcon(status);
  const issues = Array.isArray(report.issues) ? report.issues : [];
  const canOpen = issues.length > 0;

  return (
    <div ref={containerRef} className={`relative inline-flex ${className}`}>
      <button
        ref={buttonRef}
        type="button"
        aria-label={label}
        aria-expanded={canOpen ? open : undefined}
        aria-controls={canOpen && open ? panelId : undefined}
        aria-haspopup={canOpen ? "dialog" : undefined}
        disabled={!canOpen}
        onClick={() => {
          if (canOpen) setOpen((v) => !v);
        }}
        className={`mf-focus inline-flex items-center gap-1 px-2 py-0.5 rounded-md border text-[11px] font-medium transition-colors duration-fast ease-soft ${tone.badge} ${
          canOpen ? "cursor-pointer hover:opacity-90" : "cursor-default"
        }`}
      >
        <Icon className="w-3 h-3 shrink-0" strokeWidth={2} aria-hidden />
        <span>{label}</span>
      </button>

      {open && canOpen && (
        <div
          id={panelId}
          role="dialog"
          aria-label={t("catalog.quality.diagnosticsTitle")}
          className="absolute left-0 top-full z-40 mt-1.5 w-[min(20rem,calc(100vw-2rem))] max-h-[min(16rem,50vh)] overflow-y-auto rounded-panel border border-line bg-surface p-2 shadow-elevated"
        >
          <ul className="space-y-0.5">
            {issues.map((issue, i) => {
              const tab = qualityIssueTab(issue.code);
              const text = issueLabel(issue, t);
              return (
                <li key={`${issue.code}-${issue.field || ""}-${i}`}>
                  <button
                    type="button"
                    className="mf-focus w-full text-left rounded-md px-2 py-1.5 text-xs text-text-body hover:bg-surfaceSubtle transition-colors duration-fast ease-soft"
                    onClick={() => {
                      setOpen(false);
                      onNavigate?.(tab, issue);
                    }}
                  >
                    <span className="block font-medium text-text-strong">{text}</span>
                    {issue.field && (
                      <span className="mt-0.5 block font-mono text-[10px] text-text-faint">{issue.field}</span>
                    )}
                  </button>
                </li>
              );
            })}
          </ul>
        </div>
      )}
    </div>
  );
}

function statusLabel(status: QualityStatus, t: (key: string, vars?: Record<string, string | number>) => string): string {
  switch (status) {
    case "complete":
      return t("catalog.quality.complete");
    case "partial":
      return t("catalog.quality.partial");
    case "needs_verification":
      return t("catalog.quality.needsVerification");
    default:
      return status;
  }
}

function statusTone(status: QualityStatus): { badge: string } {
  switch (status) {
    case "complete":
      return {
        badge: "bg-success/10 text-success border-success/25",
      };
    case "partial":
      return {
        badge: "bg-warn/10 text-warn border-warn/25",
      };
    case "needs_verification":
      return {
        badge: "bg-surfaceSubtle text-text-muted border-line",
      };
    default:
      return { badge: "bg-surfaceSubtle text-text-muted border-line" };
  }
}

function statusIcon(status: QualityStatus) {
  switch (status) {
    case "complete":
      return Check;
    case "partial":
      return AlertTriangle;
    case "needs_verification":
      return HelpCircle;
    default:
      return HelpCircle;
  }
}

// i18n: track_position_gap copy synced with Kimi「音轨序号有缺口或重复」
function issueLabel(issue: QualityIssue, t: (key: string, vars?: Record<string, string | number>) => string): string {
  const key = `catalog.quality.issue.${issue.code}`;
  const field = issue.field || "";
  const value = issue.value || "";
  // 字典缺 key 时 t 通常回传 key 本身；此时退回服务端 message，避免露出裸码。
  const localized = t(key, { field, value });
  if (localized && localized !== key) return localized;
  return issue.message || issue.code;
}
