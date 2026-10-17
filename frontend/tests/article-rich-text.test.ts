import { test } from "node:test";
import assert from "node:assert/strict";
import { MarkdownManager } from "@tiptap/markdown";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import ReactMarkdown from "react-markdown";
import remarkMath from "remark-math";
import rehypeKatex from "rehype-katex";
import { articleRichExtensions, articleRoundTripSafe, articleInsertionURL, articleFootnoteExists } from "../src/lib/articleRichText";

test("shared KaTeX renders inline and block Markdown without enabling trusted commands", () => {
  const render = (source: string) => renderToStaticMarkup(createElement(ReactMarkdown, {
    remarkPlugins: [remarkMath], rehypePlugins: [rehypeKatex], children: source,
  }));
  const html = render("Inline $x^2$\n\n$$\n\\frac{1}{2}\n$$");
  assert.match(html, /class="katex"/);
  assert.match(html, /class="katex-display"/);
  assert.match(html, /<math/);
  const untrusted = render("$\\href{javascript:alert(1)}{click}$\n\n$\\htmlClass{injected}{x}$");
  assert.doesNotMatch(untrusted, /<a\b|class="injected"/);
});

test("visual Markdown preserves GFM tables, images, canonical links, formulas and multiline footnotes", () => {
  const markdown = new MarkdownManager({ extensions: articleRichExtensions() });
  const examples = [
    "## Background\n\n**Bold** and *italic* with [entity](/catalog/123).\n\n- One\n- Two",
    "| Edition | Notes |\n| --- | --- |\n| First | Original |",
    "![Cover](/api/storage/assets/123/content)",
    "Inline $x^2 + y^2$\n\n$$\nx = \\frac{1}{2}\n$$",
    "Source[^official].\n\n[^official]: [Official](https://example.org/source)\n\n    A second paragraph with **details**.",
    "First[^a] and second[^b].\n\n[^a]: One.\n[^b]: Two.",
    "Source[^官方].\n\n[^官方]: Evidence.",
    "```js\nconst value = '$literal';\n```",
  ];
  for (const source of examples) {
    const serialized = markdown.serialize(markdown.parse(source));
    assert.ok(articleRoundTripSafe(source, serialized), `${source}\n=>\n${serialized}`);
  }
});

test("footnote identities follow Markdown case normalization, ignoring code examples", () => {
  const source = "Source[^Official].\n\n[^Official]: A source.\n\n```md\n[^ref1]: A code example only.\n```";
  assert.ok(articleFootnoteExists(source, "official"));
  assert.ok(articleFootnoteExists(source, " Official "));
  assert.equal(articleFootnoteExists(source, "ref1"), false);
});

test("unsupported or damaged content cannot pass the mode-switch fidelity check", () => {
  assert.equal(articleRoundTripSafe("Text[^ref]\n\n[^ref]: Evidence", "Text"), false);
  assert.equal(articleRoundTripSafe("$x+y$", "x+y"), false);
  assert.equal(articleRoundTripSafe("| A | B |\n| - | - |\n| 1 | 2 |", "A B 1 2"), false);
  assert.equal(articleRoundTripSafe("<details>Evidence</details>", "Evidence"), false);
  assert.equal(articleRoundTripSafe("## Title\n\nText", "## Title\n\nText\n"), true);
});

test("insertions accept catalog/storage paths and public links without executable URL schemes", () => {
  for (const value of ["/catalog/123", "/api/storage/assets/123/content", "https://example.org/image.png", "mailto:contact@example.org"]) assert.equal(articleInsertionURL(value), value);
  for (const value of ["javascript:alert(1)", "data:image/svg+xml,<svg/>", "//example.org/image.png", "relative-path", "file:///secret"]) assert.equal(articleInsertionURL(value), null);
  assert.equal(articleInsertionURL("mailto:contact@example.org", true), null);
});
