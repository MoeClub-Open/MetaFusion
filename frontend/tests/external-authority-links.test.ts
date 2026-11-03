import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ExternalAuthorityLinks } from "../src/components/entity/ExternalAuthorityLinks";
import { I18nProvider } from "../src/i18n/I18nProvider";
import { safeExternalAuthorityURL } from "../src/lib/externalAuthorityURL";

test("外部链接仅接受绝对 HTTP(S)，完整维基 URL 保持有效", () => {
  for (const value of ["4901234567894", "/4901234567894", "//example.org/wiki/1", "javascript:alert(1)", "data:text/html,test", "https://", "https://user:password@example.org/", "https://example.org/\nunsafe"]) {
    assert.equal(safeExternalAuthorityURL(value), "", value);
  }
  assert.equal(safeExternalAuthorityURL("https://series.fandom.com/wiki/Title"), "https://series.fandom.com/wiki/Title");
  assert.equal(safeExternalAuthorityURL("https://zh.moegirl.org.cn/作品"), "https://zh.moegirl.org.cn/%E4%BD%9C%E5%93%81");
  assert.equal(safeExternalAuthorityURL("http://example.org/entry"), "http://example.org/entry");
});

test("中英两种布局显示裸条码为文字，并保留合法外链", () => {
  for (const locale of ["zh-CN", "en-US"]) {
    for (const variant of ["chips", "list"] as const) {
      const html = renderToStaticMarkup(createElement(I18nProvider, {
        initialLocale: locale,
        children: createElement(ExternalAuthorityLinks, {
          variant,
          externalLinks: [
            { kind: "ean_jan", external_id: "4901234567894", url: "4901234567894" },
            { kind: "fandom", external_id: "https://series.fandom.com/wiki/Title", url: "https://series.fandom.com/wiki/Title" },
          ],
        }),
      }));
      assert.match(html, /4901234567894/);
      assert.doesNotMatch(html, /href="4901234567894"/);
      assert.match(html, /href="https:\/\/series\.fandom\.com\/wiki\/Title"/);
      assert.equal((html.match(/<a\b/g) || []).length, 1);
    }
  }
});

test("role 为空不加角色标签；role=official 显示官方徽标", () => {
  const plain = renderToStaticMarkup(createElement(I18nProvider, {
    initialLocale: "zh-CN",
    children: createElement(ExternalAuthorityLinks, {
      externalLinks: [{ kind: "twitter", url: "https://twitter.com/example", external_id: "example" }],
    }),
  }));
  assert.doesNotMatch(plain, /官方/);

  const official = renderToStaticMarkup(createElement(I18nProvider, {
    initialLocale: "zh-CN",
    children: createElement(ExternalAuthorityLinks, {
      externalLinks: [{ kind: "twitter", url: "https://twitter.com/official", role: "official" }],
    }),
  }));
  assert.match(official, /官方/);
});

test("实体 external_links 历史标识在无预设时仍以文字展示", () => {
  const html = renderToStaticMarkup(createElement(I18nProvider, {
    initialLocale: "zh-CN",
    children: createElement(ExternalAuthorityLinks, {
      entity: {
        external_links: [
          { kind: "bilibili", external_id: "ep257552", url: "ep257552" },
          { kind: "ean_jan", external_id: "4901234567894", url: "4901234567894" },
        ],
      },
    }),
  }));
  assert.match(html, /ep257552/);
  assert.match(html, /4901234567894/);
  assert.doesNotMatch(html, /<a\b/);
});

