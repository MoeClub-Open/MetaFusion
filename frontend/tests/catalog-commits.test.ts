import { test } from "node:test";
import assert from "node:assert/strict";
import { ensureEditingWorkspace, entityOperation, stageEditingWorkspace, normalizeEditorEntity, rebaseEntityEdit, resolveEntityEdit } from "../src/lib/catalogCommits";
import { workspaceEntityChoices } from "../src/components/catalog/Fields";
import { emptyEntity } from "../src/components/catalog/api";
import type { Workspace } from "../src/lib/api/collaboration";

function copy(id = "workspace", owner = "author"): Workspace {
  return { id, owner_id: owner, name: "", version: 3, head_commit_id: null, state: "active", members: [],
    objects: [{ target: "entity", id: "entity", base_version: 1, base: { title: "old" }, committed: { title: "old" }, document: { title: "old" }, dirty: false }],
    base_commit_id: null, pending_commits: 0, dirty_count: 0, created_at: "2026-10-18T00:00:00Z", updated_at: "2026-10-18T00:00:00Z" };
}

test("editor excludes server fields and patches locales independently", () => {
  const base={...emptyEntity(),id:"id",version:7,translations:{en:{title:"Entry",body:"old"},ja:{title:"項目"}},attributes:{tags:["old"]}};
  const next=structuredClone(base);next.translations.en.body="new";
  const op=entityOperation(next,base);
  assert.deepEqual(op.patch,[{path:"/translations/en/body",value:"new"}]);
  assert.ok(!("reviewed_candidate_ids" in op));
  const created=entityOperation(next);
  assert.ok(!("state" in (created.document as object)));
  assert.ok(!("review_status" in (created.document as object)));
  const restored=entityOperation({...base,title:"Historical"},base);
  assert.deepEqual(restored.patch,[{path:"/title",value:"Historical"}]);
  assert.ok(!("base_version" in restored));
});

test("opening a default copy coalesces requests and isolates account ownership", async () => {
  const original = globalThis.fetch; const calls: string[] = [];
  globalThis.fetch=(async (url,opts)=>{
    calls.push(`${opts?.method || "GET"} ${url}`);
    if (opts?.method === "POST") return Response.json(copy("created-a", "editor-a"));
    return Response.json({ items: [copy("someone-else", "other")] });
  }) as typeof fetch;
  try {
    const [first, second] = await Promise.all([ensureEditingWorkspace("editor-a"), ensureEditingWorkspace("editor-a")]);
    assert.equal(first.id, "created-a"); assert.equal(second.id, first.id);
    assert.equal(calls.length, 2);
    globalThis.fetch = (async () => Response.json({items: [copy("copy-b", "editor-b"), copy("created-a", "editor-a")]})) as typeof fetch;
    assert.equal((await ensureEditingWorkspace("editor-b")).id, "copy-b");
  }finally{globalThis.fetch=original}
});

test("an explicit inaccessible copy fails without silently creating or switching copies", async () => {
  const original = globalThis.fetch; const calls: string[] = [];
  globalThis.fetch = (async (url) => { calls.push(String(url)); return Response.json({error: "forbidden"}, {status: 403}); }) as typeof fetch;
  try {
    await assert.rejects(ensureEditingWorkspace("viewer", "private-copy"));
    assert.equal(calls.length, 1); assert.ok(calls[0].endsWith("/catalog/workspaces/private-copy"));
  } finally { globalThis.fetch = original; }
});

test("saving accepts unrelated copy changes with CAS and never publishes facts", async () => {
  const original = globalThis.fetch; const snapshot = copy(); const latest = {...snapshot, version: 8};
  const calls: {url: string; method: string; body: any}[] = [];
  globalThis.fetch = (async (url, options) => {
    calls.push({url: String(url), method: options?.method || "GET", body: options?.body ? JSON.parse(String(options.body)) : undefined});
    return Response.json(options?.method === "PUT" ? {workspace: {...latest, version: 9}, refs: {}} : latest);
  }) as typeof fetch;
  try {
    await stageEditingWorkspace(snapshot, [{target: "entity", action: "update", id: "entity", patch: [{path: "/title", value: "mine"}]}]);
    assert.equal(calls[1].body.expected_version, 8);
    assert.ok(calls.every((call) => call.url.includes("/catalog/workspaces/")));
    assert.equal(calls[1].method, "PUT");
    assert.ok(!("sources" in calls[1].body));
  } finally { globalThis.fetch = original; }
});

