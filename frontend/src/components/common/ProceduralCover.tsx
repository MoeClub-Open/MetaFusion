"use client";

import { useId, useMemo, type CSSProperties } from "react";
import { isDistinctOriginalTitle } from "@/lib/titles";
import { coverHash, coverIdentity, coverRefCode } from "@/lib/coverIdentity";

interface ProceduralCoverProps {
  title?: string;
  originalTitle?: string;
  id?: string;
  className?: string;
  /** 小缩略图（关系卡 ~100px）：去引用码与标题、内容缩小，免得固定尺寸的内部撑爆裁剪。 */
  compact?: boolean;
}

function hsl(h: number, s: number, l: number): string {
  return `hsl(${h} ${s}% ${l}%)`;
}

function paletteFromHash(hash: number) {
  const hue = hash % 360;
  const sat = 34 + (hash % 16);
  return {
    background: hsl(hue, sat, 9),
    accent: hsl(hue, 68 + ((hash >>> 4) % 18), 52 + ((hash >>> 12) % 8)),
  };
}

export function ProceduralCover({ title = "Untitled", originalTitle, id = "", className = "", compact = false }: ProceduralCoverProps) {
  // 身份串与界面语言无关（见 lib/coverIdentity.ts）：同一实体在任何语言下
  // REF 码与配色都相同，切语言不再换一个引用码。
  const identity = coverIdentity({ id, originalTitle, title });
  const hash = useMemo(() => coverHash(identity), [identity]);
  const p = useMemo(() => paletteFromHash(hash), [hash]);
  const gridId = `cover-grid-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;

  const refCode = useMemo(() => `REF: ${coverRefCode(hash)}`, [hash]);

  const patternIndex = hash % 4;
  // 占位封面固定为深色表面，强调前景在浅色页面中也保持足够对比。
  const coverStyle = { backgroundColor: p.background, "--emphasis-rgb": "255 255 255" } as CSSProperties;

  return (
    <div className={`w-full h-full relative overflow-hidden select-none ${className}`} style={coverStyle}>
      {/* 细线网格与几何标记保持实体识别，不使用连续色阶或模糊光晕。 */}
      <svg aria-hidden="true" focusable="false" className="absolute inset-0 w-full h-full opacity-10 pointer-events-none" xmlns="http://www.w3.org/2000/svg">
        <defs>
          <pattern id={gridId} width="24" height="24" patternUnits="userSpaceOnUse">
            <path d="M 24 0 L 0 0 0 24" fill="none" stroke="#ffffff" strokeWidth="0.75" />
          </pattern>
        </defs>
        <rect width="100%" height="100%" fill={`url(#${gridId})`} />
      </svg>

      {/* Border Frame */}
      <div className="absolute inset-2 sm:inset-2.5 rounded-sm border border-emphasis/15 pointer-events-none" aria-hidden="true" />
      <div className="absolute inset-3 sm:inset-3.5 rounded-sm border border-emphasis/[0.07] pointer-events-none" aria-hidden="true" />

      {/* Content Container */}
      <div
        className={
          compact
            ? "relative z-10 w-full h-full p-2 flex flex-col justify-between text-left"
            : "relative z-10 w-full h-full p-4 sm:p-5 flex flex-col justify-between text-left"
        }
      >
        {/* 小缩略图只保留几何图形，给叠加的实体类型角标留出空间。 */}
        {!compact && (
          <div className="flex items-center justify-between font-mono text-[9px] sm:text-[10px] tracking-wider min-w-0">
            <span className="flex items-center gap-1 font-bold truncate" style={{ color: p.accent }}>
              <span className="w-1.5 h-1.5 rounded-xs inline-block shrink-0" style={{ backgroundColor: p.accent }} />
              <span className="truncate">METAFUSION</span>
            </span>
            <span className="text-emphasis/60 shrink-0">{refCode}</span>
          </div>
        )}

        {/* Central Geometric Totem */}
        <div className="my-auto py-2 flex items-center justify-center">
          <svg
            aria-hidden="true"
            focusable="false"
            className={compact ? "w-10 h-10 opacity-80" : "w-20 h-20 sm:w-24 sm:h-24 opacity-80"}
            viewBox="0 0 100 100"
            fill="none"
            xmlns="http://www.w3.org/2000/svg"
          >
            <circle cx="50" cy="50" r="44" stroke="rgba(255,255,255,0.08)" strokeWidth="1" strokeDasharray="3 3" />
            <circle cx="50" cy="50" r="34" stroke={p.accent} strokeOpacity="0.4" strokeWidth="1.5" />
            <circle cx="50" cy="50" r="24" fill="rgba(0,0,0,0.3)" stroke="rgba(255,255,255,0.12)" strokeWidth="1" />

            {patternIndex === 0 && (
              <>
                <line x1="28" y1="50" x2="72" y2="50" stroke={p.accent} strokeWidth="1.5" strokeLinecap="round" />
                <line x1="50" y1="28" x2="50" y2="72" stroke={p.accent} strokeWidth="1.5" strokeLinecap="round" />
                <circle cx="50" cy="50" r="6" fill={p.accent} />
              </>
            )}
            {patternIndex === 1 && (
              <>
                <polygon points="50,30 67,65 33,65" stroke={p.accent} strokeWidth="1.5" fill="none" />
                <circle cx="50" cy="53" r="4" fill={p.accent} />
              </>
            )}
            {patternIndex === 2 && (
              <>
                <circle cx="42" cy="46" r="6" stroke={p.accent} strokeWidth="1.5" />
                <circle cx="58" cy="54" r="8" stroke={p.accent} strokeWidth="1.5" />
                <path d="M 36 50 Q 50 35 64 50" stroke={p.accent} strokeWidth="1" />
              </>
            )}
            {patternIndex === 3 && (
              <>
                <rect x="38" y="38" width="24" height="24" stroke={p.accent} strokeWidth="1.5" fill="none" transform="rotate(45 50 50)" />
                <circle cx="50" cy="50" r="4" fill={p.accent} />
              </>
            )}
          </svg>
        </div>

        {/* Bottom Title & Credits（小卡片不渲染：名字已在图下方，省得挤占）。 */}
        {!compact && (
        <div className="space-y-1.5">
          <div className="h-0.5 w-8 rounded-full" style={{ backgroundColor: p.accent }} />
          <div>
            <h4 className="font-serif font-bold text-emphasis leading-tight line-clamp-2 text-sm sm:text-base tracking-tight">
              {title}
            </h4>
            {isDistinctOriginalTitle(originalTitle, title) && (
              <p className="font-mono text-[10px] text-emphasis/70 truncate mt-0.5">
                {originalTitle}
              </p>
            )}
          </div>
        </div>
        )}
      </div>
    </div>
  );
}
