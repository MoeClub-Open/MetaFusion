"use client";

// 我的操作记录：把本人跨服务的写操作列出来（目录编目、账号变更、互动发言、资源上传都在同一张
// audit.audit_log 里）。数据源是账号服务的 GET /api/admin/audit-logs，始终按当前用户 ID
// 过滤，管理员的个人设置也只显示本人记录；服务端继续校验调用者的读取范围。
//
// 展示口径与写入侧对齐：changes 是脱敏并截断过的摘要，界面原样展示 "[redacted]" / "a***@domain"，
// 不做二次加工，也不试图"还原"——审计要的是库里真实的那一行（同管理台的取舍）。
//
// 刻意不做的两件事：不放进公开的用户主页（审计行带登录 IP、User-Agent、凭据类型与失败原因，
// 那是私密数据）；不提供按他人过滤（非特权调用者指定他人会被服务端 403）。
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AlertCircle,
  ChevronLeft,
  ChevronRight,
  History,
  Loader2,
  RefreshCw,
  ShieldCheck,
} from "lucide-react";
import { AUDIT_PAGE_SIZE, fetchOwnAuditLogs, type AuditLogEntry } from "@/lib/api";
import { useI18n } from "@/i18n/I18nProvider";
import { isoTimestamp, localDateTime } from "@/lib/datetime";
import { useAuth } from "@/lib/authContext";

const DASH = "—";

/** changes 折叠展示：摘要够短就直接看，长的给缩进 JSON（都是本人自己的数据，不存在越权展示）。 */
function ChangesCell({ value }: { value?: Record<string, unknown> | null }) {
  const text = useMemo(() => (value == null ? "" : JSON.stringify(value)), [value]);
  if (!text || text === "{}") return null;
  const preview = text.length > 90 ? text.slice(0, 90) + "…" : text;
  return (
    <details className="mt-1">
      <summary className="cursor-pointer font-mono text-[10px] text-text-muted break-all">{preview}</summary>
      <pre className="mt-1 p-2 rounded-md bg-surfaceSubtle border border-line-subtle text-[10px] font-mono text-text-body whitespace-pre-wrap break-all overflow-auto max-h-40">
        {JSON.stringify(value, null, 2)}
      </pre>
    </details>
  );
}

export function MyActivityPanel() {
  const { user } = useAuth();
  // 身份变化立即换掉整份私密列表及分页状态；旧组件的请求回包由 effect cleanup 丢弃。
  return user?.id ? <ViewerActivityPanel key={user.id} userId={user.id} /> : null;
}

