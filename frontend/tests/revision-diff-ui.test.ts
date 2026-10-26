import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement, type ReactNode } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { I18nProvider } from "../src/i18n/I18nProvider";
import { RevisionCodeDiff, RevisionDiffInspector } from "../src/components/catalog/RevisionDiffInspector";
import { RevisionVisualDiff } from "../src/components/catalog/RevisionVisualDiff";
import { buildRevisionDiff } from "../src/components/catalog/revisionDiff";
import type { DynamicDefinitions } from "../src/lib/definitions";

const definitions: DynamicDefinitions = {
  fields: {
    format: { names: { "en-US": "Physical format" }, type: "enum", vocabulary: "format", enabled: true },
    archived: { names: { "en-US": "Archived facts" }, type: "list", enabled: true, items: {
      names: {}, type: "group", enabled: true, fields: {
        person: { names: { "en-US": "Saved participant" }, type: "entity", enabled: true },
        note: { names: { "en-US": "Saved source note" }, type: "string", hidden: true, enabled: false },
      },
    } },
    locator: { names: {}, type: "group", enabled: true, fields: {
      page: { names: { "en-US": "Page" }, type: "number", enabled: true },
    } },
  },
  vocabularies: { format: { names: {}, terms: {
    cd: { names: { "en-US": "Compact disc" }, enabled: true },
    vinyl: { names: { "en-US": "Vinyl record" }, enabled: true },
  } } },
  structure: { release: { fields: [{ code: "parent_id", names: { "en-US": "Saved parent" }, reverse_names: {} }] } },
  templates: {}, schemes: {}, relations: {},
};

function render(children: ReactNode) {
  return renderToStaticMarkup(createElement(I18nProvider, { initialLocale: "en-US", children }));
}

test("visual diff presents rendered articles, tables, pictures and definition-based historical facts", () => {
  const before = { kind: "release", title: "Original red title", version: 1,
    attributes: { format: "cd", archived: [{ person: "saved-person", note: "Old archived source" }] },
    translations: { en: { body: "## Previous article\n\n| Date | Fact |\n| --- | --- |\n| 2001 | Old fact |" } },
    parent_id: "saved-parent-old", pictures: [],
  };
  const after = { ...before, title: "Original blue title", version: 2,
    attributes: { format: "vinyl", archived: [{ person: "saved-person", note: "New archived source" }] },
    translations: { en: { body: "## Revised article\n\n| Date | Fact |\n| --- | --- |\n| 2002 | New fact |\n\nSource[^a]\n\n[^a]: Saved citation" } },
    parent_id: "saved-parent-new", pictures: [{ url: "https://example.org/cover.jpg", caption: { en: "Saved cover" } }],
  };
  const html = render(createElement(RevisionVisualDiff, { before, after, definitions, oldLabel: "v1", newLabel: "v2" }));
  for (const value of ["Physical format", "Compact disc", "Vinyl record", "Archived facts", "Old archived source", "New archived source", "Saved parent", "Previous article", "Revised article", "Old fact", "New fact", "Saved citation", "Saved cover"]) assert.ok(html.includes(value), value);
  assert.ok(html.includes("<table"));
  assert.ok(html.includes('src="https://example.org/cover.jpg"'));
  for (const id of ["saved-person", "saved-parent-old", "saved-parent-new"]) assert.ok(html.includes(`href="/catalog/${id}"`));
  assert.match(html, /<del[^>]*>red<\/del>/);
  assert.match(html, /<ins[^>]*>blue<\/ins>/);
  assert.doesNotMatch(html, /&quot;format&quot;|&quot;translations&quot;|>\[object Object\]<|<pre/);
  assert.equal(before.attributes.archived[0].note, "Old archived source");
});

