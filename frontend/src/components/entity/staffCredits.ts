// 通用署名条目构造：任一实体详情页把统一关系读取的
// {items, entities} 喂进来，得到 StaffCharacterSection 消费的结构化条目。
//
// 仅依据 definitions 的 counts_as_credit + participant_slot 判定语义，方向由当前主体
// 在 source/target 哪一端决定；分组 group 只负责视觉归组。番位名读服务端词表。

import { getRelationName, getTermName, type DynamicDefinitions } from "@/lib/definitions";
import { coverUrl } from "@/lib/cover";
import type { Entity } from "@/components/catalog/api";

export interface StaffCreditAgent {
  id: string;
  name: string;
  avatarUrl?: string;
}

export interface StaffCredit {
  id: string;
  relationType: string;
  relationLabel: string;
  /** 来源声明的自由文本职位（如"摄影监督"），优先于关系名展示。 */
  creditRole?: string;
  agent: StaffCreditAgent;
  /** 配音关系指向的角色，或登场角色（character_in）自身。 */
  character?: { id?: string; name: string; avatarUrl?: string; rankLabel?: string; rankCode?: string };
  /** 配音语言（关系属性），同一角色的多版配音据此区分。 */
  language?: string;
  /** 适用篇目/版本（关系属性 context 的展示名），多版配音据此区分。 */
  contextLabel?: string;
}

export interface RelationLike {
  id: string;
  type: string;
  source_id: string;
  target_id: string;
  attributes?: Record<string, any>;
}

export function attrText(v: any): string {
  if (v === undefined || v === null) return "";
  if (typeof v === "string") return v.trim();
  return "";
}

export function isCreditRelation(defs: DynamicDefinitions | null | undefined, type: string): boolean {
  return defs?.relations?.[type]?.counts_as_credit === true;
}

/** 卡片呈现不了的署名边仍需列在署名页签，尤其是 Agent 端与属性引用边。 */
export function unrenderedCreditRelations<R extends { id?: string; type: string }>(
  relations: R[],
  defs: DynamicDefinitions | null | undefined,
  cardIds: ReadonlySet<string>,
): R[] {
  return relations.filter((r) => isCreditRelation(defs, r.type) && !cardIds.has(r.id || ""));
}

export function buildStaffCredits(args: {
  entityId: string;
  relations: RelationLike[];
  relEntities: Record<string, Entity>;
  defs: DynamicDefinitions | null | undefined;
  locale: string;
  tr: (key: string, fallback: string) => string;
}): StaffCredit[] {
  const { entityId, relations, relEntities, defs, locale, tr } = args;
  const out: StaffCredit[] = [];
  for (const r of relations) {
    const def = defs?.relations?.[r.type];
    if (!isCreditRelation(defs, r.type) || (def?.participant_slot !== "person" && def?.participant_slot !== "character")) continue;
    const outgoing = r.source_id === entityId;
    const incoming = r.target_id === entityId;
    if (!outgoing && !incoming) continue;
    const participant = relEntities[outgoing ? r.target_id : r.source_id];
    if (!participant || participant.kind !== "agent") continue;
    const credit: StaffCredit = {
      id: r.id,
      relationType: r.type,
      relationLabel: getRelationName(defs, r.type, outgoing, locale),
      creditRole: attrText(r.attributes?.credit_role) || undefined,
      agent: { id: participant.id!, name: participant.title || "", avatarUrl: coverUrl(participant) || undefined },
    };
    if (def.participant_slot === "character") {
      const rankCode = attrText(r.attributes?.character_rank);
      const rankTerm = rankCode ? getTermName(defs, "character_rank", rankCode, locale) : "";
      const rankLabel = !rankCode
        ? ""
        : rankTerm && rankTerm !== rankCode
          ? rankTerm
          : tr(`entity.characterRank.${rankCode}`, rankCode);
      credit.character = {
        id: participant.id!,
        name: participant.title || "",
        avatarUrl: coverUrl(participant) || undefined,
        rankLabel: rankLabel && rankLabel !== rankCode ? rankLabel : undefined,
        rankCode: rankCode || undefined,
      };
    } else {
      const chId = attrText(r.attributes?.character);
      const ch = chId ? relEntities[chId] : undefined;
      if (ch) credit.character = { id: ch.id, name: ch.title, avatarUrl: coverUrl(ch) || undefined };
      credit.language = attrText(r.attributes?.language) || undefined;
      const ctxId = attrText(r.attributes?.context);
      credit.contextLabel = (ctxId ? relEntities[ctxId]?.title : "") || undefined;
    }
    out.push(credit);
  }
  return out;
}
