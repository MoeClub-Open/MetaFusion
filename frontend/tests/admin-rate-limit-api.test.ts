import { test } from "node:test";
import assert from "node:assert/strict";
import { saveAdminRateLimits, saveCommunityAdminRateLimits } from "../src/lib/api/admin";
import { mergeEntities } from "../src/lib/api/catalog";

test("service policy writes preserve community message limits and do not invent sources", async () => {
  const originalFetch = globalThis.fetch;
  const requests: Array<{ url: string; body: Record<string, unknown> }> = [];
  globalThis.fetch = (async (url, options) => {
    const body = JSON.parse(String(options?.body));
    requests.push({ url: String(url), body });
    return Response.json({ ...body, etag: "next", updated_at: "2026-10-11T00:00:00Z" });
  }) as typeof fetch;
  try {
    await saveAdminRateLimits({ default_per_minute: 180 }, "catalog-etag", "operational reason");
    const messages = { send_per_minute: 180, stranger_per_hour: 5, admin_unlimited: true };
    await saveCommunityAdminRateLimits({ groups: { admin: { per_minute: 30 } } }, messages, "community-etag", "remove exemption");
    assert.deepEqual(requests[0], { url: "/api/admin/rate-limits", body: {
      policy: { default_per_minute: 180 }, expected_etag: "catalog-etag", edit_note: "operational reason",
    } });
    assert.deepEqual(requests[1], { url: "/api/community/admin/rate-limits", body: {
      policy: { groups: { admin: { per_minute: 30 } } }, messages, expected_etag: "community-etag", edit_note: "remove exemption",
    } });
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("metadata lifecycle governance accepts an empty source list and keeps its version check", async () => {
  const originalFetch = globalThis.fetch;
  const requests: Array<{url: string; body?: Record<string, unknown>}> = [];
  globalThis.fetch = (async (url, options) => {
    requests.push({url: String(url), ...(options?.body ? {body: JSON.parse(String(options.body))} : {})});
    return Response.json({version: 3});
  }) as typeof fetch;
  try {
    await mergeEntities({ source_id: "source", target_id: "target", merge_note: "reason", sources: [] });
    assert.equal(requests.length, 2);
    assert.equal(new URL(requests[0].url, "https://example.test").pathname, "/api/catalog/entities/source");
    assert.equal(new URL(requests[1].url, "https://example.test").pathname, "/api/catalog/entities/source/lifecycle");
    assert.equal(requests[1].body?.expected_version, 3);
    assert.deepEqual(requests[1].body?.sources, []);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