test("visual diff keeps creation, deletion and relation previews readable without an entity schema", () => {
  const relation = { type: "member_of", source_id: "source-id", target_id: "target-id", attributes: { note: "Saved relation fact" } };
  for (const [before, after] of [[{}, relation], [relation, {}]]) {
    const html = render(createElement(RevisionVisualDiff, { before, after, definitions, oldLabel: "Before", newLabel: "After" }));
    assert.ok(html.includes("Saved relation fact"));
    assert.ok(html.includes('href="/catalog/source-id"'));
    assert.ok(html.includes('href="/catalog/target-id"'));
    assert.ok(html.includes("unset"));
    assert.doesNotMatch(html, /\[object Object\]|&quot;source_id&quot;/);
  }
});

test("code diff retains aligned context, actual changed lines, hunk positions and safe source text", () => {
  const before = { title: "A", attributes: { list: ["one", "three"], unsafe: "<script>old</script>" } };
  const after = { title: "A", attributes: { list: ["one", "two", "three"], unsafe: "<script>new</script>" } };
  const diff = buildRevisionDiff(before, after, "v1", "v2");
  const html = render(createElement(RevisionCodeDiff, { hunks: diff.hunks, oldLabel: "v1", newLabel: "v2" }));
  assert.ok(html.includes(diff.hunks[0].header));
  assert.ok(html.includes('data-diff="add"'));
  assert.ok(html.includes('data-diff="remove"'));
  assert.ok(html.includes('data-diff="context"'));
  assert.ok(html.includes("&quot;two&quot;"));
  assert.ok(html.includes("&lt;script&gt;new&lt;/script&gt;"));
  assert.doesNotMatch(html, /<script>/);
  const insertion = diff.hunks.flatMap(hunk => hunk.lines).find(line => line.content.includes('"two"'));
  assert.equal(insertion?.kind, "add");
  assert.equal(insertion.oldLine, null);
  assert.equal(typeof insertion.newLine, "number");
});

test("a missing snapshot never becomes a comparison of revision metadata or an invented creation", () => {
  for (const props of [
    { base: { version: 1, actor_name: "Should not be content" }, current: { version: 2, snapshot: { title: "Saved" } } },
    { base: null, current: { version: 1, actor_name: "Should not be content" } },
  ]) {
    const html = render(createElement(RevisionDiffInspector, props));
    assert.ok(html.includes("A revision snapshot is missing"));
    assert.doesNotMatch(html, /data-field=|data-diff=|copyDiffTitle/);
  }
  const creation = render(createElement(RevisionDiffInspector, { base: { version: 0, snapshot: {} }, current: { version: 1, snapshot: { title: "Creation" } } }));
  assert.ok(creation.includes('data-field="title"'));
  assert.doesNotMatch(creation, /A revision snapshot is missing/);
});

test("visual diff escapes historical HTML and rejects executable image and reference URLs", () => {
  const after = {
    kind: "work", title: "<script>alert(1)</script>",
    translations: { en: { body: "<script>alert(2)</script>\n\n[unsafe](javascript:alert(3))" } },
    pictures: [{ url: "javascript:alert(4)", caption: { en: "Saved caption" } }],
    external_links: [{ kind: "old", url: "javascript:alert(5)", external_id: "Saved ID" }],
  };
  const html = render(createElement(RevisionVisualDiff, { before: {}, after, definitions, oldLabel: "Before", newLabel: "After" }));
  assert.doesNotMatch(html, /<script|href="javascript:|src="javascript:/);
  // 读者面藏 external_id；编辑者仍可见
  assert.doesNotMatch(html, /Saved ID/);
  assert.ok(html.includes("Saved caption"));
  const editor = render(createElement(RevisionVisualDiff, { before: {}, after, definitions, oldLabel: "Before", newLabel: "After", allowOps: true }));
  assert.ok(editor.includes("Saved ID"));
});

