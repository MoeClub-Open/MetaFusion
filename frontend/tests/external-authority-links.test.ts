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
            { code: "ean_jan", name: "EAN / JAN", external_id: "4901234567894", url: "4901234567894", icon: "Barcode", icon_url: "" },
            { code: "fandom", name: "Fandom", external_id: "https://series.fandom.com/wiki/Title", url: "https://series.fandom.com/wiki/Title", icon: "BookOpen", icon_url: "" },
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

test("公开注册表未返回停用码时，历史标识仍以文字展示", () => {
  const html = renderToStaticMarkup(createElement(ExternalAuthorityLinks, {
    externalIds: { bilibili: "ep257552", ean_jan: "4901234567894" },
  }));
  assert.match(html, /ep257552/);
  assert.match(html, /4901234567894/);
  assert.doesNotMatch(html, /<a\b/);
});
