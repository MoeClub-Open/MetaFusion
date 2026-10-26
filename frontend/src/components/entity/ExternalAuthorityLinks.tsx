"use client";

import { useEffect, useState, useMemo } from "react";
import { ExternalLink, Globe, ArrowUpRight } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { fetchExternalDatabases, ExternalDatabaseDefinition, pickLocalizedName } from "@/lib/api";
import { authorityIcon } from "@/lib/authorityIcons";
import { safeExternalAuthorityURL } from "@/lib/externalAuthorityURL";
import { externalLinkSecondary } from "@/lib/externalLinkDisplay";
import type { ExternalLink as CatalogExternalLink } from "@/components/catalog/api";

interface Props {
  entity?: {
    kind?: string;
    attributes?: Record<string, any>;
    external_links?: CatalogExternalLink[];
  };
  externalLinks?: CatalogExternalLink[];
  category?: string;
  className?: string;
  label?: string;
  variant?: "chips" | "list";
  emptyText?: string;
}

export interface AuthorityLinkItem {
  code: string;
  name: string;
  url: string;
  id: string;
  externalId?: string;
  icon: string;
  icon_url?: string;
  role?: string;
  isBangumi?: boolean;
  sortOrder: number;
}

const ROLE_I18N: Record<string, string> = {
  official: "authority.role.official",
  primary: "authority.role.primary",
  secondary: "authority.role.secondary",
  operator: "authority.role.operator",
};

