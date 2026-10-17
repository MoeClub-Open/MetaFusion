"use client";

import { useEffect } from "react";
import Link from "next/link";
import { useI18n } from "@/i18n/I18nProvider";
import { BrandMark } from "@/components/Logo";
import { RotateCcw, Home, AlertTriangle } from "lucide-react";

export default function Error({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  const { t } = useI18n();

  useEffect(() => {
    // 生产环境中记录错误到监控系统
    console.error("MetaFusion Application Error Boundary Caught:", error);
  }, [error]);

  return (
    <main className="mf-enter min-h-screen relative flex flex-col items-center justify-center overflow-clip px-6 py-12 selection:bg-primary selection:text-white">

      <div className="relative z-10 max-w-lg w-full text-center flex flex-col items-center">
        <div className="mb-6">
          <BrandMark size={72} withGlow={true} idSuffix="error-boundary" />
        </div>

        <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-rose-500/10 border border-rose-500/20 text-danger text-xs font-mono tracking-wider mb-4">
          <AlertTriangle className="w-3.5 h-3.5" />
          <span>APPLICATION ERROR</span>
          {error.digest && <span>• {error.digest.slice(0, 8)}</span>}
        </div>

        <h1 className="text-3xl sm:text-4xl font-bold tracking-tight text-emphasis mb-3">
          {t("error.title")}
        </h1>
        <p className="text-sm sm:text-base text-text-muted mb-4 leading-relaxed">
          {t("error.desc")}
        </p>

        {process.env.NODE_ENV !== "production" && error.message && (
          <div className="w-full text-left bg-surfaceSubtle border border-line-subtle rounded-xl p-3 mb-6 overflow-x-auto max-h-32 text-xs font-mono text-danger">
            {error.message}
          </div>
        )}

        <div className="flex flex-wrap items-center justify-center gap-3 w-full mt-4">
          <button
            onClick={() => reset()}
            className="inline-flex items-center justify-center gap-2 px-5 py-2.5 rounded-xl bg-primary hover:bg-primary/90 text-white text-sm font-medium shadow-lg shadow-primary/25 transition-all duration-base ease-soft"
          >
            <RotateCcw className="w-4 h-4" />
            <span>{t("error.retry")}</span>
          </button>

          <Link
            href="/home"
            className="inline-flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl bg-emphasis/5 hover:bg-emphasis/10 border border-line text-text-strong text-sm font-medium transition-colors duration-fast ease-soft"
          >
            <Home className="w-4 h-4" />
            <span>{t("nav.home")}</span>
          </Link>
        </div>
      </div>
    </main>
  );
}
