import { test } from "node:test";
import assert from "node:assert/strict";
import {
  isMachineToken,
  isOpsNote,
  isOpsDiffKey,
  isOpsDiffValue,
  readerRevisionNote,
  readerSourceLabel,
  readerVisiblePointers,
  readerVisibleSources,
  readerVisibleDiffData,
  readerVisibleProse,
  readerCreditRole,
  isOpaqueAuthorityId,
} from "../src/lib/readerNotes";

test("机器码 token：attributes.* / AUX-* / snake_case", () => {
  assert.equal(isMachineToken("attributes.publisher"), true);
  assert.equal(isMachineToken("attributes.foo.bar"), true);
  assert.equal(isMachineToken("AUX-P2"), true);
  assert.equal(isMachineToken("aux-p2"), true);
  assert.equal(isMachineToken("netease_music_artist"), true);
  assert.equal(isMachineToken("bangumi_subject_id"), true);
  assert.equal(isMachineToken("修正发行日期"), false);
  assert.equal(isMachineToken("official"), false); // 无下划线，不算 snake_case
});

test("isOpsNote：#73 运维散文关键词与机器码并存", () => {
  // #73 运维散文（Claude 点名会漏回来的那种）
  assert.equal(isOpsNote("批处理：补齐 iTunes 词表说明"), true);
  assert.equal(isOpsNote("migration: external_ids → links"), true);
  assert.equal(isOpsNote("批处理导入 iTunes 词表说明"), true);
  assert.equal(isOpsNote("迁移字段"), true);
  // 机器码
  assert.equal(isOpsNote("netease_music_artist"), true);
  assert.equal(isOpsNote("attributes.publisher"), true);
  assert.equal(isOpsNote("AUX-P2"), true);
  assert.equal(isOpsNote("attributes.publisher / AUX-P2"), true);
  assert.equal(isOpsNote("补 attributes.publisher"), true);
  assert.equal(isOpsNote("批 AUX-P2 对齐"), true);
  // Kimi：散文夹带任一 snake_case 机器码也藏（不能 every）
  assert.equal(isOpsNote("同步自 netease_music_artist"), true);
  assert.equal(isOpsNote("对齐 bangumi_subject_id 后发布"), true);
  // 普通散文放行
  assert.equal(isOpsNote("修正曲目标题"), false);
  assert.equal(isOpsNote("修正发行日期"), false);
});

test("读者面修订摘要过滤运维与机器码备注", () => {
  assert.equal(
    readerRevisionNote({ edit_note: "批处理：补齐 iTunes 词表说明" }, "更新"),
    "更新",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "批处理导入 iTunes 词表说明" }, "更新"),
    "更新",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "同步自 netease_music_artist" }, "更新"),
    "更新",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "netease_music_artist" }, "更新"),
    "更新",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "attributes.publisher" }, "更新"),
    "更新",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "AUX-P2" }, "更新"),
    "更新",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "修正发行日期", summary: "" }, "更新"),
    "修正发行日期",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "批处理", summary: "读者可见摘要" }, "更新"),
    "读者可见摘要",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "netease_music_artist", summary: "补充网易云艺人链" }, "更新"),
    "补充网易云艺人链",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "批处理导入" }, "更新", { allowOps: true }),
    "批处理导入",
  );
  assert.equal(
    readerRevisionNote({ edit_note: "AUX-P2" }, "更新", { allowOps: true }),
    "AUX-P2",
  );
});

test("读者面来源丢掉无 URL 的运维 / 机器码 citation", () => {
  const sources = [
    { kind: "publication", citation: "iTunes 词表说明：对应内容表达映射" },
    { kind: "publication", citation: "netease_music_artist" },
    { kind: "publication", citation: "attributes.publisher" },
    { kind: "publication", citation: "AUX-P2" },
    { kind: "url", citation: "官方页", url: "https://example.org/a" },
    { kind: "url", citation: "migration batch note", url: "https://bgm.tv/subject/1" },
    { kind: "url", citation: "netease_music_artist", url: "https://music.163.com/#/artist?id=1" },
  ];
  const visible = readerVisibleSources(sources);
  assert.equal(visible.length, 3);
  assert.equal(readerSourceLabel(sources[0]), "");
  assert.equal(readerSourceLabel(sources[1]), "");
  assert.equal(readerSourceLabel(sources[2]), "");
  assert.equal(readerSourceLabel(sources[3]), "");
  assert.equal(readerSourceLabel(sources[4]), "官方页");
  assert.equal(readerSourceLabel(sources[5]), "bgm.tv");
  assert.equal(readerSourceLabel(sources[6]), "music.163.com");
});

test("读者面 pointers 过滤字段路径", () => {
  assert.deepEqual(
    readerVisiblePointers(["attributes.publisher", "title", "AUX-P2"]),
    ["title"],
  );
  assert.deepEqual(
    readerVisiblePointers(["attributes.publisher", "AUX-P2"], { allowOps: true }),
    ["attributes.publisher", "AUX-P2"],
  );
});

test("差异面板运维键：origin / migration / external_id / attributes.*；不误伤 schema 字段", () => {
  assert.equal(isOpsDiffKey("origin"), true);
  assert.equal(isOpsDiffKey("migration"), true);
  assert.equal(isOpsDiffKey("external_id"), true);
  assert.equal(isOpsDiffKey("attributes.publisher"), true);
  assert.equal(isOpsDiffKey("AUX-P2"), true);
  // 平台码是取值不是键；schema 字段键要保留给可读标签
  assert.equal(isOpsDiffKey("netease_music_artist"), false);
  assert.equal(isOpsDiffKey("external_links"), false);
  assert.equal(isOpsDiffKey("parent_id"), false);
  assert.equal(isOpsDiffKey("source_id"), false);
  assert.equal(isOpsDiffKey("version_label"), false);
  assert.equal(isOpsDiffKey("url"), false);
  assert.equal(isOpsDiffKey("kind"), false);
  assert.equal(isOpsDiffKey("title"), false);
});

