"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { Network } from "lucide-react";
import { type Entity, type Relation, title } from "@/components/catalog/api";
import { Card } from "@/components/ui/Card";
import { SectionTitle } from "@/components/ui/SectionTitle";
import { useI18n } from "@/i18n/I18nProvider";
import { useTitleDisplayOrder } from "@/hooks/useTitleDisplayOrder";
import { getRelationName, getTermName, useDefinitions } from "@/lib/definitions";
import { relationDirectoryRows, type RelationDirectoryRow } from "@/lib/relationDirectory";
import { readerCreditRole } from "@/lib/readerNotes";
import { useAuth } from "@/lib/authContext";
import { can, CATALOG_ENTITY_EDIT } from "@/lib/permissions";
import { useRelationFilter } from "./RelationFilterBar";
import { attrText, isCreditRelation } from "./staffCredits";

const MAX_CHIPS_PER_ROW = 8;
const INITIAL_GROUP_LIMIT = 10;

export function EntityRelationOverview({
  subjectId,
  relations,
  entities,
  groupOrder,
  truncated = false,
  onOpenGraph,
  onOpenType,
}: {
  subjectId: string;
  relations: Omit<Relation, "version">[];
  entities: Record<string, Entity>;
  groupOrder?: string[];
  truncated?: boolean;
  onOpenGraph?: () => void;
  onOpenType?: (type: string) => void;
}) {
  const { t, locale } = useI18n();
  const { definitions: defs, relationshipRules } = useDefinitions();
  const titleOrder = useTitleDisplayOrder();
  const { user } = useAuth();
  const allowOps = can(user, CATALOG_ENTITY_EDIT);
  const [showAllGroups, setShowAllGroups] = useState(false);
  const [expandedRows, setExpandedRows] = useState<Set<string>>(() => new Set());

  const nameOf = (entity: Entity | undefined, id: string) =>
    entity ? title(entity, locale, titleOrder) || entity.title || id.slice(0, 8) : id.slice(0, 8);

  const labeledRows = useMemo(
    () =>
      relationDirectoryRows(subjectId, relations, entities).map((row) => ({
        ...row,
        label:
          row.direction === "incoming" && row.type.startsWith("attribute:")
            ? t("relations.directory.referencedAs", {
                field: getRelationName(defs, row.type, true, locale, relationshipRules),
              })
            : getRelationName(defs, row.type, row.direction !== "incoming", locale, relationshipRules),
      })),
    [subjectId, relations, entities, defs, locale, relationshipRules, t],
  );

  const filter = useRelationFilter(labeledRows, groupOrder);

  // 按（分组顺序 → 关系码 → 方向谓词名）归并，非署名结构/创作关联优先于演职员长表（当模板未显式指定顺序时）。
  const predicateGroups = useMemo(() => {
    const raw: { key: string; type: string; label: string; rows: typeof labeledRows; isCredit: boolean }[] = [];
    for (const section of filter.sections) {
      for (const typeBucket of section.types) {
        const byLabel = new Map<string, typeof labeledRows>();
        for (const row of typeBucket.rows) {
          const list = byLabel.get(row.label) || [];
          list.push(row);
          byLabel.set(row.label, list);
        }
        byLabel.forEach((bucketRows, label) => {
          raw.push({
            key: `${typeBucket.type}:${label}`,
            type: typeBucket.type,
            label,
            rows: bucketRows,
            isCredit: isCreditRelation(defs, typeBucket.type),
          });
        });
      }
    }
    if (!groupOrder || groupOrder.length === 0) {
      raw.sort((a, b) => Number(a.isCredit) - Number(b.isCredit));
    }
    return raw;
  }, [filter.sections, defs, groupOrder]);

  const inlineHint = (row: RelationDirectoryRow, predicateLabel: string): string => {
    const parts: string[] = [];
    if (row.direction === "reference" && row.records[0]?.target_id) {
      parts.push(`→ ${nameOf(row.target, row.records[0].target_id)}`);
    }
    for (const rec of row.records) {
      const attrs = rec.attributes || {};
      const rawRole = attrText(attrs.credit_role);
      if (rawRole) {
        const cleanRole = readerCreditRole(rawRole, predicateLabel, "", { allowOps });
        if (cleanRole && cleanRole !== predicateLabel && !parts.includes(cleanRole)) parts.push(cleanRole);
      }
      const chId = attrText(attrs.character);
      if (chId && chId !== row.entityId) {
        const chName = nameOf(entities[chId], chId);
        if (chName && !parts.includes(chName)) parts.push(chName);
      }
      const rank = attrText(attrs.character_rank);
      if (rank && !chId) {
        const rankLabel = getTermName(defs, "character_rank", rank, locale);
        if (rankLabel && rankLabel !== rank && !parts.includes(rankLabel)) parts.push(rankLabel);
      }
    }
    return parts.slice(0, 2).join(" · ");
  };

  if (predicateGroups.length === 0) return null;

  const visibleGroups = showAllGroups ? predicateGroups : predicateGroups.slice(0, INITIAL_GROUP_LIMIT);
  const hiddenGroupCount = predicateGroups.length - visibleGroups.length;
  const totalCountText = truncated ? `${relations.length}+` : String(relations.length);

  return (
    <Card padding="section" className="space-y-3 shadow-soft">
      <SectionTitle
        icon={<Network className="w-4 h-4 text-primary" strokeWidth={1.5} />}
        actions={
          onOpenGraph ? (
            <button
              type="button"
              onClick={onOpenGraph}
              className="mf-focus inline-flex items-center gap-1.5 px-2.5 py-1 rounded-control border border-line bg-surfaceSubtle hover:border-primary/40 hover:text-primary text-xs font-medium text-text-body transition-colors cursor-pointer"
            >
              <Network className="w-3.5 h-3.5 text-primary" strokeWidth={1.6} />
              <span>{t("entity.page.viewGraph")}</span>
            </button>
          ) : undefined
        }
      >
        {t("entity.page.relationsOverview")}
        <span className="ml-1.5 px-2 py-0.5 rounded-chip bg-primary/10 text-primary font-mono text-[11px] font-semibold">
          {totalCountText}
        </span>
      </SectionTitle>

      <div className="divide-y divide-line-subtle border-t border-line-subtle text-xs">
        {visibleGroups.map((group) => {
          const isRowExpanded = expandedRows.has(group.key);
          const shownRows = isRowExpanded ? group.rows : group.rows.slice(0, MAX_CHIPS_PER_ROW);
          const restCount = group.rows.length - shownRows.length;

          return (
            <div key={group.key} className="py-2 flex flex-col sm:flex-row sm:items-baseline gap-1.5 sm:gap-3">
              <div className="sm:w-32 md:w-36 shrink-0 flex items-baseline justify-between gap-1 text-text-muted font-medium">
                <span className="break-words">{group.label}</span>
                {group.rows.length > 1 && (
                  <span className="font-mono text-[10px] text-text-faint tabular-nums">{group.rows.length}</span>
                )}
              </div>
              <div className="min-w-0 flex-1 flex flex-wrap items-center gap-1.5">
                {shownRows.map((row) => {
                  const itemLabel = nameOf(row.entity, row.entityId);
                  const note = inlineHint(row, group.label);
                  return (
                    <Link
                      key={row.key}
                      href={`/catalog/${row.entityId}`}
                      className="inline-flex items-baseline gap-1 max-w-full px-2 py-0.5 rounded border border-line-subtle bg-surfaceSubtle hover:border-primary/45 hover:text-primary transition-colors"
                    >
                      <span className="font-medium text-text-strong hover:text-primary truncate max-w-[16rem]">
                        {itemLabel}
                      </span>
                      {note && (
                        <span className="text-[11px] text-text-muted truncate max-w-[10rem]">
                          ({note})
                        </span>
                      )}
                    </Link>
                  );
                })}
                {restCount > 0 && (
                  <button
                    type="button"
                    onClick={() => {
                      if (onOpenType) {
                        onOpenType(group.type);
                      } else {
                        setExpandedRows((prev) => {
                          const next = new Set(prev);
                          next.add(group.key);
                          return next;
                        });
                      }
                    }}
                    className="px-2 py-0.5 rounded border border-dashed border-line text-[11px] font-mono text-primary hover:bg-primary/10 transition-colors cursor-pointer"
                  >
                    +{restCount}
                  </button>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {hiddenGroupCount > 0 && (
        <button
          type="button"
          onClick={() => setShowAllGroups(true)}
          className="text-xs font-medium text-primary hover:underline cursor-pointer"
        >
          {t("relations.directory.more", { count: hiddenGroupCount })}
        </button>
      )}
    </Card>
  );
}
