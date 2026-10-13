"use client";

import Link from "next/link";
import { FileQuestion, WifiOff, FolderOpen, Search, RotateCw } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";

/**
 * 条目详情的两种失败态：
 * - not-found：条目不存在 / ID 非法。给「浏览目录」「去搜索」，不给重试（重试也不会变好）。
 * - network：网络或服务故障。只有这种情况才给「重试」。
 */
export function EntityLoadError({ kind, onRetry, message }: { kind: "not-found" | "network"; onRetry?: () => void; message?: string }) {
  const { t } = useI18n();
  const notFound = kind === "not-found";
  const Icon = notFound ? FileQuestion : WifiOff;
  return (
    <div className="flex flex-col items-center text-center gap-3 py-16" role={notFound ? undefined : "alert"}>
      <Icon className={`w-10 h-10 ${notFound ? "text-text-muted" : "text-danger"}`} aria-hidden />
      <h1 className="text-xl font-bold text-emphasis">{t(notFound ? "entity.missing.title" : "entity.loadFailed.title")}</h1>
      <p className="text-sm text-text-muted">{notFound ? t("entity.missing.desc") : message || t("entity.loadFailed.desc")}</p>
      <div className="flex flex-wrap items-center justify-center gap-3 mt-2">
        {notFound ? (
          <>
            <Link href="/explore" className="inline-flex items-center gap-2 px-4 py-2 rounded-md bg-primary text-primary-foreground text-xs font-semibold hover:bg-primary-hover">
              <FolderOpen className="w-3.5 h-3.5" />
              <span>{t("entity.missing.browseCatalog")}</span>
            </Link>
            <Link href="/explore?focus=search" className="inline-flex items-center gap-2 px-4 py-2 rounded-md border border-line text-text-strong text-xs font-semibold hover:bg-emphasis/5">
              <Search className="w-3.5 h-3.5" />
              <span>{t("entity.missing.goSearch")}</span>
            </Link>
          </>
        ) : (
          <button type="button" onClick={onRetry} className="inline-flex items-center gap-2 px-4 py-2 rounded-md bg-primary text-primary-foreground text-xs font-semibold hover:bg-primary-hover cursor-pointer">
            <RotateCw className="w-3.5 h-3.5" />
            <span>{t("catalog.retry")}</span>
          </button>
        )}
      </div>
    </div>
  );
}
