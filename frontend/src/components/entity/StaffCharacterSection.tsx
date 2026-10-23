"use client";

import { useId, useState, useMemo } from "react";
import Link from "next/link";
import { User } from "lucide-react";
import { EntityCover } from "@/components/common/EntityCover";
import { Select } from "@/components/ui/Select";
import { useI18n } from "@/i18n/I18nProvider";
import { relationParticipantSlot, useDefinitions, type ParticipantSlot } from "@/lib/definitions";
import type { StaffCredit } from "./staffCredits";

export type { StaffCredit } from "./staffCredits";

// 署名区块的数据契约：各实体详情页把 relations 解析成结构化条目（见 staffCredits），
// 不再做角色/声优字符串配对。哪些关系算"署名"、哪些算"角色"由关系定义自己声明
// （ParticipantSlot），代码不写死关系码、不写死职位文本、也不拿分组码当语义用。

/** 主角番位码：character_rank 词表用 main；早期字典键 era 用过 primary（同一含义）。 */
const MAIN_CHARACTER_RANKS = new Set(["main", "primary"]);
const COLLAPSED_COUNT = 24;

interface StaffCharacterSectionProps {
  credits: StaffCredit[];
}

/** 同一角色可有多条配音：不同演员、语言、适用篇目各自保留，不互相覆盖。 */
interface CharacterVoice {
  id: string;
  name: string;
  avatar_url?: string;
  role: string;
  /** 语言 / 适用篇目等上下文，仅在存在时展示，用于区分多版配音。 */
  context?: string;
}

/** 立绘、人物照都完整放进竖向画框，不把实体封面当成可裁切的头像。 */
function ParticipantPortrait({ name, src, compact = false }: { name: string; src?: string; compact?: boolean }) {
  return (
    <div className={`${compact ? "w-8 h-10" : "w-16 h-24 sm:w-20 sm:h-28"} shrink-0 rounded-md border border-line-subtle bg-surfaceSubtle overflow-hidden flex items-center justify-center`}>
      {src ? (
        <EntityCover key={src} src={src} alt={name} title={name} className="w-full h-full" imgClassName="w-full h-full object-contain" compact />
      ) : (
        <User className="w-6 h-6 text-text-muted" strokeWidth={1.5} />
      )}
    </div>
  );
}

function CharacterCard({ item }: { item: CharacterCardItem }) {
  const characterContent = (
    <>
      {item.character.id ? <Link href={`/catalog/${item.character.id}`} className="shrink-0" aria-label={item.character.name}><ParticipantPortrait name={item.character.name} src={item.character.avatar_url} /></Link> : <ParticipantPortrait name={item.character.name} src={item.character.avatar_url} />}
      <div className="min-w-0 flex-1 space-y-2">
        <div className="text-sm font-semibold text-text-strong break-words group-hover:text-primary transition-colors duration-fast ease-soft">
          {item.character.id ? <Link href={`/catalog/${item.character.id}`} className="hover:text-primary">{item.character.name}</Link> : item.character.name}
        </div>
        <span className={`inline-block mt-1 px-1.5 py-0.5 rounded text-[10px] font-mono ${MAIN_CHARACTER_RANKS.has(item.character.rankCode || "") ? "bg-amber-500/15 text-amber-700 dark:text-warn-soft font-medium" : "bg-black/[0.04] dark:bg-white/[0.06] text-text-faint"}`}>
          {item.character.roleBadge}
        </span>
        {item.voices.length > 0 && <div className="space-y-1.5">
          {item.voices.map((voice) => (
            <Link key={`${voice.id}-${voice.role}-${voice.context || ""}`} href={`/catalog/${voice.id}`} className="flex items-center gap-2 min-w-0 group/voice">
              <ParticipantPortrait name={voice.name} src={voice.avatar_url} compact />
              <div className="min-w-0">
                <div className="text-xs text-text-strong break-words group-hover/voice:text-primary">{voice.name}</div>
                <div className="text-[10px] text-text-muted break-words">{[voice.role, voice.context].filter(Boolean).join(" · ")}</div>
              </div>
            </Link>
          ))}
        </div>}
      </div>
    </>
  );
  return (
    <div className="min-w-0 p-2.5 rounded-card border border-line bg-background/80 hover:border-primary/40 transition-colors shadow-xs">
      <div className="flex items-start gap-2.5 min-w-0 group">{characterContent}</div>
    </div>
  );
}

interface CharacterCardItem {
  id: string;
  character: {
    id?: string;
    name: string;
    avatar_url?: string;
    roleBadge: string;
    /** 番位数据码（主角 = main）：高亮判定按码，不看本地化文案。 */
    rankCode?: string;
  };
  voices: CharacterVoice[];
}

