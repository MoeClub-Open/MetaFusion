"use client";

import { useI18n } from "@/i18n/I18nProvider";
import { resolveLocalizedName, type DynamicDefinitions } from "@/lib/definitions";
import type { IdentityCandidateEntity, IdentityReviewState } from "@/lib/identityReview";
import { FieldValue } from "./TemplateAttributeSections";

function CandidateSummary({ candidate, label, definitions, kindLabel }: {
  candidate: IdentityCandidateEntity;
  label: string;
  definitions: DynamicDefinitions | null;
  kindLabel: (kind: string) => string;
}) {
  const { t, tr, locale } = useI18n();
  const scopes = [
    ["work_id", "work"], ["content_unit_id", "content_unit"],
    ["release_id", "release"], ["medium_id", "medium"], ["parent_id", candidate.kind],
  ] as const;
  return <div className="space-y-2 min-w-0">
    <h3 className="text-sm font-semibold">{label}</h3>
    <a href={`/catalog/${candidate.id}`} target="_blank" rel="noopener noreferrer" className="text-primary hover:underline break-words">
      {candidate.title}
    </a>
    <p className="text-xs text-text-muted break-all">
      {kindLabel(candidate.kind)} · {tr(`catalog.state.${candidate.state}`, candidate.state)} · {candidate.id}
    </p>
    <dl className="space-y-1 text-sm">
      {Object.entries(candidate.translations || {}).map(([language, translation]) =>
        <div key={language} className="flex flex-wrap gap-x-2">
          <dt className="text-text-muted">{language}</dt>
          <dd className="break-words">{[translation.title, ...(translation.aliases || [])].filter(Boolean).join(" · ")}</dd>
        </div>)}
      {scopes.map(([field, kind]) => {
        const id = candidate[field];
        if (!id) return null;
        const names = definitions?.structure?.[candidate.kind]?.fields?.find((item) => item.code === field)?.names;
        return <div key={field} className="flex flex-wrap gap-x-2">
          <dt className="text-text-muted">{resolveLocalizedName(names, locale, kindLabel(kind))}</dt>
          <dd><a href={`/catalog/${id}`} target="_blank" rel="noopener noreferrer" className="text-primary hover:underline break-all">{id}</a></dd>
        </div>;
      })}
    </dl>
    {(candidate.external_links || []).length > 0 && <div>
      <p className="text-xs text-text-muted">{t("catalog.externalIds")}</p>
      <dl className="space-y-1 text-sm">
        {(candidate.external_links || []).map((link) => <div key={`${link.kind}:${link.url}`} className="flex flex-wrap gap-x-2">
          <span className="font-mono text-xs text-text-muted">{link.kind}</span>
          <span className="font-mono text-xs">{link.external_id || link.url}</span>
        </div>)}
      </dl>
    </div>}
    {Object.keys(candidate.attributes || {}).length > 0 && <dl className="space-y-1 text-sm">
      {Object.entries(candidate.attributes || {}).filter(([code]) => !definitions?.fields?.[code]?.hidden).map(([code, value]) =>
        <div key={code} className="flex flex-wrap gap-x-2">
          <dt className="text-text-muted">{resolveLocalizedName(definitions?.fields?.[code]?.names, locale, code)}</dt>
          <dd><FieldValue code={code} value={value} defs={definitions} locale={locale} /></dd>
        </div>)}
    </dl>}
  </div>;
}

export function IdentityReviewPanel({ review, busy, definitions, kindLabel, onConfirm, onReload }: {
  review: IdentityReviewState;
  busy: boolean;
  definitions: DynamicDefinitions | null;
  kindLabel: (kind: string) => string;
  onConfirm: (confirmed: boolean) => void;
  onReload: () => void;
}) {
  const { t } = useI18n();
  const changed = review.data && review.expectedIDs !== null &&
    JSON.stringify(review.expectedIDs) !== JSON.stringify(review.data.candidateIDs);
  const candidates = new Map<string, { canonical: IdentityCandidateEntity; aliases: IdentityCandidateEntity[] }>();
  for (const { matched, canonical } of review.data?.items || []) {
    const group = candidates.get(canonical.id) || { canonical, aliases: [] };
    if (matched.id !== canonical.id) group.aliases.push(matched);
    candidates.set(canonical.id, group);
  }
  return <fieldset className="border border-amber-500/40 rounded-lg p-4 space-y-3">
    <legend>{t("catalog.identityReview.title")}</legend>
    <p className="cv-hint">{t("catalog.identityReview.hint")}</p>
    {review.status === "loading" && <p role="status">{t("catalog.identityReview.loading")}</p>}
    {review.status === "failed" && <p className="cv-error" role="alert">{review.error || t("catalog.identityReview.failed")}</p>}
    {changed && <p role="status" className="cv-hint">{t("catalog.identityReview.changed")}</p>}
    {review.status === "ready" && review.data?.items.length === 0 && <p>{t("catalog.identityReview.empty")}</p>}
    {Array.from(candidates.values()).map(({ aliases, canonical }) => <div key={canonical.id} className="cv-group">
      {aliases.map((matched) => <CandidateSummary key={matched.id} candidate={matched} label={t("catalog.identityReview.matched")} definitions={definitions} kindLabel={kindLabel} />)}
      <CandidateSummary candidate={canonical} label={t("catalog.identityReview.canonical")} definitions={definitions} kindLabel={kindLabel} />
    </div>)}
    <label className="flex items-start gap-2 text-sm">
      <input type="checkbox" checked={review.confirmed} disabled={busy || review.status !== "ready" || !review.data}
        onChange={(event) => onConfirm(event.target.checked)} />
      <span>{t("catalog.identityReview.confirm")}</span>
    </label>
    <button type="button" disabled={busy || review.status === "loading"} onClick={onReload}>{t("catalog.identityReview.reload")}</button>
  </fieldset>;
}
