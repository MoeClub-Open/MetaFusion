"use client";

import Link from "next/link";
import { History, RotateCcw } from "lucide-react";
import { Card } from "@/components/ui/Card";
import { EntityCover } from "@/components/common/EntityCover";
import { ExternalAuthorityLinks } from "@/components/entity/ExternalAuthorityLinks";
import { FieldValue } from "@/components/catalog/TemplateAttributeSections";
import type { Entity } from "@/components/catalog/api";
import {
  getFieldName,
  getRelationName,
  getTagName,
  getTermName,
  resolveLocalizedName,
  tagCode,
  type DynamicDefinitions,
} from "@/lib/definitions";
import { languageLabel } from "@/lib/languages";
import { useI18n } from "@/i18n/I18nProvider";
import type {
  EntityAsOfResponse,
  EntityTemporalCompareResponse,
  RelationshipEntitySummary,
  TemporalAgentFact,
  TemporalFieldDiff,
  TemporalPictureSelection,
  TemporalReleaseView,
  TimelineEvent,
} from "@/lib/api/temporal";

const INTERNAL_HISTORY_ATTRS = new Set(["former_names", "attribute_history", "events"]);

export function cleanRelationCode(code: string): string {
  return code.startsWith("relation:") ? code.slice("relation:".length) : code;
}

export function eventAnchorDate(ev: TimelineEvent): string {
  return String(ev.effective_at || ev.occurred_at || ev.begin_date || ev.earliest_date || "").trim();
}

