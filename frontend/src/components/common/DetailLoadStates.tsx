"use client";

// 详情页的取数失败态：把"找不到"与"暂时取不到"分成两种状态，并都给出出口。
//
// 之前各详情页把任何非 2xx 都渲染成"未找到该X。"：429/5xx/断网与真 404 走同一分支，
// 用户会以为库里没有这个条目（并据此去建重复条目），而页面既没有重试也没有返回。
// 分类口径（与 lib/api/client.ts 抛出的 ApiError 对应）：
//   * 404 / not_found   -> not_found：服务明确说没有这个条目，给出口、不给重试。
//   * invalid_id / invalid_kind -> invalid：地址本身不对（id 不是 uuid、或种类与路由不符），
//     重试永远无效，同样只给出口（文案与 404 区分开，不说"不存在"误导成"曾经存在"）。
//   * 429 / rate_limited -> rate_limited：限流是可预期的（线上 entities 是 120/分钟全站共享预算）。
//   * 其余（5xx、超时、断网、浏览器原生 "Failed to fetch"）-> unavailable：说"暂时不可用"并给重试。
// 任何情况下都不把裸错误码或浏览器英文错误当正文。


import Link from "next/link";
import { useRouter } from "next/navigation";
import { useI18n } from "@/i18n/I18nProvider";
import { PageShell } from "@/components/ui/PageShell";
import { ArrowLeft, Compass, Home, RefreshCw, TriangleAlert } from "lucide-react";

export type LoadFailureKind = "not_found" | "invalid" | "rate_limited" | "unavailable";

/** 把 ApiError / 原生 fetch 异常归一成四种可解释的失败。 */
export function classifyLoadFailure(err: any): LoadFailureKind {
  const status = typeof err?.status === "number" ? err.status : 0;
  const code = String(err?.message || "");
  if (status === 404 || code === "not_found" || code.startsWith("not_found")) return "not_found";
  if (code === "invalid_id" || code === "invalid_kind" || code.startsWith("invalid_")) return "invalid";
  if (status === 429 || code === "rate_limited") return "rate_limited";
  return "unavailable";
}

function Exits() {
  const { t } = useI18n();
  const router = useRouter();
  const back = () => {
    if (typeof window !== "undefined" && window.history.length > 1) router.back();
    else router.push("/home");
  };
  return (
    <div className="flex flex-wrap items-center justify-center gap-2.5">
      <button
        type="button"
        onClick={back}
        className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-surface border border-line text-xs text-text-body hover:bg-black/[0.04] dark:hover:bg-surfaceHover transition-colors duration-fast ease-soft cursor-pointer"
      >
        <ArrowLeft className="w-3.5 h-3.5" />
        <span>{t("common.back")}</span>
      </button>
      <Link
        href="/home"
        className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-primary hover:bg-primary/90 text-xs font-medium text-primary-foreground transition-colors duration-fast ease-soft"
      >
        <Home className="w-3.5 h-3.5" />
        <span>{t("nav.home")}</span>
      </Link>
      <Link
        href="/explore"
        className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-surface border border-line text-xs text-text-body hover:bg-black/[0.04] dark:hover:bg-surfaceHover transition-colors duration-fast ease-soft"
      >
        <Compass className="w-3.5 h-3.5 text-sky-500" />
        <span>{t("nav.explore")}</span>
      </Link>
    </div>
  );
}

/** 「找不到 / 地址无效」：H1 + 说明 + 返回/首页/探索，与站内 404 页同形（复用同一批出口）。 */
export function DetailNotFound({ title, desc }: { title?: string; desc?: string }) {
  const { t } = useI18n();
  return (
    <PageShell width="narrow" center className="py-16" contentClassName="text-center space-y-4">
      <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-text-strong">
        {title || t("catalog.notFoundTitle")}
      </h1>
      <p className="text-sm text-text-muted leading-relaxed">{desc || t("catalog.notFoundDesc")}</p>
      <Exits />
    </PageShell>
  );
}

/** 「暂时取不到 / 被限流」：明确说是取数失败，给重试与出口，不宣称条目不存在。 */
export function DetailUnavailable({
  kind,
  onRetry,
}: {
  kind: Exclude<LoadFailureKind, "not_found" | "invalid">;
  onRetry?: () => void;
}) {
  const { t } = useI18n();
  return (
    <PageShell width="narrow" center className="py-16" contentClassName="space-y-4">
      <div
        role="alert"
        className="rounded-xl border border-amber-500/30 bg-amber-500/5 p-5 text-center space-y-3"
      >
        <TriangleAlert className="w-6 h-6 mx-auto text-amber-500" />
        <h1 className="text-lg font-semibold text-text-strong">
          {kind === "rate_limited" ? t("catalog.rateLimited") : t("catalog.unavailableTitle")}
        </h1>
        <p className="text-xs text-text-muted leading-relaxed">
          {kind === "rate_limited" ? t("catalog.rateLimitedDesc") : t("catalog.unavailableDesc")}
        </p>
        {onRetry && (
          <button
            type="button"
            onClick={onRetry}
            className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl bg-primary/15 hover:bg-primary/25 text-primary text-xs font-semibold transition-colors duration-fast ease-soft cursor-pointer"
          >
            <RefreshCw className="w-3.5 h-3.5" />
            <span>{t("catalog.retry")}</span>
          </button>
        )}
      </div>
      <Exits />
    </PageShell>
  );
}
