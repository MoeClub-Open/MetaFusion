"use client";

import { NodeViewWrapper, type NodeViewProps } from "@tiptap/react";
import { useI18n } from "@/i18n/I18nProvider";
import MarkdownRenderer from "@/components/MarkdownRenderer";

export function ArticleFootnoteView({ node, editor, getPos, extension }: NodeViewProps) {
  const { t } = useI18n();
  const raw = String(node.attrs.raw || "");
  const match = /^\[\^([^\]]+)\]:\s*([\s\S]*)$/.exec(raw);
  const body = match?.[2]?.replace(/\n {4}/g, "\n") || raw;
  return <NodeViewWrapper contentEditable={false} className="article-footnote-card">
    <div className="mb-2 flex items-center justify-between gap-3 text-xs text-text-muted">
      <span>{t("catalog.article.tools.footnote")} · {match?.[1]}</span>
      <button type="button" onMouseDown={(event) => event.preventDefault()} aria-label={`${t("common.edit")} ${t("catalog.article.tools.footnote")} · ${match?.[1] || ""}`}
        onClick={() => { const position = getPos(); if (typeof position === "number") { editor.commands.setNodeSelection(position); extension.options.onEdit?.(raw, position); } }}>
        {t("common.edit")}
      </button>
    </div>
    <MarkdownRenderer content={body} />
  </NodeViewWrapper>;
}