/** 人员署名卡：完整图片、主体名、职位与上下文；全部与逐类筛选共用。 */
function StaffRow({ credit, role }: { credit: StaffCredit; role: string }) {
  const { t } = useI18n();
  return (
    <Link
      href={`/catalog/${credit.agent.id}`}
      className="flex items-start gap-2.5 min-w-0 p-2.5 rounded-card border border-line bg-background/80 hover:border-primary/40 hover:bg-background transition-colors group shadow-xs"
    >
      <ParticipantPortrait name={credit.agent.name} src={credit.agent.avatarUrl} compact />
      <div className="min-w-0 flex-1 py-1">
        <div className="text-sm font-medium text-text-strong break-words group-hover:text-primary transition-colors duration-fast ease-soft">
          {credit.agent.name}
        </div>
        <div className="mt-1 text-xs text-text-muted break-words">
          {role}
          {credit.character && credit.character.name !== credit.agent.name && (
            <span title={t("work.detail.relGroupCharacters")}> · {credit.character.name}</span>
          )}
        </div>
        {(credit.language || credit.contextLabel) && <div className="mt-1 text-[10px] text-text-muted break-words">{[credit.language, credit.contextLabel].filter(Boolean).join(" · ")}</div>}
      </div>
    </Link>
  );
}

