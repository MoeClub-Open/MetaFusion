"use client";

import { useId, useMemo, useState } from "react";
import Link from "next/link";
import { Search } from "lucide-react";
import { type Entity, type Relation, title } from "@/components/catalog/api";
import { EntityCover } from "@/components/common/EntityCover";
import { FieldValue } from "@/components/catalog/TemplateAttributeSections";
import { useI18n } from "@/i18n/I18nProvider";
import { useTitleDisplayOrder } from "@/hooks/useTitleDisplayOrder";
import { getFieldName, getKindName, getRelationName, useDefinitions } from "@/lib/definitions";
import { coverUrl } from "@/lib/cover";
import { isDistinctOriginalTitle } from "@/lib/titles";
import { ENTITY_KINDS } from "@/lib/kinds.generated";
import { relationDirectoryRows, relationDirectorySearch, type RelationDirectoryRow } from "@/lib/relationDirectory";
import { RelationFilterBar, useRelationFilter } from "./RelationFilterBar";

export function EntityRelationDirectory({ subjectId, relations, entities, groupOrder, summary = false }: {
  subjectId: string; relations: Omit<Relation, "version">[]; entities: Record<string, Entity>; groupOrder?: string[]; summary?: boolean;
}) {
  const { t, tr, locale } = useI18n();
  const { definitions: defs, kinds, relationshipRules } = useDefinitions();
  const titleOrder = useTitleDisplayOrder();
  const searchId = useId();
  const [search, setSearch] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const rows = useMemo(() => relationDirectoryRows(subjectId, relations, entities), [subjectId, relations, entities]);
  const filterRows = useMemo(() => rows.filter((row) => relationDirectorySearch(row, search)).map((row) => ({
    ...row, label: row.direction === "incoming" && row.type.startsWith("attribute:")
      ? t("relations.directory.referencedAs", { field: getRelationName(defs, row.type, true, locale, relationshipRules) })
      : getRelationName(defs, row.type, row.direction !== "incoming", locale, relationshipRules),
  })), [rows, search, defs, locale, relationshipRules, t]);
  const filter = useRelationFilter(filterRows, groupOrder);
  const name = (entity: Entity | undefined, id: string) => entity ? title(entity, locale, titleOrder) || entity.title || id : id;
  const count = filter.visible.reduce((sum, row) => sum + row.records.length, 0);

  const qualifiers = (row: RelationDirectoryRow) => row.records.map((record, index) => {
    const fields = Object.entries(record.attributes || {}).filter(([code, value]) =>
      value !== undefined && value !== null && value !== "" && !(Array.isArray(value) && !value.length) && !defs?.fields?.[code]?.hidden);
    if (!fields.length) return null;
    return <dl key={record.id || index} className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-text-body">
      {fields.map(([code, value]) => <div key={code} className="min-w-0 flex flex-wrap gap-x-1">
        <dt className="text-text-muted">{getFieldName(defs, code, locale)}</dt>
        <dd className="m-0 min-w-0 break-words"><FieldValue code={code} defs={defs} locale={locale}
          value={defs?.fields?.[code]?.type === "entity" && typeof value === "string" && entities[value]
            ? { id: value, title: name(entities[value], value) } : value} /></dd>
      </div>)}
    </dl>;
  });

  return <div className="min-w-0 space-y-5">
    {!summary && <div className="space-y-3">
      <label htmlFor={searchId} className="flex items-center gap-2 rounded-lg border border-line bg-surfaceSubtle px-3 py-2">
        <Search className="h-4 w-4 shrink-0 text-text-muted" aria-hidden="true" />
        <span className="sr-only">{t("relations.directory.search")}</span>
        <input id={searchId} type="search" value={search} onChange={(event) => setSearch(event.target.value)}
          placeholder={t("relations.directory.search")} className="w-full min-w-0 !border-0 !bg-transparent !p-0 text-sm outline-none" />
      </label>
      <RelationFilterBar facets={filter.facets} selection={filter.selection} onToggle={filter.toggle} />
      <p className="text-xs text-text-muted" role="status">{t("relations.directory.count", { count: filter.visible.length, records: count })}</p>
    </div>}
    {!filter.visible.length && <p className="text-sm text-text-muted">{t("relations.filterEmpty")}</p>}
    {filter.sections.map((section) => <section key={section.key} className="space-y-4">
      <h3 className="border-b border-line pb-2 text-base font-semibold text-text-strong">{section.label}</h3>
      {section.types.map((type) => {
        const byKind = new Map<string, typeof type.rows>();
        for (const row of type.rows) byKind.set(row.kind, [...(byKind.get(row.kind) || []), row]);
        const kindOrder = (kind: string) => { const index = (ENTITY_KINDS as readonly string[]).indexOf(kind); return index < 0 ? ENTITY_KINDS.length : index; };
        return Array.from(byKind).sort(([left], [right]) => kindOrder(left) - kindOrder(right)).map(([kind, entries]) => {
          const portraits = kind === "agent" && entries.every((row) => row.direction !== "reference");
          const kindName = kind ? getKindName(kinds, kind, locale, tr(`catalog.kind.${kind}`, kind)) : t("relations.directory.unknown");
          const bucketKey = `${type.type}:${kind}`;
          const limited = !summary && !search.trim() && !expanded.has(bucketKey);
          const shown = limited ? entries.slice(0, 12) : entries;
          return <div key={bucketKey} className="space-y-3">
            <h4 className="flex flex-wrap items-center gap-2 text-sm font-medium text-text-body">
              <span>{type.label}</span>{!portraits && <span className="rounded bg-surfaceSubtle px-2 py-0.5 text-xs text-text-muted">{kindName}</span>}
              <span className="text-xs tabular-nums text-text-muted">{entries.length}</span>
            </h4>
            <ul className={portraits ? "grid grid-cols-2 gap-3 sm:grid-cols-3 xl:grid-cols-5" : "divide-y divide-line-subtle rounded-lg border border-line-subtle"}>
              {shown.map((row) => {
                const label = name(row.entity, row.entityId);
                return <li key={row.key} className={portraits ? "min-w-0 overflow-hidden rounded-xl border border-line bg-surfaceSubtle" : "grid grid-cols-[3rem_minmax(0,1fr)] items-start gap-3 p-3 sm:grid-cols-[3rem_minmax(0,1fr)_minmax(0,.85fr)]"}>
                  <Link href={`/catalog/${row.entityId}`} tabIndex={-1} aria-hidden="true"
                    className={portraits ? "block aspect-[3/4] overflow-hidden border-b border-line-subtle bg-background/40 p-2" : "block aspect-[3/4] overflow-hidden rounded bg-surfaceSubtle"}>
                    <EntityCover key={row.entityId} src={coverUrl(row.entity)} title={label} id={row.entityId} alt="" compact
                      imgClassName="h-full w-full object-contain" className="h-full w-full" />
                  </Link>
                  <div className={portraits ? "space-y-1 p-3" : "min-w-0 space-y-1"}>
                    <Link href={`/catalog/${row.entityId}`} className="block break-words text-sm font-semibold leading-relaxed text-text-strong hover:text-primary">{label}</Link>
                    {row.label !== type.label && <p className="text-xs text-primary">{row.label}</p>}
                    {row.entity && isDistinctOriginalTitle(row.entity.title, label) && <p className="break-words text-xs text-text-muted">{row.entity.title}</p>}
                    {row.direction === "reference" && <p className="text-xs text-text-muted">{type.label} → <Link className="text-primary hover:underline" href={`/catalog/${row.records[0].target_id}`}>{name(row.target, row.records[0].target_id)}</Link></p>}
                    {portraits && <div className="space-y-2 pt-1">{qualifiers(row)}</div>}
                  </div>
                  {!portraits && <div className="col-start-2 space-y-2 sm:col-start-auto">{qualifiers(row)}</div>}
                </li>;
              })}
            </ul>
            {!summary && !search.trim() && entries.length > 12 && <button type="button" className="rounded-lg border border-line px-3 py-2 text-xs text-primary hover:bg-surfaceSubtle"
              aria-expanded={!limited} onClick={() => setExpanded((previous) => { const next = new Set(previous); if (next.has(bucketKey)) next.delete(bucketKey); else next.add(bucketKey); return next; })}>
              {t(limited ? "relations.directory.more" : "relations.directory.less", { count: entries.length - shown.length })}
            </button>}
          </div>;
        });
      })}
    </section>)}
  </div>;
}