test("属性回落只加链接、不写 role，不出现官方徽标样式", () => {
  const html = renderToStaticMarkup(createElement(I18nProvider, {
    initialLocale: "zh-CN",
    children: createElement(ExternalAuthorityLinks, {
      entity: {
        attributes: { website: "https://example.org/" },
        external_links: [],
      },
    }),
  }));
  assert.match(html, /href="https:\/\/example\.org\/"/);
  // kind=official_website 显示「官方网站」名称可以有，但不能有 role=official 的翡翠徽标样式
  assert.doesNotMatch(html, /bg-emerald-500\/10/);
  assert.doesNotMatch(html, /bg-primary\/10[^"]*"[^>]*>官方</);
});

test("list 变体：平台名完整显示，百分号编码副行解码", () => {
  const html = renderToStaticMarkup(createElement(I18nProvider, {
    initialLocale: "zh-CN",
    children: createElement(ExternalAuthorityLinks, {
      variant: "list",
      externalLinks: [
        { kind: "official_website", url: "https://example.org/", external_id: "" },
        { kind: "moegirl", url: "https://zh.moegirl.org.cn/%E9%B9%BF%E4%B9%83", external_id: "%E9%B9%BF%E4%B9%83" },
      ],
    }),
  }));
  // 官方网站（权威库缺省时的 i18n 名）应完整出现，不应只剩「官」
  assert.match(html, /官方网站/);
  assert.doesNotMatch(html, />官…</);
  // 副行解码；href 仍可保留百分号编码（合法 URL）
  assert.match(html, /title="鹿乃">鹿乃</);
  // CJK 用 break-keep，避免「官/方」中字断开；不截成「官…」
  assert.match(html, /font-sans break-keep">官方网站</);
});

test("读者面 list 变体副行不直出 MusicBrainz UUID（href 可含）", () => {
  const html = renderToStaticMarkup(createElement(I18nProvider, {
    initialLocale: "zh-CN",
    children: createElement(ExternalAuthorityLinks, {
      variant: "list",
      externalLinks: [
        {
          kind: "musicbrainz_artist",
          external_id: "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
          url: "https://musicbrainz.org/artist/a1b2c3d4-e5f6-7890-abcd-ef1234567890",
        },
      ],
    }),
  }));
  // 链接目标可含 UUID；可见副行 / title 不得把 UUID 当主文案
  assert.match(html, /href="https:\/\/musicbrainz\.org\/artist\/a1b2c3d4-e5f6-7890-abcd-ef1234567890"/);
  assert.match(html, /title="[^"]*musicbrainz\.org"/);
  assert.doesNotMatch(html, /title="[^"]*a1b2c3d4-e5f6-7890-abcd-ef1234567890"/);
  assert.match(html, />musicbrainz\.org</);
});


test("缺 defs 首屏主标签不闪 SPOTIFY_ARTIST / APPLE_MUSIC_ARTIST 枚举", () => {
  const html = renderToStaticMarkup(createElement(I18nProvider, {
    initialLocale: "zh-CN",
    children: createElement(ExternalAuthorityLinks, {
      variant: "list",
      externalLinks: [
        {
          kind: "spotify_artist",
          external_id: "2uH0TGvdgttfPNNqQtw4lE",
          url: "https://open.spotify.com/artist/2uH0TGvdgttfPNNqQtw4lE",
        },
        {
          kind: "apple_music_artist",
          external_id: "1038457852",
          url: "https://music.apple.com/us/artist/mili/1038457852",
        },
        {
          kind: "youtube_channel",
          external_id: "UC1234567890abcdefghijkl",
          url: "https://www.youtube.com/channel/UC1234567890abcdefghijkl",
        },
      ],
    }),
  }));
  assert.doesNotMatch(html, /SPOTIFY_ARTIST/);
  assert.doesNotMatch(html, /APPLE_MUSIC_ARTIST/);
  assert.doesNotMatch(html, /YOUTUBE_CHANNEL/);
  assert.match(html, /Spotify/);
  assert.match(html, /Apple Music/);
  assert.match(html, /YouTube/);
  // 副行主机名，不露原始机码
  assert.match(html, /open\.spotify\.com/);
  assert.doesNotMatch(html, />2uH0TGvdgttfPNNqQtw4lE</);
  assert.doesNotMatch(html, />1038457852</);
  assert.doesNotMatch(html, />UC1234567890abcdefghijkl</);
});
