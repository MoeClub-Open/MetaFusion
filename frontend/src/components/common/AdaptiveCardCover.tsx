"use client";

import React, { useEffect, useState } from "react";

interface AdaptiveCardCoverProps {
  src?: string | null;
  alt: string;
  badge?: React.ReactNode;
  statusBadge?: React.ReactNode;
  fallbackIcon?: React.ReactNode;
  fallbackTitle?: string;
  fallbackSubtitle?: string;
  fallbackContent?: React.ReactNode;
  /** 统一容器比例对应的类名；调用方已定好外框时传 "w-full h-full" */
  aspectClassName?: string;
  className?: string;
  imgClassName?: string;
}

/**
 * 网格/卡片封面：**统一容器比例 + object-fit: contain**。
 *
 * 网格里每张卡各用图片自身比例会让同一排参差不齐，统一比例才能保证对齐；
 * 默认取 1:1，控制目录网格高度。图片完整放入容器，
 * 比例不合的部分留衬底（像相框卡纸），不裁切主体；取图期间显示脉冲衬底，
 * 避免懒加载未返回时卡片看起来是一块黑洞。
 */
export function AdaptiveCardCover({
  src,
  alt,
  badge,
  statusBadge,
  fallbackIcon,
  fallbackTitle,
  fallbackSubtitle,
  fallbackContent,
  aspectClassName = "aspect-square",
  className = "",
  imgClassName = "",
}: AdaptiveCardCoverProps) {
  const [hasError, setHasError] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    setLoaded(false);
    setHasError(false);
  }, [src]);

  const hasValidImage = !!src && !hasError;

  return (
    <div
      className={`relative w-full ${aspectClassName} bg-black/[0.03] dark:bg-black/40 flex items-center justify-center overflow-hidden isolate ${className}`}
    >
      {hasValidImage ? (
        <>
          {!loaded && (
            <div className="absolute inset-0 bg-black/[0.06] dark:bg-white/[0.05] animate-pulse" aria-hidden />
          )}
          <img
            src={src!}
            alt={alt}
            // 固有尺寸提示：容器已用 aspect 类占位，这里再给 width/height，
            // 让浏览器在图片到达前就能算出比例，避免布局跳动（CSS 仍按容器拉伸）。
            width={512}
            height={512}
            decoding="async"
            loading="lazy"
            onError={() => setHasError(true)}
            onLoad={() => setLoaded(true)}
            ref={(el) => {
              // 命中缓存的图可能不触发 onLoad：挂载时已 complete 就直接算加载完成。
              if (el && el.complete && el.naturalWidth > 0) setLoaded(true);
            }}
            className={`absolute inset-0 w-full h-full object-contain select-none transition-[opacity,transform] duration-300 group-hover:scale-[1.03] ${loaded ? "opacity-100" : "opacity-0"} ${imgClassName}`}
          />
        </>
      ) : (
        /* 兜底占位层：无图或取图失败时不画破图图标 */
        fallbackContent ?? <div className="w-full h-full relative overflow-hidden bg-linear-to-br from-primary/10 via-black/[0.02] to-primary/5 dark:from-primary/20 dark:via-surface dark:to-black/40 flex flex-col items-center justify-center p-3 text-center">
          {fallbackIcon && (
            <div className="w-10 h-10 rounded-full bg-primary/10 border border-primary/20 flex items-center justify-center text-primary mb-1.5 shadow-2xs group-hover:scale-110 transition-transform duration-base ease-soft">
              {fallbackIcon}
            </div>
          )}
          {fallbackTitle && (
            <span className="text-[11px] font-medium text-text-strong line-clamp-1 w-full px-1">
              {fallbackTitle}
            </span>
          )}
          {fallbackSubtitle && (
            <span className="text-[9px] font-mono text-text-faint uppercase tracking-wider mt-0.5">
              {fallbackSubtitle}
            </span>
          )}
        </div>
      )}

      {/* 左上角类别胶囊徽标 */}
      {badge && <div className="absolute top-2 left-2 z-20 pointer-events-none">{badge}</div>}

      {/* 右上角状态徽标（草稿/待审） */}
      {statusBadge && <div className="absolute top-2 right-2 z-20 pointer-events-none">{statusBadge}</div>}
    </div>
  );
}
