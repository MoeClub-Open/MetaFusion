"use client";

import { clampSearchQuery, searchInputLimitProps } from "@/lib/searchQuery";
import React, { useEffect, useId, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Search } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { useDefinitions, getKindName } from "@/lib/definitions";
import { pickRecordTitle } from "@/lib/titles";
import { displayCover, withDisplayCovers, type CoverBearing } from "@/lib/cover";
import { CoverOriginNote } from "./CoverOriginNote";
import { useTitleDisplayOrder } from "@/hooks/useTitleDisplayOrder";
import { searchKeyAction } from "@/lib/searchKeyboard";
import { kindIcon } from "@/lib/kindIcons";

/** 实体检索的联想项，图片结构复用 lib/cover 的 CoverBearing。 */
interface SuggestItem extends CoverBearing {
  id: string;
  kind: string;
  title: string;
  original_language?: string;
  translations?: Record<string, { title?: string; summary?: string; aliases?: string[] }>;
}

interface SearchSuggestProps {
  value: string;
  onValueChange: (v: string) => void;
  /** 整词提交（回车且未选中联想项、或点搜索按钮） */
  onSubmit: (q: string) => void;
  placeholder: string;
  submitLabel: string;
  ariaLabel?: string;
  className?: string;
  onNavigate?: () => void;
}


/**
 * 带联想的搜索框：输入即下拉候选（封面缩略 + 类型 + 题名），
 * 回车选中候选直达详情，否则整词提交给调用方。
 * 没有联想时搜索只是"提交表单"——用户要整页跳转后才知道有没有命中。
 */
