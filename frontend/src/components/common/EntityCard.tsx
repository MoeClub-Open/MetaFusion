"use client";


import Link from "next/link";
import { AdaptiveCardCover } from "./AdaptiveCardCover";
import { ProceduralCover } from "./ProceduralCover";
import { GRID_COVER_ASPECT } from "@/lib/cover";
import type { ResolvedCover } from "@/lib/cover";
import { CoverOriginNote } from "./CoverOriginNote";

export interface EntityCardProps {
  id: string;
  kind: string;
  badgeLabel: string;
  /** 按请求语种解析后的展示题名 */
  title: string;
  /** 基础题名：与展示题名不同时作为原文副标题 */
  baseTitle?: string;
  /** 标签（**已按请求语种解析的展示名**，由调用方走 getTagName/getTagNames）：
   *  无原文副标题可用时，作为题名下的第二行信息。原始 tag code 不进卡片，
   *  它只用于 URL/筛选参数与写库，卡片是纯展示组件、不持有 definitions。 */
  tags?: string[];
  /** 无封面占位层的副行（如首个标签） */
  fallbackSubtitle?: string;
  pictureUrl?: string;
  cover?: ResolvedCover;
  /** 探索页质量提示：仅 partial 显示小圆点。 */
  qualityStatus?: string;
}

/**
 * 实体卡片唯一实现：首页分区与探索页网格共用。
 * 两处各写一套卡片会出现两种占位样式与两种页脚文案，用户会读成"产品不统一"。
 */
export function EntityCard({
  id,
  badgeLabel,
  title,
  baseTitle,
  tags,
  fallbackSubtitle,
  pictureUrl,
  cover,
  qualityStatus,
}: EntityCardProps) {
  // 题名下第二行只放有区分度的事实：原文题名优先，否则退到标签。
  // 与展示题名同文的原题名不渲染——复读一遍只添噪音。
  const showBaseTitle = !!baseTitle && baseTitle !== title;
  const metaLine = showBaseTitle ? baseTitle : (tags || []).slice(0, 3).join(" · ");

  return (
    <Link
      href={"/catalog/" + id}
      aria-label={title}
      title={showBaseTitle ? `${title}\n${baseTitle}` : title}
      className="mf-focus group flex min-w-0 flex-col self-start rounded-xl motion-safe:hover:-translate-y-1 transition-transform duration-base ease-soft"
    >
      <AdaptiveCardCover
        src={pictureUrl}
        alt={title}
        naturalAspect
        /* 3:4 仅用于加载中与无图占位，实际封面按原图比例撑开。 */
        aspectRatio={GRID_COVER_ASPECT}
        badge={
          <span className="px-2 py-0.5 rounded-chip bg-black/65 dark:bg-black/75 text-emphasis keep-white backdrop-blur-md border border-emphasis/20 text-[10px] font-medium shadow-2xs flex items-center gap-1.5 leading-none">
            <span className="w-1.5 h-1.5 rounded-full bg-primary shrink-0" />
            <span className="truncate max-w-[85px]">{badgeLabel}</span>
          </span>
        }
        fallbackContent={
          <div className="w-full h-full" aria-hidden>
            <ProceduralCover id={id} title={title} originalTitle={baseTitle} compact />
            {fallbackSubtitle && <span className="absolute bottom-3 inset-x-3 text-center text-xs text-text-muted line-clamp-1">{fallbackSubtitle}</span>}
          </div>
        }
        className="rounded-xl shadow-sm ring-1 ring-inset ring-black/10 dark:ring-white/10 group-hover:shadow-elevated transition-shadow duration-base ease-soft"
      />

      <div className="px-0.5 pt-2.5 pb-1">
        {cover?.url && <CoverOriginNote origin={cover.origin} from={cover.from} className="mb-1" />}
        <h3 className="font-semibold text-text-strong group-hover:text-primary transition-colors duration-fast ease-soft text-sm line-clamp-2 leading-snug mb-1">
          <span className="inline">{title}</span>
          {qualityStatus === "partial" && (
            <span
              className="ml-1.5 inline-block align-middle w-1.5 h-1.5 rounded-full bg-warn shrink-0"
              aria-hidden
            />
          )}
        </h3>
        {metaLine && (
          <p className="text-[11px] leading-5 text-text-muted line-clamp-1">{metaLine}</p>
        )}
      </div>
    </Link>
  );
}
