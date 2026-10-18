"use client";

import { useId, type ReactNode } from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { getKindName, getRelationName, resolveLocalizedName, type DynamicDefinitions, type KindMap } from "@/lib/definitions";
import { languageLabel } from "@/lib/languages";
import { safeExternalAuthorityURL } from "@/lib/externalAuthorityURL";
import MarkdownRenderer from "@/components/MarkdownRenderer";
import { FieldValue } from "./TemplateAttributeSections";
import { revisionChanges } from "./revisionData";
import { diffText } from "./revisionDiff";
import { catalogPicturePreviewUrl, type CatalogAssetContext } from "@/lib/catalogAssetPreview";

const removedPanel = "bg-rose-500/[0.07] dark:bg-rose-500/10";
const addedPanel = "bg-emerald-500/[0.07] dark:bg-emerald-500/10";

function InlineChanges({ before, after, side }: { before: string; after: string; side: "old" | "new" }) {
  return <span className="whitespace-pre-wrap break-words">{diffText(before, after).map((part, index) => {
    if (side === "old" && part.kind === "add" || side === "new" && part.kind === "remove") return null;
    return part.kind === "remove" ? <del key={index} className="rounded-sm bg-rose-500/20 decoration-rose-700 dark:decoration-rose-300">{part.text}</del>
      : part.kind === "add" ? <ins key={index} className="rounded-sm bg-emerald-500/25 no-underline">{part.text}</ins>
        : <span key={index}>{part.text}</span>;
  })}</span>;
}

