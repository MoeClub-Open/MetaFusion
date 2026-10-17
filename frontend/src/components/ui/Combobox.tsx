"use client";

import React, { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, Search } from "lucide-react";

export type ComboboxOption = {
  value: string;
  label: React.ReactNode;
  disabled?: boolean;
  /** 搜索关键词：缺省时退化为对 value 做不包含大小写的匹配；
   *  label 是 ReactNode 时无法可靠取文本，调用方应把可检索的名称传进来。 */
  search?: string;
};

function cx(...parts: Array<string | false | null | undefined>) {
  return parts.filter(Boolean).join(" ");
}

/** 把 ReactNode 压成可搜索文本：只取字符串/数字片段，忽略图标等富内容。 */
function nodeToText(node: React.ReactNode): string {
  if (node == null || typeof node === "boolean") return "";
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(nodeToText).join(" ");
  if (React.isValidElement(node)) {
    return nodeToText((node.props as { children?: React.ReactNode }).children);
  }
  return "";
}

/**
 * 可搜索下拉（Combobox）：在 Select 的 portal 定位与键盘导航之上加一个搜索框，
 * 适合选项较多（层级、词表、关系类型）的场景。视觉 token 与 Select 一致：
 * border-line / bg-surface / text-text-*。
 *
 * 键盘：打开后输入即过滤；↑↓ 移动高亮、Enter 选中、Esc 关闭；
 * 关闭状态下 ↑↓/Enter/Space 打开。已禁用项跳过。
 */
