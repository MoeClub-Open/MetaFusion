"use client";

import React, { useState, useRef, useEffect, useCallback } from "react";
import dynamic from "next/dynamic";
import {
  ForumBoard,
  Tag,
  boardDisplayName,
  boardDisplayDesc,
  createTopic,
  createPost,
} from "@/lib/api";
import { Entity, api } from "@/components/catalog/api";
import { PageContainer } from "@/components/ui/PageShell";
import {
  Bold,
  Italic,
  Quote,
  Code,
  List,
  Sigma,
  X,
  Minimize2,
  Maximize2,
  MessageSquare,
  Send,
  Search,
  BookOpen,
  Check,
  ChevronDown,
  Tag as TagIcon,
  GripVertical,
  RotateCcw,
} from "lucide-react";
const MarkdownRenderer = dynamic(() => import("@/components/MarkdownRenderer"), {
  loading: () => <div className="h-4 my-1.5 rounded bg-black/[0.04] dark:bg-white/[0.04] animate-pulse" />,
});

// ── Types ────────────────────────────────────────────────────────────────

type Mode = "createTopic" | "reply";

interface QuotedPost {
  post_number: number;
  username?: string;
  content: string;
}

interface PostComposerProps {
  mode: Mode;
  topicId?: string;
  quotedPost?: QuotedPost | null;
  defaultBoardCode?: string;
  boards: ForumBoard[];
  availableTags: Tag[];
  onSuccess: () => void;
  onClose: () => void;
  expanded: boolean;
  onExpandedChange: (v: boolean) => void;
  locale: string;
  t: (k: string, params?: Record<string, string | number>) => string;
}

// ── Markdown toolbar ───────────────────────────────────────────────────

function MarkdownToolbar({
  textareaRef,
  onInsert,
  t,
}: {
  // React 19 的 useRef<T>(null) 返回 RefObject<T | null>：这里必须带上 null 分支，
  // 否则传入的 ref 类型不匹配（@types/react 19 收紧了 RefObject 的可空性）。
  textareaRef: React.RefObject<HTMLTextAreaElement | null>;
  onInsert: (transform: (prev: string) => string) => void;
  t: (k: string, params?: Record<string, string | number>) => string;
}) {
  const wrapSelection = useCallback(
    (before: string, after: string, placeholder: string) => {
      const el = textareaRef.current;
      if (!el) {
        onInsert((prev) => prev + before + placeholder + after);
        return;
      }
      const start = el.selectionStart;
      const end = el.selectionEnd;
      const selected = el.value.slice(start, end);
      const inner = selected || placeholder;
      const replacement = before + inner + after;
      const prevVal = el.value;
      const next = prevVal.slice(0, start) + replacement + prevVal.slice(end);
      onInsert(() => next);
      requestAnimationFrame(() => {
        el.focus();
        if (selected) {
          el.setSelectionRange(start + before.length, start + before.length + selected.length);
        } else {
          el.setSelectionRange(start + before.length, start + before.length + placeholder.length);
        }
      });
    },
    [textareaRef, onInsert]
  );

  const insertBlock = useCallback(
    (snippet: string) => {
      const el = textareaRef.current;
      if (!el) {
        onInsert((prev) => prev + snippet);
        return;
      }
      const start = el.selectionStart;
      const prevVal = el.value;
      const selected = prevVal.slice(start, el.selectionEnd);
      const block = selected ? selected + snippet : snippet;
      const next = prevVal.slice(0, start) + block + prevVal.slice(el.selectionEnd);
      onInsert(() => next);
      requestAnimationFrame(() => {
        el.focus();
        el.setSelectionRange(next.length, next.length);
      });
    },
    [textareaRef, onInsert]
  );

  return (
    <div className="flex items-center gap-1 py-1 border-y border-line text-text-muted shrink-0">
      <button
        type="button"
        onClick={() => wrapSelection("**", "**", t("community.boldPlaceholder"))}
        className="p-1 hover:text-emphasis rounded hover:bg-background"
        title={t("community.bold")}
      >
        <Bold className="w-3.5 h-3.5" />
      </button>
      <button
        type="button"
        onClick={() => wrapSelection("*", "*", t("community.italicPlaceholder"))}
        className="p-1 hover:text-emphasis rounded hover:bg-background"
        title={t("community.italic")}
      >
        <Italic className="w-3.5 h-3.5" />
      </button>
      <button
        type="button"
        onClick={() => wrapSelection("\n> ", "", t("community.quotePlaceholder"))}
        className="p-1 hover:text-emphasis rounded hover:bg-background"
        title={t("community.quote")}
      >
        <Quote className="w-3.5 h-3.5" />
      </button>
      <button
        type="button"
        onClick={() => wrapSelection("\n```\n", "\n```\n", t("community.codePlaceholder"))}
        className="p-1 hover:text-emphasis rounded hover:bg-background"
        title={t("community.code")}
      >
        <Code className="w-3.5 h-3.5" />
      </button>
      <button
        type="button"
        onClick={() => insertBlock("\n- " + t("community.listPlaceholder") + "\n")}
        className="p-1 hover:text-emphasis rounded hover:bg-background"
        title={t("community.list")}
      >
        <List className="w-3.5 h-3.5" />
      </button>
      <button
        type="button"
        onClick={() => wrapSelection("$", "$", t("community.mathPlaceholder"))}
        className="p-1 hover:text-emphasis rounded hover:bg-background"
        title={t("community.math")}
      >
        <Sigma className="w-3.5 h-3.5" />
      </button>
    </div>
  );
}

