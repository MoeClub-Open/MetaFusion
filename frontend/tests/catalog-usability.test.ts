import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { matchesEntityQuery } from "../src/lib/entitySearch";
import { entityIdentitySuffix } from "../src/lib/entityIdentity";
import { searchKeyAction } from "../src/lib/searchKeyboard";
import { isCompatibleMergeTarget } from "../src/lib/mergeTarget";
import { loadCatalogSection } from "../src/lib/catalogSection";
import ts from "typescript-api";

test("local release search covers translations, aliases, external IDs and attributes", () => {
  const entity = { title: "First Press", translations: { ja: { title: "初回盤", aliases: ["限定版"], summary: "特典映像" } }, external_links: [{ kind: "musicbrainz", url: "mf://external/musicbrainz/ABC-123", external_id: "ABC-123" }], attributes: { catalog_number: "XYZ-007" } };
  for (const query of ["first", "初回", "限定", "特典", "abc-123", "musicbrainz:abc-123", "xyz-007", " "]) assert.ok(matchesEntityQuery(entity, query), query);
  assert.equal(matchesEntityQuery(entity, "unrelated"), false);
  assert.equal(matchesEntityQuery({}, "anything"), false);
});

test("search ignores IME confirmation and chooses exactly one Enter action", () => {
  for (const open of [true, false]) {
    assert.equal(searchKeyAction({ key: "Enter", isComposing: true }, open, 0, 2), "ignore");
    assert.equal(searchKeyAction({ key: "Enter", keyCode: 229 }, open, 0, 2), "ignore");
  }
  assert.equal(searchKeyAction({ key: "Enter" }, false, -1, 0), "submit");
  assert.equal(searchKeyAction({ key: "Enter" }, true, -1, 2), "submit");
  assert.equal(searchKeyAction({ key: "Enter" }, true, 1, 2), "select");
  assert.equal(searchKeyAction({ key: "Enter" }, true, 4, 2), "submit");
  assert.equal(searchKeyAction({ key: "ArrowDown" }, true, -1, 2), "next");
  assert.equal(searchKeyAction({ key: "ArrowUp" }, true, 0, 2), "previous");
  assert.equal(searchKeyAction({ key: "Escape" }, true, 0, 2), "dismiss");
  const source = readFileSync(new URL("../src/components/common/SearchSuggest.tsx", import.meta.url), "utf8");
  const handler = source.slice(source.indexOf("const handleKeyDown"), source.indexOf("const lg ="));
  assert.ok(handler.indexOf("e.preventDefault()") < handler.indexOf('action === "submit"'));
});

test("failed and malformed detail responses are distinct from empty success", async () => {
  const empty = { items: [] as string[] };
  assert.deepEqual(await loadCatalogSection(Promise.resolve(empty), empty), { data: empty, failed: false });
  assert.deepEqual(await loadCatalogSection(Promise.reject(new Error("503")), empty), { data: empty, failed: true });
  assert.equal((await loadCatalogSection(Promise.resolve({} as typeof empty), empty)).failed, true);
  const results = await Promise.all([
    loadCatalogSection(Promise.reject(new Error("timeout")), empty),
    loadCatalogSection(Promise.resolve({ items: ["healthy"] }), empty),
  ]);
  assert.equal(results[0].failed, true);
  assert.deepEqual(results[1], { data: { items: ["healthy"] }, failed: false });
  assert.equal((await loadCatalogSection(Promise.resolve(empty), empty, () => false)).failed, true);
});

test("merge preview accepts only another published record with matching ownership", () => {
  const source = { id: "source", kind: "track", work_id: "work", medium_id: "medium" };
  const target = { ...source, id: "target", state: "active" };
  assert.ok(isCompatibleMergeTarget(source, target));
  for (const changed of [{ id: "SOURCE" }, { kind: "work" }, { state: "deleted" }, { state: "merged" }, { medium_id: "other" }, { work_id: "other" }, { parent_id: "other" }, { content_unit_id: "other" }, { release_id: "other" }]) assert.equal(isCompatibleMergeTarget(source, { ...target, ...changed }), false);
});

test("picker identity differentiates same-title records without inferred metadata", () => {
  assert.equal(entityIdentitySuffix({ id: "12345678-abcd", external_links: [{ kind: "isbn", url: "mf://external/isbn/978-1", external_id: "978-1" }] }), "isbn: 978-1 · 12345678");
  assert.equal(entityIdentitySuffix({ id: "abcdef01-abcd" }), "abcdef01");
});

test("EntityEditor calls all top-level hooks before conditional return", () => {
  const text = readFileSync(new URL("../src/components/catalog/EntityEditor.tsx", import.meta.url), "utf8");
  const source = ts.createSourceFile("EntityEditor.tsx", text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const editor = source.statements.find((node): node is ts.FunctionDeclaration => ts.isFunctionDeclaration(node) && node.name?.text === "EntityEditor");
  assert.ok(editor?.body);
  let earlyReturn = false;
  for (const statement of editor.body.statements) {
    if (ts.isIfStatement(statement)) earlyReturn = true;
    if (!ts.isVariableStatement(statement)) continue;
    const visit = (node: ts.Node) => {
      if (ts.isCallExpression(node) && /^(?:React\.)?use[A-Z]/.test(node.expression.getText(source))) assert.equal(earlyReturn, false, `hook after early return: ${node.expression.getText(source)}`);
      ts.forEachChild(node, visit);
    };
    visit(statement);
  }
});