test("saving preserves another editor's changes to the same object", async () => {
  const original = globalThis.fetch; const snapshot = copy(); let writes = 0;
  const latest = structuredClone(snapshot); latest.version += 1; latest.objects[0].document = {title: "theirs"};
  globalThis.fetch = (async (_url, options) => { if (options?.method === "PUT") writes++; return Response.json(latest); }) as typeof fetch;
  try {
    await assert.rejects(stageEditingWorkspace(snapshot, [{target: "entity", action: "update", id: "entity", patch: [{path: "/title", value: "mine"}]}]), /workspace_conflict/);
    assert.equal(writes, 0);
  } finally { globalThis.fetch = original; }
});

test("a lost create response cannot be replayed as a second object", async () => {
  const original = globalThis.fetch; const snapshot = copy(); let writes = 0;
  globalThis.fetch = (async (_url, options) => { if (options?.method === "PUT") writes++; return Response.json({...snapshot, version: snapshot.version + 1}); }) as typeof fetch;
  try {
    await assert.rejects(stageEditingWorkspace(snapshot, [{target: "entity", action: "create", ref: "new", document: {kind: "work", title: "once"}}]), /workspace_conflict/);
    assert.equal(writes, 0);
  } finally { globalThis.fetch = original; }
});

test("session cancellation after a read prevents the following workspace write", async () => {
  const original = globalThis.fetch; const snapshot = copy(); const controller = new AbortController(); let writes = 0;
  globalThis.fetch = (async (_url, options) => {
    if (options?.method === "PUT") writes++;
    else controller.abort();
    return Response.json(snapshot);
  }) as typeof fetch;
  try {
    await assert.rejects(stageEditingWorkspace(snapshot, [{target: "entity", action: "update", id: "entity", patch: [{path: "/title", value: "mine"}]}], controller.signal));
    assert.equal(writes, 0);
  } finally { globalThis.fetch = original; }
});

test("session cancellation during copy selection prevents creation under the next account", async () => {
  const original = globalThis.fetch; const controller = new AbortController(); let writes = 0;
  globalThis.fetch = (async (_url, options) => {
    if (options?.method === "POST") writes++;
    else controller.abort();
    return Response.json({items: []});
  }) as typeof fetch;
  try {
    await assert.rejects(ensureEditingWorkspace("cancelled-user", undefined, controller.signal));
    assert.equal(writes, 0);
  } finally { globalThis.fetch = original; }
});

test("refreshing a shared entity merges disjoint local and remote fields without losing either", () => {
  const base = {...emptyEntity(), id: "entity", title: "original", translations: {en: {title: "Name", body: "old"}}, attributes: {tags: ["original"]}};
  const mine = structuredClone(base); mine.translations.en.body = "my article";
  const theirs = structuredClone(base); theirs.title = "new name"; theirs.attributes.tags = ["remote"];
  const merged = rebaseEntityEdit(base, mine, theirs);
  assert.equal(merged.conflicts.length, 0);
  assert.equal(merged.entity.title, "new name");
  assert.equal(merged.entity.translations.en.body, "my article");
  assert.deepEqual(merged.entity.attributes.tags, ["remote"]);
  assert.equal(mine.title, "original");
});

test("overlapping local edits require choices while arrays and deletions preserve exact intent", () => {
  const base = {...emptyEntity(), id: "entity", title: "original", attributes: {tags: ["old"], "a/b~c": "old"}};
  const mine = structuredClone(base); mine.title = "mine"; mine.attributes.tags = ["mine"];
  delete (mine.attributes as Record<string, unknown>)["a/b~c"];
  const theirs = structuredClone(base); theirs.title = "theirs"; theirs.attributes.tags = ["theirs"]; theirs.attributes["a/b~c"] = "theirs";
  const rebase = rebaseEntityEdit(base, mine, theirs);
  assert.deepEqual(rebase.conflicts.map((conflict) => conflict.pointer), ["/title", "/attributes/tags", "/attributes/a~1b~0c"]);
  assert.throws(() => resolveEntityEdit(rebase, {}), /workspace_resolution_required/);
  const resolved = resolveEntityEdit(rebase, {
    "/title": {choice: "current"}, "/attributes/tags": {choice: "custom", value: ["both"]},
    "/attributes/a~1b~0c": {choice: "proposed"},
  });
  assert.equal(resolved.title, "theirs"); assert.deepEqual(resolved.attributes.tags, ["both"]);
  assert.ok(!Object.hasOwn(resolved.attributes, "a/b~c"));
  assert.equal(theirs.attributes["a/b~c"], "theirs");
});

