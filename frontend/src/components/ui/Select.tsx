"use client";

import React, { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown } from "lucide-react";

export type SelectOption = {
  value: string;
  label: React.ReactNode;
  disabled?: boolean;
};

function cx(...parts: Array<string | false | null | undefined>) {
  return parts.filter(Boolean).join(" ");
}

export function Select({
  value,
  onChange,
  options,
  disabled,
  className,
  menuClassName,
  placeholder,
  id,
  name,
  required,
  fullWidth = true,
  "aria-label": ariaLabel,
}: {
  value: string;
  onChange: (value: string) => void;
  options: SelectOption[];
  disabled?: boolean;
  className?: string;
  menuClassName?: string;
  placeholder?: string;
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
  const menuRef = useRef<HTMLUListElement>(null);
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const [open, setOpen] = useState(false);
  const [menuStyle, setMenuStyle] = useState<React.CSSProperties | undefined>();
  const selectedIndex = useMemo(
    () => options.findIndex((o) => o.value === value),
    [options, value]
  );
  const [activeIndex, setActiveIndex] = useState(Math.max(0, selectedIndex));

  const selected = selectedIndex >= 0 ? options[selectedIndex] : undefined;

  useLayoutEffect(() => {
    if (!open) return;
    const place = () => {
      const btn = buttonRef.current;
      if (!btn) return;
      const r = btn.getBoundingClientRect();
      const maxH = 240;
      const gap = 4;
      const pad = 8;
      const vw = window.innerWidth;
      const vh = window.innerHeight;
      const width = Math.min(Math.max(r.width, 140), vw - pad * 2);
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
  }, [open, options.length]);

  useEffect(() => {
    if (!open) return;
    const start = selectedIndex >= 0 ? selectedIndex : options.findIndex((o) => !o.disabled);
    setActiveIndex(start < 0 ? 0 : start);

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
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("touchstart", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, options, selectedIndex]);

  useEffect(() => {
    if (!open) return;
    optionRefs.current[activeIndex]?.scrollIntoView({ block: "nearest" });
  }, [open, activeIndex]);

  const moveActive = (dir: 1 | -1) => {
    if (options.length === 0) return;
    let i = activeIndex;
    for (let n = 0; n < options.length; n++) {
      i = (i + dir + options.length) % options.length;
      if (!options[i]?.disabled) {
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
      if (!open) {
        setOpen(true);
        return;
      }
      if (e.key === "ArrowDown") moveActive(1);
      else if (e.key === "ArrowUp") moveActive(-1);
      else if (e.key === "Enter" || e.key === " ") {
        const opt = options[activeIndex];
        if (opt && !opt.disabled) commit(opt.value);
      }
    } else if (e.key === "Home" && open) {
      e.preventDefault();
      const i = options.findIndex((o) => !o.disabled);
      if (i >= 0) setActiveIndex(i);
    } else if (e.key === "End" && open) {
      e.preventDefault();
      for (let i = options.length - 1; i >= 0; i--) {
        if (!options[i].disabled) {
          setActiveIndex(i);
          break;
        }
      }
    }
  };

  const menu =
    open && menuStyle
      ? createPortal(
          <ul
            ref={menuRef}
            id={listId}
            role="listbox"
            aria-activedescendant={`${listId}-${activeIndex}`}
            style={menuStyle}
            className={cx(
              "list-none overflow-y-auto rounded-panel border border-line bg-surface shadow-elevated py-1",
              menuClassName
            )}
          >
            {options.map((opt, i) => {
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
          </ul>,
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
