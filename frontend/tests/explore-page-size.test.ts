import { test } from "node:test";
import assert from "node:assert/strict";
import { DEFAULT_EXPLORE_PAGE_SIZE, EXPLORE_PAGE_SIZES, parsePageSize } from "../src/lib/explorePageSize";

// 与 backend/internal/catalog/query_params.go 的 maxListLimit 同口径：
// 超过这个值的 limit 会被后端以 400 invalid_limit 拒掉，前端不能把它放进 URL。
const BACKEND_MAX_LIMIT = 100;

test("explore page sizes fill the result grid rows and stay inside the backend limit", () => {
  for (const size of EXPLORE_PAGE_SIZES) {
    assert.ok(size > 0 && size <= BACKEND_MAX_LIMIT, `limit ${size}`);
    // 网格列数是 2/3/4/5：至少要在 2、3、4 列下排满整行（公倍数 12）。
    assert.equal(size % 12, 0, `${size} 条在 2/3/4 列下会留空位`);
  }
  // 默认值还要覆盖最宽的 5 列布局：只有 60 的公倍数能做到，否则每页都缺最后一格。
  assert.equal(DEFAULT_EXPLORE_PAGE_SIZE % 20, 0);
  assert.equal(DEFAULT_EXPLORE_PAGE_SIZE % 3, 0);
  assert.ok((EXPLORE_PAGE_SIZES as readonly number[]).includes(DEFAULT_EXPLORE_PAGE_SIZE));
});

test("explore page size falls back to the default for anything outside the whitelist", () => {
  for (const size of EXPLORE_PAGE_SIZES) assert.equal(parsePageSize(String(size)), size);
  for (const raw of ["", null, undefined, "abc", "-24", "0", "1000", "NaN", "Infinity"]) {
    assert.equal(parsePageSize(raw), DEFAULT_EXPLORE_PAGE_SIZE, String(raw));
  }
});
