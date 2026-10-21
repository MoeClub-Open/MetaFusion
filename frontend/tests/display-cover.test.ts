import { test } from "node:test";
import assert from "node:assert/strict";
import { coverUrl, displayCover, withDisplayCovers, type ResolvedCover } from "../src/lib/cover";

test("list cover projections display inherited artwork without adding authoritative pictures", () => {
  const entity = { id: "release", title: "Release", pictures: [] };
  const inherited: ResolvedCover = { url: "https://example.test/work.jpg", origin: "subject_work", from: { id: "work", title: "Work" } };
  const [item] = withDisplayCovers([entity], { release: inherited });
  assert.deepEqual(displayCover(item), inherited);
  assert.equal(coverUrl(item), "");
  assert.deepEqual(entity, { id: "release", title: "Release", pictures: [] });
  const own = { ...item, pictures: [{ url: "https://example.test/own.jpg" }] };
  assert.equal(displayCover(own).origin, "self");
  assert.equal(displayCover(own).url, "https://example.test/own.jpg");
  assert.equal(displayCover(withDisplayCovers([item], {})[0]).origin, "none");
});
