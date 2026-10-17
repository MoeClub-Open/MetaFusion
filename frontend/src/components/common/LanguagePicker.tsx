"use client";

import React, { useEffect, useId, useMemo, useRef, useState } from "react";
import { Check, ChevronDown, Languages, Search } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { LanguageEntry, sameLanguage, searchLanguages } from "@/lib/languages";

interface LanguagePickerProps {
  /** 表单内按文档流展开，避免窄容器或滚动卡片裁切选项。 */
  inline?: boolean;
  /** 已添加的语种：在选择器里置灰并标记「已添加」，避免重复添加。 */
  selected?: readonly string[];
  /** 选中一个语种。回调收到的永远是语言表里的规范码（ja → ja-JP）。 */
  onSelect: (code: string) => void;
  /** 触发按钮文案；省略时按 variant 取默认（字段样式显示占位提示）。 */
  label?: string;
  /** 触发按钮的可访问名；省略时用 label / 搜索框标签。 */
  ariaLabel?: string;
  /** field：整行输入框样式（设置页用）；chip：小号胶囊（编辑器里的快捷入口）。 */
  variant?: "field" | "chip";
  /** chip 变体下不显示图标。 */
  disabled?: boolean;
  className?: string;
}

/**
 * 可搜索语言选择器：按自称、英文名、中文名、日文名、代码、别名检索（ja / 日本 / Japanese
 * / 日本語 / cy 都能命中），用户不需要先知道语言代码。
 *
 * 数据全部来自 @/lib/languages 的语言单一来源；本组件只负责交互与展示，不维护任何语种清单。
 * 键盘：↑↓ 移动、Enter 选中、Esc 关闭并把焦点还给触发按钮、Home/End 跳首尾；
 * 已添加的语种置灰且不可选中（选中态由 selected 决定）。
 */