export function StaffCharacterSection({ credits }: StaffCharacterSectionProps) {
  const { t } = useI18n();
  const { definitions: defs } = useDefinitions();
  const defaultRole = t("work.detail.staffDefaultRole");
  const characterFallback = t("work.detail.relGroupCharacters");
  const [activeTab, setActiveTab] = useState<string>("all");
  const [expandedTab, setExpandedTab] = useState<string | null>(null);
  const relationFilterId = useId();

  // 参与者槽位由关系定义声明（person/character/peer）。为什么不用"fields 里有没有 character"：
  // 29 个关系码共用同一份 fields（含 character），那个判定对每条关系都成立，
  // 于是 isCast 恒真、人员网格永不渲染、图标恒为麦克风。槽位是逐条关系声明的语义。
  const participantSlot = (code: string): ParticipantSlot | undefined => relationParticipantSlot(defs, code);
  // 署名主体由逐条关系声明决定，不按字段或展示分组猜测。
  const isCreditRelation = (code: string) => participantSlot(code) === "person";
  // 角色卡已经显示带 character 引用的主体；「全部」里的人员卡只显示其余署名，
  // 同一条关系不再同时以角色卡和人员行出现。逐类筛选仍保留原始关系行。
  const humanCredits = useMemo(
    () => credits.filter((c) => isCreditRelation(c.relationType) && !c.character),
    [credits, defs],
  );

  // 角色与声优双轨卡片：登场角色直接成卡，配音挂到其角色卡上。
  // 卡片按**角色 ID**归并（角色名会因语种/重名而误并），且配音保留为列表——
  // 同一角色在不同语言/篇目下由不同演员配音是常态，只留第一位会丢数据。
  const characterCards = useMemo(() => {
    const cardMap = new Map<string, CharacterCardItem>();
    for (const c of credits) {
      if (!c.character) continue;
      // 有角色实体 ID 用 ID 成卡；导入/手工数据缺 ID 时退回名称，避免全部并进一张空卡。
      const key = c.character.id || `name:${c.character.name}`;
      if (!key) continue;
      let card = cardMap.get(key);
      if (!card) {
        card = {
          id: c.id,
          character: {
            id: c.character.id,
            name: c.character.name,
            avatar_url: c.character.avatarUrl,
            roleBadge: characterFallback,
            rankCode: c.character.rankCode,
          },
          voices: [],
        };
        cardMap.set(key, card);
      }
      const selfIsCharacter = !!c.character && !!c.agent.id && c.agent.id === c.character.id;
      if (!selfIsCharacter) {
        const context = [c.language, c.contextLabel].filter(Boolean).join(" · ");
        const role = c.creditRole || c.relationLabel || defaultRole;
        // 同一演员、职位与上下文的重复关系只留一条；不同语言/篇目各自保留。
        const dup = card.voices.some(
          (v) => v.id === c.agent.id && v.role === role && (v.context || "") === context,
        );
        if (!dup) {
          card.voices.push({
            id: c.agent.id,
            name: c.agent.name,
            avatar_url: c.agent.avatarUrl,
            role,
            context: context || undefined,
          });
        }
      } else {
        if (!card.character.id && c.character.id) card.character.id = c.character.id;
        if (!card.character.avatar_url && c.character.avatarUrl) card.character.avatar_url = c.character.avatarUrl;
        // 角色自身的登场关系决定角色徽章；配音关系不得把「配音者」写到角色上。
        card.character.roleBadge = c.character.rankLabel || c.creditRole || c.relationLabel || characterFallback;
        if (c.character.rankCode) card.character.rankCode = c.character.rankCode;
      }
    }
    return Array.from(cardMap.values());
  }, [credits, characterFallback, defaultRole]);

  // 角色与配音只是「全部」里的合成展示，不另建 A+B 页签；关系筛选只列实际关系码。
  const relationFilters = useMemo(() => {
    const seen = new Map<string, { label: string; count: number }>();
    for (const c of credits) {
      const hit = seen.get(c.relationType);
      if (hit) hit.count += 1;
      else seen.set(c.relationType, { label: c.relationLabel || c.relationType, count: 1 });
    }
    const list: { key: string; label: string; count: number }[] = [];
    // Map 不用 for..of：tsconfig target 低，迭代器展开编译不过（与 ApiKeysPanel 同一坑）。
    seen.forEach((info, type) => {
      list.push({ key: `rel:${type}`, label: info.label, count: info.count });
    });
    return list;
  }, [credits]);

  const effectiveTab = relationFilters.some((tab) => tab.key === activeTab) ? activeTab : "all";
  const activeRelation = effectiveTab.startsWith("rel:") ? effectiveTab.slice(4) : "";
  const relationCredits = activeRelation ? credits.filter((c) => c.relationType === activeRelation) : [];

  // 格式化具体职务标签：来源职位文本优先，缺失回退关系本地化名。
  const formatRole = (c: StaffCredit) => c.creditRole || c.relationLabel || defaultRole;

  // 全部与逐类筛选共用一个网格和展开状态，不再让角色与其他人员各自滚动。
  const displayItems = effectiveTab === "all"
    ? [
        ...characterCards.map((item) => ({ kind: "character" as const, key: `character:${item.character.id || item.id}`, item })),
        ...humanCredits.map((credit) => ({ kind: "credit" as const, key: `credit:${credit.id}`, credit })),
      ]
    : relationCredits.map((credit) => ({ kind: "credit" as const, key: `credit:${credit.id}`, credit }));
  const expanded = expandedTab === effectiveTab;
  const visibleItems = expanded ? displayItems : displayItems.slice(0, COLLAPSED_COUNT);

  // 数据通常异步到达。所有 hooks 必须先执行，不能在它们之前对空列表 early return。
  if (credits.length === 0) return null;

  return (
    <div className="p-3 rounded-card border border-line bg-surfaceSubtle space-y-3 mt-2 animate-fade-in">
      <div className="flex items-center justify-between border-b border-line-subtle pb-2">
        <div className="flex items-center gap-1.5 text-xs font-mono flex-wrap">
          <button
            type="button"
            onClick={() => setActiveTab("all")}
            className={`px-3 py-1 rounded-control transition-all ${
              effectiveTab === "all"
                ? "bg-primary text-white font-medium shadow-xs"
                : "text-gray-600 dark:text-gray-400 hover:text-gray-900 dark:hover:text-white"
            }`}
          >
            {t("work.detail.tabAll")} ({credits.length})
          </button>
          {relationFilters.length > 0 && (
            <>
              <label className="sr-only" htmlFor={relationFilterId}>{t("work.detail.filterRelation")}</label>
              <Select
                id={relationFilterId}
                value={activeRelation}
                onChange={(value) => setActiveTab(value ? `rel:${value}` : "all")}
                fullWidth={false}
                className={`!h-8 max-w-[220px] text-xs font-mono ${activeRelation ? "border-primary text-primary bg-primary/10" : "bg-surface text-text-body"}`}
                options={[{ value: "", label: t("work.detail.filterRelation") }, ...relationFilters.map((filter) => ({ value: filter.key.slice(4), label: `${filter.label} (${filter.count})` }))]}
              />
            </>
          )}
        </div>
      </div>
      <div className="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-2 items-start">
        {visibleItems.map((entry) => entry.kind === "character" ? (
          <CharacterCard key={entry.key} item={entry.item} />
        ) : (
          <StaffRow key={entry.key} credit={entry.credit} role={formatRole(entry.credit)} />
        ))}
      </div>
      {displayItems.length > COLLAPSED_COUNT && (
        <button
          type="button"
          onClick={() => setExpandedTab(expanded ? null : effectiveTab)}
          aria-expanded={expanded}
          className="text-xs font-medium text-primary hover:underline underline-offset-2"
        >
          {expanded ? t("relations.collapse") : t("work.detail.showAllParticipants", { count: displayItems.length })}
        </button>
      )}
    </div>
  );
}
