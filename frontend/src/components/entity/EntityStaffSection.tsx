"use client";

// 通用署名区：从统一关系投影构造参与主体与角色。
// StaffCharacterSection。各实体详情页（作品/发行版/载体/通用详情）统一用它，
// 不再各写一套演职员展示。无署名关系时渲染空。

import { useEffect, useMemo, useState } from "react";
import { fetchEntityRelationships } from "@/lib/catalogRelationships";
import { useDefinitions } from "@/lib/definitions";
import { useI18n } from "@/i18n/I18nProvider";
import type { Entity } from "@/components/catalog/api";
import { StaffCharacterSection } from "./StaffCharacterSection";
import { buildStaffCredits, type RelationLike } from "./staffCredits";

export function EntityStaffSection({
  entityId,
  onCount,
}: {
  entityId: string;
  /** 条目数回告：包卡片/页签的页面据此决定显隐（空时不留空卡）。 */
  onCount?: (n: number) => void;
}) {
  const { tr, locale } = useI18n();
  const { definitions: defs } = useDefinitions();
  const [relations, setRelations] = useState<RelationLike[]>([]);
  const [relEntities, setRelEntities] = useState<Record<string, Entity>>({});

  useEffect(() => {
    let alive = true;
    fetchEntityRelationships(entityId)
      .then((r) => {
        if (!alive) return;
        setRelations(Array.isArray(r.items) ? r.items : []);
        setRelEntities(r.entities);
      })
      .catch(() => {
        if (!alive) return;
        setRelations([]);
        setRelEntities({});
      });
    return () => {
      alive = false;
    };
  }, [entityId]);

  const credits = useMemo(
    () => buildStaffCredits({ entityId, relations, relEntities, defs, locale, tr }),
    [entityId, relations, relEntities, defs, locale, tr],
  );
  useEffect(() => {
    onCount?.(credits.length);
  }, [credits.length, onCount]);
  if (credits.length === 0) return null;
  return <StaffCharacterSection credits={credits} />;
}