test("entity pickers include new copy records, overlay updates and hide staged deletions within kind and scope", () => {
  const main = [{...emptyEntity("work"), id: "kept", title: "Public"}, {...emptyEntity("expression"), id: "deleted", title: "Removed"}];
  const snapshot = copy(); snapshot.objects = [
    {target: "entity", id: "new-expression", base_version: 0, base: null, committed: null,
      document: {...emptyEntity("expression"), title: "New recording", work_id: "work-a"}, dirty: true},
    {target: "entity", id: "deleted", base_version: 1, base: {}, committed: {}, document: null, dirty: true},
  ];
  assert.deepEqual(workspaceEntityChoices(snapshot, main, "", ["expression"], "&work_id=work-a").map((item) => item.id), ["new-expression"]);
  assert.deepEqual(workspaceEntityChoices(snapshot, main, "recording", ["expression"]).map((item) => item.id), ["new-expression"]);
  assert.equal(workspaceEntityChoices(null, main, "", ["work"])[0].id, "kept");
});

test("conflict choices cannot remove mandatory editor shapes or replace collections with scalars", () => {
  const base = {...emptyEntity(), id: "entity", title: "base"};
  const mine = {...base, title: "mine"}; const current = {...base, title: "current"};
  const conflict = rebaseEntityEdit(base, mine, current);
  assert.throws(() => resolveEntityEdit(conflict, {"/title": {choice: "remove"}}), /workspace_resolution_invalid/);
  assert.throws(() => resolveEntityEdit(conflict, {"/title": {choice: "custom", value: null}}), /workspace_resolution_invalid/);
  const pictureConflict = { entity: current, conflicts: [{pointer: "/pictures", base: [], current: [], proposed: [], remove: false}] };
  assert.throws(() => resolveEntityEdit(pictureConflict, {"/pictures": {choice: "custom", value: {url: "bad"}}}), /workspace_resolution_invalid/);
  assert.equal(resolveEntityEdit(conflict, {"/title": {choice: "custom", value: "reviewed"}}).title, "reviewed");
});

test("a create write with a lost response is marked uncertain rather than silently replayed", async () => {
  const original = globalThis.fetch; const snapshot = copy(); let writes = 0;
  globalThis.fetch = (async (_url, options) => {
    if (options?.method === "PUT") { writes++; throw new TypeError("connection lost after write"); }
    return Response.json(snapshot);
  }) as typeof fetch;
  try {
    await assert.rejects(stageEditingWorkspace(snapshot, [{target: "entity", action: "create", ref: "entity", document: {kind: "work", title: "once"}}]), /workspace_write_uncertain/);
    assert.equal(writes, 1);
  } finally { globalThis.fetch = original; }
});

test("minimal API-created drafts with null collections can be opened and edited safely", () => {
  const document = {kind: "work", title: "Minimal", translations: null, attributes: null,
    pictures: null, external_links: null, contents: null, subjects: null};
  const entity = normalizeEditorEntity(document);
  assert.equal(entity.title, "Minimal");
  assert.deepEqual(Object.keys(entity.translations), []);
  assert.deepEqual(entity.pictures, []); assert.deepEqual(entity.contents, []);
  assert.equal(document.translations, null);
  const track = normalizeEditorEntity({kind: "track", title: "Track", contents: [{expression_id: "expression", position: 0, locator: null,
    sources: [{kind: "publication", citation: "Saved source"}]}]});
  assert.deepEqual(track.contents[0].locator, {});
  assert.equal(track.contents[0].sources?.[0].citation, "Saved source");
  assert.throws(() => normalizeEditorEntity({kind: "work", title: null}), /invalid_workspace_entity/);
  assert.throws(() => normalizeEditorEntity({kind: "work", title: "Broken", pictures: {url: "wrong shape"}}), /invalid_workspace_entity/);
});
