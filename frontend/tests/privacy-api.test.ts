import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchPrivacySettings, normalizeSessionUser, updatePrivacySettings } from "../src/lib/api/auth";
import { fetchFavorites } from "../src/lib/api/community";
import { fetchUserCommunityStats, fetchUserProfile } from "../src/lib/api/users";

test("privacy reads preserve explicit false and patches return the authoritative pair", async () => {
  const original = globalThis.fetch;
  const requests: Array<{ url: string; method: string; body: unknown }> = [];
  globalThis.fetch = (async (url, init) => {
    requests.push({ url: String(url), method: init?.method ?? "GET", body: init?.body ? JSON.parse(String(init.body)) : null });
    return Response.json({ favorites_public: false, email_public: true });
  }) as typeof fetch;
  try {
    assert.deepEqual(await fetchPrivacySettings(), { favorites_public: false, email_public: true });
    assert.deepEqual(await updatePrivacySettings({ email_public: true }), { favorites_public: false, email_public: true });
    assert.ok(requests.every((item) => item.url.endsWith("/api/auth/privacy")));
    assert.equal(requests[0]?.method, "GET");
    assert.equal(requests[1]?.method, "PATCH");
    assert.deepEqual(requests[1]?.body, { email_public: true });
  } finally { globalThis.fetch = original; }
});

test("missing privacy fields and rejected writes stay failures", async () => {
  const original = globalThis.fetch;
  try {
    for (const malformed of [{}, { favorites_public: false }, { favorites_public: null, email_public: false }]) {
      globalThis.fetch = (async () => Response.json(malformed)) as typeof fetch;
      await assert.rejects(fetchPrivacySettings(), /invalid_response: privacy_settings/);
    }
    globalThis.fetch = (async () => Response.json({ error: "authentication_required" }, { status: 403 })) as typeof fetch;
    await assert.rejects(updatePrivacySettings({ favorites_public: false }), /authentication_required/);
  } finally { globalThis.fetch = original; }
});

test("session normalization keeps known privacy choices without inventing missing choices", () => {
  assert.deepEqual(normalizeSessionUser({ id: "me", username: "me", favorites_public: false, email_public: true }), {
    id: "me", username: "me", favorites_public: false, email_public: true,
  });
  const unknown = normalizeSessionUser({ id: "me", username: "me", favorites_public: null });
  assert.equal("favorites_public" in unknown, false);
  assert.equal("email_public" in unknown, false);
});

test("private favorites remain hidden while their count stays null", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = (async (url) => {
    if (String(url).includes("/favorites")) return Response.json({ items: [], total: 0, visible: false });
    return Response.json({ stats: { topics_created: 2, comments_created: 3, favorites_count: null } });
  }) as typeof fetch;
  try {
    assert.deepEqual(await fetchFavorites("other"), { items: [], total: 0, visible: false });
    assert.equal((await fetchUserCommunityStats("other")).favorites_count, null);
    globalThis.fetch = (async () => Response.json({ items: [], total: 0 })) as typeof fetch;
    await assert.rejects(fetchFavorites("other"), /invalid_response: visible/);
  } finally { globalThis.fetch = original; }
});

test("public profiles preserve an omitted private email and explicit privacy flags", async () => {
  const original = globalThis.fetch;
  globalThis.fetch = (async () => Response.json({ user: { id: "other", username: "other", favorites_public: false, email_public: false }, stats: {} })) as typeof fetch;
  try {
    const result = await fetchUserProfile("other");
    assert.equal(result.user.email, undefined);
    assert.equal(result.user.favorites_public, false);
    globalThis.fetch = (async () => Response.json({ user: { id: "other", username: "other" }, stats: {} })) as typeof fetch;
    await assert.rejects(fetchUserProfile("other"), /invalid_response: profile_privacy/);
  } finally { globalThis.fetch = original; }
});
