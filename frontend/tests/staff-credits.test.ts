import { test } from "node:test";
import assert from "node:assert/strict";
import { buildStaffCredits, unrenderedCreditRelations } from "../src/components/entity/staffCredits";
import { canonicalDetailPath } from "../src/lib/entityRoutes";
import { readerCreditRole } from "../src/lib/readerNotes";
import type { DynamicDefinitions } from "../src/lib/definitions";
import type { Entity } from "../src/components/catalog/api";

const characterId = "character-1";
const workId = "work-1";
const actorId = "actor-1";
const groupId = "group-1";
const rows = [
  { id: "appearance", type: "character_in", source_id: characterId, target_id: workId, attributes: { character_rank: "main" } },
  { id: "voice", type: "voiced_by", source_id: workId, target_id: actorId, attributes: { character: characterId } },
  { id: "membership", type: "member_of", source_id: characterId, target_id: groupId, attributes: {} },
];
const entities = Object.fromEntries([
  { id: characterId, kind: "agent", title: "角色" },
  { id: workId, kind: "work", title: "作品" },
  { id: actorId, kind: "agent", title: "声优" },
  { id: groupId, kind: "agent", title: "团体" },
].map((entity) => [entity.id, entity])) as Record<string, Entity>;
const definitions = {
  relations: {
    character_in: { counts_as_credit: true, participant_slot: "character", group: "credits", names: { "zh-CN": "登场" }, reverse_names: { "zh-CN": "登场角色" } },
    voiced_by: { counts_as_credit: true, participant_slot: "person", group: "credits", names: { "zh-CN": "配音" }, reverse_names: { "zh-CN": "配音于" } },
    member_of: { counts_as_credit: false, participant_slot: "peer", group: "membership", names: { "zh-CN": "所属" }, reverse_names: { "zh-CN": "成员" } },
  },
  vocabularies: { character_rank: { terms: { main: { names: { "zh-CN": "主角" } } } } },
} as unknown as DynamicDefinitions;
const args = { relations: rows, relEntities: entities, defs: definitions, locale: "zh-CN", tr: (_key: string, fallback: string) => fallback };

test("作品端保留角色和声优署名，团体关系不是署名", () => {
  const credits = buildStaffCredits({ ...args, entityId: workId });
  assert.deepEqual(credits.map((credit) => credit.id), ["appearance", "voice"]);
  assert.equal(credits[0].character?.id, characterId);
  assert.equal(credits[1].character?.id, characterId);
});

test("角色端的出场作品及属性引用声优关系进入署名列表", () => {
  const cards = buildStaffCredits({ ...args, entityId: characterId });
  assert.equal(cards.length, 0);
  const visible = unrenderedCreditRelations(rows, definitions, new Set(cards.map((card) => card.id)));
  assert.deepEqual(visible.map((row) => row.id), ["appearance", "voice"]);
  assert.equal(canonicalDetailPath(visible[0].target_id), `/catalog/${workId}`);
});

test("修改展示分组不改变署名语义", () => {
  const changed = structuredClone(definitions) as DynamicDefinitions;
  changed.relations.character_in.group = "appearance";
  changed.relations.member_of.group = "credits";
  assert.deepEqual(buildStaffCredits({ ...args, entityId: workId, defs: changed }).map((credit) => credit.id), ["appearance", "voice"]);
  assert.deepEqual(unrenderedCreditRelations(rows, changed, new Set()).map((row) => row.id), ["appearance", "voice"]);
});

test("规范详情地址只由 id 决定（kind 不参与 URL 命名）", () => {
  assert.equal(canonicalDetailPath("entity-1"), "/catalog/entity-1");
  assert.equal(canonicalDetailPath(null), null);
});

test("署名职位：英文机器码回落关系名", () => {
  assert.equal(readerCreditRole("performer", "演出", "职员"), "演出");
  assert.equal(readerCreditRole("作词", "职员", "职员"), "作词");
});
