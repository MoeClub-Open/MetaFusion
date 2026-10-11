import { test } from "node:test";
import assert from "node:assert/strict";
import { saveAdminRateLimits, saveCommunityAdminRateLimits } from "../src/lib/api/admin";
import { mergeEntities, unpublishEntity } from "../src/lib/api/catalog";

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

test("metadata lifecycle wrappers reject missing evidence before making a request", async () => {
  const originalFetch = globalThis.fetch;
  let requests = 0;
  globalThis.fetch = (async () => { requests += 1; return Response.json({ version: 1 }); }) as typeof fetch;
  try {
    await assert.rejects(unpublishEntity({ entity_id: "source", expected_version: 1, edit_note: "reason", sources: [] }), /evidence_required/);
    await assert.rejects(mergeEntities({ source_id: "source", target_id: "target", merge_note: "reason", sources: [{ kind: "self", citation: "reason" }] }), /source_not_eligible/);
    assert.equal(requests, 0);
  } finally {
    globalThis.fetch = originalFetch;
  }
});
