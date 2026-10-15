"use client";

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";

export interface TabItem {
  /** 面板标识，同时作为 URL hash，便于分享与直达。 */
  id: string;
  label: string;
  icon?: React.ReactNode;
  /** 计数等次要信息，显示在标签右侧。 */
  badge?: React.ReactNode;
  /** 仅在这些条件为真时展示该标签。 */
  visible?: boolean;
}

/**
 * useHashTab 管理"当前标签 + URL hash 同步"的通用逻辑：
 * - 进入时按 location.hash 定位，无则取第一个可见标签；
 * - 切换时用 replaceState 写回 hash（可分享、可刷新，且不触发滚动跳转）；
 * - 监听 hashchange，浏览器前进/后退与外部改 hash 都能跟上；
 * - 标签集合变化（数据到达后数量变化）时校正，避免停在已消失的面板。
 */
export function detailTabFromHash(hash: string, ids: string[], anchorTabs: Record<string, string> = {}): string {
  let fragment: string;
  try { fragment = decodeURIComponent(hash.replace(/^#/, "")); } catch { return ""; }
  if (ids.includes(fragment)) return fragment;
  return Object.entries(anchorTabs).find(([prefix, tab]) => fragment.startsWith(prefix) && ids.includes(tab))?.[1] || "";
}

export function useHashTab(items: TabItem[], anchorTabs: Record<string, string> = {}) {
  const shown = useMemo(() => items.filter((x) => x.visible !== false), [items]);
  // 标签集合常随每轮渲染重建数组，用 id 签名做依赖，避免监听器反复重订阅。
  const shownKey = shown.map((x) => x.id).join("|");
  const shownRef = useRef(shown);
  shownRef.current = shown;
  const anchorTabsRef = useRef(anchorTabs);
  anchorTabsRef.current = anchorTabs;

  const readHash = () =>
    typeof window === "undefined" ? "" : detailTabFromHash(window.location.hash, shownRef.current.map((item) => item.id), anchorTabsRef.current);

  const [active, setActive] = useState<string>(() => {
    const h = readHash();
    return h && shown.some((x) => x.id === h) ? h : shown[0]?.id || "";
  });

  useEffect(() => {
    if (shown.length === 0) return;
    // 标签集合可能异步增长（如社区标签等 modules 返回后才出现）。此时若当前
    // active 仍在集合内，会被误判为"无需校正"而忽略 URL 里的深链接。因此优先
    // 采用合法 hash：select() 切换时也会写回 hash，正常情况下两者一致。
    const h = readHash();
    if (h && h !== active && shown.some((x) => x.id === h)) {
      setActive(h);
      return;
    }
    if (!shown.some((x) => x.id === active)) {
      setActive(h && shown.some((x) => x.id === h) ? h : shown[0].id);
    }
    // shown 已由 shownKey 表达；仅当标签集合或当前值变化时才校正。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shownKey, active]);

  useEffect(() => {
    const onHash = () => {
      const h = readHash();
      if (h && shownRef.current.some((x) => x.id === h)) setActive(h);
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, [shownKey]);

  const select = useCallback((id: string) => {
    setActive(id);
    if (typeof window !== "undefined") {
      window.history.replaceState(null, "", `#${encodeURIComponent(id)}`);
    }
  }, []);

  return { active, select, shown };
}

/** TabBar 只负责渲染标签按钮条（tablist 语义 + 方向键切换）。 */
export function TabBar({
  items,
  active,
  onSelect,
  ariaLabel,
  className = "",
}: {
  items: TabItem[];
  active: string;
  onSelect: (id: string) => void;
  ariaLabel: string;
  className?: string;
}) {
  const listRef = useRef<HTMLDivElement | null>(null);
  const shown = items.filter((x) => x.visible !== false);

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    const idx = shown.findIndex((x) => x.id === active);
    if (idx < 0) return;
    e.preventDefault();
    const next =
      e.key === "ArrowRight" ? (idx + 1) % shown.length : (idx - 1 + shown.length) % shown.length;
    onSelect(shown[next].id);
    listRef.current?.querySelector<HTMLButtonElement>(`[data-tab="${shown[next].id}"]`)?.focus();
  };

  return (
    <div
      ref={listRef}
      role="tablist"
      aria-label={ariaLabel}
      onKeyDown={onKeyDown}
      className={`flex items-center gap-1.5 overflow-x-auto border-b border-line pb-2.5 ${className}`}
    >
      {shown.map((tb) => {
        const on = tb.id === active;
        return (
          <button
            key={tb.id}
            data-tab={tb.id}
            type="button"
            role="tab"
            id={`tab-${tb.id}`}
            aria-selected={on}
            aria-controls={`panel-${tb.id}`}
            tabIndex={on ? 0 : -1}
            onClick={() => onSelect(tb.id)}
            className={`mf-focus shrink-0 px-3 h-9 max-sm:min-h-[44px] rounded-md text-xs font-semibold inline-flex items-center gap-1.5 border transition-colors duration-fast ease-soft ${
              on
                ? "bg-primary text-primary-foreground border-primary shadow-xs"
                : "bg-surfaceSubtle border-line text-text-muted hover:text-text-strong hover:bg-surfaceHover"
            }`}
          >
            {tb.icon}
            <span>{tb.label}</span>
            {tb.badge != null && (
              <span className={`text-[10px] font-mono ${on ? "text-primary-foreground/80" : "text-text-muted"}`}>
                {tb.badge}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}
