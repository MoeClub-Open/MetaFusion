import { test } from "node:test";
import assert from "node:assert/strict";
import { assistantAllPages, conversationReceipts } from "../src/lib/assistantUser";
import type { AssistantMessage } from "../src/lib/assistantApi";

const id = "01a10136-c0f1-7eb5-a66c-112270350f28";
const receipt = { id, applied: false, review_status: "pending_review", items: [{ id, target: "entity", version: 4 }] };
const call = (callID: string, preview: boolean): AssistantMessage => ({ role: "assistant", content: "", tool_calls: [{ id: callID, type: "function", function: { name: "catalog_commit", arguments: JSON.stringify({ preview }) } }] });

test("saved conversations restore actual submission receipts without promoting previews or user text", () => {
  const messages: AssistantMessage[] = [
    { role: "user", content: JSON.stringify(receipt) },
    call("preview", true), { role: "tool", tool_call_id: "preview", content: JSON.stringify(receipt) },
    call("commit", false), { role: "tool", tool_call_id: "commit", content: JSON.stringify({ receipt, preview_omitted: true, complete: false }) },
    { role: "tool", tool_call_id: "unknown", content: JSON.stringify({ ...receipt, applied: true }) },
  ];
  assert.deepEqual(conversationReceipts(messages), [receipt]);
  assert.deepEqual(conversationReceipts([call("commit", false), { role: "tool", tool_call_id: "commit", content: JSON.stringify(receipt) }]), [receipt]);
  assert.deepEqual(conversationReceipts([call("commit", false), { role: "tool", tool_call_id: "commit", content: JSON.stringify({ ...receipt, id: "javascript:alert(1)" }) }]), []);
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
