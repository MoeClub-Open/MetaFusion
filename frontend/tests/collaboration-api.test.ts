import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiError } from "../src/lib/api/client";
import { createWorkspaceCommit, listPullRequests, listWorkspaces, mergePullRequest, rebaseWorkspace } from "../src/lib/api/collaboration";

test("push review preserves the expected PR version and immutable head", async () => {
  const original = globalThis.fetch;
  let request: { url: string; body: unknown } | undefined;
  globalThis.fetch = (async (url, options) => {
    request = { url: String(url), body: JSON.parse(String(options?.body)) };
    return Response.json({ applied: true, workspace: {}, commits: [], pull_request: {} });
  }) as typeof fetch;
  try {
    await mergePullRequest("pr/id", { expected_version: 12, expected_head: "reviewed-head" });
    assert.ok(request?.url.endsWith("/catalog/pull-requests/pr%2Fid/merge"));
    assert.deepEqual(request?.body, { expected_version: 12, expected_head: "reviewed-head" });
  } finally { globalThis.fetch = original; }
});

test("workspace commit retries preserve caller identity and need no source evidence", async () => {
  const original = globalThis.fetch;
  const bodies: unknown[] = [];
  globalThis.fetch = (async (_url, options) => {
    bodies.push(JSON.parse(String(options?.body)));
    return Response.json({ id: "stable-id", changes: [] });
  }) as typeof fetch;
  try {
    const input = { id: "stable-id", expected_version: 5, message: "Several edits" };
    await createWorkspaceCommit("copy", input);
    await createWorkspaceCommit("copy", input);
    assert.deepEqual(bodies, [input, input]);
    assert.ok(bodies.every(body => !("sources" in (body as object))));
  } finally { globalThis.fetch = original; }
});

test("rebase keeps every field conflict and exact current version for resolution", async () => {
  const original = globalThis.fetch;
  const details = { error: "workspace_conflict", applied: false,
    conflicts: [{ target: "entity", id: "entry", pointer: "/title", base_version: 1, current_version: 7, base: "base", current: "theirs", proposed: "mine", remove: false, kind: "field" }] };
  globalThis.fetch = (async () => Response.json(details, { status: 409 })) as typeof fetch;
  try {
    await assert.rejects(rebaseWorkspace("copy", { id: "rebase-id", expected_version: 3, expected_head: "head" }), error => {
      assert.ok(error instanceof ApiError);
      assert.equal(error.status, 409);
      assert.deepEqual(error.details, details);
      return true;
    });
  } finally { globalThis.fetch = original; }
});

test("collaboration lists request server pagination rather than truncating locally", async () => {
  const original = globalThis.fetch;
  const urls: string[] = [];
  globalThis.fetch = (async url => {
    urls.push(String(url));
    return Response.json({ items: [], total: 145, limit: 20, offset: 120 });
  }) as typeof fetch;
  try {
    const workspaces = await listWorkspaces({ limit: 20, offset: 120 });
    const requests = await listPullRequests({ status: "merged", limit: 20, offset: 120 });
    assert.equal(workspaces.total, 145); assert.equal(requests.total, 145);
    assert.ok(urls[0].endsWith("/catalog/workspaces?limit=20&offset=120"));
    assert.ok(urls[1].endsWith("/catalog/pull-requests?status=merged&limit=20&offset=120"));
  } finally { globalThis.fetch = original; }
});

test("a canceled session cannot replay a commit after cookie refresh", async () => {
  const original = globalThis.fetch;
  const session = new AbortController();
  let accepted = 0;
  globalThis.fetch = (async (url, options) => {
    if (String(url).endsWith("/auth/refresh")) {
      session.abort();
      return Response.json({});
    }
    assert.equal(options?.signal, session.signal);
    if (options?.signal?.aborted) throw new DOMException("Session changed", "AbortError");
    if (accepted++ === 0) return Response.json({ error: "expired" }, { status: 401 });
    throw new Error("A canceled session must never accept this write");
  }) as typeof fetch;
  try {
    await assert.rejects(createWorkspaceCommit("copy", { id: "stable-id", expected_version: 5, message: "Old identity" }, session.signal), error =>
      error instanceof DOMException && error.name === "AbortError");
    assert.equal(accepted, 1);
  } finally { globalThis.fetch = original; }
});