// 所有取值来自两份历史文档；definitions 只解释显示名与类型，不请求当前实体填补旧版本。
export function RevisionVisualDiff({ before, after, oldLabel, newLabel, definitions, kinds = {}, assetContext }: {
  before: unknown;
  after: unknown;
  oldLabel: string;
  newLabel: string;
  definitions?: DynamicDefinitions | null;
  kinds?: KindMap;
  assetContext?: CatalogAssetContext;
}) {
  const { t, tr, locale } = useI18n();
  const prefix = `revision-diff-${useId().replace(/:/g, "")}`;
  const changes = Object.entries(revisionChanges(before, after));
  const entityKind = (after as any)?.kind || (before as any)?.kind || "";
  const kindLabel = (code: string) => getKindName(kinds, code, locale, tr(`catalog.kind.${code}`, code));
  const knownLabels: Record<string, string> = {
    title: t("revisions.fieldTitle"), kind: t("revisions.fieldKind"), state: t("revisions.fieldState"),
    original_language: t("revisions.fieldOriginalLanguage"), pictures: t("revisions.fieldPictures"),
    contents: t("catalog.contents"), subjects: t("catalog.subjects"), external_links: t("catalog.externalLinks"),
    position: t("catalog.position"), number: t("catalog.number"), parent_id: t("catalog.parent"),
    work_id: kindLabel("work"), release_id: kindLabel("release"), medium_id: kindLabel("medium"),
    content_unit_id: kindLabel("content_unit"), expression_id: kindLabel("expression"), redirect_id: t("revisions.snapshotRedirect"),
    attributes: t("catalog.attributes"), locator: t("catalog.locator"), sources: t("catalog.evidence"),
    source: t("catalog.evidence"), role: t("catalog.subjectRole"), url: t("catalog.sourceUrl"), citation: t("catalog.citation"), type: t("catalog.relationType"),
  };
  const labelFor = (key: string) => {
    if (key.startsWith("attributes.")) {
      const code = key.slice(11);
      return resolveLocalizedName(definitions?.fields?.[code]?.names, locale, code);
    }
    if (key.startsWith("translations.")) {
      const [, lang, field] = key.split(".");
      const names: Record<string, string> = { title: t("catalog.title"), summary: t("catalog.summary"), body: t("catalog.article.body"), aliases: t("catalog.aliases") };
      return t("revisions.fieldTranslation", { lang: languageLabel(lang), field: names[field] || field });
    }
    const structural = definitions?.structure?.[entityKind]?.fields?.find((field) => field.code === key);
    return resolveLocalizedName(structural?.names || definitions?.fields?.[key]?.names, locale, knownLabels[key] || key);
  };
  const definition = (code: string, type: string, extra: Record<string, unknown> = {}) => ({ type, names: { [locale]: knownLabels[code] || code }, enabled: true, ...extra });
  const reference = (code: string) => definition(code, "entity");
  const fields = {
    subjects: definition("subjects", "list", { items: definition("", "group", { fields: {
      work_id: reference("work_id"), role: definition("role", "enum", { vocabulary: "release_role" }), position: definition("position", "number"),
      attributes: { ...definition("attributes", "group"), ...definitions?.fields?.subject_attributes },
    } }) }),
    contents: definition("contents", "list", { items: definition("", "group", { fields: {
      expression_id: reference("expression_id"), position: definition("position", "number"), locator: { ...definition("locator", "group"), ...definitions?.fields?.locator },
      attributes: { ...definition("attributes", "group"), ...definitions?.fields?.inclusion_attributes },
    } }) }),
    external_links: definition("external_links", "list", { items: definition("", "group", { fields: { url: definition("url", "url") } }) }),
  };
  const fieldValue = (code: string, value: unknown, field?: any): ReactNode => {
    const def = field || definitions?.fields?.[code];
    // 历史对比必须显示所有语种；按界面语言挑一项会把其它语种的修改隐藏。
    if (Array.isArray(value)) return value.length ? <ol className="space-y-2 pl-5 list-decimal">
      {value.map((item, index) => <li key={index} className="min-w-0">{fieldValue(code, item, def?.items || { type: "string" })}</li>)}
    </ol> : <span className="text-text-muted">{t("revisions.emptyValue")}</span>;
    if (value && typeof value === "object" && def?.type !== "entity") {
      const multilingual = def?.type === "multilingual";
      return <dl className="space-y-2">{Object.entries(value).map(([child, item]) => <div key={child} className="min-w-0">
        <dt className="text-xs text-text-muted">{multilingual ? languageLabel(child) : resolveLocalizedName(def?.fields?.[child]?.names, locale, knownLabels[child] || child)}</dt>
        <dd className="mt-0.5 min-w-0 break-words">{fieldValue(child, item, multilingual ? { type: "string" } : def?.fields?.[child])}</dd>
      </div>)}</dl>;
    }
    return <FieldValue code={code} value={value} field={def} defs={definitions} locale={locale} resolveReferences={false} includeHidden />;
  };

  const renderValue = (key: string, value: unknown, other: unknown, side: "old" | "new"): ReactNode => {
    if (value === undefined) return <span className="text-text-muted">{t("revisions.unsetValue")}</span>;
    if (value === null || value === "") return <span className="text-text-muted">{t("revisions.emptyValue")}</span>;
    if (key.startsWith("attributes.")) return fieldValue(key.slice(11), value);
    if (key.startsWith("translations.") && key.endsWith(".body") && typeof value === "string") {
      return <div lang={key.split(".")[1]}><MarkdownRenderer content={value} headingPrefix={`${prefix}-${side}-${key.replace(/[^\w-]/g, "-")}`}
        footnoteLabel={t("catalog.article.references")} footnoteBackLabel={t("catalog.article.backReference")} /></div>;
    }
    if (key === "kind") return kindLabel(String(value));
    if (key === "type" && typeof value === "string") return getRelationName(definitions, value, true, locale);
    if (key === "original_language") return languageLabel(String(value));
    if (key === "state") return value === "active" ? t("admin.entities.stateActive") : tr(`catalog.state.${value}`, String(value));
    if (key.endsWith("_id")) return fieldValue(key, value, reference(key));
    if (key === "pictures" && Array.isArray(value)) return value.length ? <div className="flex flex-wrap items-start gap-4">{value.map((picture, index) => {
      if (!picture || typeof picture !== "object" || Array.isArray(picture)) return <span key={index}>{fieldValue("", picture)}</span>;
      const url = catalogPicturePreviewUrl(picture, assetContext);
      const src = safeExternalAuthorityURL(url) || (/^\/(?!\/)/.test(url) ? url : "");
      return <figure key={index} className="min-w-0 max-w-full space-y-2">
        {src && <a href={src} target="_blank" rel="noopener noreferrer"><img src={src} alt={resolveLocalizedName(picture.caption, locale, "")}
          className="h-auto max-h-56 w-auto max-w-full rounded-lg" loading="lazy" /></a>}
        <figcaption className="max-w-sm break-words text-xs">{fieldValue("", picture, definition("", "group", { fields: {
          url: definition("url", "url"), caption: definition("caption", "multilingual"), version_label: definition("version_label", "multilingual"),
          role: definition("role", "enum", { vocabulary: "picture_role" }),
        } }))}</figcaption>
      </figure>;
    })}</div> : <span className="text-text-muted">{t("revisions.noPictures")}</span>;
    if (typeof value === "string" && (key === "title" || key === "number" || key.startsWith("translations."))) {
      return <InlineChanges before={side === "old" ? value : typeof other === "string" ? other : ""}
        after={side === "new" ? value : typeof other === "string" ? other : ""} side={side} />;
    }
    return fieldValue(key, value, fields[key as keyof typeof fields]);
  };

  if (!changes.length) return <p className="p-6 text-center text-sm text-text-muted">{t("revisions.noDiff")}</p>;
  return <div className="min-w-0 space-y-4">{changes.map(([key, change]) => {
    const type = change.old === undefined ? "added" : change.new === undefined ? "removed" : "modified";
    return <section key={key} className="min-w-0 overflow-hidden rounded-lg border border-line" data-field={key}>
      <header className="flex min-w-0 flex-wrap items-center justify-between gap-2 border-b border-line bg-surfaceSubtle px-3 py-2.5">
        <h4 className="min-w-0 break-words text-sm font-semibold text-text-strong">{labelFor(key)}</h4>
        <span className={`rounded px-2 py-0.5 text-xs ${type === "added" ? "bg-emerald-500/15 text-emerald-800 dark:text-emerald-200" : type === "removed" ? "bg-rose-500/15 text-rose-800 dark:text-rose-200" : "text-text-muted"}`}>
          {t(type === "added" ? "revisions.changeAdded" : type === "removed" ? "revisions.changeRemoved" : "revisions.changeModified")}
        </span>
      </header>
      <div className="grid min-w-0 grid-cols-1 divide-y divide-line md:grid-cols-2 md:divide-x md:divide-y-0">
        {([['old', change.old, change.new, oldLabel], ['new', change.new, change.old, newLabel]] as const).map(([side, value, other, label]) => <div key={side}
          className={`min-w-0 space-y-3 p-3 sm:p-4 ${side === "old" ? removedPanel : addedPanel}`}>
          <h5 className={`break-words text-xs font-medium ${side === "old" ? "text-rose-800 dark:text-rose-200" : "text-emerald-800 dark:text-emerald-200"}`}>
            <span aria-hidden="true" className="mr-2 font-mono">{side === "old" ? "−" : "+"}</span>{label}
          </h5>
          <div className="min-w-0 overflow-x-auto break-words text-sm leading-relaxed text-text-body [overflow-wrap:anywhere]">
            {renderValue(key, value, other, side)}
          </div>
        </div>)}
      </div>
    </section>;
  })}</div>;
}