export function SearchSuggest({
  value,
  onValueChange,
  onSubmit,
  placeholder,
  submitLabel,
  ariaLabel,
  className = "",
  onNavigate,
}: SearchSuggestProps) {
  const { t, tr, locale } = useI18n();
  const { kinds } = useDefinitions();
  const titleOrder = useTitleDisplayOrder();
  const router = useRouter();

  const [items, setItems] = useState<SuggestItem[]>([]);
  const [focused, setFocused] = useState(false);
  const [active, setActive] = useState(-1);
  const inputRef = useRef<HTMLInputElement>(null);
  const composingRef = useRef(false);
  const compositionSubmitRef = useRef(false);
  const listId = useId();

  const q = value.trim();

  useEffect(() => {
    if (!q || !focused) {
      setItems([]);
      return;
    }
    setItems([]);
    setActive(-1);
    let alive = true;
    const timer = setTimeout(() => {
      fetch(
        "/api/catalog/entities?q=" + encodeURIComponent(q) + "&limit=8&state=active",
        { credentials: "same-origin" },
      )
        .then((res) => (res.ok ? res.json() : Promise.reject(new Error(String(res.status)))))
        .then((data) => {
          if (alive) setItems(Array.isArray(data.items) ? withDisplayCovers(data.items.slice(0, 8), data.covers) : []);
        })
        .catch(() => {
          // 联想失败不打断输入：静默收起下拉，整词搜索仍可用。
          if (alive) setItems([]);
        });
    }, 250);
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [q, focused]);

  const open = focused && q.length > 0 && items.length > 0;

  const displayTitle = (item: SuggestItem) =>
    pickRecordTitle(locale, item.translations, item.title, {
      order: titleOrder,
      originalLanguage: item.original_language,
    });

  const go = (item: SuggestItem) => {
    setFocused(false);
    setActive(-1);
    inputRef.current?.blur();
    router.push("/catalog/" + item.id);
    onNavigate?.();
  };

  const submit = () => {
    setFocused(false);
    setActive(-1);
    inputRef.current?.blur();
    onSubmit(clampSearchQuery(q));
  };

  const limit = searchInputLimitProps(onValueChange);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    const action = searchKeyAction({ key: e.key, isComposing: composingRef.current || e.nativeEvent.isComposing, keyCode: e.nativeEvent.keyCode }, open, active, items.length);
    if (action === "ignore") {
      if (e.key === "Enter") {
        compositionSubmitRef.current = true;
        setTimeout(() => { compositionSubmitRef.current = false; }, 0);
      }
      return;
    }
    if (action === "none") return;
    e.preventDefault();
    if (action === "next") setActive((i) => (i + 1) % items.length);
    else if (action === "previous") setActive((i) => (i <= 0 ? items.length - 1 : i - 1));
    else if (action === "select") go(items[active]);
    else if (action === "submit") submit();
    else if (action === "dismiss") { setFocused(false); setActive(-1); }
  };

  return (
    <form
      className={"relative flex items-center " + className}
      onSubmit={(e) => {
        e.preventDefault();
        if (!composingRef.current && !compositionSubmitRef.current) submit();
      }}
      role="search"
    >
      <Search
        className="absolute top-1/2 -translate-y-1/2 text-text-muted pointer-events-none w-4 h-4 left-3.5"
      />
      <input
        ref={inputRef}
        type="text"
        value={value}
        role="combobox"
        aria-expanded={open}
        aria-controls={listId}
        aria-activedescendant={open && active >= 0 ? `${listId}-${active}` : undefined}
        aria-autocomplete="list"
        aria-label={ariaLabel || placeholder}
        // 输入端就卡住后端上限（256 UTF-8 字节），免得提交后才拿到 400 query_too_long。
        maxLength={limit.maxLength}
        onChange={(e) => {
          limit.onChange(e);
          setActive(-1);
        }}
        onFocus={() => setFocused(true)}
        onBlur={() => {
          // 延迟收起：让候选项的 mousedown 先于失焦生效。
          setTimeout(() => setFocused(false), 120);
        }}
        onCompositionStart={() => { composingRef.current = true; }}
        onCompositionEnd={(e) => { composingRef.current = false; limit.onCompositionEnd(e); }}
        onKeyDown={handleKeyDown}
        placeholder={placeholder}
        className={
          "w-full border border-line hover:border-emphasis/20 focus:border-primary focus:ring-1 " +
          "focus:ring-primary text-emphasis placeholder:text-text-faint outline-none " +
          "transition-all duration-base ease-soft " +
          "pl-10 pr-20 h-10 md:h-9 rounded-lg text-base md:text-xs bg-black/[0.02] dark:bg-white/[0.04] focus:bg-surface"
        }
      />
      <button
        type="submit"
        className="absolute top-1/2 -translate-y-1/2 transition-colors duration-fast ease-soft cursor-pointer right-1.5 px-3 py-1 rounded text-xs font-semibold bg-primary/15 hover:bg-primary/25 text-primary"
      >
        {submitLabel}
      </button>

      {open && (
        <ul
          id={listId}
          role="listbox"
          aria-label={t("search.suggestions")}
          className="absolute left-0 right-0 top-full mt-2 z-50 max-h-[45dvh] overflow-y-auto rounded-panel border border-line bg-surface shadow-elevated"
        >
          {items.map((item, i) => {
            const KindIcon = kindIcon(item.kind);
            // 缩略图即该条目的封面：取值走 lib/cover（首张图=封面只有一处定义）。
            const cover = displayCover(item);
            return (
              <li key={item.id} id={`${listId}-${i}`} role="option" aria-selected={i === active}>
                <button
                  type="button"
                  onMouseDown={(e) => {
                    e.preventDefault();
                    go(item);
                  }}
                  onMouseEnter={() => setActive(i)}
                  className={
                    "w-full flex items-center gap-2.5 px-3 py-2 text-left cursor-pointer transition-colors duration-fast " +
                    (i === active ? "bg-surfaceHover" : "hover:bg-surfaceHover")
                  }
                >
                  <span className="w-7 h-9 rounded-md bg-black/[0.04] dark:bg-black/40 border border-line-subtle shrink-0 overflow-hidden flex items-center justify-center">
                    {cover.url ? (
                      <img
                        src={cover.url}
                        alt=""
                        className="w-full h-full object-contain"
                        loading="lazy"
                      />
                    ) : (
                      <KindIcon className="w-4 h-4 text-text-muted" />
                    )}
                  </span>
                  <div className="min-w-0 flex-1 text-sm text-text-strong truncate">
                    {displayTitle(item)}
                    <CoverOriginNote origin={cover.origin} from={cover.from} />
                  </div>
                  <span className="shrink-0 text-[10px] font-mono text-text-faint">
                    {getKindName(kinds, item.kind, locale, tr("catalog.kind." + item.kind, item.kind))}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </form>
  );
}
