import { test } from "node:test";
import assert from "node:assert/strict";
import {
  filterRelationDirectoryTemporal,
  hasTemporalQualifiers,
  relationDirectoryRows,
  relationDirectorySearch,
  validPartialISODate,
} from "../src/lib/relationDirectory";
import { ENTITY_KINDS } from "../src/lib/kinds.generated";
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

test("temporal filtering handles active_only, ended flag, partial ISO as_of, and uncertainty bounds", () => {
  assert.ok((ENTITY_KINDS as readonly string[]).includes("event"));
  assert.equal(validPartialISODate("2020"), true);
  assert.equal(validPartialISODate("2020-02-29"), true);
  assert.equal(validPartialISODate("2019-02-29"), false);

  const nowMs = Date.UTC(2026, 5, 1);
  const records = [
    relation("active", "person", "band", { begin_date: "2022-04" }),
    relation("endedFlag", "work", "band", { begin_date: "2018", ended: true }),
    relation("pastTenure", "recording", "band", { begin_date: "2015-04", end_date: "2019-03" }),
    relation("rejoined", "recording", "band", { begin_date: "2023-01" }),
    relation("uncertain", "live", "band", { earliest_date: "2016", latest_date: "2017" }),
  ];
  const rows = relationDirectoryRows("band", records, entities);
  assert.equal(hasTemporalQualifiers(rows), true);

  const active = filterRelationDirectoryTemporal(rows, { activeOnly: true }, nowMs);
  assert.deepEqual(active.map((r) => r.entityId), ["person", "recording"]);
  assert.deepEqual(active.find((r) => r.entityId === "recording")?.records.map((rec) => rec.id), ["rejoined"]);

  const asOf2018 = filterRelationDirectoryTemporal(rows, { asOf: "2018" }, nowMs);
  assert.deepEqual(asOf2018.map((r) => r.entityId), ["work", "recording"]);
  assert.deepEqual(asOf2018.find((r) => r.entityId === "recording")?.records.map((rec) => rec.id), ["pastTenure"]);

  const asOf2016 = filterRelationDirectoryTemporal(rows, { asOf: "2016-06" }, nowMs);
  assert.ok(asOf2016.some((r) => r.entityId === "live"));
});
