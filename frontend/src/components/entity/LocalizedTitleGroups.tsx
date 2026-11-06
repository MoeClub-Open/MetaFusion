"use client";

import React from "react";
import { useI18n } from "@/i18n/I18nProvider";
import { ChevronDown, ChevronUp } from "lucide-react";
import {
  groupTitlesByLocale,
  titleLocaleLabelKey,
} from "@/lib/titles";
import { languageNativeName } from "@/lib/languages";

interface Props {
  /** 统一 DTO 的 translations：按 locale 分组的对象（{loc:{title,aliases}}）。 */
  translations?: Record<string, { title?: string; name?: string; nonofficial?: boolean; summary?: string; aliases?: string[]; nonofficial_aliases?: string[] }>;
  /** 实体内容语言（ISO 639-1），用于标记原始语言分组 */
  originalLanguage?: string | null;
  /** 主标题行已展示的标题：分组内重复时自动隐藏 */
  displayTitle?: string | null;
  /** 在其他位置单独展示的标题文本，参与逐值去重。 */
  extraKnown?: Array<string | null | undefined>;
  className?: string;
  itemClassName?: string;
}

/**
 * 多语言标题按语种分组展示：`中文：A / B`、`日本語（原始语言）：C`。
 * 分组按"原始语言优先"排序；默认只展开首组（原始语言标题始终可见），
 * 其余语种与并列别名收起，点击切换——避免详情页头部被长标题列表撑开。
 */
export function LocalizedTitleGroups({
  translations,
  originalLanguage,
  displayTitle,
  extraKnown,
  className,
  itemClassName,
}: Props) {
  const { t } = useI18n();
  const [expanded, setExpanded] = React.useState(false);
  // 实体级基础字段（title 等）与表格主标题一起参与去重：只比主标题本身、不吞并别名。
  const knownKey = [displayTitle, ...(extraKnown || [])].join("\u0000");
  const known = React.useMemo(
    () =>
      new Set(
        [displayTitle, ...(extraKnown || [])]
          .map((v) => (v ?? "").trim().toLocaleLowerCase())
          .filter(Boolean),
      ),
    // 依赖拼好的字符串而不是数组本身：调用方常在 render 里新建数组字面量。
    [knownKey],
  );
  const groups = React.useMemo(
    () =>
      groupTitlesByLocale(translations, originalLanguage).flatMap((group) => {
        // 只去掉别处已展示的标题值；整组过滤会连原语言标题一起隐藏。
        const officialCandidates = [...(group.nonofficial ? [] : [group.primary]), ...group.aliases]
          .map((value) => value.trim())
          .filter((value) => value && !known.has(value.toLocaleLowerCase()));
        const unofficialCandidates = [...(group.nonofficial ? [group.primary] : []), ...group.nonofficialAliases]
          .map((value) => value.trim())
          .filter((value) => value && !known.has(value.toLocaleLowerCase()));
        return officialCandidates.length || unofficialCandidates.length
          ? [{ ...group, official: officialCandidates, unofficial: unofficialCandidates }]
          : [];
      }),
    [translations, originalLanguage, known],
  );
  if (groups.length === 0) return null;
  const cls = itemClassName ?? "text-text-faint";
  const hiddenRows = groups.slice(1).length;
  const visibleGroups = expanded ? groups : groups.slice(0, 1);
  // 组内去重：数据里别名常含与主标题相同的值（同一分组内逐字重复只展示一次）。
  const joinUnique = (values: string[]) => {
    const seen = new Set<string>();
    return values
      .map((v) => (v ?? "").trim())
      .filter((v) => {
        if (!v) return false;
        const key = v.toLocaleLowerCase();
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      })
      .join(" / ");
  };
  return (
    <div className={className ?? "space-y-0.5"}>
      {visibleGroups.map((g) => {
        // 字典没配该语种时用语言表的自称（cy 显示 Cymraeg 而不是裸代码）；语言表也没有才回落原始代码。
        const labelKey = titleLocaleLabelKey(g.locale);
        const localeLabel = labelKey ? t(labelKey) : languageNativeName(g.locale) || g.locale;
        const officialTitles = joinUnique(g.official);
        const unofficialTitles = joinUnique(
          g.unofficial.filter((v) => !g.official.some((o) => o.toLocaleLowerCase() === v.toLocaleLowerCase())),
        );
        const mainText = officialTitles || unofficialTitles;
        return (
          <p key={g.locale} className={cls}>
            {t(g.isOriginal ? "entity.titles.groupOriginal" : "entity.titles.group", {
              locale: localeLabel,
              titles: mainText,
            })}
            {!officialTitles && unofficialTitles && (
              <span className="ml-1.5 inline-block rounded border border-line bg-surfaceSubtle px-1.5 py-0.5 text-[10px] text-text-muted">
                {t("entity.titles.nonofficialBadge")}
              </span>
            )}
            {officialTitles && unofficialTitles && (
              <span className="ml-2 inline-flex flex-wrap items-center gap-1">
                <span className="rounded border border-line bg-surfaceSubtle px-1.5 py-0.5 text-[10px] text-text-muted">
                  {t("entity.titles.nonofficialBadge")}
                </span>
                <span>{unofficialTitles}</span>
              </span>
            )}
          </p>
        );
      })}
      {hiddenRows > 0 && (
        <button
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded((v) => !v)}
          className={`inline-flex cursor-pointer items-center gap-1 transition-colors duration-fast ease-soft hover:text-primary ${cls}`}
        >
          {expanded
            ? t("entity.titles.collapse")
            : t("entity.titles.expandMore", { count: hiddenRows })}
          {expanded ? (
            <ChevronUp className="h-3 w-3" aria-hidden />
          ) : (
            <ChevronDown className="h-3 w-3" aria-hidden />
          )}
        </button>
      )}
    </div>
  );
}
