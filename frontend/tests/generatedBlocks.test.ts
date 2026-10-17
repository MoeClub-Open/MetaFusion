import { test } from "node:test";
import assert from "node:assert/strict";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import GeneratedBlocks from "../src/components/assistant/GeneratedBlocks";
import { parseGeneratedBlocks } from "../src/lib/generatedBlocks";

test("generated blocks validate all supported shapes and copy only defined fields", () => {
  const blocks = [
    { type: "markdown", title: "Overview", markdown: "**Original** <script>alert(1)</script>", html: "discard" },
    { type: "table", columns: ["Title", "Year"], rows: [["Release", "2026"]] },
    { type: "timeline", events: [{ date: "2026-10", title: "Release", description: "Original source", entity_id: "01a10136-c0f1-7eb5-a66c-112270350f28" }] },
    { type: "chart", chart_type: "bar", labels: ["A", "B"], series: [{ name: "Count", values: [0, -1.5] }] },
    { type: "chart", chart_type: "line", labels: ["A"], series: [{ name: "Value", values: [Number.MAX_VALUE] }] },
  ];
  const parsed = parseGeneratedBlocks(blocks);
  assert.equal(parsed.length, 5);
  assert.deepEqual(parsed[0], { type: "markdown", title: "Overview", markdown: "**Original** <script>alert(1)</script>" });
  assert.deepEqual(parsed.slice(1), blocks.slice(1));
  (blocks[1].rows as string[][])[0][0] = "Changed";
  assert.equal((parsed[1] as Extract<typeof parsed[number], { type: "table" }>).rows[0][0], "Release");
});

test("generated blocks skip malformed values, unsafe entity links and nonfinite chart values", () => {
  for (const value of [null, {}, "[]", 1]) assert.deepEqual(parseGeneratedBlocks(value), []);
  const invalid = [
    null, [], { type: "html", html: "<script>" },
    { type: "markdown", markdown: 1 }, { type: "markdown", markdown: "OK", title: {} },
    { type: "table", columns: [], rows: [] },
    { type: "table", columns: ["One"], rows: [["One", "Extra"]] },
    { type: "table", columns: ["One"], rows: [[null]] },
    { type: "timeline", events: [{ date: 2026, title: "Wrong date" }] },
    { type: "timeline", events: [{ date: "2026", title: "Wrong link", entity_id: "javascript:alert(1)" }] },
    { type: "timeline", events: [{ date: "2026", title: "Wrong link", entity_id: "../../../admin" }] },
    { type: "chart", chart_type: "pie", labels: ["A"], series: [{ name: "V", values: [1] }] },
    { type: "chart", chart_type: "bar", labels: ["A"], series: [{ name: "V", values: [NaN] }] },
    { type: "chart", chart_type: "bar", labels: ["A"], series: [{ name: "V", values: [Infinity] }] },
    { type: "chart", chart_type: "bar", labels: ["A"], series: [{ name: "V", values: ["1"] }] },
    { type: "chart", chart_type: "line", labels: ["A", "B"], series: [{ name: "V", values: [1] }] },
  ];
  assert.deepEqual(parseGeneratedBlocks([...invalid, { type: "markdown", markdown: "Valid" }]), [{ type: "markdown", markdown: "Valid" }]);
});

test("generated blocks bound their render size without truncating structured facts", () => {
  assert.equal(parseGeneratedBlocks(Array.from({ length: 100 }, () => ({ type: "markdown", markdown: "OK" }))).length, 32);
  assert.deepEqual(parseGeneratedBlocks([
    { type: "markdown", markdown: "x".repeat(40_001) },
    { type: "markdown", title: "x".repeat(201), markdown: "OK" },
    { type: "table", columns: Array(17).fill("C"), rows: [] },
    { type: "table", columns: ["C"], rows: Array.from({ length: 201 }, () => ["R"]) },
    { type: "table", columns: ["C"], rows: [["x".repeat(1_001)]] },
    { type: "timeline", events: Array.from({ length: 101 }, () => ({ date: "2026", title: "Event" })) },
    { type: "chart", chart_type: "line", labels: Array(101).fill("L"), series: [{ name: "V", values: Array(101).fill(1) }] },
    { type: "chart", chart_type: "line", labels: ["L"], series: Array.from({ length: 9 }, () => ({ name: "V", values: [1] })) },
  ]), []);
  assert.equal(parseGeneratedBlocks(Array.from({ length: 32 }, () => ({ type: "markdown", markdown: "x".repeat(40_000) }))).length, 5);
});

test("generated rendering escapes HTML, uses UUID-only entity links and supplies accessible chart data", () => {
  const html = renderToStaticMarkup(createElement(GeneratedBlocks, { blocks: [
    { type: "markdown", markdown: "<script>alert(1)</script>\n\n**Safe** [unsafe](javascript:alert(1))" },
    { type: "table", title: "<img onerror=alert(1)>", columns: ["Column"], rows: [["<script>untrusted</script>"]] },
    { type: "timeline", events: [{ date: "2026", title: "Record", entity_id: "01a10136-c0f1-7eb5-a66c-112270350f28" }] },
    { type: "timeline", events: [{ date: "2026", title: "Unsafe", entity_id: "javascript:alert(1)" }] },
    { type: "chart", title: "Measurements", chart_type: "line", labels: ["A", "B"], series: [{ name: "Amount", values: [-Number.MAX_VALUE, Number.MAX_VALUE] }] },
  ] }));
  assert.equal(html.includes("<script"), false);
  assert.equal(html.includes("<img"), false);
  assert.equal(html.includes("javascript:"), false);
  assert.equal(html.includes('href="/catalog/01a10136-c0f1-7eb5-a66c-112270350f28"'), true);
  assert.equal(html.includes('scope="col"'), true);
  assert.equal(html.includes('scope="row"'), true);
  assert.equal(html.includes('role="img"'), true);
  assert.equal(html.includes("<caption"), true);
  assert.equal(html.includes("<title"), true);
  assert.equal(/(?:NaN|Infinity)/.test(html), false);
});
