import { unified } from "unified";
import remarkParse from "remark-parse";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import { toString } from "mdast-util-to-string";
import type { Nodes, Root } from "mdast";

export function normalizeLatexDelimiters(text: string): string {
  if (!text) return "";
  return text.split(/(```[\s\S]*?```|`[^`\n]+`)/g).map((part, index) => index % 2 ? part : part
    .replace(/\\\[([\s\S]*?)\\\]/g, (_, formula) => `$$${formula}$$`)
    .replace(/\\\(([\s\S]*?)\\\)/g, (_, formula) => `$${formula}$`)).join("");
}

export interface ArticleHeading { id: string; text: string; depth: number }
const headingPunctuation = new RegExp("[^\\p{L}\\p{N}\\p{M}_\\s-]", "gu");

// The outline and rendered document use the same Markdown AST and slug rules.
// Fenced code, Setext headings, formatted titles and duplicate names stay aligned.
function annotateHeadings(tree: Root, prefix: string): ArticleHeading[] {
  const headings: ArticleHeading[] = [];
  const used = new Set<string>();
  const walk = (node: Nodes) => {
    if (node.type === "footnoteDefinition") return;
    if (node.type === "heading") {
      const text = toString(node).trim();
      const slug = text.normalize("NFC").toLowerCase().replace(headingPunctuation, "").trim().replace(/\s+/g, "-") || "section";
      const base = `${prefix}-section-${slug}`;
      let id = base;
      for (let suffix = 2; used.has(id); suffix++) id = `${base}-${suffix}`;
      used.add(id);
      node.data = { ...node.data, hProperties: { ...(node.data?.hProperties as Record<string, unknown>), id } };
      headings.push({ id, text, depth: node.depth });
    }
    if ("children" in node) node.children.forEach(walk);
  };
  walk(tree);
  return headings;
}

const articleParser = unified().use(remarkParse).use(remarkGfm).use(remarkMath, { singleDollarTextMath: true });

export function articleHeadings(content: string, prefix = "article"): ArticleHeading[] {
  return annotateHeadings(articleParser.parse(normalizeLatexDelimiters(content)), prefix);
}

export function remarkArticleHeadings({ prefix }: { prefix: string }) {
  return (tree: Root) => { if (prefix) annotateHeadings(tree, prefix); };
}
