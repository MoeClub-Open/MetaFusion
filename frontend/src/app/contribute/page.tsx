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
    },
    {
      href: "/new?kind=agent",
      icon: Users,
      title: t("create.hub.cardArtistTitle"),
      desc: t("create.hub.cardArtistDesc"),
      accent: "text-success",
      border: "hover:border-emerald-500/30",
    },
    {
      href: "/new?kind=release",
      icon: Disc,
      title: t("create.hub.cardReleaseTitle"),
      desc: t("create.hub.cardReleaseDesc"),
      accent: "text-warning",
      border: "hover:border-amber-500/30",
    },
    {
      href: "/new?kind=collection",
      icon: Network,
      title: t("create.hub.cardFranchiseTitle"),
      desc: t("create.hub.cardFranchiseDesc"),
      accent: "text-accent",
      border: "hover:border-violet-500/30",
    },
  ];

  return (
    <>
      <Navbar />
      <PageShell>
        <div className="mb-8">
          <h1 className="text-2xl font-bold text-fg">{t("create.hub.title")}</h1>
          <p className="mt-2 text-sm text-fg-muted">{t("create.hub.subtitle")}</p>
        </div>
        <div className="grid gap-4 sm:grid-cols-2">
          {cards.map((card) => {
            const Icon = card.icon;
            return (
              <Link
                key={card.href}
                href={card.href}
                className={`group rounded-2xl border border-border bg-surface p-5 transition ${card.border}`}
              >
                <div className="flex items-start gap-3">
                  <Icon className={`mt-0.5 h-5 w-5 ${card.accent}`} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center justify-between gap-2">
                      <h2 className="font-semibold text-fg">{card.title}</h2>
                      <ArrowRight className="h-4 w-4 text-fg-muted opacity-0 transition group-hover:opacity-100" />
                    </div>
                    <p className="mt-1 text-sm text-fg-muted">{card.desc}</p>
                  </div>
                </div>
              </Link>
            );
          })}
        </div>
      </PageShell>
    </>
  );
}
