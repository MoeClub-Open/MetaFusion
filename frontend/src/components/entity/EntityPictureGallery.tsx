"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { Card } from "@/components/ui/Card";
import { Select } from "@/components/ui/Select";
import { EntityCover } from "@/components/common/EntityCover";
import { PictureLightbox, type LightboxPicture } from "@/components/catalog/PictureLightbox";
import type { Entity } from "@/components/catalog/api";
import { COVER_PICTURE_INDEX, PICTURE_ROLE_VOCABULARY } from "@/lib/cover";
import { getTermName, resolveLocalizedName, type DynamicDefinitions } from "@/lib/definitions";
import { useI18n } from "@/i18n/I18nProvider";
import { fetchEntityPictures, type ScopedPictureItem } from "@/lib/api/temporal";

type GalleryTab = "current" | "historical" | "icon" | "visual" | "all";

const VISUAL_ROLES = new Set(["key_visual", "cover_art", "poster"]);

function isPastDate(dateStr?: string | null): boolean {
  const raw = String(dateStr || "").trim();
  if (!raw) return false;
  const today = new Date().toISOString().slice(0, 10);
  if (raw.length === 4) return raw < today.slice(0, 4);
  if (raw.length === 7) return raw < today.slice(0, 7);
  return raw.slice(0, 10) <= today;
}

function fallbackScopedItems(entity: Entity): ScopedPictureItem[] {
  return (entity.pictures || []).map((pic, index) => {
    const end = String(pic.valid_to || pic.usage_period?.end || "").trim();
    const historical = end ? isPastDate(end) : false;
    return {
      owner_id: entity.id || "",
      owner_kind: entity.kind,
      owner_title: entity.title || "",
      index,
      is_current: !historical,
      is_historical: historical,
      picture: pic,
      recorded_at: pic.recorded_at,
    };
  });
}

