import { test } from "node:test";
import assert from "node:assert/strict";
import { relationDirectoryRows, relationDirectorySearch } from "../src/lib/relationDirectory";
import type { Entity, Relation } from "../src/components/catalog/api";

const entities = { work: { id: "work", kind: "work", title: "Song", translations: { "zh-CN": { title: "曲目", aliases: ["别名"] } } },
  recording: { id: "recording", kind: "expression", title: "Song" }, person: { id: "person", kind: "agent", title: "Member" } } as unknown as Record<string, Entity>;
const relation = (id: string, source: string, target: string, attributes = {}) => ({ id, type: "custom_credit", source_id: source, target_id: target, attributes }) as Relation;

test("statements sharing endpoints retain each qualifier while same-title records stay distinct", () => {
  const records = [relation("op", "work", "band", { credit_role: "Opening" }), relation("ed", "work", "band", { credit_role: "Ending" }), relation("recording", "recording", "band")];
  const rows = relationDirectoryRows("band", records, entities);
  assert.equal(rows.length, 2);
  assert.equal(rows[0].records.length, 2);
  assert.deepEqual(rows[0].records.map((record) => record.attributes?.credit_role), ["Opening", "Ending"]);
  assert.deepEqual(rows.map((row) => row.kind), ["work", "expression"]);
  assert.equal(rows.flatMap((row) => row.records).length, records.length);
  assert.ok(relationDirectorySearch(rows[0], "ending"));
  assert.ok(relationDirectorySearch(rows[0], "别名"));
});

test("incoming, outgoing and property-reference relationships preserve their actual endpoints", () => {
  const records = [relation("in", "person", "band"), relation("out", "band", "person"), relation("ref", "work", "person", { context: "band" })];
  const rows = relationDirectoryRows("band", records, entities);
  assert.deepEqual(rows.map((row) => row.direction), ["incoming", "outgoing", "reference"]);
  assert.equal(rows[2].source?.id, "work");
  assert.equal(rows[2].target?.id, "person");
  assert.ok(relationDirectorySearch(rows[2], "member"));
});

test("missing related summaries do not remove relationships or invent a title", () => {
  const rows = relationDirectoryRows("band", [relation("missing", "missing", "band")], entities);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].entityId, "missing");
  assert.equal(rows[0].entity, undefined);
  assert.equal(rows[0].records[0].id, "missing");
});