test("visual diff exposes changes in every saved language, including nested multilingual attributes and image captions", () => {
  const multilingualDefinitions: DynamicDefinitions = { ...definitions, fields: { ...definitions.fields,
    credits: { names: { "en-US": "Credits" }, type: "list", enabled: true, items: { names: {}, type: "group", enabled: true, fields: {
      note: { names: { "en-US": "Credit note" }, type: "multilingual", enabled: true },
    } } },
  } };
  const before = { attributes: { credits: [{ note: { "en-US": "Same note", ja: "変更前の注記" } }] },
    pictures: [{ url: "/old.jpg", caption: { "en-US": "Same caption", ja: "変更前の説明" }, version_label: { en: "Same version", ja: "変更前の版" } }],
  };
  const after = { attributes: { credits: [{ note: { "en-US": "Same note", ja: "変更後の注記" } }] },
    pictures: [{ url: "/old.jpg", caption: { "en-US": "Same caption", ja: "変更後の説明" }, version_label: { en: "Same version", ja: "変更後の版" } }],
  };
  const html = render(createElement(RevisionVisualDiff, { before, after, definitions: multilingualDefinitions, oldLabel: "v1", newLabel: "v2" }));
  for (const value of ["変更前の注記", "変更後の注記", "変更前の説明", "変更後の説明", "変更前の版", "変更後の版"]) assert.ok(html.includes(value), value);
  assert.ok(html.includes("Same note"));
  assert.ok(html.includes("Same caption"));
});

test("reader visual diff hides Mili-style ops keys; editors still see raw", () => {
  const before = {
    kind: "person",
    title: "Mili",
    version: 2,
    external_links: [
      { kind: "netease_music_artist", external_id: "339594", origin: "migration", url: "https://music.163.com/#/artist?id=339594" },
      { kind: "official_website", origin: "migration", url: "https://projectmili.com" },
    ],
  };
  const after = {
    ...before,
    version: 3,
    external_links: [
      { kind: "apple_music_artist", external_id: "906934266", origin: "migration", url: "https://music.apple.com/jp/artist/906934266" },
      { kind: "bandcamp_artist", origin: "migration", url: "https://project-mili.bandcamp.com" },
      { kind: "official_website", origin: "migration", url: "https://projectmili.com" },
    ],
  };
  const reader = render(createElement(RevisionVisualDiff, {
    before, after, definitions, oldLabel: "v2", newLabel: "v3", allowOps: false,
  }));
  for (const leak of ["netease_music_artist", "apple_music_artist", "bandcamp_artist", "official_website", "external_id", "origin", "migration"]) {
    assert.doesNotMatch(reader, new RegExp(leak), `reader must not show ${leak}`);
  }
  assert.ok(reader.includes("music.163.com") || reader.includes("projectmili.com") || reader.includes("bandcamp.com") || reader.includes("music.apple.com"), "urls remain");

  const editor = render(createElement(RevisionVisualDiff, {
    before, after, definitions, oldLabel: "v2", newLabel: "v3", allowOps: true,
  }));
  for (const token of ["netease_music_artist", "apple_music_artist", "external_id", "origin", "migration"]) {
    assert.ok(editor.includes(token), `editor still sees ${token}`);
  }
});

test("reader RevisionDiffInspector code view also strips ops keys", () => {
  const current = {
    version: 4,
    snapshot: {
      kind: "person",
      title: "Artist",
      external_links: [
        { kind: "official_website", origin: "migration", url: "https://www.kodansha.co.jp/comic/products/0808017864" },
      ],
    },
  };
  const html = render(createElement(RevisionDiffInspector, {
    base: { version: 3, snapshot: { kind: "person", title: "Artist", external_links: [] } },
    current,
    allowOps: false,
  }));
  for (const leak of ["official_website", "origin", "migration"]) {
    assert.doesNotMatch(html, new RegExp(leak), `inspector must not show ${leak}`);
  }
  const editor = render(createElement(RevisionDiffInspector, {
    base: { version: 3, snapshot: { kind: "person", title: "Artist", external_links: [] } },
    current,
    allowOps: true,
  }));
  assert.ok(editor.includes("official_website"));
  assert.ok(editor.includes("migration"));
});
