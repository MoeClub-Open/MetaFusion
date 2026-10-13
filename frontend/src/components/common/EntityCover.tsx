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
