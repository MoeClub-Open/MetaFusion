"use client";

import { Suspense, useEffect } from "react";
import { useSearchParams } from "next/navigation";
import { Navbar } from "@/components/Navbar";
import { PageShell } from "@/components/ui/PageShell";
import { useI18n } from "@/i18n/I18nProvider";
import { LoadingFallback } from "@/components/common/LoadingFallback";
import { getStorageEntityUrl, hasResourceStation } from "@/lib/services";
import { DownloadCloud, ExternalLink, HardDrive } from "lucide-react";

function DownloadsInner() {
  const { t } = useI18n();
  const searchParams = useSearchParams();
  const subjectId = searchParams.get("subject_id") || searchParams.get("ref") || "";
  const configured = hasResourceStation();
  const targetUrl = getStorageEntityUrl(subjectId || undefined);

  useEffect(() => {
    // 仅在配置了外部资源站时跳转；否则会自我重定向回本页形成死循环。
    if (configured) window.location.replace(targetUrl);
  }, [configured, targetUrl]);

  return (
    <div className="min-h-screen flex flex-col text-text-strong">
      <Navbar />

      <PageShell width="narrow" center className="py-16">
        {configured ? (
          <div className="text-center space-y-4 max-w-sm">
            <DownloadCloud className="w-8 h-8 text-primary mx-auto" />
            <p className="text-sm text-text-muted">{t("catalog.resourceGatewayRedirecting")}</p>
            <a
              href={targetUrl}
              className="inline-flex items-center gap-2 px-5 py-2.5 rounded-xl bg-primary hover:bg-primary/90 text-surface font-semibold text-sm transition-colors duration-fast ease-soft"
            >
              <span>{t("catalog.resourceGatewayVisit")}</span>
              <ExternalLink className="w-4 h-4" />
            </a>
          </div>
        ) : (
          <div className="text-center space-y-3 max-w-sm">
            <HardDrive className="w-8 h-8 text-text-faint mx-auto" />
            <p className="text-sm text-text-muted leading-relaxed">
              {t("catalog.resourceStationNotReady")}
            </p>
          </div>
        )}
      </PageShell>
    </div>
  );
}

export default function DownloadsPage() {
  return (
    <Suspense fallback={<LoadingFallback className="min-h-screen grid place-items-center text-xs font-mono text-text-faint" />}>
      <DownloadsInner />
    </Suspense>
  );
}
