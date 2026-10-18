import { test } from "node:test";
import assert from "node:assert/strict";
import { assistantAllPages, conversationReceipts } from "../src/lib/assistantUser";
import type { AssistantMessage } from "../src/lib/assistantApi";

const id = "01a10136-c0f1-7eb5-a66c-112270350f28";
const commitID = "01a10136-c0f1-7eb5-a66c-112270350f29";
const receipt = { applied: false, workspace: { id, head_commit_id: commitID }, commits: [{ id: commitID, merged_at: null, changes: [{ id, target: "entity" }] }], pull_request: { id, status: "open" } };
const call = (callID: string, name = "catalog_workspace_push"): AssistantMessage => ({ role: "assistant", content: "", tool_calls: [{ id: callID, type: "function", function: { name, arguments: "{}" } }] });

test("saved conversations restore push outcomes without promoting staged edits or user text", () => {
  const messages: AssistantMessage[] = [
    { role: "user", content: JSON.stringify(receipt) },
    call("stage", "catalog_workspace_stage"), { role: "tool", tool_call_id: "stage", content: JSON.stringify(receipt) },
    call("push"), { role: "tool", tool_call_id: "push", content: JSON.stringify({ receipt, details_omitted: true }) },
    { role: "tool", tool_call_id: "unknown", content: JSON.stringify({ ...receipt, applied: true }) },
  ];
  assert.deepEqual(conversationReceipts(messages), [receipt]);
  assert.deepEqual(conversationReceipts([call("push"), { role: "tool", tool_call_id: "push", content: JSON.stringify(receipt) }]), [receipt]);
  assert.deepEqual(conversationReceipts([call("push"), { role: "tool", tool_call_id: "push", content: JSON.stringify({ ...receipt, workspace: { id: "javascript:alert(1)", head_commit_id: commitID } }) }]), []);
  const merged = { ...receipt, applied: true, pull_request: null, commits: [{ ...receipt.commits[0], merged_at: "2026-10-18T00:00:00Z" }] };
  assert.deepEqual(conversationReceipts([call("merge", "catalog_pull_request_merge"), { role: "tool", tool_call_id: "merge", content: JSON.stringify(merged) }]), [merged]);
  const reviewer = { ...merged, workspace: null, pull_request: { id, status: "merged" } };
  assert.deepEqual(conversationReceipts([call("review", "catalog_pull_request_merge"), { role: "tool", tool_call_id: "review", content: JSON.stringify(reviewer) }]), [reviewer]);

});

test("assistant lists follow total and reject incomplete pages instead of silently truncating", async () => {
  const originalFetch = globalThis.fetch;
  const urls: string[] = [];
  try {
    globalThis.fetch = (async (url) => {
      urls.push(String(url));
      const offset = Number(new URL(String(url)).searchParams.get("offset"));
      return Response.json({ items: offset === 0 ? [{ id: "a" }] : [{ id: "b" }], total: 2, limit: 100, offset });
    }) as typeof fetch;
    assert.deepEqual(await assistantAllPages("/models"), [{ id: "a" }, { id: "b" }]);
    assert.equal(urls.length, 2);
    assert.equal(new URL(urls[1]).searchParams.get("offset"), "1");
    globalThis.fetch = (async () => Response.json({ items: [], total: 2, limit: 100, offset: 0 })) as typeof fetch;
    await assert.rejects(assistantAllPages("/models"), /assistant_incomplete_page/);
  } finally { globalThis.fetch = originalFetch; }
});