// ── Helpers ────────────────────────────────────────────────────────────

const BOARD_ICON_FALLBACK = BookOpen;

function resolveBoardIcon(board: ForumBoard): React.ElementType {
  // Boards use short color names; icons live on board.icon but we treat all uniformly.
  // Keep simple; show BookOpen circle style consistent with original drawer.
  void board;
  return BOARD_ICON_FALLBACK;
}

function draftKey(mode: Mode, topicId?: string): string {
  return `mf_composer_draft_${mode}_${topicId || "new"}`;
}

// ── Component ──────────────────────────────────────────────────────────

function ComposerEditor({ contentRef, content, onChange, placeholder, t }: {
  contentRef: React.RefObject<HTMLTextAreaElement | null>;
  content: string;
  onChange: (value: string) => void;
  placeholder: string;
  t: PostComposerProps["t"];
}) {
  const [share, setShare] = useState(50);
  const editorRef = useRef<HTMLDivElement>(null);
  const changeShare = (value: number) => setShare(Math.min(80, Math.max(20, Math.round(value))));
  return (
    <div ref={editorRef} className="flex-1 grid grid-cols-1 md:grid-cols-[minmax(0,var(--composer-editor-share))_0.75rem_minmax(0,1fr)] min-h-[200px] md:min-h-[240px]" style={{ "--composer-editor-share": `${share}%` } as React.CSSProperties}>
      <textarea
        ref={contentRef}
        required
        aria-label={placeholder}
        placeholder={placeholder}
        value={content}
        onChange={(event) => onChange(event.target.value)}
        className="min-w-0 w-full h-full p-3.5 bg-background border border-line rounded-lg text-emphasis text-sm focus:outline-none focus:border-primary resize-none font-mono leading-relaxed overflow-y-auto"
      />
      <div
        role="separator"
        aria-label={t("community.composerSplit")}
        aria-orientation="vertical"
        aria-valuemin={20}
        aria-valuemax={80}
        aria-valuenow={share}
        tabIndex={0}
        className="mf-focus hidden md:flex items-center justify-center touch-none cursor-col-resize text-text-faint hover:text-primary rounded"
        onPointerDown={(event) => {
          event.preventDefault();
          event.currentTarget.focus();
          event.currentTarget.setPointerCapture(event.pointerId);
        }}
        onPointerMove={(event) => {
          if (!event.currentTarget.hasPointerCapture(event.pointerId)) return;
          const box = editorRef.current?.getBoundingClientRect();
          if (box?.width) changeShare((event.clientX - box.left) / box.width * 100);
        }}
        onKeyDown={(event) => {
          if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
          event.preventDefault();
          changeShare(event.key === "Home" ? 20 : event.key === "End" ? 80 : share + (event.key === "ArrowLeft" ? -5 : 5));
        }}
      >
        <GripVertical className="w-3 h-5" aria-hidden />
      </div>
      <div className="hidden md:block min-w-0 h-full p-3.5 bg-background border border-line rounded-lg overflow-y-auto">
        {content ? <MarkdownRenderer content={content} /> : <span className="text-text-faint font-mono">{t("community.livePreview")}</span>}
      </div>
    </div>
  );
}

