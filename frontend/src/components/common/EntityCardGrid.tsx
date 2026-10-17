"use client";

import { useLayoutEffect, useRef, type ReactNode } from "react";

/** 按卡片实际高度排布；DOM 顺序保留接口排序，图片加载与断点变化时重新计算。 */
export function EntityCardGrid({ children, className = "" }: { children: ReactNode; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    const grid = ref.current;
    if (!grid) return;
    const cards = Array.from(grid.children) as HTMLElement[];
    const gap = parseFloat(getComputedStyle(grid).rowGap) || 0;
    const resize = (card: HTMLElement) => {
      const span = Math.ceil(card.getBoundingClientRect().height + gap);
      card.style.gridRowEnd = `span ${Math.max(1, span)}`;
    };
    grid.style.gridAutoRows = "1px";
    grid.style.rowGap = "0px";
    cards.forEach(resize);
    const observer = new ResizeObserver((entries) => {
      entries.forEach(({ target }) => resize(target as HTMLElement));
    });
    cards.forEach((card) => observer.observe(card));
    return () => {
      observer.disconnect();
      grid.style.gridAutoRows = "";
      grid.style.rowGap = "";
      cards.forEach((card) => { card.style.gridRowEnd = ""; });
    };
  }, [children]);

  return <div ref={ref} className={`grid items-start gap-x-3 gap-y-6 ${className}`}>{children}</div>;
}
