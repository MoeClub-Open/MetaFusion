import { test } from "node:test";
import assert from "node:assert/strict";
import { applyPatch } from "diff";
import { buildRevisionDiff, buildTextDiff, diffText, isRevisionSnapshot, serializeRevisionContent } from "../src/components/catalog/revisionDiff";
import { revisionChanges, revisionContent } from "../src/components/catalog/revisionData";

test("an inserted line keeps following lines aligned and carries both line numbers", () => {
  const diff = buildTextDiff("one\ntwo\nthree\n", "one\ninserted\ntwo\nthree\n", "v1", "v2");
  assert.equal(diff.additions, 1);
  assert.equal(diff.deletions, 0);
  assert.deepEqual(diff.hunks[0].lines.map(({ kind, oldLine, newLine }) => [kind, oldLine, newLine]), [
    ["context", 1, 1], ["add", null, 2], ["context", 2, 3], ["context", 3, 4],
  ]);
  assert.equal(applyPatch("one\ntwo\nthree\n", diff.unified), "one\ninserted\ntwo\nthree\n");
});

test("duplicate and reordered lines produce a real patch rather than index-based substitutions", () => {
  const before = "start\nrepeat\na\nrepeat\nb\nend\n";
  const after = "start\na\nrepeat\nb\nrepeat\nend\n";
  const diff = buildTextDiff(before, after, "v1", "v2");
  assert.equal(diff.additions, 1);
  assert.equal(diff.deletions, 1);
  assert.equal(applyPatch(before, diff.unified), after);
});

test("distant edits get separate hunks with correct positions and context", () => {
  const lines = Array.from({ length: 30 }, (_, i) => `line ${i + 1}`);
  const changed = [...lines];
  changed[1] = "changed early";
  changed[27] = "changed late";
  const before = lines.join("\n") + "\n";
  const after = changed.join("\n") + "\n";
  const diff = buildTextDiff(before, after, "v1", "v2", 2);
  assert.equal(diff.hunks.length, 2);
  assert.equal(diff.hunks[0].header, "@@ -1,4 +1,4 @@");
  assert.equal(diff.hunks[1].header, "@@ -26,5 +26,5 @@");
  assert.equal(applyPatch(before, diff.unified), after);
});

test("first version has an explicit empty baseline while absent snapshots stay unavailable", () => {
  const snapshot = { kind: "work", title: "First" };
  const diff = buildRevisionDiff(null, snapshot, "initial", "v1");
  assert.equal(diff.hunks[0].oldStart, 0);
  assert.equal(diff.hunks[0].oldLines, 0);
  assert.equal(applyPatch("", diff.unified), serializeRevisionContent(snapshot));
  for (const missing of [undefined, [], "metadata"]) {
    assert.throws(() => buildRevisionDiff(missing, snapshot, "v0", "v1"), /revision_snapshot_unavailable/);
  }
  assert.throws(() => buildRevisionDiff(snapshot, undefined, "v1", "v2"), /revision_snapshot_unavailable/);
  assert.throws(() => buildRevisionDiff(snapshot, null, "v1", "v2"), /revision_snapshot_unavailable/);
  assert.equal(isRevisionSnapshot({}), true); // A creation/deletion or relationship preview.
  assert.equal(isRevisionSnapshot(null), false);
  assert.equal(isRevisionSnapshot([]), false);
});

test("code and visual views share the content projection, including empty containers and metadata", () => {
  const before = { id: "one", kind: "release", title: "Saved", version: 1, created_by: "user", created_at: "old", updated_at: "old", localized_title: "cache", attributes: {}, translations: { ja: {} } };
  const after = { id: "one", title: "Saved", kind: "release", version: 2, updated_at: "new", localized_summary: "new cache" };
  assert.deepEqual(revisionChanges(before, after), {});
  assert.equal(serializeRevisionContent(before), serializeRevisionContent(after));
  assert.equal(buildRevisionDiff(before, after, "v1", "v2").hunks.length, 0);
  assert.deepEqual(revisionContent(before), { kind: "release", title: "Saved" });
});

