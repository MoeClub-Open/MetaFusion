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

test("输入框挂载的限制：超长输入写回 DOM 并只上报截断值", async () => {
  const { searchInputLimitProps } = await import("../src/lib/searchQuery");
  let got = "";
  const props = searchInputLimitProps((v) => { got = v; });
  assert.equal(props.maxLength, SEARCH_QUERY_MAX_BYTES);
  const target = { value: "中".repeat(200) };
  props.onChange({ currentTarget: target });
  assert.ok(bytes(target.value) <= SEARCH_QUERY_MAX_BYTES);
  assert.equal(got, target.value);
  assert.equal(target.value.length, 85);
});

test("输入法组字中不截断，组字结束再截断", async () => {
  const { searchInputLimitProps } = await import("../src/lib/searchQuery");
  let got = "";
  const props = searchInputLimitProps((v) => { got = v; });
  const target = { value: "あ".repeat(100) };
  props.onChange({ currentTarget: target, nativeEvent: { isComposing: true } });
  assert.equal(target.value.length, 100);
  props.onCompositionEnd({ currentTarget: target });
  assert.equal(got.length, 85);
  assert.equal(target.value, got);
});
