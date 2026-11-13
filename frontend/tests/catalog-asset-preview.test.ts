import { test } from "node:test";
import assert from "node:assert/strict";
import {
  catalogInlineAssetPreviewUrl,
  catalogPicturePreviewUrl,
  extractDocumentAssetIds,
  type CatalogAssetContext,
} from "../src/lib/catalogAssetPreview";

const id = "11111111-2222-4333-8444-555555555555";
const assetID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const bodyAssetID = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff";
const bodyAssetID2 = "cccccccc-dddd-4eee-8fff-000000000000";
const bodyAssetID3 = "dddddddd-eeee-4fff-8000-111111111111";
const picture = { url: `/api/storage/assets/${assetID}/content`, asset_id: assetID };

test("private preview projects each context without mutating the authoritative URL", () => {
  for (const [kind, path] of [["workspace", "workspaces"], ["pull-request", "pull-requests"], ["commit", "commits"]] as const) {
    assert.equal(catalogPicturePreviewUrl(picture, { kind, id }), `/api/catalog/${path}/${id}/assets/${assetID}/content`);
    assert.equal(catalogInlineAssetPreviewUrl(picture.url, { kind, id }), `/api/catalog/${path}/${id}/assets/${assetID}/content`);
    assert.equal(picture.url, `/api/storage/assets/${assetID}/content`);
  }
  assert.equal(catalogPicturePreviewUrl(picture), picture.url);
  assert.equal(catalogInlineAssetPreviewUrl(picture.url), picture.url);
});

test("external URLs and malformed or mismatching asset references are not projected", () => {
  const context: CatalogAssetContext = { kind: "workspace", id };
  for (const value of [
    { url: "https://example.org/cover.jpg", asset_id: assetID },
    { url: `/api/storage/assets/${id}/content`, asset_id: assetID },
    { url: `${picture.url}?token=private`, asset_id: assetID },
    { url: picture.url, asset_id: "../escape" },
    { url: "https://user:password@example.org" + picture.url, asset_id: assetID },
  ]) assert.equal(catalogPicturePreviewUrl(value, context), value.url);
  assert.equal(catalogPicturePreviewUrl(picture, { kind: "workspace", id: "../escape" }), picture.url);
  for (const raw of [
    "https://example.org/inline.jpg",
    `${picture.url}?token=1`,
    `${picture.url}#frag`,
    "/api/storage/assets/AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE/content",
    "/api/storage/assets/../escape/content",
  ]) assert.equal(catalogInlineAssetPreviewUrl(raw, context), raw);
});

test("allowedAssetIds gates workspace preview until asset is referenced in saved snapshot", () => {
  const unreferenced: CatalogAssetContext = { kind: "workspace", id, allowedAssetIds: new Set() };
  assert.equal(catalogPicturePreviewUrl(picture, unreferenced), picture.url);
  assert.equal(catalogInlineAssetPreviewUrl(picture.url, unreferenced), picture.url);

  const doc = {
    pictures: [picture, { url: "https://example.org/ext.png" }],
    translations: {
      "zh-CN": {
        body: `正文插图 ![内页 [P1]](/api/storage/assets/${bodyAssetID}/content)![紧邻](/api/storage/assets/${bodyAssetID2}/content)\n[ref1]: </api/storage/assets/${id}/content>\n[ref2]: /api/storage/assets/${bodyAssetID3}/content`,
      },
    },
  };
  const allowed = extractDocumentAssetIds(doc);
  assert.deepEqual([...allowed].sort(), [id, assetID, bodyAssetID, bodyAssetID2, bodyAssetID3].sort());
  const referenced: CatalogAssetContext = { kind: "workspace", id, allowedAssetIds: allowed };
  assert.equal(catalogInlineAssetPreviewUrl(`/api/storage/assets/${bodyAssetID}/content`, referenced), `/api/catalog/workspaces/${id}/assets/${bodyAssetID}/content`);
});