export function LanguagePicker({
  selected,
  onSelect,
  label,
  ariaLabel,
  variant = "field",
  inline = false,
  disabled,
  className,
}: LanguagePickerProps) {
  const { t, locale } = useI18n();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const listId = useId();

  const chosen = selected || [];
  const results = useMemo(() => (open ? searchLanguages(query) : []), [open, query]);
  const ordered = useMemo(() => {
    const isAdded = (e: LanguageEntry) => chosen.some((c) => sameLanguage(c, e.code));
    // 浏览（空查询）时把已添加的排到尾部：一屏之内先看到能选的那些。
    // 有查询时保持相关度排序——否则「搜 ja 时日文掉到列表末尾」，等于搜索没帮上忙。
    if (query.trim()) return results;
    return [...results.filter((e) => !isAdded(e)), ...results.filter((e) => isAdded(e))];
  }, [results, chosen, query]);

  /** 该语种在当前界面语言下的名字（用于列表第二行），界面四语之外回落英文名。 */
  const localizedName = (entry: LanguageEntry): string => {
    if (locale === "zh-CN" || locale === "zh-TW") return entry.nameZh || "";
    if (locale === "ja-JP") return entry.nameJa || "";
    return "";
  };

  useEffect(() => {
    if (!open) return;
    inputRef.current?.focus();
    const onDown = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const openPanel = () => {
    setQuery("");
    setActive(0);
    setOpen(true);
  };
  const closePanel = (focusTrigger = true) => {
    setOpen(false);
    if (focusTrigger) triggerRef.current?.focus();
  };
  const isAdded = (entry: LanguageEntry) => chosen.some((c) => sameLanguage(c, entry.code));
  const select = (entry: LanguageEntry) => {
    if (isAdded(entry)) return;
    onSelect(entry.code);
    closePanel();
  };

  /** 可选项下标集合：已添加项要跳过，方向键不能停在不可选的行上。 */
  const selectableIndexes = ordered.map((e, i) => (isAdded(e) ? -1 : i)).filter((i) => i >= 0);
  const move = (delta: number) => {
    if (selectableIndexes.length === 0) return;
    const at = selectableIndexes.indexOf(active);
    if (at < 0) {
      setActive(delta > 0 ? selectableIndexes[0] : selectableIndexes[selectableIndexes.length - 1]);
      return;
    }
    const next = (at + delta + selectableIndexes.length) % selectableIndexes.length;
    setActive(selectableIndexes[next]);
  };

  const onSearchKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      move(1);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      move(-1);
    } else if (event.key === "Home") {
      event.preventDefault();
      if (selectableIndexes.length) setActive(selectableIndexes[0]);
    } else if (event.key === "End") {
      event.preventDefault();
      if (selectableIndexes.length) setActive(selectableIndexes[selectableIndexes.length - 1]);
    } else if (event.key === "Enter") {
      event.preventDefault();
      const entry = ordered[active];
      if (entry && !isAdded(entry)) select(entry);
    } else if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      closePanel();
    } else if (event.key === "Tab") {
      setOpen(false);
    }
  };

  const fieldTrigger = variant === "field";
  const triggerClass = fieldTrigger
    ? "w-full h-8 px-2.5 rounded-md bg-black/[0.03] dark:bg-white/[0.04] border border-line text-left text-xs text-text-strong hover:border-primary/60 focus:outline-none focus:border-primary inline-flex items-center gap-2"
    : "px-2 py-0.5 rounded bg-emphasis/[0.04] hover:bg-emphasis/[0.08] text-text-muted hover:text-emphasis border border-line text-[10px] font-mono flex items-center gap-1 transition-colors duration-fast ease-soft disabled:opacity-50";

  return (
    <div ref={containerRef} className={"relative min-w-0 max-w-full " + (className || (fieldTrigger || inline ? "w-full" : "w-full sm:w-auto sm:inline-block"))}>
      <button
        ref={triggerRef}
        type="button"
        data-testid="language-picker-trigger"
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-label={ariaLabel || label || t("common.searchLanguage")}
        onClick={() => (open ? closePanel(false) : openPanel())}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            if (!open) openPanel();
          }
        }}
        className={triggerClass}
      >
        {fieldTrigger ? (
          <>
            <Search className="w-3.5 h-3.5 shrink-0 text-text-muted" strokeWidth={1.6} />
            <span className="truncate text-text-muted">{label || t("common.languageSearchPlaceholder")}</span>
          </>
        ) : (
          <>
            <Languages className="w-2.5 h-2.5" strokeWidth={1.8} />
            <span>{label || t("common.searchLanguage")}</span>
            <ChevronDown className="w-2.5 h-2.5" />
          </>
        )}
      </button>

      {open && (
        <div
          className={`${inline ? "relative" : "relative sm:absolute sm:min-w-[19rem]"} left-0 z-50 mt-1 w-full rounded-panel border border-line bg-surface shadow-elevated p-1.5`}
          onClick={(event) => event.stopPropagation()}
        >
          <input
            ref={inputRef}
            data-testid="language-picker-search"
            type="text"
            role="combobox"
            aria-expanded
            aria-controls={listId}
            aria-autocomplete="list"
            aria-activedescendant={ordered[active] ? listId + "-opt-" + ordered[active].code : undefined}
            aria-label={t("common.searchLanguage")}
            placeholder={t("common.languageSearchPlaceholder")}
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setActive(0);
            }}
            onKeyDown={onSearchKeyDown}
            className="w-full h-8 px-2 rounded-md bg-black/[0.03] dark:bg-white/[0.06] border border-line-subtle text-xs text-text-strong placeholder:text-text-muted focus:outline-none focus:border-primary"
          />
          <div
            id={listId}
            role="listbox"
            data-testid="language-picker-list"
            aria-label={t("common.searchLanguage")}
            className="mt-1 max-h-64 overflow-y-auto space-y-0.5"
          >
            {ordered.length === 0 ? (
              <p className="px-2 py-3 text-xs text-text-faint font-sans">{t("common.languageNoMatch")}</p>
            ) : (
              ordered.map((entry, index) => {
                const added = isAdded(entry);
                const extra = localizedName(entry);
                const secondary = [extra, entry.en].filter(
                  (v) => v && v !== entry.native && v !== extra,
                );
                return (
                  <div
                    key={entry.code}
                    id={listId + "-opt-" + entry.code}
                    data-testid={"language-option-" + entry.code}
                    role="option"
                    aria-selected={added}
                    aria-disabled={added}
                    onMouseEnter={() => !added && setActive(index)}
                    onClick={() => select(entry)}
                    className={
                      "px-2 py-1.5 rounded-md flex items-center gap-2 text-xs " +
                      (added
                        ? "opacity-45 cursor-not-allowed"
                        : index === active
                          ? "bg-primary/15 cursor-pointer"
                          : "cursor-pointer hover:bg-black/5 dark:hover:bg-surfaceHover")
                    }
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-text-strong font-sans">{entry.native}</span>
                      {secondary.length > 0 && (
                        <span className="block truncate text-[10px] text-text-faint">{secondary.join(" · ")}</span>
                      )}
                    </span>
                    <span className="shrink-0 font-mono text-[10px] text-text-muted">{entry.code}</span>
                    {added && (
                      <span className="shrink-0 inline-flex items-center gap-0.5 text-[10px] text-primary">
                        <Check className="w-3 h-3" strokeWidth={2.4} />
                        <span className="font-sans">{t("common.languageAdded")}</span>
                      </span>
                    )}
                  </div>
                );
              })
            )}
          </div>
          <p className="mt-1 px-2 pt-1 border-t border-line-subtle font-mono text-[10px] text-text-faint">
            {t("common.languagePickerHint")}
          </p>
        </div>
      )}
    </div>
  );
}
