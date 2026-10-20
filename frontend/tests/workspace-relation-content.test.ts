import { test } from "node:test";
import assert from "node:assert/strict";
import { checkoutRelationObjects, relationWorkspaceItems } from "../src/components/editor/RelationEditorField";
import { trackContentOperation } from "../src/components/catalog/TrackContentEditor";
import type { Workspace, WorkspaceObject } from "../src/lib/api/collaboration";

const relation = (id: string, position = 0) => ({
  id, version: 3, type: "credit", source_id: "subject", target_id: "person", position, attributes: { role: "composer" },
});
const workspace = (objects: WorkspaceObject[]): Workspace => ({
  id: "copy", owner_id: "owner", name: "Working copy", version: 6, head_commit_id: null, state: "active",
  members: [], objects, base_commit_id: null, pending_commits: 0, dirty_count: objects.length, created_at: "", updated_at: "",
});
const object = (id: string, document: WorkspaceObject["document"], base: WorkspaceObject["base"] = null): WorkspaceObject => ({
  target: "relation", id, base_version: base ? 3 : 0, base, committed: base, document, dirty: true,
});

test("relationship editing uses staged updates, creations and deletions together without changing main facts", () => {
  const kept = relation("kept", 1);
  const removed = relation("removed", 2);
  const sources = [{ kind: "publication", citation: "Saved publisher credit" }];
  const staged = { ...kept, position: 4, attributes: { role: "arranger" }, sources };
  const created = { ...relation("new", 0), source_id: "other-person", target_id: "subject" };
  const copy = workspace([
    object("kept", staged, kept), object("removed", null, removed), object("new", created),
    object("unrelated", { ...relation("unrelated"), source_id: "another", target_id: "elsewhere" }),
  ]);
  const projected = relationWorkspaceItems(copy, "subject", [kept, removed]);
  assert.deepEqual(projected.map((item) => item.id), ["new", "kept"]);
  assert.equal(projected[0].version, 0);
  assert.deepEqual(projected[1].attributes, { role: "arranger" });
  assert.deepEqual((projected[1] as unknown as Record<string, unknown>).sources, sources);
  assert.equal(kept.position, 1);
  assert.deepEqual(kept.attributes, { role: "composer" });
  assert.equal(copy.objects[1].document, null);
});

test("content replacement preserves existing evidence as data while needing no new evidence interaction", () => {
  const record = { expression_id: "expression", position: 5, locator: { track: 2 }, attributes: { title: "Saved" },
    sources: [{ kind: "url", citation: "Publisher", url: "https://example.test/track" }] };
  const operation = trackContentOperation("track", record, 2, false);
  assert.equal(operation.action, "content");
  assert.equal(operation.content_change?.action, "replace");
  assert.equal(operation.content_change?.position, 2);
  assert.deepEqual(operation.content_change?.inclusion, record);
  record.sources[0].citation = "Changed after save";
  assert.equal((operation.content_change?.inclusion as typeof record).sources[0].citation, "Publisher");
  assert.ok(!("source_refs" in operation));
});

test("new inclusions and removals use the original inclusion position without requiring a source", () => {
  const record = { expression_id: "expression", position: 7, locator: {}, attributes: {} };
  const addition = trackContentOperation("track", record, null, false);
  assert.equal(addition.content_change?.action, "add");
  assert.deepEqual(addition.content_change?.inclusion, record);
  const removal = trackContentOperation("track", record, 2, true);
  assert.deepEqual(removal.content_change, { action: "delete", position: 2 });
});

test("large relationship collections checkout in bounded batches using each returned workspace version", async () => {
  const originalFetch = globalThis.fetch;
  const calls: { expected_version: number; relation_ids: string[]; entity_ids: string[] }[] = [];
  globalThis.fetch = (async (url, options) => {
    assert.ok(String(url).endsWith("/catalog/workspaces/copy/checkout"));
    const body = JSON.parse(String(options?.body));
    calls.push(body);
    return Response.json({ ...workspace([]), version: body.expected_version + 1 });
  }) as typeof fetch;
  try {
    const ids = Array.from({ length: 205 }, (_, index) => `relation-${index}`);
    const result = await checkoutRelationObjects(workspace([]), ids);
    assert.deepEqual(calls.map((call) => call.relation_ids.length), [100, 100, 5]);
    assert.deepEqual(calls.map((call) => call.expected_version), [6, 7, 8]);
    assert.deepEqual(calls.flatMap((call) => call.relation_ids), ids);
    assert.ok(calls.every((call) => call.entity_ids.length === 0));
    assert.equal(result.version, 9);
  } finally { globalThis.fetch = originalFetch; }
});

test("aborting a relationship checkout prevents a later batch from mutating another session", async () => {
  const originalFetch = globalThis.fetch;
  const controller = new AbortController();
  let calls = 0;
  globalThis.fetch = (async () => {
    calls += 1;
    controller.abort();
    return Response.json({ ...workspace([]), version: 7 });
  }) as typeof fetch;
  try {
    await assert.rejects(checkoutRelationObjects(workspace([]), Array.from({ length: 101 }, (_, index) => `relation-${index}`), controller.signal), { name: "AbortError" });
    assert.equal(calls, 1);
  } finally { globalThis.fetch = originalFetch; }
});
