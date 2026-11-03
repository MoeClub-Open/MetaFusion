"use client";

import React from "react";
import { ImageOff } from "lucide-react";

/**
 * 中性封面占位：加载失败 / 无图时用浅底，不再回落深色调试网格（ProceduralCover）。
 */
export function NeutralCover({
  className = "",
  label,
}: {
  className?: string;
  /** 可选辅助文案（通常不展示给读者） */
  label?: string;
}) {
  return (
    <div
      className={`w-full h-full relative overflow-hidden bg-surfaceSubtle flex flex-col items-center justify-center text-text-faint ${className}`}
      role="img"
      aria-label={label || undefined}
    >
      <div className="absolute inset-0 bg-gradient-to-b from-black/[0.02] to-black/[0.05] dark:from-white/[0.03] dark:to-white/[0.06]" aria-hidden />
      <ImageOff className="relative w-7 h-7 opacity-40" strokeWidth={1.4} aria-hidden />
    </div>
  );
}
