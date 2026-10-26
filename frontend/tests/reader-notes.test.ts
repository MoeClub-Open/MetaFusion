import { test } from "node:test";
import assert from "node:assert/strict";
import {
  isOpsNote,
  readerRevisionNote,
  readerSourceLabel,
  readerVisibleSources,
} from "../src/lib/readerNotes";

test("识别迁移/批处理/词表运维备注", () => {
  assert.equal(isOpsNote("migration: external_ids → links"), true);
  assert.equal(isOpsNote("批处理：补齐 iTunes 词表说明"), true);
  assert.equal(isOpsNote("修正曲目标题"), false);
});

test("读者面修订摘要过滤运维 edit_note", () => {
  assert.equal(
    readerRevisionNote({ edit_note: "批处理导入 iTunes 词表说明" }, "更新"),
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
    readerRevisionNote({ edit_note: "批处理导入" }, "更新", { allowOps: true }),
    "批处理导入",
  );
});

test("读者面来源丢掉无 URL 的运维 citation", () => {
  const sources = [
    { kind: "publication", citation: "iTunes 词表说明：对应内容表达映射" },
    { kind: "url", citation: "官方页", url: "https://example.org/a" },
    { kind: "url", citation: "migration batch note", url: "https://bgm.tv/subject/1" },
  ];
  const visible = readerVisibleSources(sources);
  assert.equal(visible.length, 2);
  assert.equal(readerSourceLabel(sources[0]), "");
  assert.equal(readerSourceLabel(sources[1]), "官方页");
  assert.equal(readerSourceLabel(sources[2]), "bgm.tv");
});
