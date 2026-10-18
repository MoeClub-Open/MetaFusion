import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { activityChanges, activityTargetHref, activityTargetName } from "../src/lib/activity";
import { ACTIVITY_ACTIONS } from "../src/lib/activity-actions";
import type { AuditLogEntry } from "../src/lib/api/audit";

const entry: AuditLogEntry = { id: "log", service: "catalog", action: "pull_request.merged", occurred_at: "2026-10-18T00:00:00Z", target_type: "pull_request", target_id: "11111111-1111-1111-1111-111111111111" };

test("activity links use only known object routes and canonical IDs", () => {
  assert.equal(activityTargetHref(entry), "/pull-requests/" + entry.target_id);
  for (const target_type of ["https://evil.test", "__proto__", "constructor", "unknown"])
    assert.equal(activityTargetHref({ ...entry, target_type }), undefined);
  for (const target_id of ["../settings", "javascript:alert(1)", entry.target_id + "/edit", "", "//evil.test"])
    assert.equal(activityTargetHref({ ...entry, target_id }), undefined);
});

test("activity captions prefer captured names and do not invent missing history", () => {
  assert.equal(activityTargetName({ ...entry, changes: { target_name: "Original title", title: "Another title" } }), "Original title");
  assert.equal(activityTargetName({ ...entry, changes: { title: { before: "Old", after: "New" } } }), "New");
  assert.equal(activityTargetName(entry), "");
});

test("readable changes preserve nulls, deletions, booleans, arrays and redactions", () => {
  assert.deepEqual(activityChanges(undefined), []);
  const rows = activityChanges({ before: { name: "old", removed: true, unchanged: 1 }, after: { name: null, added: ["[redacted]"], unchanged: 1 } });
  assert.deepEqual(rows, [
    { field: "name", before: "old", after: null, hasBefore: true, hasAfter: true },
    { field: "removed", before: true, after: undefined, hasBefore: true, hasAfter: false },
    { field: "added", before: undefined, after: ["[redacted]"], hasBefore: false, hasAfter: true },
  ]);
  assert.deepEqual(activityChanges({ target_name: "title", workspace_id: "id", _truncated: true, applied: false, field: { before: null, after: "[redacted]" } }), [
    { field: "applied", after: false, hasBefore: false, hasAfter: true },
    { field: "field", before: null, after: "[redacted]", hasBefore: true, hasAfter: true },
  ]);
});

test("all registered audit actions have human labels in each site language", () => {
  const sources = [
    "../../backend/internal/catalog/audit.go", "../../backend/internal/assistant/audit.go",
  ];
  const codes = new Set(ACTIVITY_ACTIONS.map((action) => action.code));
  for (const source of sources) {
    const content = readFileSync(new URL(source, import.meta.url), "utf8");
    for (const match of content.matchAll(/"(?:POST|PUT|PATCH|DELETE) [^"]+":\s*"([a-z_]+\.[a-z_]+)"/g))
      assert.ok(codes.has(match[1]), `missing action option: ${match[1]}`);
  }
  for (const locale of ["zh-CN", "en-US", "zh-TW", "ja-JP"]) {
    const messages = JSON.parse(readFileSync(new URL(`../src/messages/${locale}.json`, import.meta.url), "utf8"));
    for (const code of codes) assert.ok(messages[`settings.activity.action.${code}`], `${locale}: ${code}`);
  }
});
