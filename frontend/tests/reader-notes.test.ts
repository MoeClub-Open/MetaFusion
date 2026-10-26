import { test } from "node:test";
import assert from "node:assert/strict";
import {
  isMachineToken,
  isOpsNote,
  readerRevisionNote,
  readerSourceLabel,
  readerVisiblePointers,
  readerVisibleSources,
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
