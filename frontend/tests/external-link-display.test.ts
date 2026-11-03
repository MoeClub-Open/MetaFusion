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

test("读者面副行不直出 UUID / Wikidata Q-id，回落主机名", () => {
  assert.equal(
    externalLinkSecondary(
      "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "https://musicbrainz.org/artist/a1b2c3d4-e5f6-7890-abcd-ef1234567890",
    ),
    "musicbrainz.org",
  );
  assert.equal(
    externalLinkSecondary("Q42", "https://www.wikidata.org/wiki/Q42"),
    "www.wikidata.org",
  );
  assert.equal(
    externalLinkSecondary(
      "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      "https://musicbrainz.org/artist/a1b2c3d4-e5f6-7890-abcd-ef1234567890",
      { allowOpaque: true },
    ),
    "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  );
});

test("可读产品码与 Bangumi ep 仍作副行", () => {
  assert.equal(externalLinkSecondary("ep257552", "https://bgm.tv/ep/257552"), "ep257552");
  assert.equal(externalLinkSecondary("4901234567894", "4901234567894"), "4901234567894");
});

