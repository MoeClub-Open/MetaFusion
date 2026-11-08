"use client";

import React, { useState, useMemo } from "react";
import Link from "next/link";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import rehypeHighlight from "rehype-highlight";
import { Check, Code2, Copy, Eye, ExternalLink } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { parseGeneratedBlocks } from "@/lib/generatedBlocks";
import { GeneratedBlocksView } from "@/components/assistant/GeneratedBlocks";
import { normalizeLatexDelimiters, remarkArticleHeadings } from "@/lib/markdown";
export { normalizeLatexDelimiters } from "@/lib/markdown";

import "katex/dist/katex.min.css";
// 语法高亮主题在仓库内，跟随主题变量（浅色不再是一块写死的黑板）：见 markdown-highlight.css
import "./markdown-highlight.css";

interface MarkdownRendererProps {
  content: string;
  className?: string;
  compact?: boolean;
  interactive?: boolean;
  headingPrefix?: string;
  footnoteLabel?: string;
  footnoteBackLabel?: string;
}

const ENTITY_PATH_RE = /^(?:https?:\/\/(?:www\.)?(?:findverse\.cc|metafusion\.cc))?\/(?:catalog|agent|collection|work|content_unit|content-unit|expression|release|medium|track)\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:[/?#].*)?$/i;

function buildInteractiveSrcDoc(code: string, isSvg: boolean): string {
  const body = isSvg ? `<div style="display:flex;justify-content:center;align-items:center;min-height:160px;">${code}</div>` : code;
  return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>:root{color-scheme:light dark;}body{margin:0;padding:12px;font-family:system-ui,-apple-system,sans-serif;font-size:13px;line-height:1.5;color:#1f2937;background:transparent;}@media (prefers-color-scheme:dark){body{color:#e5e7eb;}}a{color:#2563eb;text-decoration:none;}a:hover{text-decoration:underline;}table{border-collapse:collapse;width:100%;}th,td{border:1px solid rgba(128,128,128,0.25);padding:6px 10px;text-align:left;}</style></head><body>${body}</body></html>`;
}

function CodeBlockWrapper({
  language,
  code,
  interactive = false,
  children,
}: {
  language?: string;
  code: string;
  interactive?: boolean;
  children: React.ReactNode;
}) {
  const { t, tr } = useI18n();
  const [copied, setCopied] = useState(false);
  const lang = (language || "").toLowerCase();
  const uiBlocks = useMemo(() => {
    if (lang !== "ui" && lang !== "metafusion-ui" && !(interactive && lang === "json")) return [];
    try {
      const raw = JSON.parse(code);
      const arr = Array.isArray(raw) ? raw : raw && typeof raw === "object" ? [raw] : [];
      return parseGeneratedBlocks(arr);
    } catch {
      return [];
    }
  }, [code, interactive, lang]);
  const isHtmlPreview = interactive && (lang === "html" || lang === "svg") && code.trim().startsWith("<");
  const hasPreview = uiBlocks.length > 0 || isHtmlPreview;
  const [showCode, setShowCode] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(code);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // ignore
    }
  };

  return (
    <div className="relative group my-3 rounded-lg overflow-hidden border border-line bg-background shadow-xs">
      <div className="flex items-center justify-between px-3 py-1.5 bg-surfaceSubtle border-b border-line-subtle text-xs text-text-muted font-mono select-none">
        <div className="flex items-center gap-2">
          <span className="text-[11px] font-semibold text-text-muted uppercase tracking-wider">{hasPreview ? tr("assistantUI.interactivePreview", "Interactive View") : (language || "code")}</span>
          {hasPreview && (
            <button
              type="button"
              onClick={() => setShowCode((v) => !v)}
              className="inline-flex items-center gap-1 rounded border border-line bg-surface px-2 py-0.5 font-sans text-[11px] text-text-body hover:text-primary"
            >
              {showCode ? <Eye className="w-3 h-3" /> : <Code2 className="w-3 h-3" />}
              <span>{showCode ? tr("assistantUI.interactivePreview", "Interactive View") : tr("assistantUI.viewCode", "Source")}</span>
            </button>
          )}
        </div>
        <button
          type="button"
          onClick={handleCopy}
          className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-emphasis/5 hover:bg-emphasis/15 text-text-body hover:text-emphasis transition-colors duration-fast ease-soft cursor-pointer text-[11px]"
          title={t("common.copy")}
        >
          {copied ? (
            <>
              <Check className="w-3 h-3 text-success" />
              <span className="text-success font-medium">{t("common.copied")}</span>
            </>
          ) : (
            <>
              <Copy className="w-3 h-3" />
              <span>{t("common.copy")}</span>
            </>
          )}
        </button>
      </div>
      {hasPreview && !showCode ? (
        <div className="p-3 bg-surface">
          {uiBlocks.length > 0 ? (
            <GeneratedBlocksView blocks={uiBlocks} />
          ) : (
            <iframe
              title={tr("assistantUI.interactivePreview", "Interactive View")}
              sandbox="allow-scripts"
              srcDoc={buildInteractiveSrcDoc(code, lang === "svg")}
              className="w-full min-h-[16rem] rounded border border-line-subtle bg-background"
            />
          )}
        </div>
      ) : (
        <div className="p-3.5 overflow-x-auto text-xs sm:text-sm font-mono text-text-strong leading-relaxed scrollbar-thin">
          {children}
        </div>
      )}
    </div>
  );
}