test("object keys are stable at every depth while array order and unknown saved fields remain meaningful", () => {
  const before = { attributes: { saved: { z: ["one", "two"], a: 0 } }, translations: { ja: { summary: "Saved", body: "Body" } }, retired_field: { b: false, a: null } };
  const reordered = { retired_field: { a: null, b: false }, translations: { ja: { body: "Body", summary: "Saved" } }, attributes: { saved: { a: 0, z: ["one", "two"] } } };
  assert.equal(serializeRevisionContent(before), serializeRevisionContent(reordered));
  const after = { ...before, attributes: { saved: { z: ["two", "one"], a: 0 } } };
  const diff = buildRevisionDiff(before, after, "v1", "v2");
  assert.ok(diff.additions > 0 && diff.deletions > 0);
  assert.equal(applyPatch(serializeRevisionContent(before), diff.unified), serializeRevisionContent(after));
  assert.ok(serializeRevisionContent(before).includes("retired_field"));
});

test("inline highlights preserve whitespace and isolate Chinese changes and English words", () => {
  const before = "旧正文\n\nA preserved old word.\n";
  const after = "新正文\n\nA preserved new word.\n";
  const parts = diffText(before, after);
  assert.equal(parts.filter((part) => part.kind !== "add").map((part) => part.text).join(""), before);
  assert.equal(parts.filter((part) => part.kind !== "remove").map((part) => part.text).join(""), after);
  assert.equal(parts.find((part) => part.kind === "remove")?.text, "旧");
  assert.ok(parts.some((part) => part.kind === "context" && part.text.includes("正文\n\nA preserved ")));
  assert.ok(parts.some((part) => part.kind === "remove" && part.text === "old"));
  assert.ok(parts.some((part) => part.kind === "add" && part.text === "new"));
});

test("Japanese and Unicode emoji highlights round-trip without dropping code points or joiners", () => {
  const examples = [
    ["古い説明です。🎵", "新しい説明です。🎶"],
    ["👩🏽‍💻の作品\nCafe\u0301", "👨🏽‍💻の新作品\nCafé"],
    ["本文👍🏽️\r\n保存", "本文👍🏻️\r\n保存！"],
  ];
  for (const [before, after] of examples) {
    const parts = diffText(before, after);
    assert.equal(parts.filter((part) => part.kind !== "add").map((part) => part.text).join(""), before);
    assert.equal(parts.filter((part) => part.kind !== "remove").map((part) => part.text).join(""), after);
  }
});

test("large replacements retain every changed line when fine-grained alignment reaches its budget", () => {
  const before = "same start\n" + Array.from({ length: 12_000 }, (_, i) => `old ${i}\n`).join("") + "same end\n";
  const after = "same start\n" + Array.from({ length: 12_000 }, (_, i) => `new ${i}\n`).join("") + "same end\n";
  const diff = buildTextDiff(before, after, "v1", "v2");
  assert.equal(diff.coarse, true);
  assert.equal(diff.additions, 12_000);
  assert.equal(diff.deletions, 12_000);
  assert.equal(diff.hunks[0].lines[0].kind, "context");
  assert.equal(diff.hunks[0].lines.at(-1)?.content, "same end");
  assert.equal(applyPatch(before, diff.unified), after);
});

test("generated patches round-trip repeated lines, empty files and missing final newlines", () => {
  const cases = ["", "a", "a\n", "a\na", "\n", "a\nb\na\n", "b\na\nb", "a\r\nb\r\n"];
  for (const before of cases) {
    for (const after of cases) {
      for (const context of [0, 1, 3]) {
        const diff = buildTextDiff(before, after, "v1", "v2", context);
        if (before === after) assert.equal(diff.hunks.length, 0);
        else assert.equal(applyPatch(before, diff.unified), after, JSON.stringify({ before, after, context, patch: diff.unified }));
      }
    }
  }
});

test("untrusted labels cannot add forged unified patch headers", () => {
  const diff = buildTextDiff("old\n", "new\n", "v1\n--- forged", "v2\tname\rnext");
  assert.ok(diff.unified.startsWith("--- v1 --- forged\n+++ v2 name next\n@@"));
  assert.equal(applyPatch("old\n", diff.unified), "new\n");
});