test("差异面板运维取值：机器码与 migration", () => {
  assert.equal(isOpsDiffValue("netease_music_artist"), true);
  assert.equal(isOpsDiffValue("apple_music_artist"), true);
  assert.equal(isOpsDiffValue("official_website"), true);
  assert.equal(isOpsDiffValue("migration"), true);
  assert.equal(isOpsDiffValue("origin"), true);
  assert.equal(isOpsDiffValue("https://music.163.com/#/artist?id=1"), false);
  assert.equal(isOpsDiffValue("isni"), true); // P1：权威短码对读者面也藏
  assert.equal(isOpsDiffValue("wikidata"), true);
  assert.equal(isOpsDiffValue("Q42"), true);
  assert.equal(isOpsDiffValue(339594), false);
});

test("读者面差异树藏掉 Mili 式外链机器码；编辑者仍见原文", () => {
  const snapshot = {
    title: "Mili",
    external_links: [
      {
        kind: "netease_music_artist",
        external_id: "339594",
        origin: "migration",
        url: "https://music.163.com/#/artist?id=339594",
      },
      {
        kind: "official_website",
        origin: "migration",
        url: "https://projectmili.com",
      },
      {
        kind: "isni",
        external_id: "0000000470325273",
        origin: "migration",
        url: "https://isni.org/isni/0000000470325273",
      },
    ],
  };

  const reader = readerVisibleDiffData(snapshot);
  assert.equal(reader.title, "Mili");
  assert.equal(reader.external_links.length, 3);
  for (const link of reader.external_links) {
    assert.equal("external_id" in link, false, "external_id key hidden");
    assert.equal("origin" in link, false, "origin key hidden");
    assert.ok(link.url, "url kept");
  }
  // snake_case kind 与 isni/wikidata 权威短码对读者面都藏
  assert.equal("kind" in reader.external_links[0], false);
  assert.equal("kind" in reader.external_links[1], false);
  assert.equal("kind" in reader.external_links[2], false);

  const editor = readerVisibleDiffData(snapshot, { allowOps: true });
  assert.deepEqual(editor, snapshot);
  assert.equal(editor.external_links[0].kind, "netease_music_artist");
  assert.equal(editor.external_links[0].origin, "migration");
  assert.equal(editor.external_links[0].external_id, "339594");
});

test("P1 运维散文：Agent / 不虚构该ID / 版权 unknown / batch", () => {
  assert.equal(isOpsNote("Agent 批量核对 artist_id"), true);
  assert.equal(isOpsNote("不虚构该ID，留空待查"), true);
  assert.equal(isOpsNote("版权 unknown，Spotify 核对"), true);
  assert.equal(isOpsNote("batch import from Bangumi"), true);
  assert.equal(isOpsNote("补充中文别名"), false);
});

test("readerVisibleProse：剥 edition_type=standard / (physical)，保留可读散文", () => {
  const raw =
    "Bangumi 图书条目 8492「進撃の巨人 (1)」的单行纸质版次：出版社 講談社、发售日 2010-03-17、ISBN 9784063842760、192 頁、￥440 円。条目未载限定版/特典/BOX 等版次字样，故 edition_type=standard、流通为实体纸本（physical）、发售地区 JP。";
  const cleaned = readerVisibleProse(raw);
  assert.match(cleaned, /進撃の巨人/);
  assert.match(cleaned, /講談社/);
  assert.doesNotMatch(cleaned, /edition_type/);
  assert.doesNotMatch(cleaned, /physical/);
  assert.equal(readerVisibleProse(raw, { allowOps: true }), raw);
});

test("readerCreditRole：performer 等机器码回落关系名；中文职位保留", () => {
  assert.equal(readerCreditRole("performer", "演出", "职员"), "演出");
  assert.equal(readerCreditRole("摄影监督", "职员", "职员"), "摄影监督");
  assert.equal(readerCreditRole("Vo.", "演唱", "职员"), "Vo.");
  assert.equal(readerCreditRole("performer", "演出", "职员", { allowOps: true }), "performer");
});

test("isOpaqueAuthorityId：UUID / Q-id / 32hex", () => {
  assert.equal(isOpaqueAuthorityId("a1b2c3d4-e5f6-7890-abcd-ef1234567890"), true);
  assert.equal(isOpaqueAuthorityId("Q42"), true);
  assert.equal(isOpaqueAuthorityId("0123456789abcdef0123456789abcdef"), true);
  assert.equal(isOpaqueAuthorityId("ep257552"), false);
  assert.equal(isOpaqueAuthorityId("4901234567894"), false);
});

test("差异树藏 entity_hierarchy / isni 键", () => {
  const snap = {
    title: "X",
    entity_hierarchy: ["work", "expression"],
    isni: "0000000470325273",
    wikidata: "Q42",
    parent_id: "keep-me",
  };
  const reader = readerVisibleDiffData(snap);
  assert.equal(reader.title, "X");
  assert.equal("entity_hierarchy" in reader, false);
  assert.equal("isni" in reader, false);
  assert.equal("wikidata" in reader, false);
  assert.equal(reader.parent_id, "keep-me");
});

