"use client";

import { Suspense } from "react";
import Link from "next/link";
import { Compass, LogIn } from "lucide-react";
import { LoadingFallback } from "@/components/common/LoadingFallback";
import { BrandMark } from "@/components/Logo";
import { ThemePicker } from "@/components/ThemePicker";
import { ThemeModeSwitcher } from "@/components/ThemeModeSwitcher";
import { LocaleSwitcher } from "@/components/LocaleSwitcher";
import { GitHubIcon } from "@/components/Icons";
import { PageContainer, PageShell } from "@/components/ui/PageShell";
import { useAuth } from "@/lib/authContext";
import { useI18n } from "@/i18n/I18nProvider";

function RootLandingInner() {
  const { user } = useAuth();
  const { t } = useI18n();
  const actionClass = "inline-flex items-center justify-center gap-2 px-4 h-10 rounded-control bg-surface border border-line text-text-strong hover:bg-surfaceHover font-medium text-sm transition-colors duration-fast ease-soft";

  return (
    <div className="min-h-screen min-h-svh text-text-strong relative flex flex-col overflow-clip selection:bg-primary selection:text-primary-foreground">

      {/* 控件保留右上角布局，但参与文档流，避免手机和矮窗口遮住主内容。 */}
      <PageContainer as="header" className="relative z-20 py-4">
        <nav aria-label={t("landing.pageControls")} className="flex flex-wrap items-center justify-end gap-2">
          <LocaleSwitcher compact />
          <ThemeModeSwitcher />
          <ThemePicker />
        </nav>
      </PageContainer>

      <PageShell width="narrow" center spacing="none" contentClassName="flex flex-col items-center py-6 sm:py-10">
        <div className="mb-5">
          <BrandMark size={72} idSuffix="landing-hero" />
        </div>
        <h1 className="text-3xl sm:text-4xl font-semibold tracking-tight text-text-strong mb-6">MetaFusion</h1>
        <div className="flex flex-wrap items-center justify-center gap-3">
          <Link href="/home" className="inline-flex items-center justify-center gap-2 px-4 h-10 rounded-control bg-primary text-primary-foreground hover:bg-primary/90 font-medium text-sm transition-colors duration-fast ease-soft">
            <Compass className="w-4 h-4" aria-hidden /><span>{t("landing.enter")}</span>
          </Link>
          <Link href="/explore" className={actionClass}>{t("landing.exploreArchive")}</Link>
          {!user && <a href="/login?tab=register&redirect=%2Fhome" className={actionClass}><LogIn className="w-4 h-4 text-primary" aria-hidden /><span>{t("landing.join")}</span></a>}
        </div>
      </PageShell>

      <PageContainer as="footer" className="relative z-10 py-4 border-t border-line-subtle">
        <div className="flex flex-col sm:flex-row items-center justify-between gap-3 text-xs text-text-muted">
          <div className="flex flex-wrap items-center justify-center gap-x-4 gap-y-2">
            <span>© 2026 MetaFusion</span>
            <a href="/docs/catalog" className="hover:text-primary transition-colors">{t("landing.docsCenter")}</a>
            <Link href="/developers" className="hover:text-primary transition-colors">{t("home.footerApi")}</Link>
          </div>
          <a href="https://github.com/MoeclubM/MetaFusion" target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-2 hover:text-primary transition-colors"><GitHubIcon className="w-3.5 h-3.5" /><span>github.com/MoeclubM/MetaFusion</span></a>
        </div>
      </PageContainer>
    </div>
  );
}

export default function RootLandingPage() {
  return <Suspense fallback={<LoadingFallback />}><RootLandingInner /></Suspense>;
}
