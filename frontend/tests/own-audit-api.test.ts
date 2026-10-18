import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchOwnAuditLogs } from "../src/lib/api/audit";

test("personal audit queries always filter the supplied viewer, including pagination", async () => {
  const originalFetch = globalThis.fetch;
  const requests: URL[] = [];
  globalThis.fetch = (async (url) => {
    const request = new URL(String(url), "http://localhost");
    requests.push(request);
    const actor = request.searchParams.get("actor_user_id");
    return Response.json({ items: [{ id: "audit-" + actor, actor_user_id: actor }], total: 1, page: 2, per_page: 10 });
  }) as typeof fetch;
  try {
    for (const viewer of ["11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"]) {
      const result = await fetchOwnAuditLogs(viewer, { page: 2, per_page: 10 });
      const request = requests.at(-1)!;
      assert.equal(request.pathname, "/api/admin/audit-logs");
      assert.equal(request.searchParams.get("actor_user_id"), viewer);
      assert.equal(request.searchParams.get("page"), "2");
      assert.equal(request.searchParams.get("per_page"), "10");
      assert.equal(result.items[0]?.actor_user_id, viewer);
    }
    assert.equal(requests.length, 2);
  } finally { globalThis.fetch = originalFetch; }
});

test("filters and cancellation cannot override the personal audit scope", async () => {
  const originalFetch = globalThis.fetch;
  const controller = new AbortController();
  let request: URL | undefined;
  let signal: AbortSignal | null | undefined;
  globalThis.fetch = (async (url, init) => {
    request = new URL(String(url), "http://localhost");
    signal = init?.signal;
    return Response.json({ items: [], total: 0 });
  }) as typeof fetch;
  try {
    const query = { service: " catalog ", action: "pull_request.merged", result: "success" as const,
      from: "2026-10-18T00:00:00Z", to: "2026-10-18T23:59:59.999999999Z", target_id: " object ", request_id: " req ",
      actor_user_id: "someone-else", actor: "admin" };
    await fetchOwnAuditLogs("viewer", query, controller.signal);
    assert.equal(request!.searchParams.get("actor_user_id"), "viewer");
    assert.equal(request!.searchParams.has("actor"), false);
    assert.equal(request!.searchParams.get("service"), "catalog");
    assert.equal(request!.searchParams.get("action"), query.action);
    assert.equal(request!.searchParams.get("result"), "success");
    assert.equal(request!.searchParams.get("to"), query.to);
    assert.equal(request!.searchParams.get("target_id"), "object");
    assert.equal(request!.searchParams.get("request_id"), "req");
    assert.equal(signal, controller.signal);
  } finally { globalThis.fetch = originalFetch; }
});

test("unavailable audit rows are errors rather than an empty history", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = (async () => Response.json({ total: 12 })) as typeof fetch;
  try { await assert.rejects(fetchOwnAuditLogs("viewer")); }
  finally { globalThis.fetch = originalFetch; }
});

test("personal audit queries reject a missing viewer before requesting any records", async () => {
  const originalFetch = globalThis.fetch;
  let requests = 0;
  globalThis.fetch = (async () => {
    requests += 1;
    return Response.json({ items: [], total: 0 });
  }) as typeof fetch;
  try {
    await assert.rejects(fetchOwnAuditLogs(""), /actor_user_id_required/);
    await assert.rejects(fetchOwnAuditLogs("   "), /actor_user_id_required/);
    assert.equal(requests, 0);
  } finally { globalThis.fetch = originalFetch; }
});