export default function MarkdownRenderer({
  content,
  className = "",
  compact = false,
  interactive = false,
  headingPrefix,
  footnoteLabel,
  footnoteBackLabel,
}: MarkdownRendererProps) {
  const normalizedContent = useMemo(() => normalizeLatexDelimiters(content), [content]);

  return (
    <div
      className={`markdown-body text-text-strong break-words leading-relaxed text-sm ${
        compact ? "compact-markdown space-y-1.5" : "space-y-2.5"
      } ${className}`}
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm, [remarkMath, { singleDollarTextMath: true }], [remarkArticleHeadings, { prefix: headingPrefix || "" }]]}
        remarkRehypeOptions={{ ...(headingPrefix ? { clobberPrefix: `${headingPrefix}-` } : {}), ...(footnoteLabel ? { footnoteLabel } : {}), ...(footnoteBackLabel ? { footnoteBackLabel } : {}) }}
        rehypePlugins={[rehypeKatex, [rehypeHighlight, { ignoreMissing: true }]]}
        components={{
          h1: ({ ...props }) => (
            <h1
              className="text-lg sm:text-xl font-bold text-text-strong mt-4 mb-2 pb-1.5 border-b border-line tracking-tight"
              {...props}
            />
          ),
          h2: ({ ...props }) => (
            <h2
              className="text-base sm:text-lg font-bold text-text-strong mt-3.5 mb-1.5 pb-1 border-b border-line-subtle tracking-tight"
              {...props}
            />
          ),
          h3: ({ ...props }) => (
            <h3 className="text-sm sm:text-base font-semibold text-text-strong mt-3 mb-1" {...props} />
          ),
          h4: ({ ...props }) => (
            <h4 className="text-xs sm:text-sm font-semibold text-text-strong mt-2 mb-1" {...props} />
          ),
          p: ({ ...props }) => <p className="my-1.5 leading-relaxed" {...props} />,
          ul: ({ ...props }) => <ul className="list-disc list-outside pl-5 my-2 space-y-1" {...props} />,
          ol: ({ ...props }) => <ol className="list-decimal list-outside pl-5 my-2 space-y-1" {...props} />,
          li: ({ ...props }) => <li className="leading-relaxed" {...props} />,
          blockquote: ({ ...props }) => (
            <blockquote
              className="border-l-2 border-emerald-500/70 dark:border-emerald-500/50 bg-emerald-500/5 dark:bg-emerald-500/10 px-3.5 py-1.5 my-2 rounded-r text-text-body italic"
              {...props}
            />
          ),
          hr: ({ ...props }) => <hr className="my-3.5 border-line" {...props} />,
          table: ({ ...props }) => (
            <div className="overflow-x-auto my-3 rounded-lg border border-line">
              <table className="min-w-full divide-y divide-black/10 dark:divide-white/10 text-left text-xs sm:text-sm" {...props} />
            </div>
          ),
          thead: ({ ...props }) => (
            <thead className="bg-black/5 dark:bg-white/5 font-semibold text-text-strong" {...props} />
          ),
          tbody: ({ ...props }) => (
            <tbody className="divide-y divide-black/5 dark:divide-white/5 bg-transparent" {...props} />
          ),
          tr: ({ ...props }) => (
            <tr className="hover:bg-surfaceSubtle transition-colors duration-fast ease-soft" {...props} />
          ),
          th: ({ ...props }) => <th className="px-3 py-2 font-semibold text-text-strong" {...props} />,
          td: ({ ...props }) => <td className="px-3 py-2 text-text-strong" {...props} />,
          a: ({ href, children, ...props }) => {
            const rawHref = href ? href.trim() : "#";
            const lowerHref = rawHref.toLowerCase();
            const isUnsafe =
              lowerHref.startsWith("javascript:") ||
              lowerHref.startsWith("vbscript:") ||
              lowerHref.startsWith("data:") ||
              lowerHref.startsWith("file:");
            const safeHref = isUnsafe ? "#" : rawHref;
            const entityMatch = !isUnsafe ? ENTITY_PATH_RE.exec(safeHref) : null;
            if (entityMatch) {
              const canonicalPath = `/catalog/${entityMatch[1].toLowerCase()}`;
              const childText = Array.isArray(children) && children.length === 1 && typeof children[0] === "string"
                ? children[0]
                : typeof children === "string" ? children : null;
              const displayChildren = childText && ENTITY_PATH_RE.test(childText.trim())
                ? childText.replace(/\/(?:agent|collection|work|content_unit|content-unit|expression|release|medium|track)\//i, "/catalog/")
                : children;
              return (
                <Link
                  href={canonicalPath}
                  className="text-primary hover:underline underline-offset-2 font-medium transition-colors duration-fast ease-soft inline-flex items-center gap-0.5"
                >
                  <span>{displayChildren}</span>
                </Link>
              );
            }
            const isExternal = safeHref.startsWith("http://") || safeHref.startsWith("https://");
            return (
              <a
                href={safeHref}
                target={isExternal ? "_blank" : undefined}
                rel={isExternal ? "noopener noreferrer" : undefined}
                className="text-primary hover:underline underline-offset-2 font-medium transition-colors duration-fast ease-soft inline-flex items-center gap-0.5"
                {...props}
              >
                <span>{children}</span>
                {isExternal && <ExternalLink className="w-3 h-3 opacity-60 inline-block shrink-0" />}
              </a>
            );
          },
          img: ({ ...props }) => (
            <img
              className="rounded-lg max-h-96 max-w-full object-contain my-2.5 border border-line shadow-xs"
              loading="lazy"
              {...props}
            />
          ),
          del: ({ ...props }) => <del className="line-through text-text-muted" {...props} />,
          code: ({ node, className, children, ...props }: any) => {
            const match = /language-(\w+)/.exec(className || "");
            const isMultiLine = String(children).includes("\n");
            if (match || isMultiLine) {
              const language = match ? match[1] : "";
              const codeString = String(children).replace(/\n$/, "");
              return (
                <CodeBlockWrapper language={language} code={codeString} interactive={interactive}>
                  <pre className="!bg-transparent !p-0 !m-0 overflow-x-auto">
                    <code className={className} {...props}>
                      {children}
                    </code>
                  </pre>
                </CodeBlockWrapper>
              );
            }
            return (
              <code
                className="px-1.5 py-0.5 rounded dark:bg-white/10 text-amber-600 dark:text-warn font-mono text-[0.875em] break-all border border-line-subtle"
                {...props}
              >
                {children}
              </code>
            );
          },
        }}
      >
        {normalizedContent}
      </ReactMarkdown>
    </div>
  );
}
