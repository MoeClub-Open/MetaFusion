import { test } from "node:test";
import assert from "node:assert/strict";
import { clampSearchQuery, SEARCH_QUERY_MAX_BYTES } from "../src/lib/searchQuery";

const bytes = (s: string) => new TextEncoder().encode(s).length;

test("短词原样返回", () => {
  assert.equal(clampSearchQuery("kuninaru"), "kuninaru");
});

test("ASCII 截到上限", () => {
  assert.equal(clampSearchQuery("a".repeat(300)), "a".repeat(SEARCH_QUERY_MAX_BYTES));
});

test("多字节字符不被切断", () => {
  const out = clampSearchQuery("鹿".repeat(200));
  assert.ok(bytes(out) <= SEARCH_QUERY_MAX_BYTES);
  assert.equal(out, "鹿".repeat(85));
});
