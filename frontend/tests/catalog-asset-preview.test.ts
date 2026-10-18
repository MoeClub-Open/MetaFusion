import { test } from "node:test";
import assert from "node:assert/strict";
import { catalogPicturePreviewUrl, type CatalogAssetContext } from "../src/lib/catalogAssetPreview";

const id = "11111111-2222-4333-8444-555555555555";
const assetID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const picture = { url: `/api/storage/assets/${assetID}/content`, asset_id: assetID };

test("private preview projects each context without mutating the authoritative URL", () => {
  for (const [kind, path] of [["workspace", "workspaces"], ["pull-request", "pull-requests"], ["commit", "commits"]] as const) {
    assert.equal(catalogPicturePreviewUrl(picture, { kind, id }), `/api/catalog/${path}/${id}/assets/${assetID}/content`);
    assert.equal(picture.url, `/api/storage/assets/${assetID}/content`);
  }
  assert.equal(catalogPicturePreviewUrl(picture), picture.url);
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
});
