import { test } from "node:test";
import assert from "node:assert/strict";
import type { WorkspaceConflict } from "../src/lib/api/collaboration";
import { workspaceConflictResolution } from "../src/lib/workspaceConflicts";

const conflict: WorkspaceConflict = {
  target: "entity", id: "entry", pointer: "/attributes/optional", base_version: 1, current_version: 7,
  base: "before", current: null, proposed: null,
  base_present: true, current_present: false, proposed_present: true, remove: false, kind: "field",
};

test("taking either side preserves field deletion separately from an explicit JSON null", () => {
  const deleted = workspaceConflictResolution(conflict, { choice: "current", custom: "" });
  assert.deepEqual(deleted, { target: "entity", id: "entry", pointer: "/attributes/optional", current_version: 7, remove: true });
  assert.ok(!("value" in deleted));
  const nullable = workspaceConflictResolution(conflict, { choice: "proposed", custom: "" });
  assert.equal(nullable.remove, false); assert.equal(nullable.value, null);
  const reversed = { ...conflict, current_present: true, proposed_present: false };
  assert.deepEqual(workspaceConflictResolution(reversed, { choice: "proposed", custom: "" }), deleted);
  assert.equal(workspaceConflictResolution(reversed, { choice: "current", custom: "" }).remove, false);
});

test("custom null and an explicit remove remain different choices", () => {
  const custom = workspaceConflictResolution(conflict, { choice: "custom", custom: "null" });
  assert.equal(custom.remove, false); assert.equal(custom.value, null);
  const removed = workspaceConflictResolution(conflict, { choice: "remove", custom: "null" });
  assert.equal(removed.remove, true); assert.ok(!("value" in removed));
  assert.throws(() => workspaceConflictResolution(conflict, { choice: "custom", custom: "invalid" }), SyntaxError);
});
