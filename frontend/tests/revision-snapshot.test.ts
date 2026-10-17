import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { I18nProvider } from "../src/i18n/I18nProvider";
import { RevisionSnapshotContent } from "../src/components/catalog/RevisionSnapshotView";
import type { DynamicDefinitions } from "../src/lib/definitions";
import { emptyEntity } from "../src/components/catalog/api";

const definitions: DynamicDefinitions = {
  fields: {
    format: { names: { "en-US": "Historical format" }, type: "enum", vocabulary: "format", enabled: true },
    nested: { names: { "en-US": "Archived details" }, type: "list", enabled: true, items: {
      names: {}, type: "group", enabled: true, fields: {
        entity: { names: { "en-US": "Related entry" }, type: "entity", enabled: true },
        hidden_value: { names: { "en-US": "Archived source" }, type: "string", hidden: true, enabled: false },
      },
    } },
    website: { names: { "en-US": "Website" }, type: "url", enabled: true },
    subject_attributes: { names: {}, type: "group", enabled: true, fields: {
      note: { names: { "en-US": "Release note" }, type: "string", enabled: true },
    } },
    locator: { names: {}, type: "group", enabled: true, fields: {
      page: { names: { "en-US": "Page" }, type: "number", enabled: true },
    } },
  },
  vocabularies: {
    format: { names: {}, terms: { compact: { names: { "en-US": "Compact disc" }, enabled: true } } },
    release_role: { names: {}, terms: { main: { names: { "en-US": "Main content" }, enabled: true } } },
  },
  templates: {}, relations: {}, schemes: {}, structure: {
    release: { fields: [{ code: "parent_id", names: { "en-US": "Historical parent" }, reverse_names: {} }] },
  },
};

function render(snapshot: unknown) {
  return renderToStaticMarkup(createElement(I18nProvider, { initialLocale: "en-US", children:
    createElement(RevisionSnapshotContent, { snapshot, definitions }) }));
}

test("historical snapshot renders saved multilingual content, Markdown and structured facts as a document", () => {
  const snapshot = {
    ...emptyEntity("release"), id: "entry", title: "Original release title", version: 2, original_language: "ja",
    translations: {
      "en-US": { title: "Saved English title", summary: "Saved introduction", aliases: ["Saved alias"], body: "## Historical article\n\n| Fact | Value |\n| --- | --- |\n| Date | 2001 |\n\nText[^source]\n\n[^source]: Historical source" },
      ja: { title: "保存された題名", body: "保存された本文" },
    },
    attributes: { format: "compact", nested: [{ entity: "saved-person-id", hidden_value: "Original archived value" }], unknown_record: { retired_field: "Original retired value" }, zero_value: 0, false_value: false },
    parent_id: "saved-parent-id", position: 1, number: "ABC-001",
    subjects: [{ work_id: "saved-work-id", role: "main", position: 1, attributes: { note: "Saved subject note" } }],
    contents: [{ expression_id: "saved-expression-id", position: 1, locator: { page: 8 }, sources: [{ kind: "publication", citation: "Saved inclusion source" }] }],
    pictures: [{ url: "https://example.org/historical.jpg", caption: { "en-US": "Historical cover" }, source: { kind: "publication", citation: "Historical cover source" } }],
    external_links: [{ kind: "official_website", url: "https://example.org/historical", external_id: "Archived identifier", role: "official" }],
  };
  const html = render(snapshot);
  for (const saved of ["Original release title", "Saved English title", "Saved introduction", "Saved alias", "Historical article", "保存された題名", "保存された本文", "Compact disc", "Archived details", "Original archived value", "Original retired value", "Historical parent", "Main content", "Saved subject note", "Saved inclusion source", "ABC-001", "Historical cover", "Historical cover source", "Archived identifier"]) {
    assert.ok(html.includes(saved), saved);
  }
  for (const id of ["saved-parent-id", "saved-person-id", "saved-work-id", "saved-expression-id"]) assert.ok(html.includes(`href="/catalog/${id}"`), id);
  assert.ok(html.includes("<table"));
  assert.ok(html.includes(">Active<"));
  assert.doesNotMatch(html, />Reviewed</);
  assert.ok(html.includes('id="revision-2-en-US-fn-source"'));
  assert.doesNotMatch(html, /<pre|<button|<textarea|<input/);
  assert.doesNotMatch(html, /&quot;attributes&quot;|&quot;translations&quot;/);
  assert.equal(snapshot.attributes.nested[0].hidden_value, "Original archived value");
});

test("each version renders its own content without filling missing fields from another version", () => {
  const first = render({ ...emptyEntity(), title: "First version", version: 1, attributes: { archived: "First fact" } });
  const second = render({ ...emptyEntity(), title: "Second version", version: 2, translations: { en: { title: "Second title", body: "Second body" } } });
  assert.ok(first.includes("First fact"));
  assert.doesNotMatch(first, /Second title|Second body/);
  assert.ok(second.includes("Second body"));
  assert.doesNotMatch(second, /First fact|First version/);
});

test("missing snapshot does not treat revision metadata as an entity or invent an empty current record", () => {
  for (const value of [undefined, null, [], {}, { version: 3, actor_name: "Editor", summary: "Edit summary" }]) {
    const html = render(value);
    assert.doesNotMatch(html, /<pre|<h4|Editor|Edit summary/);
    assert.ok(html.includes("This revision has no entry snapshot to display."));
  }
});

test("historical URLs and Markdown cannot emit executable links or HTML", () => {
  const html = render({ ...emptyEntity(), title: "Historical entry", attributes: { website: "javascript:alert(1)" },
    translations: { en: { title: "Saved", body: "<script>alert(1)</script>\n\n[bad](javascript:alert(1))" } },
    pictures: [{ url: "javascript:alert(1)", caption: {} }], external_links: [{ kind: "old", url: "javascript:alert(1)", external_id: "Saved external ID" }] });
  assert.doesNotMatch(html, /<script|href="javascript:|src="javascript:/);
  assert.ok(html.includes("Saved external ID"));
});
