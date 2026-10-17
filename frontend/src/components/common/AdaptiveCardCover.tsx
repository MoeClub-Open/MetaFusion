"use client";

import React, { useState } from "react";

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
  /**
   * 容器比例（宽/高）的数值形式，优先于 aspectClassName。
   * 自然比例模式下作为图片加载前与无图时的占位比例。
   */
  aspectRatio?: number;
  /** 图片加载后让容器贴合原图比例；不裁切、不添加模糊衬底。 */
  naturalAspect?: boolean;
  className?: string;
  imgClassName?: string;
}

/**
 * 首页与探索页使用自然比例，边缘贴合图片；比较表等固定尺寸布局由调用方指定比例。
 * 图片完整显示，加载失败时回落占位图。固定尺寸模式用同图模糊衬底填补比例差异。
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
  aspectRatio,
  naturalAspect = false,
  className = "",
  imgClassName = "",
}: AdaptiveCardCoverProps) {
  // 加载结果绑定图片地址，换图时不沿用上一张图的比例或失败状态。
  const [image, setImage] = useState<{ src: string; ratio: number; failed: boolean } | null>(null);
  const hasError = image?.src === src && image?.failed;
  const loaded = image?.src === src && !image?.failed && !!image?.ratio;
  const hasValidImage = !!src && !hasError;
  const displayRatio = naturalAspect && loaded ? image!.ratio : aspectRatio;
  // 数值比例优先：调用方给了就完全接管容器尺寸，不用再写 aspect-* 类。
  const sizedByNumber = typeof displayRatio === "number" && isFinite(displayRatio) && displayRatio > 0;

  const recordLoaded = (el: HTMLImageElement) => {
    if (!src || !el.complete || el.naturalWidth <= 0 || el.naturalHeight <= 0) return;
    const ratio = el.naturalWidth / el.naturalHeight;
    setImage((current) => current?.src === src && current.ratio === ratio && !current.failed
      ? current
      : { src, ratio, failed: false });
  };

  return (
    <div
      style={sizedByNumber ? { aspectRatio: String(displayRatio) } : undefined}
      className={`relative w-full ${sizedByNumber ? "" : aspectClassName} bg-black/[0.03] dark:bg-black/40 flex items-center justify-center overflow-hidden isolate ${className}`}
    >
      {hasValidImage ? (
        <>
          {/* 固定比例布局才使用衬底；自然比例时容器与原图完全贴合。 */}
          {!naturalAspect && (
            <img
              src={src!}
              alt=""
              aria-hidden
              width={32}
              height={32}
              loading="lazy"
              decoding="async"
              className="absolute inset-0 w-full h-full object-cover scale-110 blur-2xl opacity-45 dark:opacity-40 saturate-125 select-none"
            />
          )}
          {!loaded && (
            <div className="absolute inset-0 bg-black/[0.06] dark:bg-white/[0.05] animate-pulse" aria-hidden />
          )}
          <img
            src={src!}
            alt={alt}
            // 实际布局由容器比例决定；自然比例在图片加载后取 naturalWidth/naturalHeight。
            width={512}
            height={512}
            decoding="async"
            loading="lazy"
            onError={() => { if (src) setImage({ src, ratio: 0, failed: true }); }}
            onLoad={(event) => recordLoaded(event.currentTarget)}
            ref={(el) => {
              // 命中缓存的图可能不触发 onLoad：挂载时已 complete 就直接算加载完成。
              if (el) recordLoaded(el);
            }}
            className={`absolute inset-0 w-full h-full object-contain select-none transition-[opacity,transform] duration-300 ${naturalAspect ? "" : "group-hover:scale-[1.03]"} ${loaded ? "opacity-100" : "opacity-0"} ${imgClassName}`}
          />
        </>
      ) : (
        /* 兜底占位层：无图或取图失败时不画破图图标 */
        fallbackContent ?? <div className="w-full h-full relative overflow-hidden bg-surfaceSubtle flex flex-col items-center justify-center p-3 text-center">
          <svg
            aria-hidden="true"
            focusable="false"
            viewBox="0 0 240 320"
            preserveAspectRatio="xMidYMid slice"
            className="absolute inset-0 w-full h-full text-primary/[0.12] pointer-events-none"
            fill="none"
          >
            <path d="M24 40H88M24 48H60M152 272H216M180 280H216" stroke="currentColor" strokeWidth="1" />
            <path d="M24 64V96M24 224V256M216 64V96M216 224V256" stroke="currentColor" strokeWidth="1" />
            <path d="M48 80H96L144 128H192M48 240H96L144 192H192" stroke="currentColor" strokeWidth="1" />
            <circle cx="48" cy="80" r="3" fill="currentColor" />
            <circle cx="192" cy="128" r="3" fill="currentColor" />
            <circle cx="48" cy="240" r="3" fill="currentColor" />
            <circle cx="192" cy="192" r="3" fill="currentColor" />
          </svg>
          {fallbackIcon && (
            <div className="relative w-10 h-10 rounded-full bg-primary/10 border border-primary/20 flex items-center justify-center text-primary mb-1.5 shadow-2xs group-hover:scale-110 transition-transform duration-base ease-soft">
              {fallbackIcon}
            </div>
          )}
          {fallbackTitle && (
            <span className="relative text-[11px] font-medium text-text-strong line-clamp-1 w-full px-1">
              {fallbackTitle}
            </span>
          )}
          {fallbackSubtitle && (
            <span className="relative text-[9px] font-mono text-text-faint uppercase tracking-wider mt-0.5">
              {fallbackSubtitle}
            </span>
          )}
        </div>
      )}

      {/* 左上角类别胶囊徽标 */}
      {badge && <div className="absolute top-2 left-2 z-20 pointer-events-none">{badge}</div>}

      {/* 右上角附加徽标 */}
      {statusBadge && <div className="absolute top-2 right-2 z-20 pointer-events-none">{statusBadge}</div>}
    </div>
  );
}
