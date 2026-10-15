"use client";

import Link from "next/link";
import { Navbar } from "@/components/Navbar";
import { useI18n } from "@/i18n/I18nProvider";
import { Layers, Users, Disc, Network, ArrowRight } from "lucide-react";
import { PageShell } from "@/components/ui/PageShell";

export default function ContributeHubPage() {
  const { t } = useI18n();
  const cards = [
    {
      href: "/new?kind=work",
      icon: Layers,
      title: t("create.hub.cardWorkTitle"),
      desc: t("create.hub.cardWorkDesc"),
      accent: "text-info",
      border: "hover:border-sky-500/30",
      bg: "bg-sky-500/10",
    },
    {
      href: "/new?kind=agent",
      icon: Users,
      title: t("create.hub.cardArtistTitle"),
      desc: t("create.hub.cardArtistDesc"),
      accent: "text-warn",
      border: "hover:border-amber-500/30",
      bg: "bg-amber-500/10",
    },
    {
      href: "/new?kind=release",
      icon: Disc,
      title: t("create.hub.cardReleaseTitle"),
      desc: t("create.hub.cardReleaseDesc"),
      accent: "text-success",
      border: "hover:border-emerald-500/30",
      bg: "bg-emerald-500/10",
    },
    {
      href: "/new?kind=collection",
      icon: Network,
      title: t("create.hub.cardFranchiseTitle"),
      desc: t("create.hub.cardFranchiseDesc"),
      accent: "text-alt",
      border: "hover:border-indigo-500/30",
      bg: "bg-indigo-500/10",
    },
  ] as const;

  return (
    <div className="min-h-screen flex flex-col bg-background text-text-strong">
      <Navbar />
      <PageShell width="narrow" spacing="none" contentClassName="space-y-4">
        <div className="space-y-1">
          <h1 className="font-display text-xl sm:text-2xl font-bold tracking-tight text-text-strong">
            {t("create.hub.title")}
          </h1>
          <p className="font-mono text-xs text-text-faint max-w-3xl">
            {t("create.hub.subtitle")}
          </p>
        </div>

        <Link href="/submissions" className="inline-block text-primary underline">{t("catalog.review.mySubmissions")}</Link>
        <div className="space-y-2">
          <div className="text-xs font-mono text-text-muted uppercase tracking-wider font-semibold">
            {t("create.hub.manualSectionTitle")}
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            {cards.map((c) => {
              const Icon = c.icon;
              return (
                <Link
                  key={c.href}
                  href={c.href}
                  className={`group p-3 sm:p-4 rounded-card border border-line bg-surface hover:border-primary/40 transition-all space-y-3 shadow-xs hover:shadow-md ${c.border}`}
                >
                  <div className={`w-9 h-9 rounded-xl border border-line grid place-items-center ${c.bg}`}>
                    <Icon className={`w-4 h-4 ${c.accent}`} />
                  </div>
                  <div className="font-semibold text-text-strong text-base flex items-center gap-1.5 group-hover:text-primary transition-colors duration-fast ease-soft">
                    <span>{c.title}</span>
                    <ArrowRight className="w-4 h-4 text-text-muted group-hover:text-primary group-hover:translate-x-0.5 transition-all duration-base ease-soft" />
                  </div>
                  <div className="font-mono text-xs leading-relaxed text-text-muted">
                    {c.desc}
                  </div>
                </Link>
              );
            })}
          </div>
        </div>
      </PageShell>
    </div>
  );
}
