"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { AlertCircle, Check, ChevronLeft, ChevronRight, RefreshCw } from "lucide-react";
import { Navbar } from "@/components/Navbar";
import { PageShell } from "@/components/ui/PageShell";
import { useI18n } from "@/i18n/I18nProvider";
import { useAuth } from "@/lib/authContext";
import { fetchMyReports, submitAppeal, type MyReportItem } from "@/lib/api/reports";
import { localizeReportError } from "@/lib/reportErrors";
import { isoTimestamp, localDateTime } from "@/lib/datetime";

const PAGE_SIZE = 20;

/**
 * 我的举报：列表 + 申诉入口。
 *
 * 三条状态严格分开——"取数失败"（loadFailure）、"还没有举报"（empty）、"有数据"：
 * 空列表是服务端明确回的 items=[]，不能和失败共用同一段文案，否则断网会被讲成"你没举报过"。
 * 申诉入口只认服务端给的 can_appeal，不在前端重算"本人 + 已受理 + 未申诉"。
 */
export default function MyReportsPage() {
  const { t, locale } = useI18n();
  const { user, loading: authLoading } = useAuth();

  const [items, setItems] = useState<MyReportItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  // 失败原因保存原始错误对象，渲染时再翻成文案：翻译函数进依赖会导致切语言重拉一次列表。
  const [loadFailure, setLoadFailure] = useState<unknown>(null);
  const [reloadKey, setReloadKey] = useState(0);

  // 申诉表单一次只开一条（同时只有一份说明在写），提交成功后整页重拉。
  const [appealForId, setAppealForId] = useState<string | null>(null);
  const [appealBody, setAppealBody] = useState("");
  const [appealBusy, setAppealBusy] = useState(false);
  const [appealFailure, setAppealFailure] = useState<unknown>(null);
  const [appealDoneId, setAppealDoneId] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setLoadFailure(null);
    try {
      const res = await fetchMyReports(page, PAGE_SIZE);
      setItems(res.items);
      setTotal(res.total);
    } catch (err) {
      setItems([]);
      setTotal(0);
      setLoadFailure(err);
    } finally {
      setLoading(false);
    }
  }, [page]);

  useEffect(() => {
    if (!user) return;
    void load();
  }, [user, load, reloadKey]);

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const openAppeal = (reportId: string) => {
    setAppealForId(reportId);
    setAppealBody("");
    setAppealFailure(null);
    setAppealDoneId("");
  };

  const closeAppeal = () => {
    setAppealForId(null);
    setAppealBody("");
    setAppealFailure(null);
  };

  const handleAppeal = async (report: MyReportItem) => {
    if (appealBusy) return;
    setAppealBusy(true);
    setAppealFailure(null);
    try {
      await submitAppeal(report.id, appealBody);
      closeAppeal();
      setAppealDoneId(report.id);
      // 服务端是唯一事实来源：提交成功后重拉列表（can_appeal 与申诉结论都会变）。
      await load();
    } catch (err) {
      setAppealFailure(err);
    } finally {
      setAppealBusy(false);
    }
  };

  const targetLabel = (item: MyReportItem) => t(`report.target.${item.target_type}`);
  const reasonLabel = (item: MyReportItem) => t(`report.reason.${item.reason}`);
  const statusLabel = (item: MyReportItem) => t(`report.status.${item.status}`);
  const enforcementLabel = (item: MyReportItem) =>
    item.enforcement ? t(`report.enforcement.${item.enforcement}`) : t("report.enforcement.none");
  const appealStatusLabel = (item: MyReportItem) =>
    item.appeal ? t(`report.appealStatus.${item.appeal.status}`) : "";

  return (
    <div className="min-h-screen flex flex-col">
      <Navbar />
      <PageShell width="narrow" spacing="none" contentClassName="space-y-4">
        <div className="space-y-1">
          <h1 className="font-display text-xl sm:text-2xl font-bold tracking-tight text-text-strong">
            {t("reports.pageTitle")}
          </h1>
          <p className="text-xs text-text-muted">{t("reports.pageDesc")}</p>
        </div>

        {authLoading ? (
          <div className="p-8 text-center text-xs font-mono text-text-faint">{t("common.loading")}</div>
        ) : !user ? (
          // 未登录不跳转（/reports 不在 AuthGate 的受保护前缀里）：就地给登录入口，
          // 登录后回到本页。
          <div className="p-8 rounded-card border border-line bg-surface text-center space-y-3">
            <AlertCircle className="w-5 h-5 text-amber-500 mx-auto" strokeWidth={1.6} />
            <p className="text-sm text-text-body">{t("report.loginRequired")}</p>
            <Link
              href={`/login?redirect=${encodeURIComponent("/reports")}`}
              className="inline-flex items-center px-3.5 h-8 rounded-md bg-primary text-white text-xs font-semibold hover:opacity-90"
            >
              {t("nav.login")}
            </Link>
          </div>
        ) : loading ? (
          <div className="p-8 text-center text-xs font-mono text-text-faint">{t("common.loading")}</div>
        ) : loadFailure ? (
          // 失败态：只讲"取不到"并给重试，绝不说成"没有举报"。
          <div role="alert" className="p-8 rounded-card border border-line bg-surface text-center space-y-3">
            <p className="text-sm text-amber-700 dark:text-warn-soft">{t("reports.loadFailed")}</p>
            <p className="text-xs font-mono text-text-faint break-words">{localizeReportError(loadFailure, t)}</p>
            <button
              type="button"
              onClick={() => setReloadKey((k) => k + 1)}
              className="inline-flex items-center gap-1.5 px-3 h-8 rounded-md border border-line text-xs text-text-body hover:text-primary"
            >
              <RefreshCw className="w-3 h-3" />
              <span>{t("reports.retry")}</span>
            </button>
          </div>
        ) : items.length === 0 ? (
          <div className="p-5 rounded-card border border-dashed border-line bg-surface text-center text-sm text-text-faint">
            {t("reports.empty")}
          </div>
        ) : (
          <ul className="space-y-3">
            {items.map((item) => (
              <li key={item.id} className="rounded-card border border-line bg-surface shadow-2xs overflow-hidden">
                <div className="px-4 py-3 border-b border-line-subtle flex items-start justify-between gap-3">
                  <div className="min-w-0 space-y-1">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-xs font-semibold text-text-strong">{targetLabel(item)}</span>
                      <span className="text-[11px] font-mono text-text-muted truncate max-w-[220px]">
                        {item.target_id}
                      </span>
                    </div>
                    {item.target_context?.excerpt && (
                      <p className="text-xs text-text-muted line-clamp-2 break-words">
                        {item.target_context.excerpt}
                      </p>
                    )}
                  </div>
                  <span className="shrink-0 px-2 py-0.5 rounded-sm border border-line text-[11px] font-mono text-text-body">
                    {statusLabel(item)}
                  </span>
                </div>

                <div className="px-4 py-3 space-y-2 text-xs">
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                    <div className="space-y-0.5">
                      <span className="font-mono text-text-faint">{t("reports.colTime")}</span>
                      <p className="text-text-body" title={localDateTime(item.created_at, locale)}>
                        {isoTimestamp(item.created_at)}
                      </p>
                    </div>
                    <div className="space-y-0.5">
                      <span className="font-mono text-text-faint">{t("reports.colReason")}</span>
                      <p className="text-text-body">{reasonLabel(item)}</p>
                    </div>
                    <div className="space-y-0.5">
                      <span className="font-mono text-text-faint">{t("reports.colHandler")}</span>
                      <p className="text-text-body">{item.reviewer_name || "—"}</p>
                    </div>
                    <div className="space-y-0.5">
                      <span className="font-mono text-text-faint">{t("reports.colEnforcement")}</span>
                      <p className="text-text-body">{enforcementLabel(item)}</p>
                    </div>
                    <div className="space-y-0.5 sm:col-span-2">
                      <span className="font-mono text-text-faint">{t("reports.colNote")}</span>
                      <p className="text-text-body break-words">{item.review_note || "—"}</p>
                    </div>
                    {item.detail && (
                      <div className="space-y-0.5 sm:col-span-2">
                        <span className="font-mono text-text-faint">{t("report.detailLabel")}</span>
                        <p className="text-text-body break-words">{item.detail}</p>
                      </div>
                    )}
                    {item.evidence_url && (
                      <div className="space-y-0.5 sm:col-span-2">
                        <span className="font-mono text-text-faint">{t("report.evidenceLabel")}</span>
                        <a
                          href={item.evidence_url}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-primary hover:underline break-all"
                        >
                          {item.evidence_url}
                        </a>
                      </div>
                    )}
                  </div>
                </div>

                {(item.appeal || item.can_appeal) && (
                  <div className="px-4 py-3 border-t border-line-subtle bg-surfaceSubtle space-y-2 text-xs">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-mono text-text-faint">{t("reports.colAppeal")}</span>
                      {item.appeal ? (
                        <span className="px-2 py-0.5 rounded-sm border border-line text-[11px] font-mono text-text-body">
                          {appealStatusLabel(item)}
                        </span>
                      ) : (
                        <span className="text-text-muted">{t("report.appealStatus.none")}</span>
                      )}
                    </div>
                    {item.appeal && (
                      <div className="space-y-1">
                        <p className="text-text-body break-words">{item.appeal.body}</p>
                        {(item.appeal.reviewer_name || item.appeal.review_note) && (
                          <p className="text-text-muted break-words">
                            {[
                              item.appeal.reviewer_name,
                              localDateTime(item.appeal.reviewed_at, locale),
                              item.appeal.review_note,
                            ]
                              .filter(Boolean)
                              .join(" · ")}
                          </p>
                        )}
                      </div>
                    )}

                    {appealDoneId === item.id && (
                      <p className="text-emerald-600 dark:text-success flex items-center gap-1.5">
                        <Check className="w-3.5 h-3.5 shrink-0" />
                        <span>{t("reports.appealSubmitted")}</span>
                      </p>
                    )}

                    {item.can_appeal && appealForId !== item.id && (
                      <button
                        type="button"
                        onClick={() => openAppeal(item.id)}
                        className="px-3 h-8 rounded-md border border-line text-text-body hover:text-primary text-xs"
                      >
                        {t("reports.appealTitle")}
                      </button>
                    )}

                    {item.can_appeal && appealForId === item.id && (
                      <div className="space-y-2">
                        <label
                          htmlFor={`appeal-${item.id}`}
                          className="font-mono text-text-faint block"
                        >
                          {t("reports.appealBody")}
                        </label>
                        <textarea
                          id={`appeal-${item.id}`}
                          value={appealBody}
                          onChange={(e) => setAppealBody(e.target.value)}
                          disabled={appealBusy}
                          rows={3}
                          placeholder={t("reports.appealPlaceholder")}
                          className="w-full px-3 py-2 rounded-md bg-background border border-line text-xs text-text-strong placeholder:text-text-muted focus:outline-none focus:border-primary/50 disabled:opacity-50"
                        />
                        {appealFailure ? (
                          <p role="alert" className="text-red-500 dark:text-danger-soft font-mono">
                            {localizeReportError(appealFailure, t)}
                          </p>
                        ) : null}
                        <div className="flex items-center justify-end gap-2">
                          <button
                            type="button"
                            onClick={closeAppeal}
                            disabled={appealBusy}
                            className="px-3 h-8 rounded-md border border-line text-text-body hover:text-emphasis disabled:opacity-50"
                          >
                            {t("report.cancel")}
                          </button>
                          <button
                            type="button"
                            onClick={() => void handleAppeal(item)}
                            disabled={appealBusy || appealBody.trim() === ""}
                            className="px-3 h-8 rounded-md bg-primary text-white font-semibold hover:opacity-90 disabled:opacity-50 disabled:cursor-not-allowed"
                          >
                            {appealBusy ? t("report.submitting") : t("reports.appealSubmit")}
                          </button>
                        </div>
                      </div>
                    )}
                  </div>
                )}
              </li>
            ))}
          </ul>
        )}

        {user && !loading && !loadFailure && totalPages > 1 && (
          <div className="flex items-center justify-between gap-2 text-xs font-mono text-text-faint">
            <span>{t("reports.pageInfo", { page, pages: totalPages })}</span>
            <div className="flex items-center gap-2">
              <button
                type="button"
                disabled={page <= 1}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
                className="px-3 h-8 rounded-md border border-line bg-surface hover:bg-surfaceBorder disabled:opacity-40 disabled:pointer-events-none inline-flex items-center gap-1"
              >
                <ChevronLeft className="w-3 h-3" />
                <span>{t("reports.pagePrev")}</span>
              </button>
              <button
                type="button"
                disabled={page >= totalPages}
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                className="px-3 h-8 rounded-md border border-line bg-surface hover:bg-surfaceBorder disabled:opacity-40 disabled:pointer-events-none inline-flex items-center gap-1"
              >
                <span>{t("reports.pageNext")}</span>
                <ChevronRight className="w-3 h-3" />
              </button>
            </div>
          </div>
        )}
      </PageShell>
    </div>
  );
}
