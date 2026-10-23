"use client";


import Link from "next/link";
import { useRouter } from "next/navigation";
import { useI18n } from "@/i18n/I18nProvider";
import { BrandMark } from "@/components/Logo";
import { Compass, Home, ArrowLeft } from "lucide-react";

export default function NotFound() {
  const { t } = useI18n();
  const router = useRouter();

  const handleBack = () => {
    if (typeof window !== "undefined" && window.history.length > 1) {
      router.back();
    } else {
      router.push("/home");
    }
  };

  return (
    <main className="mf-enter min-h-screen relative flex flex-col items-center justify-center overflow-clip px-6 py-12 selection:bg-primary selection:text-white">

      <div className="relative z-10 max-w-md w-full text-center flex flex-col items-center">
        <div className="mb-6 animate-pulse">
          <BrandMark size={72} withGlow={true} idSuffix="not-found" />
        </div>

        <div className="inline-flex items-center gap-2 px-3 py-1 rounded-chip bg-primary/10 border border-primary/20 text-primary text-xs font-mono tracking-wider mb-4">
          <span>STATUS 404</span>
          <span>•</span>
          <span>NOT FOUND</span>
        </div>

        <h1 className="text-3xl sm:text-4xl font-bold tracking-tight text-emphasis mb-3">
          {t("notFound.title")}
        </h1>
        <p className="text-sm sm:text-base text-text-muted mb-8 leading-relaxed">
          {t("notFound.desc")}
        </p>

        <div className="flex flex-wrap items-center justify-center gap-3 w-full">
          <button
            type="button"
            onClick={handleBack}
            className="inline-flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl bg-emphasis/5 hover:bg-emphasis/10 border border-line text-text-strong text-sm font-medium transition-colors duration-fast ease-soft"
          >
            <ArrowLeft className="w-4 h-4" />
            <span>{t("common.back")}</span>
          </button>

          <Link
            href="/home"
            className="inline-flex items-center justify-center gap-2 px-5 py-2.5 rounded-xl bg-primary hover:bg-primary/90 text-white text-sm font-medium shadow-lg shadow-primary/25 transition-all duration-base ease-soft"
          >
            <Home className="w-4 h-4" />
            <span>{t("nav.home")}</span>
          </Link>

          <Link
            href="/explore"
            className="inline-flex items-center justify-center gap-2 px-4 py-2.5 rounded-xl bg-emphasis/5 hover:bg-emphasis/10 border border-line text-text-strong text-sm font-medium transition-colors duration-fast ease-soft"
          >
            <Compass className="w-4 h-4 text-info" />
            <span>{t("nav.explore")}</span>
          </Link>
        </div>
      </div>
    </main>
  );
}