export function HistoricalModeBanner({
  snapshot,
  showTimelineAction = false,
  onOpenTimeline,
  onExit,
}: {
  snapshot: EntityAsOfResponse;
  showTimelineAction?: boolean;
  onOpenTimeline?: () => void;
  onExit: () => void;
}) {
  const { t } = useI18n();
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-primary/30 bg-primary/10 px-3.5 py-2.5 text-xs">
      <div className="flex flex-wrap items-center gap-2 min-w-0">
        <History className="w-4 h-4 text-primary shrink-0" strokeWidth={1.7} />
        <span className="font-mono font-semibold text-primary">
          {t("temporal.historicalBannerTitle", { at: snapshot.at })}
        </span>
        {snapshot.scope?.region && (
          <span className="px-1.5 py-0.5 rounded bg-surface border border-line-subtle font-mono text-[10px] text-text-body">
            {snapshot.scope.region}
          </span>
        )}
        {snapshot.scope?.platform && (
          <span className="px-1.5 py-0.5 rounded bg-surface border border-line-subtle font-mono text-[10px] text-text-body">
            {snapshot.scope.platform}
          </span>
        )}
        {snapshot.unknown_fields?.length > 0 && (
          <span className="text-text-muted">
            {t("temporal.unknownSummary", { count: snapshot.unknown_fields.length })}
          </span>
        )}
        {(snapshot.disputed_fields?.length || 0) > 0 && (
          <span className="text-warn font-medium">
            {t("temporal.disputedSummary", { count: snapshot.disputed_fields!.length })}
          </span>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-2 shrink-0">
        {showTimelineAction && onOpenTimeline && (
          <button
            type="button"
            onClick={onOpenTimeline}
            className="px-2.5 py-1 rounded-md border border-line bg-surface hover:bg-surfaceHover text-text-body font-mono text-[11px] cursor-pointer"
          >
            {t("temporal.tabTitle")}
          </button>
        )}
        <button
          type="button"
          onClick={onExit}
          className="inline-flex items-center gap-1 px-2.5 py-1 rounded-md bg-primary text-primary-foreground font-mono text-[11px] font-semibold hover:opacity-90 cursor-pointer"
        >
          <RotateCcw className="w-3 h-3" />
          <span>{t("temporal.exitHistoricalMode")}</span>
        </button>
      </div>
    </div>
  );
}

function StateBadge({ state }: { state: string }) {
  const { t } = useI18n();
  if (state === "confirmed") {
    return (
      <span className="px-1.5 py-0.5 rounded bg-success/15 text-success font-mono text-[10px] font-semibold">
        {t("temporal.stateConfirmed")}
      </span>
    );
  }
  if (state === "pinned") {
    return (
      <span className="px-1.5 py-0.5 rounded bg-info/15 text-info font-mono text-[10px] font-semibold">
        {t("temporal.statePinned")}
      </span>
    );
  }
  if (state === "disputed") {
    return (
      <span className="px-1.5 py-0.5 rounded bg-warn/15 text-warn font-mono text-[10px] font-semibold">
        {t("temporal.stateDisputed")}
      </span>
    );
  }
  return (
    <span className="px-1.5 py-0.5 rounded bg-emphasis/[0.06] text-text-muted font-mono text-[10px]">
      {t("temporal.stateUnknown")}
    </span>
  );
}

export function TemporalAsOfSection({
  entityId,
  defs,
  blurAdultImages = false,
  asOfInput,
  onAsOfInputChange,
  asOfData,
  asOfLoading,
  asOfError,
  quickDates,
  onRunAsOf,
  onClearAsOf,
}: {
  entityId: string;
  defs: DynamicDefinitions | null | undefined;
  blurAdultImages?: boolean;
  asOfInput: string;
  onAsOfInputChange: (val: string) => void;
  asOfData: EntityAsOfResponse | null;
  asOfLoading: boolean;
  asOfError: string;
  quickDates: { date: string; label: string }[];
  onRunAsOf: (date: string) => void;
  onClearAsOf: () => void;
}) {
  const { t, locale } = useI18n();

  const statusTermLabel = (code?: string) =>
    code ? getTermName(defs, "release_status", code, locale) : "";

  const renderAgentFacts = (facts: TemporalAgentFact[], state: string) => {
    if (!facts || facts.length === 0) {
      return (
        <span className="inline-flex items-center gap-1.5 text-xs text-text-faint">
          <StateBadge state={state} />
        </span>
      );
    }
    return (
      <div className="flex flex-wrap items-center gap-1.5">
        {facts.map((f, idx) => (
          <span
            key={f.agent_id + "-" + idx}
            className="inline-flex items-center gap-1 px-2 py-0.5 rounded-md bg-surfaceSubtle border border-line-subtle text-xs"
          >
            <Link href={"/catalog/" + f.agent_id} className="text-primary hover:underline font-medium">
              {f.title || f.agent_id.slice(0, 8)}
            </Link>
            {(f.credit_role || f.role) && (
              <span className="text-[10px] text-text-muted">({f.credit_role || f.role})</span>
            )}
            {(f.valid_from || f.valid_to) && (
              <span className="font-mono text-[10px] text-text-faint">
                [{f.valid_from || "?"}..{f.valid_to || t("temporal.presentOpenEnd")})
              </span>
            )}
          </span>
        ))}
        {state !== "confirmed" && <StateBadge state={state} />}
      </div>
    );
  };

  const renderPicSelection = (sel: TemporalPictureSelection, label: string, isIcon = false) => (
    <div className="rounded-lg border border-line-subtle bg-surfaceSubtle p-2.5 space-y-2">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-[11px] text-text-muted">{label}</span>
        <StateBadge state={sel.status} />
      </div>
      {sel.picture?.url ? (
        <div className="space-y-1.5">
          <EntityCover
            src={sel.picture.url}
            alt={label}
            title={label}
            id={sel.owner_id || entityId}
            imgClassName={
              (isIcon ? "w-16 h-16 rounded-xl object-contain mx-auto" : "w-full max-h-40 object-contain rounded") +
              (blurAdultImages ? " blur-xl scale-110" : "")
            }
          />
          {(sel.picture.valid_from || sel.picture.valid_to) && (
            <div className="font-mono text-[10px] text-text-faint text-center">
              [{sel.picture.valid_from || "?"}..{sel.picture.valid_to || t("temporal.presentOpenEnd")})
            </div>
          )}
        </div>
      ) : (
        <div className="h-20 grid place-items-center text-xs text-text-faint font-mono">
          {t("temporal.stateUnknown")}
        </div>
      )}
    </div>
  );

  const renderReconstructedTranslations = (
    translations: Entity["translations"] | undefined,
    disputedFields: string[] | undefined,
  ) => {
    const entries = Object.entries(translations || {}).filter(([, row]) => String(row?.title || "").trim() !== "");
    const disputedLocales = (disputedFields || [])
      .filter((d) => d.startsWith("translations."))
      .map((d) => d.slice("translations.".length));
    if (entries.length === 0 && disputedLocales.length === 0) return null;
    return (
      <div className="flex flex-wrap gap-1.5">
        {entries.map(([loc, row]) => (
          <span
            key={loc}
            className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-surface border border-line-subtle text-xs"
          >
            <span className="font-mono text-[10px] text-text-muted">{languageLabel(loc)}:</span>
            <span className="font-medium text-text-strong">{row.title}</span>
            {row.nonofficial && (
              <span className="font-mono text-[10px] text-text-faint">({t("catalog.nonofficialTitle")})</span>
            )}
            {disputedLocales.includes(loc) && <StateBadge state="disputed" />}
          </span>
        ))}
        {disputedLocales
          .filter((loc) => !translations?.[loc]?.title)
          .map((loc) => (
            <span
              key={"disp-" + loc}
              className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-surface border border-line-subtle text-xs"
            >
              <span className="font-mono text-[10px] text-text-muted">{languageLabel(loc)}:</span>
              <StateBadge state="disputed" />
            </span>
          ))}
      </div>
    );
  };

  const renderReconstructedAttributes = (
    attrs: Record<string, any> | undefined,
    attrStates: Record<string, string> | undefined,
    unknownFields: string[] | undefined,
    disputedFields: string[] | undefined,
    entitiesMap: Record<string, RelationshipEntitySummary>,
    skipKeys: Set<string>,
  ) => {
    const keys = new Set<string>();
    for (const k of Object.keys(attrs || {})) {
      if (!INTERNAL_HISTORY_ATTRS.has(k) && !skipKeys.has(k)) keys.add(k);
    }
    for (const k of Object.keys(attrStates || {})) {
      if (!INTERNAL_HISTORY_ATTRS.has(k) && !skipKeys.has(k)) keys.add(k);
    }
    for (const f of [...(unknownFields || []), ...(disputedFields || [])]) {
      if (f.startsWith("attributes.")) {
        const k = f.slice("attributes.".length);
        if (k && !INTERNAL_HISTORY_ATTRS.has(k) && !skipKeys.has(k)) keys.add(k);
      }
    }
    const sorted = Array.from(keys).sort();
    if (sorted.length === 0) return null;
    return (
      <dl className="grid grid-cols-1 sm:grid-cols-2 gap-x-4 gap-y-1.5 text-xs">
        {sorted.map((code) => {
          const val = attrs?.[code];
          const hasVal =
            val !== undefined && val !== null && val !== "" && (!Array.isArray(val) || val.length > 0);
          const st =
            attrStates?.[code] ||
            (disputedFields?.includes("attributes." + code)
              ? "disputed"
              : unknownFields?.includes("attributes." + code)
                ? "unknown"
                : hasVal
                  ? "confirmed"
                  : "unknown");
          const fDef = defs?.fields?.[code];
          return (
            <div key={code} className="flex flex-wrap items-baseline gap-1.5 min-w-0">
              <dt className="font-mono text-[10px] text-text-muted shrink-0">
                {getFieldName(defs, code, locale) || code}:
              </dt>
              <dd className="flex flex-wrap items-center gap-1.5 min-w-0 text-text-strong">
                {hasVal ? (
                  code === "tags" && Array.isArray(val) ? (
                    <span className="flex flex-wrap gap-1">
                      {val.map((rawTag: any, idx: number) => {
                        const tc = tagCode(rawTag);
                        return tc ? (
                          <span
                            key={tc + "-" + idx}
                            className="px-1.5 py-0.5 rounded-chip bg-emphasis/[0.06] text-[11px] text-text-body"
                          >
                            {getTagName(defs, tc, locale)}
                          </span>
                        ) : null;
                      })}
                    </span>
                  ) : fDef?.type === "entity" && typeof val === "string" ? (
                    <Link href={"/catalog/" + val} className="text-primary hover:underline font-medium">
                      {entitiesMap[val]?.title || val.slice(0, 8)}
                    </Link>
                  ) : (
                    <FieldValue code={code} value={val} defs={defs} locale={locale} resolveReferences={false} />
                  )
                ) : null}
                {(!hasVal || st !== "confirmed") && <StateBadge state={st} />}
              </dd>
            </div>
          );
        })}
      </dl>
    );
  };

  const renderReleaseAsOfCard = (rv: TemporalReleaseView) => {
    const relSkip = new Set([
      "release_status",
      "publisher",
      "operator",
      "country",
      "region",
      "platform",
      "distribution_channel",
      "channel",
    ]);
    const relTransNode = renderReconstructedTranslations(rv.translations, rv.disputed_fields);
    const relAttrsNode = renderReconstructedAttributes(
      rv.attributes,
      rv.attribute_states,
      rv.unknown_fields,
      rv.disputed_fields,
      asOfData?.entities || {},
      relSkip,
    );
    return (
      <Card key={rv.release_id} tone="subtle" padding="card" className="space-y-2.5">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line-subtle pb-2">
          <div className="flex flex-wrap items-center gap-1.5 min-w-0">
            <Link href={"/catalog/" + rv.release_id} className="font-semibold text-xs sm:text-sm text-primary hover:underline">
              {rv.title || rv.release_id.slice(0, 8)}
            </Link>
            {rv.title_status !== "confirmed" && <StateBadge state={rv.title_status} />}
          </div>
          <div className="flex flex-wrap items-center gap-1 font-mono text-[10px] text-text-muted">
            {rv.region && <span className="px-1.5 py-0.5 rounded bg-emphasis/[0.06]">{rv.region}</span>}
            {rv.platform && <span className="px-1.5 py-0.5 rounded bg-emphasis/[0.06]">{rv.platform}</span>}
            {(rv.channel || rv.distribution_channel) && (
              <span className="px-1.5 py-0.5 rounded bg-emphasis/[0.06]">
                {rv.channel || getTermName(defs, "distribution_channel", rv.distribution_channel || "", locale)}
              </span>
            )}
          </div>
        </div>
        <dl className="grid grid-cols-1 sm:grid-cols-3 gap-2 text-xs">
          <div className="space-y-1">
            <dt className="font-mono text-[10px] text-text-muted">{t("temporal.fieldReleaseStatus")}</dt>
            <dd className="flex flex-wrap items-center gap-1.5">
              {rv.release_status ? (
                <span className="font-medium text-text-strong">{statusTermLabel(rv.release_status)}</span>
              ) : null}
              <StateBadge state={rv.status_state} />
            </dd>
          </div>
          <div className="space-y-1">
            <dt className="font-mono text-[10px] text-text-muted">{t("temporal.fieldPublishers")}</dt>
            <dd>{renderAgentFacts(rv.publishers, rv.publisher_state)}</dd>
          </div>
          <div className="space-y-1">
            <dt className="font-mono text-[10px] text-text-muted">{t("temporal.fieldOperators")}</dt>
            <dd>{renderAgentFacts(rv.operators, rv.operator_state)}</dd>
          </div>
        </dl>
        {relTransNode}
        {relAttrsNode}
        {rv.external_links && rv.external_links.length > 0 && (
          <ExternalAuthorityLinks externalLinks={rv.external_links} strictOverride variant="chips" />
        )}
        {(rv.cover.picture?.url || rv.app_icon.picture?.url) && (
          <div className="grid grid-cols-2 gap-2 pt-1">
            {renderPicSelection(rv.cover, t("temporal.fieldCover"), false)}
            {renderPicSelection(rv.app_icon, t("temporal.fieldAppIcon"), true)}
          </div>
        )}
      </Card>
    );
  };

  return (
    <div className="space-y-3">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onRunAsOf(asOfInput);
        }}
        className="flex flex-wrap items-center gap-2"
      >
        <label className="inline-flex items-center gap-2 text-xs text-text-body">
          <span className="font-mono">{t("temporal.selectDate")}</span>
          <input
            value={asOfInput}
            onChange={(e) => onAsOfInputChange(e.target.value)}
            placeholder="YYYY-MM-DD / YYYY-MM / YYYY"
            className="h-9 w-52 px-2.5 rounded-md bg-surface border border-line text-xs text-text-strong font-mono placeholder:text-text-faint"
          />
        </label>
        <button
          type="submit"
          disabled={!asOfInput.trim() || asOfLoading}
          className="h-9 px-3.5 rounded-md bg-primary text-primary-foreground text-xs font-semibold hover:opacity-90 disabled:opacity-50 cursor-pointer"
        >
          {asOfLoading ? t("common.loading") : t("temporal.reconstructAction")}
        </button>
        {asOfData && (
          <button
            type="button"
            onClick={onClearAsOf}
            className="inline-flex items-center gap-1 h-9 px-3 rounded-md border border-line text-xs text-text-body hover:bg-surfaceHover cursor-pointer"
          >
            <RotateCcw className="w-3.5 h-3.5" />
            <span>{t("temporal.exitHistoricalMode")}</span>
          </button>
        )}
      </form>

      {quickDates.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="font-mono text-[11px] text-text-faint">{t("temporal.quickJumpNodes")}</span>
          {quickDates.map((qd) => (
            <button
              key={qd.date}
              type="button"
              onClick={() => onRunAsOf(qd.date)}
              className={
                "px-2 py-0.5 rounded-chip border font-mono text-[11px] transition-colors cursor-pointer " +
                (asOfData?.at === qd.date
                  ? "bg-primary/15 border-primary/35 text-primary font-semibold"
                  : "bg-surfaceSubtle border-line-subtle text-text-body hover:border-primary/30")
              }
            >
              {qd.label}
            </button>
          ))}
        </div>
      )}

      {asOfError && (
        <div role="alert" className="rounded-lg border border-warn/30 bg-warn/10 p-3 text-xs text-warn">
          {asOfError}
        </div>
      )}

      {asOfData && (
        <div className="space-y-3 pt-1">
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-primary/25 bg-primary/10 px-3 py-2 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono font-bold text-primary">
                {t("temporal.snapshotAtBadge", { at: asOfData.at })}
              </span>
              {asOfData.unknown_fields?.length > 0 && (
                <span className="text-text-muted">
                  {t("temporal.unknownSummary", { count: asOfData.unknown_fields.length })}
                </span>
              )}
              {(asOfData.disputed_fields?.length || 0) > 0 && (
                <span className="text-warn font-medium">
                  {t("temporal.disputedSummary", { count: asOfData.disputed_fields!.length })}
                </span>
              )}
            </div>
            <span className="font-mono text-[11px] text-text-faint">
              {t("temporal.recordedAtLabel", { date: asOfData.recorded_at.slice(0, 10) })}
            </span>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_240px] gap-3">
            <Card tone="subtle" padding="card" className="space-y-3">
              <dl className="space-y-2.5 text-xs">
                <div>
                  <dt className="font-mono text-[10px] text-text-muted mb-0.5">{t("temporal.fieldTitle")}</dt>
                  <dd className="flex flex-wrap items-center gap-2">
                    {asOfData.title ? (
                      <span className="text-sm font-bold text-text-strong">{asOfData.title}</span>
                    ) : null}
                    <StateBadge state={asOfData.title_status} />
                    {asOfData.disputed_titles && asOfData.disputed_titles.length > 0 && (
                      <span className="font-mono text-[11px] text-warn">
                        ({asOfData.disputed_titles.join(" / ")})
                      </span>
                    )}
                  </dd>
                </div>

                {(() => {
                  const transNode = renderReconstructedTranslations(asOfData.translations, asOfData.disputed_fields);
                  return transNode ? (
                    <div>
                      <dt className="font-mono text-[10px] text-text-muted mb-1">{t("catalog.translations")}</dt>
                      <dd>{transNode}</dd>
                    </div>
                  ) : null;
                })()}

                {asOfData.kind === "release" && (
                  <>
                    <div>
                      <dt className="font-mono text-[10px] text-text-muted mb-0.5">{t("temporal.fieldReleaseStatus")}</dt>
                      <dd className="flex flex-wrap items-center gap-1.5">
                        {asOfData.release_status ? (
                          <span className="font-medium text-text-strong">{statusTermLabel(asOfData.release_status)}</span>
                        ) : null}
                        <StateBadge state={asOfData.status_state} />
                      </dd>
                    </div>
                    <div>
                      <dt className="font-mono text-[10px] text-text-muted mb-0.5">{t("temporal.fieldPublishers")}</dt>
                      <dd>{renderAgentFacts(asOfData.publishers, asOfData.publisher_state)}</dd>
                    </div>
                    <div>
                      <dt className="font-mono text-[10px] text-text-muted mb-0.5">{t("temporal.fieldOperators")}</dt>
                      <dd>{renderAgentFacts(asOfData.operators, asOfData.operator_state)}</dd>
                    </div>
                  </>
                )}

                {(() => {
                  const skip =
                    asOfData.kind === "release"
                      ? new Set(["release_status", "publisher", "operator"])
                      : new Set<string>();
                  const attrsNode = renderReconstructedAttributes(
                    asOfData.attributes,
                    asOfData.attribute_states,
                    asOfData.unknown_fields,
                    asOfData.disputed_fields,
                    asOfData.entities || {},
                    skip,
                  );
                  return attrsNode ? (
                    <div>
                      <dt className="font-mono text-[10px] text-text-muted mb-1">{t("catalog.attributes")}</dt>
                      <dd>{attrsNode}</dd>
                    </div>
                  ) : null;
                })()}

                {((asOfData.external_links && asOfData.external_links.length > 0) ||
                  asOfData.unknown_fields?.includes("external_links") ||
                  asOfData.disputed_fields?.includes("external_links")) && (
                  <div>
                    <dt className="font-mono text-[10px] text-text-muted mb-1">{t("catalog.externalLinks")}</dt>
                    <dd className="flex flex-wrap items-center gap-1.5">
                      {asOfData.external_links && asOfData.external_links.length > 0 && (
                        <ExternalAuthorityLinks
                          externalLinks={asOfData.external_links}
                          strictOverride
                          variant="chips"
                        />
                      )}
                      {asOfData.disputed_fields?.includes("external_links") ? (
                        <StateBadge state="disputed" />
                      ) : (!asOfData.external_links || asOfData.external_links.length === 0) &&
                          asOfData.unknown_fields?.includes("external_links") ? (
                        <StateBadge state="unknown" />
                      ) : null}
                    </dd>
                  </div>
                )}

                {asOfData.active_relations && asOfData.active_relations.length > 0 && (
                  <div>
                    <dt className="font-mono text-[10px] text-text-muted mb-1">{t("temporal.activeRelationsAt")}</dt>
                    <dd className="flex flex-wrap gap-1.5">
                      {asOfData.active_relations.map((rel) => {
                        const peerId = rel.source_id === entityId ? rel.target_id : rel.source_id;
                        const peer = asOfData.entities?.[peerId];
                        const relCode = cleanRelationCode(rel.rule_code);
                        return (
                          <Link
                            key={rel.key}
                            href={"/catalog/" + peerId}
                            className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-surface border border-line-subtle text-xs hover:border-primary/40"
                          >
                            <span className="text-text-muted">
                              {getRelationName(defs, relCode, rel.source_id === entityId, locale)}:
                            </span>
                            <span className="text-primary font-medium">{peer?.title || peerId.slice(0, 8)}</span>
                          </Link>
                        );
                      })}
                    </dd>
                  </div>
                )}
              </dl>
            </Card>

            <div className="grid grid-cols-2 md:grid-cols-1 gap-2">
              {renderPicSelection(asOfData.cover, t("temporal.fieldCover"), false)}
              {renderPicSelection(asOfData.app_icon, t("temporal.fieldAppIcon"), true)}
            </div>
          </div>

          {asOfData.kind === "work" && asOfData.releases && asOfData.releases.length > 0 && (
            <div className="space-y-2">
              <h4 className="font-mono text-[11px] uppercase tracking-wider text-text-muted">
                {t("temporal.perReleaseHeading")}
              </h4>
              <div className="space-y-2">{asOfData.releases.map(renderReleaseAsOfCard)}</div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

export function TemporalCompareSection({
  entityId,
  defs,
  blurAdultImages = false,
  compareFrom,
  compareTo,
  onCompareFromChange,
  onCompareToChange,
  compareData,
  compareLoading,
  compareError,
  quickDates,
  onRunCompare,
  onJumpAsOf,
}: {
  entityId: string;
  defs: DynamicDefinitions | null | undefined;
  blurAdultImages?: boolean;
  compareFrom: string;
  compareTo: string;
  onCompareFromChange: (val: string) => void;
  onCompareToChange: (val: string) => void;
  compareData: EntityTemporalCompareResponse | null;
  compareLoading: boolean;
  compareError: string;
  quickDates: { date: string; label: string }[];
  onRunCompare: (from: string, to: string) => void;
  onJumpAsOf: (date: string) => void;
}) {
  const { t, locale } = useI18n();

  const eventTypeLabel = (code: string) => {
    const fromEvent = getTermName(defs, "event_type", code, locale);
    if (fromEvent !== code) return fromEvent;
    return getTermName(defs, "embedded_event_kind", code, locale);
  };

  const statusTermLabel = (code?: string) =>
    code ? getTermName(defs, "release_status", code, locale) : "";

  const formatDiffFieldLabel = (field: string): string => {
    const clean = field.startsWith("release.") ? field.slice("release.".length) : field;
    if (clean.startsWith("translations.")) {
      const loc = clean.slice("translations.".length);
      return `${t("catalog.translations")} (${languageLabel(loc)})`;
    }
    if (clean.startsWith("attributes.")) {
      const attrCode = clean.slice("attributes.".length);
      return getFieldName(defs, attrCode, locale) || attrCode;
    }
    switch (clean) {
      case "title":
        return t("temporal.fieldTitle");
      case "cover":
        return t("temporal.fieldCover");
      case "app_icon":
        return t("temporal.fieldAppIcon");
      case "release_status":
        return t("temporal.fieldReleaseStatus");
      case "publishers":
        return t("temporal.fieldPublishers");
      case "operators":
        return t("temporal.fieldOperators");
      case "external_links":
        return t("catalog.externalLinks");
      default:
        return getFieldName(defs, clean, locale) || field;
    }
  };

  const renderDiffValue = (
    diff: TemporalFieldDiff,
    val: any,
    status: string,
    entitiesMap: Record<string, RelationshipEntitySummary>,
  ) => {
    const clean = diff.field.startsWith("release.") ? diff.field.slice("release.".length) : diff.field;
    const isVisual = diff.field.endsWith("cover") || diff.field.endsWith("app_icon");
    const isAgents = diff.field.endsWith("publishers") || diff.field.endsWith("operators");
    const isStatus = diff.field.endsWith("release_status");
    const isExtLinks = clean === "external_links";

    if (isVisual) {
      const url = typeof val === "string" ? val.trim() : "";
      return (
        <div className="flex items-center gap-2">
          {url ? (
            <EntityCover
              src={url}
              alt={diff.field}
              title={diff.field}
              id={diff.release_id || entityId}
              imgClassName={
                "w-12 h-12 rounded object-contain border border-line-subtle" +
                (blurAdultImages ? " blur-md" : "")
              }
            />
          ) : null}
          <StateBadge state={status} />
        </div>
      );
    }
    if (isExtLinks) {
      const links = Array.isArray(val) ? val : [];
      if (links.length === 0) return <StateBadge state={status} />;
      return (
        <div className="flex flex-wrap items-center gap-1.5">
          <ExternalAuthorityLinks externalLinks={links} strictOverride variant="chips" />
          {status !== "confirmed" && <StateBadge state={status} />}
        </div>
      );
    }
    if (clean.startsWith("attributes.")) {
      const attrCode = clean.slice("attributes.".length);
      const hasVal =
        val !== undefined && val !== null && val !== "" && (!Array.isArray(val) || val.length > 0);
      if (!hasVal) return <StateBadge state={status} />;
      const fDef = defs?.fields?.[attrCode];
      if (attrCode === "tags" && Array.isArray(val)) {
        return (
          <div className="flex flex-wrap items-center gap-1">
            {val.map((rawTag: any, idx: number) => {
              const tc = tagCode(rawTag);
              return tc ? (
                <span
                  key={tc + "-" + idx}
                  className="px-1.5 py-0.5 rounded-chip bg-surface border border-line-subtle text-[11px] text-text-strong"
                >
                  {getTagName(defs, tc, locale)}
                </span>
              ) : null;
            })}
            {status !== "confirmed" && <StateBadge state={status} />}
          </div>
        );
      }
      if (fDef?.type === "entity" && typeof val === "string") {
        return (
          <div className="flex flex-wrap items-center gap-1.5">
            <Link href={"/catalog/" + val} className="text-primary hover:underline text-xs font-medium">
              {entitiesMap[val]?.title || val.slice(0, 8)}
            </Link>
            {status !== "confirmed" && <StateBadge state={status} />}
          </div>
        );
      }
      return (
        <div className="flex flex-wrap items-center gap-1.5 text-xs text-text-strong">
          <FieldValue code={attrCode} value={val} defs={defs} locale={locale} resolveReferences={false} />
          {status !== "confirmed" && <StateBadge state={status} />}
        </div>
      );
    }
    if (isAgents && Array.isArray(val)) {
      if (val.length === 0) return <StateBadge state={status} />;
      return (
        <div className="flex flex-wrap items-center gap-1.5">
          {val.map((aid: string) => (
            <Link key={aid} href={"/catalog/" + aid} className="text-primary hover:underline text-xs font-medium">
              {entitiesMap[aid]?.title || aid.slice(0, 8)}
            </Link>
          ))}
          {status !== "confirmed" && <StateBadge state={status} />}
        </div>
      );
    }
    if (isStatus) {
      const s = typeof val === "string" ? val.trim() : "";
      return (
        <div className="flex flex-wrap items-center gap-1.5">
          {s ? <span className="text-xs font-medium text-text-strong">{statusTermLabel(s)}</span> : null}
          <StateBadge state={status} />
        </div>
      );
    }
    const textVal = val === undefined || val === null || val === "" ? "" : String(val);
    return (
      <div className="flex flex-wrap items-center gap-1.5">
        {textVal ? <span className="text-xs font-medium text-text-strong break-words">{textVal}</span> : null}
        <StateBadge state={status} />
      </div>
    );
  };

  return (
    <div className="space-y-3">
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onRunCompare(compareFrom, compareTo);
        }}
        className="flex flex-wrap items-center gap-2"
      >
        <label className="inline-flex items-center gap-1.5 text-xs text-text-body">
          <span className="font-mono">{t("temporal.compareFromLabel")}</span>
          <input
            list={entityId + "-quickdates"}
            value={compareFrom}
            onChange={(e) => onCompareFromChange(e.target.value)}
            placeholder="2021-02-04"
            className="h-9 w-36 px-2.5 rounded-md bg-surface border border-line text-xs text-text-strong font-mono placeholder:text-text-faint"
          />
        </label>
        <label className="inline-flex items-center gap-1.5 text-xs text-text-body">
          <span className="font-mono">{t("temporal.compareToLabel")}</span>
          <input
            list={entityId + "-quickdates"}
            value={compareTo}
            onChange={(e) => onCompareToChange(e.target.value)}
            placeholder="2023-08-03"
            className="h-9 w-36 px-2.5 rounded-md bg-surface border border-line text-xs text-text-strong font-mono placeholder:text-text-faint"
          />
        </label>
        <datalist id={entityId + "-quickdates"}>
          {quickDates.map((qd) => (
            <option key={qd.date} value={qd.date}>
              {qd.label}
            </option>
          ))}
        </datalist>
        <button
          type="submit"
          disabled={!compareFrom.trim() || !compareTo.trim() || compareLoading}
          className="h-9 px-3.5 rounded-md bg-primary text-primary-foreground text-xs font-semibold hover:opacity-90 disabled:opacity-50 cursor-pointer"
        >
          {compareLoading ? t("common.loading") : t("temporal.compareAction")}
        </button>
      </form>

      {compareError && (
        <div role="alert" className="rounded-lg border border-warn/30 bg-warn/10 p-3 text-xs text-warn">
          {compareError}
        </div>
      )}

      {compareData && (
        <div className="space-y-4 pt-1">
          <div className="space-y-2">
            <h4 className="font-mono text-[11px] uppercase tracking-wider text-text-muted">
              {t("temporal.fieldChangesHeading", { count: compareData.field_changes.length })}
            </h4>
            {compareData.field_changes.length === 0 ? (
              <Card tone="subtle" padding="card" className="text-xs text-text-faint text-center">
                {t("temporal.noFieldChanges")}
              </Card>
            ) : (
              <div className="space-y-2">
                {compareData.field_changes.map((diff, idx) => {
                  const relEnt = diff.release_id ? compareData.entities?.[diff.release_id] : undefined;
                  return (
                    <div
                      key={diff.field + "-" + (diff.release_id || "") + "-" + idx}
                      className="rounded-lg border border-primary/25 bg-primary/[0.04] p-3 space-y-2"
                    >
                      <div className="flex flex-wrap items-center gap-2 text-xs">
                        <span className="font-semibold text-text-strong">{formatDiffFieldLabel(diff.field)}</span>
                        {diff.release_id && (
                          <Link
                            href={"/catalog/" + diff.release_id}
                            className="px-1.5 py-0.5 rounded bg-surface border border-line-subtle text-primary hover:underline font-mono text-[10px]"
                          >
                            {relEnt?.title || diff.release_id.slice(0, 8)}
                          </Link>
                        )}
                        {diff.scope_label && (
                          <span className="font-mono text-[10px] text-text-muted">{diff.scope_label}</span>
                        )}
                      </div>
                      <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                        <div className="rounded-md border border-danger/25 bg-danger/[0.06] p-2 space-y-1">
                          <div className="font-mono text-[10px] text-text-muted">{compareData.from}</div>
                          {renderDiffValue(diff, diff.from_value, diff.from_status, compareData.entities || {})}
                        </div>
                        <div className="rounded-md border border-success/25 bg-success/[0.06] p-2 space-y-1">
                          <div className="font-mono text-[10px] text-text-muted">{compareData.to}</div>
                          {renderDiffValue(diff, diff.to_value, diff.to_status, compareData.entities || {})}
                        </div>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </div>

          {compareData.relation_changes.length > 0 && (
            <div className="space-y-2">
              <h4 className="font-mono text-[11px] uppercase tracking-wider text-text-muted">
                {t("temporal.relationChangesHeading", { count: compareData.relation_changes.length })}
              </h4>
              <div className="space-y-1.5">
                {compareData.relation_changes.map((rc, idx) => {
                  const peerId = rc.source_id === entityId ? rc.target_id : rc.source_id;
                  const peer = compareData.entities?.[peerId];
                  const relCode = cleanRelationCode(rc.rule_code);
                  const toneClass =
                    rc.change === "added"
                      ? "border-success/30 bg-success/10 text-success"
                      : rc.change === "removed"
                        ? "border-danger/30 bg-danger/10 text-danger"
                        : "border-warn/30 bg-warn/10 text-warn";
                  const changeLabel =
                    rc.change === "added"
                      ? t("temporal.relAdded")
                      : rc.change === "removed"
                        ? t("temporal.relRemoved")
                        : t("temporal.relChanged");
                  return (
                    <div
                      key={rc.rule_code + "-" + peerId + "-" + idx}
                      className="flex flex-wrap items-center gap-2 rounded-md border border-line-subtle bg-surfaceSubtle px-3 py-2 text-xs"
                    >
                      <span className={"px-1.5 py-0.5 rounded border font-mono text-[10px] font-semibold " + toneClass}>
                        {changeLabel}
                      </span>
                      <span className="text-text-body">
                        {getRelationName(defs, relCode, rc.source_id === entityId, locale)}
                      </span>
                      <Link href={"/catalog/" + peerId} className="text-primary hover:underline font-medium">
                        {peer?.title || peerId.slice(0, 8)}
                      </Link>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {compareData.events_between.length > 0 && (
            <div className="space-y-2">
              <h4 className="font-mono text-[11px] uppercase tracking-wider text-text-muted">
                {t("temporal.eventsBetweenHeading", { count: compareData.events_between.length })}
              </h4>
              <div className="space-y-1.5">
                {compareData.events_between.map((ev) => {
                  const anchor = eventAnchorDate(ev);
                  const evTitle = resolveLocalizedName(ev.title_names, locale, ev.title) || eventTypeLabel(ev.event_type);
                  return (
                    <div
                      key={ev.id}
                      className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-line-subtle bg-surfaceSubtle px-3 py-2 text-xs"
                    >
                      <div className="flex flex-wrap items-center gap-2 min-w-0">
                        <span className="font-mono text-primary font-semibold">
                          {ev.occurred_at || ev.begin_date || anchor}
                        </span>
                        <span className="px-1.5 py-0.5 rounded bg-primary/10 text-primary font-mono text-[10px]">
                          {eventTypeLabel(ev.event_type)}
                        </span>
                        <span className="text-text-strong font-medium truncate">{evTitle}</span>
                      </div>
                      {anchor && (
                        <button
                          type="button"
                          onClick={() => onJumpAsOf(anchor)}
                          className="text-primary hover:underline font-mono text-[11px] cursor-pointer"
                        >
                          {t("temporal.jumpAsOf")}
                        </button>
                      )}
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
