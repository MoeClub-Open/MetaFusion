import { test } from "node:test";
import assert from "node:assert/strict";
import { decodeDisplayText, externalLinkSecondary } from "../src/lib/externalLinkDisplay";

test("百分号编码 URL 解码为可读副行", () => {
  assert.equal(decodeDisplayText("%E9%B9%BF%E4%B9%83"), "鹿乃");
  assert.equal(
    externalLinkSecondary(undefined, "https://zh.moegirl.org.cn/%E9%B9%BF%E4%B9%83"),
    "鹿乃",
  );
});

test("有 external_id 时副行优先 ID，不截成平台名", () => {
  assert.equal(externalLinkSecondary("ep257552", "https://bgm.tv/ep/257552"), "ep257552");
  assert.equal(externalLinkSecondary("4901234567894", "4901234567894"), "4901234567894");
});

test("无 ID 时副行取短域名或末段路径", () => {
  assert.equal(externalLinkSecondary(undefined, "https://example.org/"), "example.org");
  assert.equal(externalLinkSecondary(undefined, "https://example.org/wiki/Title"), "Title");
});
