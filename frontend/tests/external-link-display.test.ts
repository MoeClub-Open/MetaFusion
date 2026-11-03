import { test } from "node:test";
import assert from "node:assert/strict";
import { decodeDisplayText, externalLinkPrimaryFallback, externalLinkSecondary } from "../src/lib/externalLinkDisplay";

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


test("读者面副行不直出 Spotify / YouTube / ISNI / Apple 机码，回落主机名", () => {
  assert.equal(
    externalLinkSecondary(
      "2uH0TGvdgttfPNNqQtw4lE",
      "https://open.spotify.com/artist/2uH0TGvdgttfPNNqQtw4lE",
    ),
    "open.spotify.com",
  );
  assert.equal(
    externalLinkSecondary(
      "UC1234567890abcdefghijkl",
      "https://www.youtube.com/channel/UC1234567890abcdefghijkl",
    ),
    "www.youtube.com",
  );
  assert.equal(
    externalLinkSecondary("000000012146438X", "https://isni.org/isni/000000012146438X"),
    "isni.org",
  );
  assert.equal(
    externalLinkSecondary("1038457852", "https://music.apple.com/us/artist/mili/1038457852"),
    "music.apple.com",
  );
  assert.equal(
    externalLinkSecondary("1234567", "https://www.deezer.com/artist/1234567"),
    "www.deezer.com",
  );
  assert.equal(
    externalLinkSecondary(
      "2uH0TGvdgttfPNNqQtw4lE",
      "https://open.spotify.com/artist/2uH0TGvdgttfPNNqQtw4lE",
      { allowOpaque: true },
    ),
    "2uH0TGvdgttfPNNqQtw4lE",
  );
});

test("缺 defs 时主标签用主机品牌，不闪 UPPER_SNAKE 枚举", () => {
  assert.equal(
    externalLinkPrimaryFallback("spotify_artist", "https://open.spotify.com/artist/2uH0TGvdgttfPNNqQtw4lE", {
      genericLabel: "外部链接",
    }),
    "Spotify",
  );
  assert.equal(
    externalLinkPrimaryFallback("apple_music_artist", "https://music.apple.com/us/artist/mili/1038457852", {
      genericLabel: "外部链接",
    }),
    "Apple Music",
  );
  assert.equal(
    externalLinkPrimaryFallback("youtube_channel", "https://www.youtube.com/channel/UC1234567890abcdefghijkl", {
      genericLabel: "外部链接",
    }),
    "YouTube",
  );
  assert.equal(
    externalLinkPrimaryFallback("mystery_kind", "not-a-url", { genericLabel: "外部链接" }),
    "外部链接",
  );
  assert.notEqual(
    externalLinkPrimaryFallback("spotify_artist", "https://open.spotify.com/artist/x", {
      genericLabel: "外部链接",
    }).toUpperCase(),
    "SPOTIFY_ARTIST",
  );
});
