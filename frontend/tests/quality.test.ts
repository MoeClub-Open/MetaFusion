import { test } from "node:test";
import assert from "node:assert/strict";
import { qualityIssueTab } from "../src/lib/quality";

test("qualityIssueTab maps diagnostic codes to detail tabs", () => {
  assert.equal(qualityIssueTab("dangling_reference"), "relations");
  assert.equal(qualityIssueTab("missing_required_field"), "overview");
  assert.equal(qualityIssueTab("release_missing_media"), "contents");
  assert.equal(qualityIssueTab("medium_missing_tracks"), "contents");
  assert.equal(qualityIssueTab("track_empty_contents"), "contents");
  assert.equal(qualityIssueTab("track_position_gap"), "contents");
  assert.equal(qualityIssueTab("unknown_code"), "overview");
});
