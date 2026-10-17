import { Node, type AnyExtension } from "@tiptap/core";
import { ReactNodeViewRenderer } from "@tiptap/react";
import StarterKit from "@tiptap/starter-kit";
import { Markdown } from "@tiptap/markdown";
import { TableKit } from "@tiptap/extension-table";
import Image from "@tiptap/extension-image";
import Mathematics from "@tiptap/extension-mathematics";
import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import { ArticleFootnoteView } from "@/components/catalog/ArticleFootnoteView";

const FootnoteReference = Node.create({
  name: "articleFootnoteReference", inline: true, group: "inline", atom: true,
  addAttributes: () => ({ label: { default: "" } }),
  parseHTML: () => [{ tag: "sup[data-footnote-reference]", getAttrs: (element) => ({ label: element.getAttribute("data-footnote-reference") }) }],
  renderHTML: ({ node }) => ["sup", { "data-footnote-reference": node.attrs.label }, `[${node.attrs.label}]`],
  parseMarkdown: (token) => ({ type: "articleFootnoteReference", attrs: { label: token.label } }),
  renderMarkdown: (node) => `[^${node.attrs?.label || ""}]`,
  markdownTokenizer: {
    name: "articleFootnoteReference", level: "inline", start: (source) => source.indexOf("[^"),
    tokenize: (source) => {
      const match = /^\[\^([^\]\s]+)\]/.exec(source);
      return match ? { type: "articleFootnoteReference", raw: match[0], label: match[1] } : undefined;
    },
  },
});

const FootnoteDefinition = Node.create({
  name: "articleFootnoteDefinition", group: "block", atom: true,
  addOptions: () => ({ onEdit: undefined as ((raw: string, position: number) => void) | undefined }),
  addNodeView: () => ReactNodeViewRenderer(ArticleFootnoteView),
  addAttributes: () => ({ raw: { default: "" } }),
  parseHTML: () => [{ tag: "pre[data-footnote-definition]", getAttrs: (element) => ({ raw: element.textContent || "" }) }],
  renderHTML: ({ node }) => ["pre", { "data-footnote-definition": "", class: "article-footnote-definition" }, node.attrs.raw],
  parseMarkdown: (token) => ({ type: "articleFootnoteDefinition", attrs: { raw: String(token.raw || "").trimEnd() } }),
  renderMarkdown: (node) => node.attrs?.raw || "",
  markdownTokenizer: {
    name: "articleFootnoteDefinition", level: "block", start: (source) => source.search(/^\[\^[^\]\s]+\]:/m),
    tokenize: (source) => {
      if (!/^\[\^[^\]\s]+\]:/.test(source)) return undefined;
      const lines = source.split("\n");
      let end = 1;
      while (end < lines.length) {
        if (/^( {4}|\t)/.test(lines[end])) { end++; continue; }
        if (!lines[end].trim()) {
          let next = end + 1;
          while (next < lines.length && !lines[next].trim()) next++;
          if (next < lines.length && /^( {4}|\t)/.test(lines[next])) { end = next; continue; }
        }
        break;
      }
      return { type: "articleFootnoteDefinition", raw: lines.slice(0, end).join("\n") };
    },
  },
});

export function articleRichExtensions(onMath?: (latex: string, position: number, block: boolean) => void,
  onFootnote?: (raw: string, position: number) => void): AnyExtension[] {
  return [StarterKit.configure({ heading: { levels: [1, 2, 3, 4, 5, 6] }, link: { openOnClick: false, defaultProtocol: "https" } }),
    TableKit.configure({ table: { resizable: false } }), Image.configure({ inline: true, allowBase64: false }),
    FootnoteDefinition.configure({ onEdit: onFootnote }), FootnoteReference,
    Mathematics.configure({ katexOptions: { throwOnError: false, trust: false },
      inlineOptions: { onClick: (node, position) => onMath?.(String(node.attrs.latex), position, false) },
      blockOptions: { onClick: (node, position) => onMath?.(String(node.attrs.latex), position, true) } }), Markdown];
}

const parser = unified().use(remarkParse).use(remarkGfm).use(remarkMath);

export function articleFootnoteExists(source: string, label: string) {
  const normalize = (value: string) => value.trim().replace(/\s+/g, " ").toLocaleLowerCase();
  const wanted = normalize(label);
  const visit = (node: { type: string; identifier?: string; children?: unknown[] }): boolean =>
    (node.type === "footnoteDefinition" && normalize(node.identifier || "") === wanted)
    || !!node.children?.some((child) => visit(child as typeof node));
  return visit(parser.parse(source));
}

// Opening or switching modes must never silently discard unsupported Markdown.
// Formatting whitespace can change, but the complete parsed content must agree.
export function articleRoundTripSafe(source: string, serialized: string) {
  const normalize = (text: string) => JSON.stringify(parser.parse(text), (key, value) => key === "position" || key === "data" ? undefined : value);
  try { return normalize(source) === normalize(serialized); } catch { return false; }
}

export function articleInsertionURL(value: string, image = false) {
  const raw = value.trim();
  if (/^\/(?!\/)/.test(raw)) return raw;
  try {
    const url = new URL(raw);
    return (url.protocol === "https:" || url.protocol === "http:" || (!image && url.protocol === "mailto:")) ? raw : null;
  } catch { return null; }
}