export function Combobox({
  value,
  onChange,
  options,
  disabled,
  className,
  menuClassName,
  placeholder,
  searchPlaceholder,
  id,
  name,
  required,
  fullWidth = true,
  "aria-label": ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  options: ComboboxOption[];
  disabled?: boolean;
  className?: string;
  menuClassName?: string;
  placeholder?: string;
  searchPlaceholder?: string;
  id?: string;
  name?: string;
  required?: boolean;
  fullWidth?: boolean;
  "aria-label"?: string;
}) {
  const uid = useId();
  const listId = `${uid}-list`;
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const [open, setOpen] = useState(false);
  const [menuStyle, setMenuStyle] = useState<React.CSSProperties | undefined>();
  const [query, setQuery] = useState("");

  const selectedIndex = useMemo(
    () => options.findIndex((o) => o.value === value),
    [options, value]
  );
  const selected = selectedIndex >= 0 ? options[selectedIndex] : undefined;

  // 过滤后的选项：按 query 在 search 文本（或 value/label 文本）上做不区分大小写匹配。
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return options;
    return options.filter((o) => {
      const hay = `${o.search ?? ""} ${o.value} ${nodeToText(o.label)}`.toLowerCase();
      return hay.includes(q);
    });
  }, [options, query]);

  const [activeIndex, setActiveIndex] = useState(0);

  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const btn = buttonRef.current;
      if (!btn) return;
      const r = btn.getBoundingClientRect();
      const maxH = 280;
      const gap = 4;
      const pad = 8;
      const vw = window.innerWidth;
      const vh = window.innerHeight;
      const width = Math.min(Math.max(r.width, 180), vw - pad * 2);
      let left = r.left;
      if (width > r.width && r.left > vw / 2) {
        left = r.right - width;
      }
      left = Math.min(Math.max(pad, left), vw - width - pad);

      const menuH = Math.min(menuRef.current?.scrollHeight ?? maxH, maxH);
      const spaceBelow = vh - r.bottom - gap;
      const spaceAbove = r.top - gap;
      const openUp = spaceBelow < Math.min(160, menuH) && spaceAbove > spaceBelow;

      setMenuStyle({
        position: "fixed",
        left,
        width,
        maxHeight: maxH,
        zIndex: 100,
        margin: 0,
        ...(openUp
          ? { top: "auto", bottom: vh - r.top + gap }
          : { top: r.bottom + gap, bottom: "auto" }),
      });
    };
    place();
    window.addEventListener("resize", place);
    window.addEventListener("scroll", place, true);
    return () => {
      window.removeEventListener("resize", place);
      window.removeEventListener("scroll", place, true);
    };
  }, [open, filtered.length]);

  useEffect(() => {
    if (!open) return;
    // 打开时高亮已选项（或第一个可选项）；聚焦搜索框，输入即过滤。
    const start = filtered.findIndex((o) => o.value === value);
    setActiveIndex(start < 0 ? 0 : start);
    setQuery("");
    // 下一帧聚焦，避免打开动画期间抢焦点。
    const t = window.setTimeout(() => inputRef.current?.select(), 0);

    const onPointer = (e: MouseEvent | TouchEvent) => {
      const target = e.target as Node;
      if (rootRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        setOpen(false);
        buttonRef.current?.focus();
      }
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("touchstart", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      window.clearTimeout(t);
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("touchstart", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, filtered, value]);

  useEffect(() => {
    if (!open) return;
    optionRefs.current[activeIndex]?.scrollIntoView({ block: "nearest" });
  }, [open, activeIndex]);

  const moveActive = (dir: 1 | -1) => {
    if (filtered.length === 0) return;
    let i = activeIndex;
    for (let n = 0; n < filtered.length; n++) {
      i = (i + dir + filtered.length) % filtered.length;
      if (!filtered[i]?.disabled) {
        setActiveIndex(i);
        return;
      }
    }
  };

  const commit = (next: string) => {
    onChange(next);
    setOpen(false);
    buttonRef.current?.focus();
  };

  const onButtonKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>) => {
    if (disabled) return;
    if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      if (!open) setOpen(true);
    }
  };

  const onInputKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      moveActive(1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      moveActive(-1);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const opt = filtered[activeIndex];
      if (opt && !opt.disabled) commit(opt.value);
    } else if (e.key === "Home") {
      e.preventDefault();
      const i = filtered.findIndex((o) => !o.disabled);
      if (i >= 0) setActiveIndex(i);
    } else if (e.key === "End") {
      e.preventDefault();
      for (let i = filtered.length - 1; i >= 0; i--) {
        if (!filtered[i].disabled) {
          setActiveIndex(i);
          break;
        }
      }
    } else if (e.key === "Tab") {
      setOpen(false);
    }
  };

  const menu =
    open && menuStyle
      ? createPortal(
          <div
            ref={menuRef}
            style={menuStyle}
            className={cx(
              "overflow-hidden rounded-panel border border-line bg-surface shadow-elevated",
              menuClassName
            )}
          >
            <div className="flex items-center gap-2 px-2.5 py-1.5 border-b border-line-subtle">
              <Search className="w-3.5 h-3.5 shrink-0 text-text-muted" strokeWidth={1.8} />
              <input
                ref={inputRef}
                type="text"
                role="combobox"
                aria-expanded
                aria-controls={listId}
                aria-autocomplete="list"
                aria-label={searchPlaceholder || ariaLabel}
                placeholder={searchPlaceholder}
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setActiveIndex(0);
                }}
                onKeyDown={onInputKeyDown}
                className="w-full h-7 bg-transparent text-sm text-text-strong placeholder:text-text-muted focus:outline-none"
              />
            </div>
            <ul
              id={listId}
              role="listbox"
              aria-activedescendant={`${listId}-${activeIndex}`}
              className="list-none overflow-y-auto py-1"
              style={{ maxHeight: 220 }}
            >
              {filtered.length === 0 && (
                <li className="px-3 py-2 text-sm text-text-muted">
                  {searchPlaceholder}
                </li>
              )}
              {filtered.map((opt, i) => {
                const isSelected = opt.value === value;
                const isActive = i === activeIndex;
                return (
                  <li key={`${opt.value}-${i}`} role="none">
                    <button
                      ref={(el) => {
                        optionRefs.current[i] = el;
                      }}
                      type="button"
                      role="option"
                      id={`${listId}-${i}`}
                      aria-selected={isSelected}
                      disabled={opt.disabled}
                      onMouseEnter={() => setActiveIndex(i)}
                      onClick={() => {
                        if (!opt.disabled) commit(opt.value);
                      }}
                      className={cx(
                        "w-full flex items-center justify-between gap-2 px-3 py-2 text-sm text-left",
                        opt.disabled && "opacity-40 cursor-not-allowed",
                        isSelected
                          ? "text-primary bg-primary/10"
                          : "text-text-strong",
                        isActive && !isSelected && "bg-black/[0.04] dark:bg-white/[0.06]",
                        !opt.disabled && "hover:bg-black/[0.04] dark:hover:bg-surfaceHover"
                      )}
                    >
                      <span className="min-w-0 truncate">{opt.label}</span>
                      {isSelected && <Check className="w-3.5 h-3.5 shrink-0 text-primary" strokeWidth={2.2} />}
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>,
          document.body
        )
      : null;

  return (
    <div className={cx("relative", fullWidth ? "w-full" : "inline-block")} ref={rootRef}>
      {name ? <input type="hidden" name={name} value={value} required={required} /> : null}
      <button
        ref={buttonRef}
        id={id}
        type="button"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listId}
        aria-label={ariaLabel}
        onClick={() => !disabled && setOpen((v) => !v)}
        onKeyDown={onButtonKeyDown}
        className={cx(
          "inline-flex w-full items-center justify-between gap-2 h-10 px-3 rounded-md bg-background border border-line text-sm text-text-strong text-left",
          "hover:border-black/20 dark:hover:border-white/20 focus:outline-none focus-visible:border-primary",
          "disabled:opacity-50 disabled:cursor-not-allowed",
          className
        )}
      >
        <span className={cx("min-w-0 truncate", !selected && "text-text-muted")}>
          {selected ? selected.label : placeholder || "\u00a0"}
        </span>
        <ChevronDown
          className={cx("w-3.5 h-3.5 shrink-0 text-text-muted transition-transform duration-base ease-soft", open && "rotate-180")}
          strokeWidth={1.8}
        />
      </button>
      {menu}
    </div>
  );
}