export function EntityPictureGallery({
  entity,
  defs,
  blurAdultImages = false,
  isAdult = false,
  adultMode = "blur",
  onToggleAdultReveal,
  asOfDate,
  externalOpenIndex,
  onExternalOpenHandled,
}: {
  entity: Entity;
  defs: DynamicDefinitions | null | undefined;
  blurAdultImages?: boolean;
  isAdult?: boolean;
  adultMode?: string;
  onToggleAdultReveal?: () => void;
  asOfDate?: string;
  externalOpenIndex?: number | null;
  onExternalOpenHandled?: () => void;
}) {
  const { t, locale } = useI18n();
  const entityId = entity.id || "";
  const [remoteItems, setRemoteItems] = useState<ScopedPictureItem[] | null>(null);
  const [tab, setTab] = useState<GalleryTab>("all");
  const [scopeFilter, setScopeFilter] = useState<string>("");
  const [lightboxIndex, setLightboxIndex] = useState<number | null>(null);
  const closeLightbox = useCallback(() => setLightboxIndex(null), []);

  useEffect(() => {
    if (!entityId) {
      setRemoteItems(null);
      return;
    }
    const ctrl = new AbortController();
    fetchEntityPictures(entityId, asOfDate ? { at: asOfDate } : {}, ctrl.signal)
      .then((res) => {
        if (Array.isArray(res.items)) {
          setRemoteItems(res.items);
        }
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setRemoteItems(null);
      });
    return () => ctrl.abort();
  }, [entityId, entity.version, asOfDate]);

  const allItems = useMemo<ScopedPictureItem[]>(() => {
    if (remoteItems && remoteItems.length > 0) return remoteItems;
    return fallbackScopedItems(entity);
  }, [remoteItems, entity]);

  const releaseScopes = useMemo(() => {
    const seen = new Map<string, string>();
    for (const item of allItems) {
      if (item.owner_kind === "release" && item.owner_id && item.owner_id !== entityId) {
        seen.set(item.owner_id, item.owner_title || item.owner_id.slice(0, 8));
      } else if (item.picture.release_id && item.picture.release_id !== entityId) {
        if (!seen.has(item.picture.release_id)) {
          seen.set(item.picture.release_id, item.picture.release_id.slice(0, 8));
        }
      }
    }
    return Array.from(seen.entries()).map(([id, label]) => ({ id, label }));
  }, [allItems, entityId]);

  const scopeFilteredItems = useMemo(() => {
    if (!scopeFilter) return allItems;
    return allItems.filter(
      (it) => it.owner_id === scopeFilter || it.picture.release_id === scopeFilter,
    );
  }, [allItems, scopeFilter]);

  const counts = useMemo(() => {
    let current = 0;
    let historical = 0;
    let icon = 0;
    let visual = 0;
    for (const item of scopeFilteredItems) {
      const role = String(item.picture.role || "").trim();
      if (item.is_historical) historical += 1;
      else current += 1;
      if (role === "app_icon") icon += 1;
      if (VISUAL_ROLES.has(role)) visual += 1;
    }
    return { current, historical, icon, visual, all: scopeFilteredItems.length };
  }, [scopeFilteredItems]);

  const visibleItems = useMemo(() => {
    switch (tab) {
      case "current":
        return scopeFilteredItems.filter((item) => !item.is_historical);
      case "historical":
        return scopeFilteredItems.filter((item) => item.is_historical);
      case "icon":
        return scopeFilteredItems.filter((item) => String(item.picture.role || "").trim() === "app_icon");
      case "visual":
        return scopeFilteredItems.filter((item) => VISUAL_ROLES.has(String(item.picture.role || "").trim()));
      default:
        return scopeFilteredItems;
    }
  }, [scopeFilteredItems, tab]);

  useEffect(() => {
    if (externalOpenIndex !== null && externalOpenIndex !== undefined && externalOpenIndex >= 0) {
      setTab("all");
      setScopeFilter("");
      setLightboxIndex(externalOpenIndex);
      onExternalOpenHandled?.();
    }
  }, [externalOpenIndex, onExternalOpenHandled]);

  const pictureRoleLabel = useCallback(
    (role?: string | null) =>
      String(role || "").trim()
        ? getTermName(defs, PICTURE_ROLE_VOCABULARY, String(role).trim(), locale)
        : "",
    [defs, locale],
  );

  const formatScopeLabel = useCallback(
    (item: ScopedPictureItem) => {
      const parts: string[] = [];
      if (item.owner_id && item.owner_id !== entityId && item.owner_title) {
        parts.push(item.owner_title);
      }
      if (item.picture.region) parts.push(item.picture.region);
      if (item.picture.platform) parts.push(item.picture.platform);
      if (item.picture.channel) parts.push(item.picture.channel);
      return parts.join(" · ");
    },
    [entityId],
  );

  const lightboxPictures = useMemo<LightboxPicture[]>(
    () =>
      visibleItems.map((item) => {
        const p = item.picture;
        const from = String(p.valid_from || p.usage_period?.begin || "").trim();
        const until = String(p.valid_to || p.usage_period?.end || "").trim();
        return {
          url: p.url,
          caption: resolveLocalizedName(p.caption, locale, item.owner_title || entity.title || ""),
          roleLabel: pictureRoleLabel(p.role),
          takenAt: String(p.taken_at || "").trim(),
          versionLabel: resolveLocalizedName(p.version_label, locale, ""),
          inUseFrom: from,
          inUseUntil: until,
          isCover: item.owner_id === entityId && item.index === COVER_PICTURE_INDEX && !item.is_historical,
          isHistorical: item.is_historical,
          isPinned: !!p.pinned,
          scopeLabel: formatScopeLabel(item),
          recordedAt: item.recorded_at || p.recorded_at,
          source: p.source,
        };
      }),
    [visibleItems, locale, entity.title, entityId, pictureRoleLabel, formatScopeLabel],
  );

  if (allItems.length === 0) return null;

  const filterTabs: { id: GalleryTab; label: string; count: number }[] = [
    { id: "current", label: t("temporal.galleryTabCurrent"), count: counts.current },
    { id: "historical", label: t("temporal.galleryTabHistorical"), count: counts.historical },
    { id: "icon", label: t("temporal.galleryTabIcon"), count: counts.icon },
    { id: "visual", label: t("temporal.galleryTabVisual"), count: counts.visual },
    { id: "all", label: t("temporal.galleryTabAll"), count: counts.all },
  ];

  return (
    <div id="pictures" className="pt-2 space-y-3 border-t border-line-subtle">
      <div className="flex flex-wrap items-center justify-between gap-2 pt-3">
        <div className="flex items-center gap-2">
          <h3 className="text-[11px] font-mono uppercase tracking-wider text-text-muted">
            {t("catalog.pictureGallery")}
          </h3>
          <span className="px-2 py-0.5 rounded-chip bg-primary/10 text-primary font-mono text-[11px] font-semibold">
            {visibleItems.length}
          </span>
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          <div className="flex flex-wrap items-center gap-1 rounded-lg bg-surfaceSubtle border border-line-subtle p-0.5 text-xs">
            {filterTabs.map((ft) => {
              const active = tab === ft.id;
              return (
                <button
                  key={ft.id}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setTab(ft.id)}
                  className={
                    "px-2 py-1 rounded-md font-mono text-[11px] transition-colors cursor-pointer " +
                    (active
                      ? "bg-surface text-primary font-semibold shadow-2xs"
                      : "text-text-muted hover:text-text-strong")
                  }
                >
                  <span>{ft.label}</span>
                  <span className="ml-1 opacity-70">{ft.count}</span>
                </button>
              );
            })}
          </div>
          {releaseScopes.length > 0 && (
            <Select
              fullWidth={false}
              value={scopeFilter}
              aria-label={t("temporal.filterRelease")}
              onChange={setScopeFilter}
              options={[
                { value: "", label: t("temporal.allReleasesScope") },
                ...releaseScopes.map((rs) => ({ value: rs.id, label: rs.label })),
              ]}
              className="!h-7 min-w-[7.5rem] !px-2 !text-xs font-mono !bg-surfaceSubtle"
            />
          )}
        </div>
      </div>

      {visibleItems.length === 0 ? (
        <Card tone="subtle" padding="card" className="text-center text-xs text-text-faint">
          {t("temporal.galleryEmptyFilter")}
        </Card>
      ) : (
        <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-3">
          {visibleItems.map((item, i) => {
            const pic = lightboxPictures[i];
            const isAppIcon = String(item.picture.role || "").trim() === "app_icon";
            const foreignReleaseId =
              item.owner_id && item.owner_id !== entityId && item.owner_kind === "release"
                ? item.owner_id
                : item.picture.release_id && item.picture.release_id !== entityId
                  ? item.picture.release_id
                  : "";
            return (
              <figure key={item.owner_id + "-" + item.index + "-" + pic.url} className="space-y-1.5">
                <button
                  type="button"
                  onClick={() => setLightboxIndex(i)}
                  className="group block w-full text-left rounded-control mf-focus cursor-pointer"
                  title={pic.source?.citation || pic.caption}
                >
                  <Card tone="subtle" padding="none" className="relative overflow-hidden">
                    <EntityCover
                      src={pic.url}
                      alt={pic.caption}
                      title={pic.caption}
                      id={item.owner_id || entity.id}
                      imgClassName={
                        (isAppIcon
                          ? "w-24 h-24 mx-auto my-3 rounded-2xl object-contain"
                          : "w-full h-auto max-h-48 object-contain") +
                        (blurAdultImages ? " blur-xl scale-110" : "")
                      }
                    />
                    <span className="absolute left-1.5 top-1.5 flex flex-wrap items-center gap-1">
                      {pic.isCover && (
                        <span className="px-1.5 py-0.5 rounded-chip bg-black/65 text-emphasis keep-white border border-emphasis/20 font-mono text-[9px] font-semibold leading-none">
                          {t("catalog.pictureCoverBadge")}
                        </span>
                      )}
                      {pic.isHistorical && (
                        <span className="px-1.5 py-0.5 rounded-chip bg-warn/85 text-primary-foreground font-mono text-[9px] font-semibold leading-none">
                          {t("temporal.pictureHistoricalBadge")}
                        </span>
                      )}
                      {pic.roleLabel && (
                        <span className="px-1.5 py-0.5 rounded-chip bg-black/65 text-emphasis keep-white border border-emphasis/20 font-mono text-[9px] leading-none max-w-[7rem] truncate">
                          {pic.roleLabel}
                        </span>
                      )}
                    </span>
                  </Card>
                </button>
                <figcaption className="space-y-0.5 font-mono text-[10px] text-text-faint">
                  <div className="truncate">
                    {pic.versionLabel ||
                      (pic.inUseFrom || pic.inUseUntil
                        ? t("catalog.picturePeriod", {
                            from: pic.inUseFrom || "?",
                            until: pic.inUseUntil || t("temporal.presentOpenEnd"),
                          })
                        : pic.takenAt
                          ? t("catalog.pictureTakenAtValue", { date: pic.takenAt })
                          : t("catalog.imageTimeUnknown"))}
                  </div>
                  {(item.picture.region || item.picture.platform || foreignReleaseId) && (
                    <div className="flex flex-wrap items-center gap-1 text-[10px] text-text-muted">
                      {foreignReleaseId && (
                        <Link
                          href={"/catalog/" + foreignReleaseId}
                          className="text-primary hover:underline truncate max-w-[10rem]"
                        >
                          {item.owner_id === foreignReleaseId && item.owner_title
                            ? item.owner_title
                            : foreignReleaseId.slice(0, 8)}
                        </Link>
                      )}
                      {(item.picture.region || item.picture.platform) && (
                        <span>
                          {[item.picture.region, item.picture.platform].filter(Boolean).join(" / ")}
                        </span>
                      )}
                    </div>
                  )}
                </figcaption>
              </figure>
            );
          })}
        </div>
      )}

      <PictureLightbox
        pictures={lightboxPictures}
        index={lightboxIndex}
        entityId={entity.id}
        blurred={blurAdultImages}
        blurLabel={blurAdultImages ? t("catalog.adultRevealImage") : t("catalog.adultHideImage")}
        onToggleBlur={isAdult && adultMode === "blur" ? onToggleAdultReveal : undefined}
        onIndexChange={setLightboxIndex}
        onClose={closeLightbox}
      />
    </div>
  );
}
