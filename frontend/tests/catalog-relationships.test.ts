import { test } from "node:test";
import assert from "node:assert/strict";
import { loadEntityRelationships, editableRelations, type RelationshipLink } from "../src/lib/catalogRelationships";
import { relationDirectoryRows } from "../src/lib/relationDirectory";
import { getRelationName, type RelationshipRule } from "../src/lib/definitions";
import { emptyEntity } from "../src/components/catalog/api";

const link = (key: string, extra: Partial<RelationshipLink> = {}): RelationshipLink => ({
  key, rule_code: "attribute:publisher", class: "reference", source_id: "release", target_id: "publisher", position: 0, ...extra,
});

test("publisher references survive pagination and show the release from the organization side", async () => {
  const offsets: number[] = [];
  const entities = { publisher: { ...emptyEntity("agent"), id: "publisher", title: "Publisher" }, release: { ...emptyEntity("release"), id: "release", title: "棘アリ [通常盤]" } };
  const result = await loadEntityRelationships("publisher", async (offset) => {
    offsets.push(offset);
    return { subject_id: "publisher", items: offset === 0
      ? [link("ownership", { class: "ownership", rule_code: "structure:release_medium" })]
      : [link("publisher-reference"), link("credit", { class: "semantic", rule_code: "relation:custom_credit", version: 7, attributes: { credit_role: "発売元" } })],
    entities, has_more: offset === 0, limit: 2 };
  });
  assert.deepEqual(offsets, [0, 2]);
  assert.equal(result.items.length, 2);
  const rows = relationDirectoryRows("publisher", result.items, result.entities);
  assert.equal(rows[0].direction, "incoming");
  assert.equal(rows[0].entity?.title, "棘アリ [通常盤]");
  assert.equal(rows[0].type, "attribute:publisher");
  assert.deepEqual(editableRelations(result.items).map((record) => [record.id, record.version, record.attributes.credit_role]), [["credit", 7, "発売元"]]);
});

test("nested reference paths retain qualifiers and use current server labels", async () => {
  const result = await loadEntityRelationships("publisher", async () => ({ subject_id: "publisher", items: [link("store", { rule_code: "attribute:attachments[].store", field: "attachments[0].store" })], entities: {}, limit: 100, has_more: false }));
  assert.equal(result.items[0].field, "attachments[0].store");
  const rules: RelationshipRule[] = [{ code: "attribute:attachments[].store", class: "reference", source_kinds: ["release"], target_kinds: ["agent"], max_incoming: 0, max_outgoing: 0, ordered: true, read_only: true, enabled: true, names: { "zh-CN": "店铺", "en-US": "Store" }, reverse_names: { "zh-CN": "店铺", "en-US": "Store" } }];
  assert.equal(getRelationName(null, result.items[0].type, false, "zh-CN", rules), "店铺");
  assert.equal(getRelationName(null, result.items[0].type, false, "en-US", rules), "Store");
});

test("missing relation versions and failed later pages never become editable partial results", async () => {
  await assert.rejects(loadEntityRelationships("publisher", async () => ({ subject_id: "publisher", items: [link("credit", { class: "semantic", rule_code: "relation:credit_for" })], entities: {}, limit: 100, has_more: false })), /catalog.links.version/);
  await assert.rejects(loadEntityRelationships("publisher", async (offset) => {
    if (offset) throw new Error("page unavailable");
    return { subject_id: "publisher", items: [link("reference")], entities: {}, limit: 100, has_more: true };
  }), /page unavailable/);
});