export function ExternalAuthorityLinks({
  entity,
  externalLinks: directLinks,
  category,
  className = "",
  label,
  variant = "chips",
  emptyText,
}: Props) {
  const { t, locale } = useI18n();
  const [definitions, setDefinitions] = useState<ExternalDatabaseDefinition[]>([]);

  useEffect(() => {
    fetchExternalDatabases(category)
      .then((res) => {
        if (Array.isArray(res?.items)) {
          setDefinitions(res.items);
        }
      })
      .catch(() => {});
  }, [category]);

  const displayLabel = label || t("entity.authority.label");

  const resolvedLinks = useMemo(() => {
    const fromEntity = entity?.external_links || [];
    const links = (directLinks && directLinks.length > 0 ? directLinks : fromEntity).slice();
    // Optional website attributes only when no official link already present.
    const hasOfficial = links.some((link) => link.role === "official" || link.kind === "official_website");
    const officialAttr =
      entity?.attributes?.official_website ||
      entity?.attributes?.website ||
      entity?.attributes?.official_url ||
      entity?.attributes?.url;
    if (officialAttr && !hasOfficial) {
      const url = safeExternalAuthorityURL(String(officialAttr));
      if (url) {
        links.push({ kind: "official_website", url });
      }
    }
    return links;
  }, [entity, directLinks]);

  const linkItems = useMemo<AuthorityLinkItem[]>(() => {
    if (!resolvedLinks.length) return [];

    const defMap = new Map<string, ExternalDatabaseDefinition>();
    for (const def of definitions) {
      defMap.set(def.code.toLowerCase(), def);
    }

    const items: AuthorityLinkItem[] = [];

    for (const link of resolvedLinks) {
      if (!link?.kind || !link?.url) continue;
      if (link.kind === "metafusion_import") continue;

      const key = link.kind.toLowerCase();
      const def = defMap.get(key);
      let name = "";
      if (def) {
        name = pickLocalizedName(locale, def.names, def.code);
      } else if (key === "official_website") {
        name = t("authority.officialWebsite");
      } else {
        name = key.toUpperCase();
      }

      const url = safeExternalAuthorityURL(link.url);
      const role = link.role || "";
      const isBangumi = key.startsWith("bangumi");
      const defaultOrder = role === "official" ? 5 : isBangumi ? 10 : def?.sort_order ?? 100;

      items.push({
        code: `${key}:${url}`,
        name,
        url,
        id: link.external_id || link.url,
        externalId: link.external_id || "",
        icon: def?.icon || (role === "official" ? "Globe" : isBangumi ? "Tv" : "Globe"),
        icon_url: def?.icon_url || "",
        role,
        isBangumi,
        sortOrder: def ? def.sort_order : defaultOrder,
      });
    }

    return items.sort(
      (a, b) => Number(b.role === "official") - Number(a.role === "official") || a.sortOrder - b.sortOrder,
    );
  }, [resolvedLinks, definitions, entity, locale, t]);

  const roleBadge = (role?: string) => {
    if (!role || !ROLE_I18N[role]) return null;
    const labelText = t(ROLE_I18N[role]);
    return (
      <span className="shrink-0 px-1.5 py-0.5 rounded bg-primary/10 text-primary border border-primary/25 text-[10px] font-semibold leading-none">
        {labelText}
      </span>
    );
  };

  if (linkItems.length === 0) {
    if (variant !== "list" || !emptyText) return null;
    return (
      <div className={`p-4 sm:p-5 rounded-xl border border-line bg-surface shadow-soft space-y-3 ${className}`}>
        <div className="flex items-center gap-2 border-b border-line-subtle pb-2.5">
          <Globe className="w-4 h-4 text-primary" strokeWidth={1.5} />
          <h3 className="font-display text-xs font-bold uppercase tracking-wider text-text-strong font-mono">{displayLabel}</h3>
        </div>
        <p className="text-xs text-text-muted">{emptyText}</p>
      </div>
    );
  }

  if (variant === "list") {
    return (
      <div className={`p-4 sm:p-5 rounded-xl border border-line bg-surface shadow-soft space-y-3 ${className}`}>
        <div className="flex items-center justify-between border-b border-line-subtle pb-2.5">
          <div className="flex items-center gap-2">
            <Globe className="w-4 h-4 text-primary" strokeWidth={1.5} />
            <h3 className="font-display text-xs font-bold uppercase tracking-wider text-text-strong font-mono">
              {displayLabel}
            </h3>
          </div>
          <span className="text-[10px] font-mono text-text-muted bg-black/[0.04] dark:bg-white/[0.06] px-1.5 py-0.5 rounded">
            {linkItems.length}
          </span>
        </div>

        <div className="flex flex-col gap-2">
          {linkItems.map((item) => {
            const IconComp = authorityIcon(item.icon);
            const LinkTag = item.url ? "a" : "span";
            const isOfficial = item.role === "official";
            const isBangumi = item.isBangumi;
            let badgeClass =
              "bg-black/[0.04] dark:bg-white/[0.06] border-line text-text-body hover:border-primary/40 hover:text-primary";
            if (isOfficial) {
              badgeClass =
                "bg-emerald-500/10 text-emerald-600 dark:text-success border-emerald-500/25 hover:bg-emerald-500/20";
            } else if (isBangumi) {
              badgeClass =
                "bg-[#f09199]/10 text-[#f09199] border-[#f09199]/25 hover:bg-[#f09199]/20";
            }

            return (
              <LinkTag
                key={item.code}
                {...(item.url ? { href: item.url, target: "_blank", rel: "noopener noreferrer" } : {})}
                title={`${item.name}: ${externalLinkSecondary(item.externalId, item.url || item.id)}`}
                className={`inline-flex items-center justify-between px-3 py-2 rounded-lg border transition-all text-xs font-medium group ${badgeClass}`}
              >
                <span className="flex items-center gap-2 min-w-0 flex-1">
                  {item.icon_url ? (
                    <img src={item.icon_url} alt="" className="w-4 h-4 object-contain shrink-0" />
                  ) : (
                    <IconComp className={`w-4 h-4 shrink-0 ${isOfficial ? "text-emerald-500" : isBangumi ? "text-[#f09199]" : "opacity-70"}`} />
                  )}
                  {/* break-keep：CJK 不中字断开（官/方）；拉丁可在空格处换行，不用 truncate 回「官…」 */}
                  <span className="font-sans break-keep">{item.name}</span>
                  {roleBadge(item.role)}
                </span>
                <span className="flex min-w-0 max-w-[40%] shrink items-center gap-1 ml-2 font-mono text-[11px] opacity-75">
                  <span className="truncate" title={externalLinkSecondary(item.externalId, item.url || item.id)}>
                    {externalLinkSecondary(item.externalId, item.url || item.id)}
                  </span>
                  {item.url && <ArrowUpRight className="w-3.5 h-3.5 opacity-60 group-hover:opacity-100 group-hover:translate-x-0.5 group-hover:-translate-y-0.5 transition-transform duration-base ease-soft" />}
                </span>
              </LinkTag>
            );
          })}
        </div>
      </div>
    );
  }

  return (
    <div className={`flex flex-wrap items-center gap-1.5 ${className}`}>
      {linkItems.map((item) => {
        const IconComp = authorityIcon(item.icon);
        const LinkTag = item.url ? "a" : "span";
        const isOfficial = item.role === "official";
        const isBangumi = item.isBangumi;
        let badgeClass =
          "bg-black/[0.04] dark:bg-white/[0.06] hover:bg-sky-500/10 border-line hover:border-sky-500/40 text-gray-700 dark:text-gray-200 hover:text-sky-600 dark:hover:text-info-soft";
        if (isOfficial) {
          badgeClass =
            "bg-emerald-500/10 text-emerald-600 dark:text-success border-emerald-500/25 hover:bg-emerald-500/20";
        } else if (isBangumi) {
          badgeClass =
            "bg-[#f09199]/10 text-[#f09199] border-[#f09199]/25 hover:bg-[#f09199]/20";
        }

        return (
          <LinkTag
            key={item.code}
            {...(item.url ? { href: item.url, target: "_blank", rel: "noopener noreferrer" } : {})}
            title={`${item.name} (${item.id})`}
            className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md border text-[11px] font-medium transition-all shadow-2xs ${badgeClass}`}
          >
            {item.icon_url ? (
              <img src={item.icon_url} alt="" className="w-3.5 h-3.5 object-contain shrink-0" />
            ) : (
              <IconComp className={`w-3.5 h-3.5 shrink-0 ${isOfficial ? "text-emerald-500" : isBangumi ? "text-[#f09199]" : "opacity-70"}`} />
            )}
            <span>{item.name}</span>
            {roleBadge(item.role)}
            {item.url ? <ExternalLink className="w-2.5 h-2.5 opacity-40 ml-0.5" /> : <span className="font-mono">{item.id}</span>}
          </LinkTag>
        );
      })}
    </div>
  );
}
