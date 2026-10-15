import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { I18nProvider } from "../src/i18n/I18nProvider";
import { CatalogArticle } from "../src/components/catalog/CatalogArticle";
import { articleHeadings } from "../src/lib/markdown";
import { articleBodyError, MAX_ARTICLE_LENGTH, pickArticleTranslation, ARTICLE_ANCHOR_TABS } from "../src/lib/catalogArticle";
import { detailTabFromHash } from "../src/components/catalog/DetailTabs";
import { revisionChanges, prepareRevisionRestore } from "../src/components/catalog/revisionData";
import { emptyEntity } from "../src/components/catalog/api";
import { matchesEntityQuery } from "../src/lib/entitySearch";

test("article outline follows Markdown syntax, formatted headings and colliding names", () => {
  const content = "# **创作背景**\n\n```md\n## not a heading\n```\n\n发行沿革\n---\n\n## 创作背景\n\n## 创作背景-2\n\n## !!!";
  assert.deepEqual(articleHeadings(content), [
    { id: "article-section-创作背景", text: "创作背景", depth: 1 },
    { id: "article-section-发行沿革", text: "发行沿革", depth: 2 },
    { id: "article-section-创作背景-2", text: "创作背景", depth: 2 },
    { id: "article-section-创作背景-2-2", text: "创作背景-2", depth: 2 },
    { id: "article-section-section", text: "!!!", depth: 2 },
  ]);
});

test("rendered article keeps outline links, footnotes, tables and canonical links while rejecting executable HTML", () => {
  const content = "## Background\nArticle[^official]\n\n## Editions\n[Related](/catalog/123)\n\n| Edition | Notes |\n| --- | --- |\n| First | Original |\n\n[^official]: [Source](https://example.org/source)\n\n<script>alert(1)</script>\n\n[bad](javascript:alert(1))";
  const html = renderToStaticMarkup(createElement(I18nProvider, { initialLocale: "en-US", children: createElement(CatalogArticle, { content }) }));
  for (const heading of articleHeadings(content)) {
    assert.ok(html.includes(`id="${heading.id}"`));
    assert.ok(html.includes(`href="#${heading.id}"`));
  }
  for (const expected of ["Contents", "References", 'href="#article-fn-official"', 'href="/catalog/123"', "<table", 'href="https://example.org/source"']) assert.ok(html.includes(expected), expected);
  assert.doesNotMatch(html, /<script|href="javascript:/);
});

test("article language selection skips empty bodies and honors title language preferences", () => {
  const entity = { original_language: "ja", translations: { "zh-CN": { title: "中文", body: " " }, ja: { title: "日本語", body: "日本語の本文" }, "en-US": { title: "English", body: "English article" } } };
  assert.equal(pickArticleTranslation(entity, "zh-CN", [])?.locale, "ja");
  assert.equal(pickArticleTranslation(entity, "en-US", [])?.locale, "en-US");
  assert.equal(pickArticleTranslation(entity, "en-US", ["ja-JP"])?.locale, "ja");
  assert.equal(pickArticleTranslation({ original_language: "en", translations: { en: { title: "No body" } } }, "en-US"), undefined);
});

test("article and footnote permalinks select the overview without swallowing other tabs", () => {
  for (const fragment of ["#article-background", "#article-fn-official", "#article-%E5%88%9B%E4%BD%9C%E8%83%8C%E6%99%AF"]) {
    assert.equal(detailTabFromHash(fragment, ["overview", "revisions"], ARTICLE_ANCHOR_TABS), "overview");
  }
  assert.equal(detailTabFromHash("#revisions", ["overview", "revisions"], ARTICLE_ANCHOR_TABS), "revisions");
  assert.equal(detailTabFromHash("#%ZZ", ["overview"], ARTICLE_ANCHOR_TABS), "");
  assert.equal(detailTabFromHash("#article-background", ["revisions"], ARTICLE_ANCHOR_TABS), "");
});

test("article validation counts Unicode characters and rejects nulls", () => {
  assert.equal(articleBodyError("🎵".repeat(MAX_ARTICLE_LENGTH)), undefined);
  assert.equal(articleBodyError("🎵".repeat(MAX_ARTICLE_LENGTH + 1)), "article_too_long");
  assert.equal(articleBodyError("text\0text"), "invalid_article_body");
});

test("local filtering keeps records matched only by article text", () => {
  assert.equal(matchesEntityQuery({ title: "Entry", translations: { en: { title: "Entry", body: "Documented release history" } } }, "release history"), true);
});

test("article revisions identify the exact locale field and restore text alongside structured facts", () => {
  const current = { ...emptyEntity(), id: "entry", version: 4, state: "active", translations: { en: { title: "Entry", summary: "Summary", body: "new body" }, ja: { title: "項目", body: "日本語" } }, attributes: { language: "en" } };
  const historical = { ...current, version: 2, translations: { ...current.translations, en: { ...current.translations.en, body: "old body" } }, attributes: { language: "ja" } };
  const diff = revisionChanges(historical, current);
  assert.deepEqual(diff["translations.en.body"], { old: "old body", new: "new body" });
  assert.equal(diff["translations.en.title"], undefined);
  const restored = prepareRevisionRestore(current, historical);
  assert.equal(restored.version, 4);
  assert.equal(restored.state, "active");
  assert.equal(restored.translations.en.body, "old body");
  assert.equal(restored.translations.ja.body, "日本語");
  assert.equal(restored.attributes.language, "ja");
  assert.equal(current.translations.en.body, "new body");
});
