"use client";

import type { ReactNode } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { DynamicDefinitions, getKindName, KindMap, resolveLocalizedName, useDefinitions } from "@/lib/definitions";
import { languageLabel } from "@/lib/languages";
import { safeExternalAuthorityURL } from "@/lib/externalAuthorityURL";
import { CatalogArticle } from "./CatalogArticle";
import { FieldValue } from "./TemplateAttributeSections";

function record(value: unknown): Record<string, any> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, any> : undefined;
}

function entries(value: unknown) {
  return Object.entries(record(value) || {}).filter(([, item]) => item !== undefined && item !== null && item !== "");
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return <section className="min-w-0 space-y-3 border-t border-line-subtle pt-4">
    <h4 className="text-sm font-semibold text-text-strong">{title}</h4>
    {children}
  </section>;
}

// 只读取传入的历史文档。定义只负责字段名称/取值格式，不补取当前条目、关系或 AI 正文。
export function RevisionSnapshotContent({ snapshot, definitions, kinds = {} }: {
  snapshot: unknown;
  definitions?: DynamicDefinitions | null;
  kinds?: KindMap;
}) {
  const { t, tr, locale } = useI18n();
  const entity = record(snapshot);
  if (!entity || typeof entity.kind !== "string" || typeof entity.title !== "string") {
    return <p className="text-sm text-text-muted">{t("revisions.snapshotUnavailable")}</p>;
  }

  const kindLabel = (code: string) => getKindName(kinds, code, locale, tr(`catalog.kind.${code}`, code));
  const fieldValue = (code: string, value: unknown, field?: any) => <FieldValue code={code} value={value}
    defs={definitions} locale={locale} field={field} resolveReferences={false} includeHidden />;
  const reference = (value: unknown) => fieldValue("", value, { type: "entity" });
  const attributes = entries(entity.attributes);
  const translations = entries(entity.translations).filter(([, row]) => record(row));
  const pictures = Array.isArray(entity.pictures) ? entity.pictures.filter(record) : [];
  const links = Array.isArray(entity.external_links) ? entity.external_links.filter(record) : [];
  const subjects = Array.isArray(entity.subjects) ? entity.subjects.filter(record) : [];
  const contents = Array.isArray(entity.contents) ? entity.contents.filter(record) : [];
  const structureFields = definitions?.structure?.[entity.kind]?.fields || [];
  const referenceLabels: Record<string, string> = {
    work_id: kindLabel("work"), content_unit_id: kindLabel("content_unit"), release_id: kindLabel("release"),
    medium_id: kindLabel("medium"), parent_id: t("catalog.parent"), redirect_id: t("revisions.snapshotRedirect"),
  };
  const structuralRefs = Object.keys(referenceLabels).filter((code) => entity[code]);
  const details = (items: Array<[string, ReactNode]>) => <dl className="grid gap-3 sm:grid-cols-2">
    {items.map(([label, value], i) => <div key={i} className="min-w-0 space-y-1">
      <dt className="text-xs text-text-muted">{label}</dt>
      <dd className="break-words text-sm text-text-body">{value}</dd>
    </div>)}
  </dl>;

  return <div className="min-w-0 space-y-5">
    <header className="space-y-3">
      <h4 className="break-words text-xl font-semibold text-text-strong">{entity.title}</h4>
      {details([
        [t("catalog.kindLabel"), kindLabel(entity.kind)],
        ...(entity.original_language ? [[t("catalog.originalLanguage"), languageLabel(entity.original_language)] as [string, ReactNode]] : []),
        ...(entity.state ? [[t("revisions.fieldState"), entity.state === "active" ? t("admin.entities.stateActive") : tr(`catalog.state.${entity.state}`, entity.state)] as [string, ReactNode]] : []),
      ])}
    </header>

    {translations.length > 0 && <Section title={t("revisions.snapshotTranslations")}>
      <div className="space-y-3">{translations.map(([lang, row]) => <details key={lang} open className="rounded-lg border border-line bg-surface p-3 sm:p-4">
        <summary className="cursor-pointer text-sm font-medium text-text-strong">{languageLabel(lang)}</summary>
        <div lang={lang} className="mt-3 min-w-0 space-y-3">
          {row.title && <h5 className="break-words text-base font-semibold text-text-strong">{row.title}</h5>}
          {Array.isArray(row.aliases) && row.aliases.length > 0 && <div className="text-sm text-text-muted">
            <span>{t("revisions.snapshotAliases")}: </span>{row.aliases.join(" · ")}
          </div>}
          {row.summary && <p className="whitespace-pre-line text-sm leading-relaxed text-text-body">{row.summary}</p>}
          {row.body && <CatalogArticle content={row.body} lang={lang} idPrefix={`revision-${entity.version || 0}-${lang}`} />}
        </div>
      </details>)}</div>
    </Section>}

    {attributes.length > 0 && <Section title={t("catalog.attributes")}>
      {details(attributes.map(([code, value]) => [resolveLocalizedName(definitions?.fields?.[code]?.names, locale, code), fieldValue(code, value)]))}
    </Section>}

    {(structuralRefs.length > 0 || entity.number || entity.position || subjects.length > 0 || contents.length > 0) && <Section title={t("catalog.structure")}>
      {details([
        ...structuralRefs.map((code): [string, ReactNode] => [resolveLocalizedName(structureFields.find((field) => field.code === code)?.names, locale, referenceLabels[code]), reference(entity[code])]),
        ...(entity.number ? [[t("catalog.number"), String(entity.number)] as [string, ReactNode]] : []),
        ...(entity.position ? [[t("catalog.position"), String(entity.position)] as [string, ReactNode]] : []),
      ])}
      {subjects.length > 0 && <div className="space-y-2">
        <h5 className="text-xs font-medium text-text-muted">{t("catalog.subjects")}</h5>
        <ol className="space-y-2">{subjects.map((item, index) => <li key={index} className="space-y-2 rounded-lg border border-line-subtle p-3">
          {details([
            [kindLabel("work"), reference(item.work_id)],
            ...(item.role ? [[t("catalog.subjectRole"), fieldValue("", item.role, { type: "enum", vocabulary: "release_role" })] as [string, ReactNode]] : []),
            [t("catalog.position"), item.position ?? "—"],
          ])}
          {entries(item.attributes).length > 0 && fieldValue("subject_attributes", item.attributes)}
        </li>)}</ol>
      </div>}
      {contents.length > 0 && <div className="space-y-2">
        <h5 className="text-xs font-medium text-text-muted">{t("catalog.contents")}</h5>
        <ol className="space-y-2">{contents.map((item, index) => <li key={index} className="space-y-2 rounded-lg border border-line-subtle p-3">
          {details([[kindLabel("expression"), reference(item.expression_id)], [t("catalog.position"), item.position ?? "—"]])}
          {entries(item.locator).length > 0 && fieldValue("locator", item.locator)}
          {entries(item.attributes).length > 0 && fieldValue("inclusion_attributes", item.attributes)}
          {Array.isArray(item.sources) && item.sources.length > 0 && <ul className="space-y-1 text-xs text-text-muted">
            {item.sources.filter(record).map((source: any, index: number) => <li key={index}>{source.citation || source.url || source.kind}</li>)}
          </ul>}
        </li>)}</ol>
      </div>}
    </Section>}

    {pictures.length > 0 && <Section title={t("revisions.fieldPictures")}>
      <div className="flex flex-wrap items-start gap-4">{pictures.map((picture, index) => {
        const url = typeof picture.url === "string" ? picture.url : "";
        const src = safeExternalAuthorityURL(url) || (/^\/(?!\/)/.test(url) ? url : "");
        return <figure key={index} className="max-w-full space-y-2">
          {src && <a href={src} target="_blank" rel="noopener noreferrer"><img src={src} alt={resolveLocalizedName(picture.caption, locale, "")} loading="lazy" className="h-auto max-h-64 w-auto max-w-full rounded-lg" /></a>}
          <figcaption className="max-w-xs space-y-1 break-words text-xs text-text-muted">
            {picture.caption && <div>{fieldValue("", picture.caption, { type: "multilingual" })}</div>}
            {picture.taken_at && <div>{picture.taken_at}</div>}
            {picture.version_label && <div>{fieldValue("", picture.version_label, { type: "multilingual" })}</div>}
            {picture.usage_period && <div>{[picture.usage_period.begin, picture.usage_period.end].filter(Boolean).join(" – ")}</div>}
            {picture.role && <div>{fieldValue("", picture.role, { type: "enum", vocabulary: "picture_role" })}</div>}
            {picture.source?.citation && <div>{picture.source.citation}</div>}
          </figcaption>
        </figure>;
      })}</div>
    </Section>}

    {links.length > 0 && <Section title={t("catalog.externalLinks")}>
      <ul className="space-y-2 text-sm">{links.map((link, index) => {
        const url = typeof link.url === "string" ? safeExternalAuthorityURL(link.url) : "";
        return <li key={index} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 break-all">
          <span className="text-text-muted">{link.kind}</span>
          {url ? <a href={url} target="_blank" rel="noopener noreferrer" className="text-primary hover:underline">{link.external_id || link.url}</a>
            : <span className="text-text-body">{link.external_id || link.url}</span>}
          {link.role && <span className="text-xs text-text-muted">{tr(`authority.role.${link.role}`, link.role)}</span>}
        </li>;
      })}</ul>
    </Section>}
  </div>;
}

export function RevisionSnapshotView({ snapshot }: { snapshot: unknown }) {
  const { definitions, kinds } = useDefinitions();
  return <RevisionSnapshotContent snapshot={snapshot} definitions={definitions} kinds={kinds} />;
}