export default function PostComposer({
  mode,
  topicId,
  quotedPost,
  defaultBoardCode,
  boards,
  availableTags,
  onSuccess,
  onClose,
  expanded,
  onExpandedChange,
  locale,
  t,
}: PostComposerProps) {
  // ── Shared content state ──
  const [newContent, setNewContent] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const contentRef = useRef<HTMLTextAreaElement>(null);
  const [widthPercent, setWidthPercent] = useState<number | null>(null);
  useEffect(() => {
    try {
      const saved = Number(localStorage.getItem("mf_community_composer_width"));
      if (Number.isInteger(saved) && saved >= 50 && saved <= 100) setWidthPercent(saved);
    } catch {}
  }, []);
  const changeWidth = (value: number | null) => {
    setWidthPercent(value);
    try {
      if (value === null) localStorage.removeItem("mf_community_composer_width");
      else localStorage.setItem("mf_community_composer_width", String(value));
    } catch {}
  };

  // ── createTopic-only state ──
  const [newTitle, setNewTitle] = useState("");
  const [newBoardCode, setNewBoardCode] = useState(defaultBoardCode || "");
  const [workSearchQuery, setWorkSearchQuery] = useState("");
  const [searchedWorks, setSearchedWorks] = useState<Entity[]>([]);
  const [selectedWork, setSelectedWork] = useState<Entity | null>(null);
  const boardQueryRef = useRef<HTMLInputElement>(null);

  const [boardQuery, setBoardQuery] = useState("");
  const [boardDropdownOpen, setBoardDropdownOpen] = useState(false);
  const boardDropdownRef = useRef<HTMLDivElement>(null);

  const [tagInput, setTagInput] = useState("");
  const [tagDropdownOpen, setTagDropdownOpen] = useState(false);
  const [selectedTagIds, setSelectedTagIds] = useState<number[]>([]);
  const [customTagNames, setCustomTagNames] = useState<string[]>([]);
  const tagInputRef = useRef<HTMLInputElement>(null);

  // quoted post local dismiss
  const [activeQuotedPost, setActiveQuotedPost] = useState<QuotedPost | null | undefined>(quotedPost ?? null);
  useEffect(() => {
    setActiveQuotedPost(quotedPost ?? null);
  }, [quotedPost]);

  // Keep board default synced if prop changes while composer open
  useEffect(() => {
    if (mode === "createTopic" && defaultBoardCode) setNewBoardCode(defaultBoardCode);
  }, [defaultBoardCode, mode]);

  // ── Draft persist ──
  const key = draftKey(mode, topicId);

  // Restore on mount
  useEffect(() => {
    try {
      const raw = localStorage.getItem(key);
      if (!raw) return;
      const d = JSON.parse(raw) as Record<string, unknown>;
      if (mode === "createTopic") {
        if (typeof d.newTitle === "string") setNewTitle(d.newTitle);
        if (typeof d.newBoardCode === "string") setNewBoardCode(d.newBoardCode);
        if (typeof d.newContent === "string") setNewContent(d.newContent);
        if (Array.isArray(d.selectedTagIds)) setSelectedTagIds(d.selectedTagIds as number[]);
        if (Array.isArray(d.customTagNames)) setCustomTagNames(d.customTagNames as string[]);
        // work / tags snapshot kept minimal; ignoring selectedWork restore (Work object not rehydrated)
      } else {
        if (typeof d.newContent === "string") setNewContent(d.newContent);
      }
    } catch {}
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Save on change (debounced lightly via immediate writes; cheap)
  useEffect(() => {
    try {
      if (mode === "createTopic") {
        localStorage.setItem(
          key,
          JSON.stringify({ newTitle, newBoardCode, newContent, selectedTagIds, customTagNames })
        );
      } else {
        localStorage.setItem(key, JSON.stringify({ newContent }));
      }
    } catch {}
  }, [key, mode, newTitle, newBoardCode, newContent, selectedTagIds, customTagNames]);

  const clearDraft = useCallback(() => {
    try {
      localStorage.removeItem(key);
    } catch {}
  }, [key]);

  // ── Work debounced search ──
  useEffect(() => {
    if (!workSearchQuery.trim()) {
      setSearchedWorks([]);
      return;
    }
    const timer = setTimeout(() => {
      api<{ items: Entity[] }>(`/catalog/entities?kind=work&q=${encodeURIComponent(workSearchQuery.trim())}&limit=10`)
        .then(({ items }) => setSearchedWorks(items))
        .catch(() => setSearchedWorks([]));
    }, 300);
    return () => clearTimeout(timer);
  }, [workSearchQuery]);

  // ── Outside click for board dropdown ──
  useEffect(() => {
    const handler = (e: MouseEvent) => {
      if (boardDropdownRef.current && !boardDropdownRef.current.contains(e.target as Node)) {
        setBoardDropdownOpen(false);
      }
    };
    if (boardDropdownOpen) document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [boardDropdownOpen]);

  // ── Derived ──
  const boardOptions = boards.filter((b) => b.code !== "all" && b.is_enabled !== false);
  const filteredBoards = boardOptions.filter((b) => {
    if (!boardQuery.trim()) return true;
    const q = boardQuery.toLowerCase();
    const name = boardDisplayName(b, locale, t).toLowerCase();
    const desc = boardDisplayDesc(b, locale, t).toLowerCase();
    return name.includes(q) || desc.includes(q) || b.code.toLowerCase().includes(q);
  });
  const selectedBoardObj = boardOptions.find((b) => b.code === newBoardCode);

  useEffect(() => {
    if (!selectedBoardObj) setNewBoardCode(boardOptions[0]?.code || "");
  }, [boards, newBoardCode]);

  const filteredAvailableTags = availableTags
    .filter((tag) => {
      if (!tagInput.trim()) return true;
      return tag.name.toLowerCase().includes(tagInput.trim().toLowerCase());
    })
    .filter((tag) => !selectedTagIds.includes(tag.id) && !customTagNames.includes(tag.name));

  const selectedTagsForDisplay: { id?: number; name: string }[] = [
    ...selectedTagIds.map((id) => {
      const found = availableTags.find((x) => x.id === id);
      return found ? { id: found.id, name: found.name } : { id, name: String(id) };
    }),
    ...customTagNames.map((n) => ({ name: n })),
  ];

  const addTag = (tag: Tag) => {
    setSelectedTagIds((prev) => [...prev, tag.id]);
    setTagInput("");
    setTagDropdownOpen(false);
  };

  const addCustomTag = (nameRaw: string) => {
    const name = nameRaw.trim();
    if (!name) return;
    if (customTagNames.includes(name)) return;
    if (availableTags.some((entry) => entry.name === name && selectedTagIds.includes(entry.id))) return;
    const existing = availableTags.find((entry) => entry.name === name);
    if (existing) {
      addTag(existing);
      return;
    }
    setCustomTagNames((prev) => [...prev, name]);
    setTagInput("");
    setTagDropdownOpen(false);
  };

  const removeTagId = (id: number) => setSelectedTagIds((prev) => prev.filter((v) => v !== id));
  const removeCustomTag = (name: string) => setCustomTagNames((prev) => prev.filter((v) => v !== name));

  const setContentFromToolbar = useCallback((transform: (prev: string) => string) => {
    setNewContent((prev) => transform(prev));
  }, []);

  // ── Submits ──
  const handleCreateTopic = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedBoardObj || !newTitle.trim() || !newContent.trim()) return;
    setSubmitting(true);
    try {
      await createTopic({
        board_code: newBoardCode,
        title: newTitle.trim(),
        content: newContent.trim(),
        entity_id: selectedWork?.id,
        tag_ids: selectedTagIds.length ? selectedTagIds : undefined,
        tag_names: customTagNames.length ? customTagNames : undefined,
      });
      clearDraft();
      setNewTitle("");
      setNewContent("");
      setSelectedWork(null);
      setWorkSearchQuery("");
      setSelectedTagIds([]);
      setCustomTagNames([]);
      setTagInput("");
      setBoardQuery("");
      onSuccess();
      onClose();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      alert(msg || t("auth.requestFailed"));
    } finally {
      setSubmitting(false);
    }
  };

  const handleReply = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newContent.trim()) return;
    if (!topicId) {
      alert(t("community.missingTopicId"));
      return;
    }
    setSubmitting(true);
    try {
      await createPost(topicId, {
        content: newContent.trim(),
        reply_to_post_number: activeQuotedPost?.post_number ?? null,
      });
      clearDraft();
      setNewContent("");
      setActiveQuotedPost(null);
      onSuccess();
      onClose();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      alert(msg || t("auth.requestFailed"));
    } finally {
      setSubmitting(false);
    }
  };

  const headerTitle =
    mode === "createTopic" ? t("community.createTitle") : t("community.replyTopic");

  // ── Render ──
  return (
    <PageContainer className="fixed bottom-3 inset-x-0 z-50 pointer-events-none md:w-[var(--composer-width)]" style={{ "--composer-width": expanded ? "100%" : widthPercent === null ? "100%" : `${widthPercent}vw`, maxWidth: expanded || widthPercent !== null ? "100%" : undefined } as React.CSSProperties}>
    <div
      role="dialog"
      aria-label={headerTitle}
      className={`pointer-events-auto w-full bg-surface border border-line rounded-xl shadow-elevated flex flex-col overflow-hidden pb-[env(safe-area-inset-bottom)] ${expanded ? "h-[calc(100dvh-var(--mf-header-h)-2rem)]" : "h-[min(640px,calc(100dvh-var(--mf-header-h)-2rem))]"}`}
    >
      {/* Header */}
      <div className="flex items-center justify-between px-4 py-2 bg-background border-b border-line shrink-0">
        <div className="flex items-center gap-2 min-w-0">
          <MessageSquare className="w-4 h-4 text-success" />
          <span className="font-bold text-emphasis text-xs truncate">{headerTitle}</span>
        </div>
        <div className="flex items-center gap-2">
          <label className={`hidden md:flex items-center gap-2 text-xs text-text-muted ${expanded ? "opacity-50" : ""}`}>
            <span>{t("community.composerWidth")}</span>
            <input type="range" min={50} max={100} step={5} value={widthPercent ?? 80} disabled={expanded} onChange={(event) => changeWidth(Number(event.target.value))} aria-label={t("community.composerWidth")} aria-valuetext={widthPercent === null ? t("community.composerWidthPage") : `${widthPercent}%`} className="w-24 accent-primary cursor-ew-resize disabled:cursor-default" />
            <span className="min-w-14 text-right tabular-nums">{widthPercent === null ? t("community.composerWidthPage") : `${widthPercent}%`}</span>
          </label>
          <button type="button" onClick={() => changeWidth(null)} disabled={expanded || widthPercent === null} title={t("community.composerWidthPage")} aria-label={t("community.composerWidthPage")} className="hidden md:block mf-focus p-1.5 rounded text-text-muted hover:text-emphasis disabled:opacity-40">
            <RotateCcw className="w-3.5 h-3.5" />
          </button>
          <button
            type="button"
            onClick={() => onExpandedChange(!expanded)}
            className="mf-focus p-2 text-text-muted hover:text-emphasis"
            aria-label={expanded ? t("community.composerCollapse") : t("community.composerExpand")}
            title={expanded ? t("community.composerCollapse") : t("community.composerExpand")}
          >
            {expanded ? <Minimize2 className="w-3.5 h-3.5" /> : <Maximize2 className="w-3.5 h-3.5" />}
          </button>
          <button
            type="button"
            onClick={onClose}
            className="mf-focus p-2 text-text-muted hover:text-emphasis"
            aria-label={t("community.closeComposer")}
            title={t("community.closeComposer")}
          >
            <X className="w-4 h-4" />
          </button>
        </div>
      </div>

      {/* Body */}
      {mode === "createTopic" ? (
        <form onSubmit={handleCreateTopic} className="flex-1 min-h-0 flex flex-col p-4 gap-3.5 overflow-y-auto">
          <div className="grid grid-cols-1 md:grid-cols-[minmax(0,2fr)_minmax(0,1fr)] gap-3 shrink-0">
            <div className="min-w-0">
              <input
                type="text"
                required
                placeholder={t("community.topicTitlePlaceholder")}
                value={newTitle}
                onChange={(e) => setNewTitle(e.target.value)}
                className="w-full px-3.5 h-10 bg-background border border-line rounded-lg text-emphasis text-sm focus:outline-none focus:border-gray-600 font-medium"
              />
            </div>

            {/* Work link */}
            <div className="relative min-w-0">
              {selectedWork ? (
                <div className="flex items-center justify-between px-3.5 h-10 rounded-lg bg-background border border-emerald-500/40 text-success-soft">
                  <span className="flex items-center gap-2 truncate text-sm font-semibold">
                    <BookOpen className="w-4 h-4 text-success shrink-0" />
                    <span className="truncate">{selectedWork.title}</span>
                  </span>
                  <button type="button" onClick={() => setSelectedWork(null)} className="text-text-muted hover:text-emphasis ml-1.5 cursor-pointer">
                    <X className="w-4 h-4" />
                  </button>
                </div>
              ) : (
                <div>
                  <input
                    type="text"
                    placeholder={t("community.workSearchPlaceholder")}
                    value={workSearchQuery}
                    onChange={(e) => setWorkSearchQuery(e.target.value)}
                    className="w-full px-3.5 h-10 bg-background border border-line rounded-lg text-emphasis text-sm focus:outline-none focus:border-gray-600"
                  />
                  {searchedWorks.length > 0 && (
                    <div className="absolute top-full mt-2 left-0 right-0 bg-surface/95 backdrop-blur-2xl border border-line rounded-xl shadow-elevated max-h-48 overflow-y-auto z-50 isolate animate-slide-up divide-y divide-line-subtle">
                      {searchedWorks.map((w) => (
                        <div
                          key={w.id}
                          role="button"
                          tabIndex={0}
                          onKeyDown={(e) => {
                            if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              e.currentTarget.click();
                            }
                          }}
                          onClick={() => {
                            setSelectedWork(w);
                            setWorkSearchQuery("");
                            setSearchedWorks([]);
                            setNewBoardCode("comment");
                          }}
                          className="p-2.5 hover:bg-emphasis/[0.04] cursor-pointer flex items-center justify-between text-text-body hover:text-emphasis text-sm"
                        >
                          <span className="font-medium truncate">{w.title}</span>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}
            </div>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-[minmax(0,14rem)_minmax(0,1fr)] gap-3 shrink-0" data-mf-composer-meta="">
            {/* Searchable board selector */}
            <div className="relative min-w-0" data-mf-composer-board="" ref={boardDropdownRef}>
              <button
                type="button"
                aria-label={t("community.board")}
                aria-expanded={boardDropdownOpen}
                onClick={() => setBoardDropdownOpen((v) => !v)}
                className="w-full px-3.5 h-10 bg-background border border-line rounded-lg text-emphasis text-sm flex items-center justify-between gap-2 hover:border-gray-600 transition-colors duration-fast ease-soft cursor-pointer"
              >
                <span className="flex items-center gap-2 truncate">
                  {selectedBoardObj ? (
                    <>
                      <span className={`w-2.5 h-2.5 rounded-full ${selectedBoardObj.bgColor} ${selectedBoardObj.borderColor} border`} />
                      <span className="truncate font-medium">{boardDisplayName(selectedBoardObj, locale, t)}</span>
                    </>
                  ) : (
                    <span className="truncate">{t("community.board")}</span>
                  )}
                </span>
                <ChevronDown className={`w-4 h-4 text-text-faint shrink-0 transition-transform duration-base ease-soft ${boardDropdownOpen ? "rotate-180" : ""}`} />
              </button>
              {boardDropdownOpen && (
                <div className="absolute top-full mt-2 left-0 right-0 bg-surface/95 backdrop-blur-2xl border border-line rounded-card shadow-elevated z-50 isolate animate-slide-up overflow-hidden flex flex-col">
                  <div className="p-2 border-b border-line">
                    <div className="relative">
                      <Search className="w-3 h-3 text-text-faint absolute left-2 top-2.5" />
                      <input
                        ref={boardQueryRef}
                        autoFocus
                        type="text"
                        placeholder={t("community.boardSearchPlaceholder")}
                        value={boardQuery}
                        onChange={(e) => setBoardQuery(e.target.value)}
                        className="w-full pl-7 pr-2 py-1.5 bg-background border border-line rounded text-emphasis text-xs placeholder-gray-500 focus:outline-none focus:border-gray-600"
                      />
                    </div>
                  </div>
                  <div className="max-h-48 overflow-y-auto divide-y divide-surfaceBorder">
                    {filteredBoards.length === 0 ? (
                      <div className="p-3 text-center text-[11px] text-text-faint">{t("community.noBoardMatch")}</div>
                    ) : (
                      filteredBoards.map((b) => {
                        const active = newBoardCode === b.code;
                        const Icon = resolveBoardIcon(b);
                        return (
                          <button
                            key={b.code}
                            type="button"
                            onClick={() => {
                              setNewBoardCode(b.code);
                              setBoardDropdownOpen(false);
                              setBoardQuery("");
                            }}
                            className={`w-full flex items-center gap-2.5 px-3 py-2 text-left hover:bg-emphasis/[0.04] transition-colors duration-fast ease-soft ${active ? "bg-emphasis/[0.025]" : ""}`}
                          >
                            <span className={`w-7 h-7 rounded-md flex items-center justify-center border shrink-0 ${b.bgColor} ${b.borderColor}`}>
                              <Icon className={`w-3.5 h-3.5 ${b.color}`} />
                            </span>
                            <span className="flex-1 min-w-0">
                              <span className={`block text-xs font-semibold truncate ${active ? "text-emphasis" : "text-text-strong"}`}>
                                {boardDisplayName(b, locale, t)}
                              </span>
                              <span className="block text-[10px] text-text-faint truncate">{boardDisplayDesc(b, locale, t)}</span>
                            </span>
                            {active && <Check className="w-3.5 h-3.5 text-success shrink-0" />}
                          </button>
                        );
                      })
                    )}
                  </div>
                </div>
              )}
            </div>

          {/* Tags */}
          <div className="min-w-0 space-y-2" data-mf-composer-tags="">
            <div className="flex min-h-10 items-center gap-2 flex-wrap">
              <span className="text-xs font-mono text-text-faint flex items-center gap-1 shrink-0 ml-1">
                <TagIcon className="w-3.5 h-3.5" />
                {t("community.tags")}
              </span>
              {selectedTagsForDisplay.map((entry) => (
                <span
                  key={`${entry.id ?? entry.name}-${entry.name}`}
                  className="inline-flex items-center gap-1.5 px-3 py-1 rounded-full bg-emerald-500/15 border border-emerald-500/30 text-success-soft text-xs font-medium"
                >
                  #{entry.name}
                  <button
                    type="button"
                    onClick={() => {
                      if (entry.id) removeTagId(entry.id);
                      else removeCustomTag(entry.name);
                    }}
                    className="ml-0.5 p-0.5 hover:bg-emerald-500/20 rounded-full"
                  >
                    <X className="w-3.5 h-3.5" />
                  </button>
                </span>
              ))}
              <div className="relative flex-1 min-w-[150px]">
                <input
                  ref={tagInputRef}
                  type="text"
                  placeholder={t("community.tagPlaceholder")}
                  value={tagInput}
                  onChange={(e) => {
                    setTagInput(e.target.value);
                    setTagDropdownOpen(true);
                  }}
                  onFocus={() => setTagDropdownOpen(true)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault();
                      if (tagInput.trim()) {
                        const exact = filteredAvailableTags.find((x) => x.name === tagInput.trim());
                        if (exact) addTag(exact);
                        else addCustomTag(tagInput);
                      }
                    } else if (e.key === "Escape") {
                      setTagDropdownOpen(false);
                    }
                  }}
                  className="w-full pl-3.5 pr-2.5 h-10 bg-background border border-line rounded-lg text-emphasis text-xs placeholder-gray-500 focus:outline-none focus:border-emerald-500/50"
                />
                {tagDropdownOpen && tagInput.trim() && (
                  <div className="absolute top-full mt-2 left-0 right-0 bg-surface/95 backdrop-blur-2xl border border-line rounded-card shadow-elevated z-50 isolate animate-slide-up max-h-40 overflow-y-auto divide-y divide-line-subtle">
                    {filteredAvailableTags.slice(0, 8).map((ftag) => (
                      <button
                        key={ftag.id}
                        type="button"
                        onClick={() => addTag(ftag)}
                        className="w-full px-3 py-2 text-left hover:bg-emphasis/[0.04] flex items-center justify-between text-xs text-text-body hover:text-emphasis"
                      >
                        <span className="flex items-center gap-1.5">
                          <TagIcon className="w-3 h-3 text-text-faint" />
                          {ftag.name}
                        </span>
                      </button>
                    ))}
                    {tagInput.trim() && !availableTags.some((entry) => entry.name === tagInput.trim()) && (
                      <button
                        type="button"
                        onClick={() => addCustomTag(tagInput)}
                        className="w-full px-3 py-2 text-left hover:bg-emerald-500/10 flex items-center gap-1.5 text-xs text-success-soft"
                      >
                        <Check className="w-3 h-3" />
                        {t("community.createTag", { name: tagInput.trim() })}
                      </button>
                    )}
                  </div>
                )}
              </div>
            </div>
          </div>

          </div>

          <MarkdownToolbar textareaRef={contentRef} onInsert={setContentFromToolbar} t={t} />

          <ComposerEditor contentRef={contentRef} content={newContent} onChange={setNewContent} placeholder={t("community.contentPlaceholder")} t={t} />

          <div className="flex items-center justify-end pt-2.5 border-t border-line shrink-0">
            <div className="flex items-center gap-2.5">
              <button
                type="button"
                onClick={() => {
                  clearDraft();
                  onClose();
                }}
                className="px-4 h-10 rounded-lg bg-surface hover:bg-surfaceBorder text-text-muted hover:text-emphasis text-sm transition-colors duration-fast ease-soft cursor-pointer"
              >
                {t("community.discard")}
              </button>
              <button
                type="submit"
                disabled={submitting || !selectedBoardObj}
                className="px-5 h-10 rounded-lg bg-primary hover:opacity-90 text-white font-semibold flex items-center gap-2 transition-colors duration-fast ease-soft disabled:opacity-50 text-sm shadow-xs cursor-pointer"
              >
                <Send className="w-4 h-4" />
                <span>{submitting ? t("community.creating") : t("community.createTopic")}</span>
              </button>
            </div>
          </div>
        </form>
      ) : (
        <form onSubmit={handleReply} className="flex-1 min-h-0 flex flex-col p-4 gap-3.5 overflow-y-auto">
          {/* Quoted banner */}
          {activeQuotedPost && (
            <div className="flex items-center justify-between px-3.5 py-2.5 rounded-lg bg-amber-500/10 border border-amber-500/30 text-warn-soft text-xs sm:text-sm shrink-0">
              <span className="flex items-center gap-2">
                <Quote className="w-4 h-4" />
                <span>
                  {t("community.replyingTo", { n: activeQuotedPost.post_number })}
                  {activeQuotedPost.username ? ` · @${activeQuotedPost.username}` : ""}
                </span>
              </span>
              <button
                type="button"
                onClick={() => setActiveQuotedPost(null)}
                className="px-2.5 py-1 rounded bg-amber-500/20 hover:bg-amber-500/30 text-warn-soft text-xs font-mono cursor-pointer"
              >
                {t("community.cancelReply")}
              </button>
            </div>
          )}

          <MarkdownToolbar textareaRef={contentRef} onInsert={setContentFromToolbar} t={t} />

          <ComposerEditor contentRef={contentRef} content={newContent} onChange={setNewContent} placeholder={t("community.replyPlaceholder")} t={t} />

          <div className="flex items-center justify-end pt-2.5 border-t border-line shrink-0">
            <div className="flex items-center gap-2.5">
              <button
                type="button"
                onClick={() => {
                  clearDraft();
                  onClose();
                }}
                className="px-4 h-10 rounded-lg bg-surface hover:bg-surfaceBorder text-text-muted hover:text-emphasis text-sm transition-colors duration-fast ease-soft cursor-pointer"
              >
                {t("community.discard")}
              </button>
              <button
                type="submit"
                disabled={submitting}
                className="px-5 h-10 rounded-lg bg-primary hover:opacity-90 text-white font-semibold flex items-center gap-2 transition-colors duration-fast ease-soft disabled:opacity-50 text-sm shadow-xs cursor-pointer"
              >
                <Send className="w-4 h-4" />
                <span>{submitting ? t("community.creating") : t("community.reply")}</span>
              </button>
            </div>
          </div>
        </form>
      )}
    </div>
    </PageContainer>
  );
}

export type { Mode, QuotedPost, PostComposerProps };
