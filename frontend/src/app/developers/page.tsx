"use client";

import { useEffect } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { DOCS_SERVICE_URL } from "@/lib/services";

// 统一文档与 API 中心是独立服务，地址由 NEXT_PUBLIC_DOCS_URL 决定。
const DOCS_API_OVERVIEW_URL = `${DOCS_SERVICE_URL}/api-overview`;

export default function DevelopersRedirectPage() {
  const { t } = useI18n();

  useEffect(() => {
    window.location.replace(DOCS_API_OVERVIEW_URL);
  }, []);

  return (
    <div className="min-h-screen flex flex-col items-center justify-center p-4 font-mono text-sm text-text-faint">
      <div className="flex items-center gap-2">
        <span className="w-2 h-2 rounded-full bg-primary animate-ping" />
        <span>{t("developers.redirecting")}</span>
      </div>
      <a href={DOCS_API_OVERVIEW_URL} className="mt-3 text-xs text-primary underline">
        {t("developers.clickIfNoRedirect")}
      </a>
    </div>
  );
}
