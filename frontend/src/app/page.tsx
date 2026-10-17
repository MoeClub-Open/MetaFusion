"use client";

import { Suspense } from "react";
import Link from "next/link";
import { ArrowRight, BookOpen, Compass, Database, LogIn } from "lucide-react";
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
  const shortcutClass = "inline-flex items-center justify-center gap-1.5 px-3 h-9 rounded-full bg-surface/60 border border-line text-text-body hover:text-text-strong text-xs font-medium transition-colors duration-fast ease-soft";
  const actionClass = "inline-flex items-center justify-center gap-2.5 px-6 sm:px-8 min-h-12 rounded-full bg-emphasis/[0.04] border border-line text-text-strong hover:bg-emphasis/[0.08] font-medium text-sm transition-colors duration-fast ease-soft";

  return (
    <div className="min-h-screen min-h-svh text-text-strong relative flex flex-col overflow-clip selection:bg-primary selection:text-primary-foreground">

      {/* 控件保留右上角布局，但参与文档流，避免手机和矮窗口遮住主内容。 */}
      <PageContainer as="header" className="relative z-20 py-4">
        <nav aria-label={t("landing.pageControls")} className="flex flex-wrap items-center justify-end gap-2">
          <Link href="/home" className={shortcutClass} aria-label={t("landing.enterHome")} title={t("landing.enterHome")}><span className="hidden sm:inline">{t("landing.enterHome")}</span><ArrowRight className="w-3.5 h-3.5" aria-hidden /></Link>
          <a href="/docs/catalog" className={shortcutClass} aria-label={t("landing.docsTitle")} title={t("landing.docsTitle")}>
            <BookOpen className="w-3.5 h-3.5 text-primary" aria-hidden /><span className="hidden sm:inline">{t("navigation.docs")}</span>
          </a>
          <a href="https://github.com/MoeclubM/MetaFusion" target="_blank" rel="noopener noreferrer" className={shortcutClass} aria-label="GitHub" title="GitHub">
            <GitHubIcon className="w-3.5 h-3.5" /><span className="hidden sm:inline">GitHub</span>
          </a>
          <LocaleSwitcher compact />
          <ThemeModeSwitcher />
          <ThemePicker />
        </nav>
      </PageContainer>

      <PageShell width="narrow" center spacing="none" contentClassName="flex flex-col items-center py-6 sm:py-10">
        <div className="mb-6 relative group">
          <BrandMark size={112} withGlow idSuffix="landing-hero" className="relative z-10 drop-shadow-xl" />
        </div>
        <div className="inline-flex items-center gap-2 px-3.5 py-1 rounded-full bg-primary/10 border border-primary/20 font-mono text-xs tracking-wide text-primary font-semibold mb-4">
          <Database className="w-3.5 h-3.5 shrink-0" aria-hidden /><span>{t("landing.tagline")}</span>
        </div>
        <h1 className="text-4xl sm:text-6xl md:text-7xl font-extrabold tracking-tight text-text-strong mb-4 font-display">MetaFusion</h1>
        <div className="flex flex-wrap items-center justify-center gap-3 sm:gap-4 mb-8">
          <Link href="/home" className="inline-flex items-center justify-center gap-2.5 px-6 sm:px-8 min-h-12 rounded-full bg-primary text-primary-foreground hover:bg-primary/90 font-semibold text-sm shadow-lg shadow-primary/20 transition-colors duration-fast ease-soft">
            <Compass className="w-4 h-4" aria-hidden /><span>{t("landing.enter")}</span>
          </Link>
          <Link href="/explore" className={actionClass}>{t("landing.exploreArchive")}</Link>
          {!user && <a href="/login?tab=register&redirect=%2Fhome" className={actionClass}><LogIn className="w-4 h-4 text-primary" aria-hidden /><span>{t("landing.join")}</span></a>}
        </div>
        <ul className="flex flex-wrap items-center justify-center gap-2 text-xs text-text-muted">
          {["versions", "contents", "contributors"].map((feature) => <li key={feature} className="px-2.5 py-1 rounded-chip bg-surfaceSubtle border border-line-subtle">{t(`landing.feature.${feature}`)}</li>)}
        </ul>
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