function ViewerActivityPanel({ userId }: { userId: string }) {
  const { t, tr, locale } = useI18n();
  const [items, setItems] = useState<AuditLogEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [perPage, setPerPage] = useState(AUDIT_PAGE_SIZE);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reloadKey, setReloadKey] = useState(0);

  // 服务名与凭据类型是受控词表（service 由各服务写死、credential_type 是契约 §7 的枚举），
  // 走 tr 兜底原始码：将来多一个服务时界面显示机器码，而不是空白或错译。
  const serviceLabel = (code: string) => tr(`settings.activity.service.${code}`, code);
  const credentialLabel = (code: string) => tr(`settings.activity.credential.${code}`, code);

  const errorText = useCallback(
    (e: unknown): string => {
      const status = typeof (e as { status?: unknown })?.status === "number" ? (e as { status: number }).status : undefined;
      if (status === 401) return t("settings.activity.loginRequired");
      if (status === 403) return t("settings.activity.forbidden");
      return t("settings.activity.loadFailed");
    },
    [t]
  );

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    fetchOwnAuditLogs(userId, page, AUDIT_PAGE_SIZE)
      .then((r) => {
        if (!alive) return;
        setItems(r.items);
        setTotal(r.total);
        setPerPage(r.per_page);
      })
      .catch((e: unknown) => {
        if (!alive) return;
        // 取不到就清空并如实报错：界面有"暂无记录"文案，留着旧数据会让人以为这就是当前结果。
        setItems([]);
        setTotal(0);
        setError(errorText(e));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [userId, page, reloadKey, errorText]);

  const totalPages = Math.max(1, Math.ceil(total / (perPage || AUDIT_PAGE_SIZE)));

  return (
    <div className="p-4 sm:p-5 space-y-4">
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1 min-w-0">
          <h3 className="text-sm font-semibold text-text-strong flex items-center gap-2">
            <History className="w-4 h-4 text-primary" strokeWidth={1.8} />
            <span>{t("settings.activity.title")}</span>
          </h3>
          <p className="text-xs text-text-faint leading-relaxed">{t("settings.activity.desc")}</p>
        </div>
        <button
          type="button"
          onClick={() => setReloadKey((k) => k + 1)}
          disabled={loading}
          className="shrink-0 px-2.5 h-7 rounded-md bg-black/[0.04] dark:bg-white/[0.06] border border-line text-[11px] text-text-body inline-flex items-center gap-1.5 hover:text-primary disabled:opacity-50"
        >
          <RefreshCw className={loading ? "w-3 h-3 animate-spin" : "w-3 h-3"} />
          <span>{t("common.refresh")}</span>
        </button>
      </div>

      {/* 完整性的诚实说明：审计是旁路（落库失败不回滚业务，也不补写），changes 是脱敏+截断摘要。
          不写这一段就会让人把这份列表当成"逐条完整、忠实还原"的账本。 */}
      <div className="p-3 rounded-lg bg-surfaceSubtle border border-line-subtle text-[11px] text-text-faint leading-relaxed flex items-start gap-2">
        <AlertCircle className="w-3.5 h-3.5 shrink-0 mt-0.5 text-text-muted" strokeWidth={1.5} />
        <span>{t("settings.activity.completeness")}</span>
      </div>

      {error && (
        <div className="p-3 rounded-lg bg-red-500/10 border border-red-500/20 text-red-600 dark:text-danger-soft font-mono text-xs flex items-center gap-2">
          <AlertCircle className="w-4 h-4 shrink-0" strokeWidth={1.5} />
          <span className="flex-1">{error}</span>
          <button
            type="button"
            onClick={() => setReloadKey((k) => k + 1)}
            className="px-2 h-6 rounded-md bg-black/[0.04] dark:bg-white/[0.06] border border-line text-[11px] inline-flex items-center gap-1 shrink-0"
          >
            <RefreshCw className="w-3 h-3" />
            <span>{t("common.retry")}</span>
          </button>
        </div>
      )}

      {loading && items.length === 0 ? (
        <div className="p-8 text-center text-text-faint text-xs font-mono flex items-center justify-center gap-2">
          <Loader2 className="w-4 h-4 animate-spin text-primary" />
          <span>{t("common.loading")}</span>
        </div>
      ) : !error && items.length === 0 ? (
        <div className="p-6 rounded-xl bg-surfaceSubtle border border-line-subtle text-center space-y-2">
          <ShieldCheck className="w-5 h-5 text-text-muted mx-auto" strokeWidth={1.5} />
          <div className="text-xs text-text-body">{t("settings.activity.empty")}</div>
        </div>
      ) : items.length > 0 ? (
        <ul className="divide-y divide-black/5 dark:divide-white/[0.06] rounded-lg border border-line-subtle bg-background overflow-hidden">
          {items.map((entry) => {
            const failed = entry.result === "failure";
            return (
              <li key={entry.id} className="p-3 space-y-1.5">
                <div className="flex items-start justify-between gap-3 flex-wrap">
                  <div className="flex items-center gap-2 flex-wrap min-w-0">
                    <span className="text-[10px] font-mono px-1.5 py-0.5 rounded-sm bg-black/[0.04] dark:bg-white/[0.06] border border-line text-text-muted">
                      {serviceLabel(entry.service)}
                    </span>
                    <span className="font-mono text-xs text-text-strong break-all">{entry.action || DASH}</span>
                    <span
                      className={
                        "text-[10px] font-mono px-1.5 py-0.5 rounded-sm border " +
                        (failed
                          ? "bg-rose-500/10 text-rose-600 dark:text-danger border-rose-500/30"
                          : "bg-emerald-500/10 text-emerald-600 dark:text-success border-emerald-500/30")
                      }
                    >
                      {failed ? t("settings.activity.resultFailure") : t("settings.activity.resultSuccess")}
                    </span>
                  </div>
                  <span className="text-[10px] font-mono text-text-faint whitespace-nowrap">
                    {entry.occurred_at ? (
                      <time dateTime={isoTimestamp(entry.occurred_at)} title={localDateTime(entry.occurred_at, locale)}>
                        {isoTimestamp(entry.occurred_at)}
                      </time>
                    ) : (
                      DASH
                    )}
                  </span>
                </div>

                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] font-mono text-text-faint">
                  {entry.target_type || entry.target_id ? (
                    <span>
                      {t("settings.activity.colTarget")}:{" "}
                      <span className="text-text-body break-all">
                        {[entry.target_type, entry.target_id].filter(Boolean).join(":")}
                      </span>
                    </span>
                  ) : null}
                  {entry.credential_type ? (
                    <span>
                      {t("settings.activity.colCredential")}:{" "}
                      <span className="text-text-body">{credentialLabel(entry.credential_type)}</span>
                    </span>
                  ) : null}
                  {entry.actor_ip ? (
                    <span>
                      {t("settings.activity.colIp")}: <span className="text-text-body">{entry.actor_ip}</span>
                    </span>
                  ) : null}
                  {failed && entry.error_code ? (
                    <span>
                      {t("settings.activity.colError")}:{" "}
                      <span className="text-rose-600 dark:text-danger-soft break-all">{entry.error_code}</span>
                    </span>
                  ) : null}
                  {entry.route ? <span className="break-all">{entry.route}</span> : null}
                  {entry.request_id ? (
                    <span className="break-all" title={t("settings.activity.requestId")}>
                      {entry.request_id}
                    </span>
                  ) : null}
                </div>

                <ChangesCell value={entry.changes} />
              </li>
            );
          })}
        </ul>
      ) : null}

      {!error && total > 0 && (
        <div className="flex items-center justify-between gap-2">
          <span className="font-mono text-[11px] text-text-faint">
            {t("settings.activity.total", { count: total })}
          </span>
          <div className="flex items-center gap-1.5">
            <button
              type="button"
              disabled={loading || page <= 1}
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              className="px-2.5 h-6.5 rounded-md bg-black/[0.04] dark:bg-white/[0.06] border border-line disabled:opacity-40 hover:text-primary transition-colors duration-fast ease-soft text-xs inline-flex items-center gap-1"
            >
              <ChevronLeft className="w-3 h-3" />
              <span>{t("settings.activity.prev")}</span>
            </button>
            <span className="font-mono text-[11px] text-text-muted">
              {t("settings.activity.page", { page, pages: totalPages })}
            </span>
            <button
              type="button"
              disabled={loading || page >= totalPages}
              onClick={() => setPage((p) => p + 1)}
              className="px-2.5 h-6.5 rounded-md bg-black/[0.04] dark:bg-white/[0.06] border border-line disabled:opacity-40 hover:text-primary transition-colors duration-fast ease-soft text-xs inline-flex items-center gap-1"
            >
              <span>{t("settings.activity.next")}</span>
              <ChevronRight className="w-3 h-3" />
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

export default MyActivityPanel;
