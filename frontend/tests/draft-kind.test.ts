import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { emptyEntity } from "../src/components/catalog/api";
import { changeDraftKind, hasKindSpecificDraftData } from "../src/lib/draftKind";

test("all dynamically selected entity states have real labels in every locale", () => {
  for (const locale of ["zh-CN", "zh-TW", "en-US", "ja-JP"]) {
    const messages = JSON.parse(readFileSync(new URL(`../src/messages/${locale}.json`, import.meta.url), "utf8"));
    for (const state of ["draft", "pending_review", "published", "merged", "deleted"]) {
      const key = `catalog.state.${state}`;
      assert.equal(typeof messages[key], "string", `${locale}: missing ${key}`);
      assert.ok(messages[key].trim());
      assert.notEqual(messages[key], key);
    }
  }
});

test("changing Work to Agent preserves all kind-independent draft content", () => {
  const source = {
    ...emptyEntity("work"), title: "初音ミク", status: "published", original_language: "ja",
    translations: { ja: { title: "初音ミク", summary: "説明", aliases: ["ミク"] } },
    attributes: { tags: ["初音未来"], work_type: "song" }, external_links: [{ kind: "wikidata", url: "mf://external/wikidata/Q123", external_id: "Q123" }],
    pictures: [{ url: "https://example.test/cover.png", caption: { ja: "画像" }, source: { kind: "self", citation: "test" } }],
    work_id: "parent", medium_id: "disc", parent_id: "side", number: "2", position: 3,
    subjects: [{ work_id: "subject", role: "main", position: 0 }],
    contents: [{ expression_id: "expression", position: 0, locator: {} }],
  };
  const result = changeDraftKind(source, emptyEntity("agent"));
  assert.equal(result.kind, "agent");
  for (const key of ["title", "status", "original_language", "translations", "external_links", "pictures"] as const) assert.deepEqual(result[key], source[key]);
  assert.deepEqual(result.attributes, { tags: ["初音未来"] });
  assert.equal(result.work_id, undefined);
  assert.equal(result.medium_id, undefined);
  assert.equal(result.parent_id, undefined);
  assert.equal(result.position, 0);
  assert.equal(result.number, "");
  assert.deepEqual(result.contents, []);
  assert.deepEqual(result.subjects, []);
  result.translations.ja.title = "changed";
  assert.equal(source.translations.ja.title, "初音ミク");
  assert.equal(source.attributes.work_type, "song");
});

test("confirmation is required for structural data, non-tag attributes or queued relations", () => {
  const common = { ...emptyEntity("work"), title: "Title", original_language: "ja", attributes: { tags: ["tag"] } };
  assert.equal(hasKindSpecificDraftData(common, 0), false);
  assert.equal(hasKindSpecificDraftData(common, 1), true);
  for (const change of [
    { work_id: "parent" }, { release_id: "release" }, { medium_id: "medium" }, { content_unit_id: "unit" }, { parent_id: "parent" },
    { number: "1" }, { position: 1 }, { attributes: { flag: false } }, { attributes: { count: 0 } },
    { contents: [{ expression_id: "expression", position: 0, locator: {} }] }, { subjects: [{ work_id: "work", role: "main", position: 0 }] },
  ]) assert.ok(hasKindSpecificDraftData({ ...common, ...change }, 0));
  assert.equal(hasKindSpecificDraftData({ ...common, attributes: { blank: "", empty: [], group: {} } }, 0), false);
  assert.equal(changeDraftKind(common, emptyEntity("work")), common);
});

test("kind selector defers destructive reset until confirmation and clears queued relations", () => {
  const source = readFileSync(new URL("../src/components/catalog/EntityEditor.tsx", import.meta.url), "utf8");
  assert.ok(source.includes("if (hasKindSpecificDraftData(e, pendingRelations.length)) setRequestedKind(value)"));
  assert.ok(source.includes('onClose={() => setRequestedKind("")}'));
  const apply = source.slice(source.indexOf("const applyKindChange"), source.indexOf("const d = definitions"));
  assert.ok(apply.includes("setPendingRelations([])"));
  assert.ok(apply.includes("changeDraftKind(current, emptyEntity(kind))"));
});
