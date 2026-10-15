"use client";

import React, { useState } from "react";
import { ProceduralCover } from "./ProceduralCover";

interface EntityCoverProps {
  src?: string | null;
  alt?: string;
  title?: string;
  originalTitle?: string;
  id?: string;
  className?: string;
  imgClassName?: string;
  loading?: "lazy" | "eager";
  onLoad?: (e: React.SyntheticEvent<HTMLImageElement>) => void;
  compact?: boolean;
  /**
   * 衬底：同一张图放大模糊后铺满容器（默认关）。
   * 只在图片按 contain 展示时打开——容器比例与图片比例不一致时，留白会变成
   * 一块纯色（竖版两侧、横图上下），垫上同色柔化画面后读起来才是相框卡纸。
   * object-cover 的场合图片已经铺满，多画一层只是白费一次合成。
   */
  backdrop?: boolean;
  /** 固有尺寸提示（px），只用于浏览器预留比例；实际显示尺寸由 CSS 决定。 */
  width?: number;
  height?: number;
}

export function EntityCover({
  src,
  alt = "",
  title = "Untitled",
  originalTitle,
  id = "",
  className = "",
  imgClassName,
  loading = "lazy",
  onLoad,
  compact = false,
  backdrop = false,
  width = 512,
  height = 512,
}: EntityCoverProps) {
  const [hasError, setHasError] = useState(false);

  if (!src || hasError) {
    return (
      <ProceduralCover
        title={title}
        originalTitle={originalTitle}
        id={id}
        className={className}
        compact={compact}
      />
    );
  }

  if (backdrop) {
    return (
      <>
        <img
          src={src}
          alt=""
          aria-hidden
          className="absolute inset-0 w-full h-full object-cover scale-110 blur-2xl opacity-45 dark:opacity-40 saturate-125 select-none"
          loading={loading}
          width={32}
          height={32}
          decoding="async"
        />
        {/* 主图加 relative：绝对定位的衬底在静态元素之后绘制，不盖住主图。 */}
        <img
          src={src}
          alt={alt || title}
          className={(imgClassName ?? "w-full h-full object-contain") + " relative"}
          loading={loading}
          width={width}
          height={height}
          decoding="async"
          onError={() => setHasError(true)}
          onLoad={onLoad}
        />
      </>
    );
  }

  return (
    <img
      src={src}
      alt={alt || title}
      className={imgClassName ?? "w-full h-full object-cover"}
      loading={loading}
      width={width}
      height={height}
      decoding="async"
      onError={() => setHasError(true)}
      onLoad={onLoad}
    />
  );
}
