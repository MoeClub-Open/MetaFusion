/**
 * 探索页「每页显示条数」。
 *
 * 三个约束同时成立才允许进入这个白名单：
 *   1. 后端 `/api/catalog/entities` 的 limit 上限（query_params.go 的 maxListLimit = 100），
 *      超限直接 400 invalid_limit，前端不能在 URL 里放任意数字；
 *   2. 结果网格的列数（2 / 3 / 4 / 5）——每页条数不是列数的整数倍时，最后一排必然留空位；
 *   3. 一页要装得下整排，又不能大到把首屏图片压垮。
 *
 * 默认 60 = 2、3、4、5 的最小公倍数：四种列宽下都能排满整行。
 * 旧默认 24 在 5 列布局里每页都少 1 个（24 = 4×5 + 4），用户看到的就是"最后留空"。
 */
export const EXPLORE_PAGE_SIZES = [24, 48, 60, 96] as const;

/** 默认每页条数：与 EXPLORE_PAGE_SIZES 的成员保持同步，深链不带 page_size 时用它。 */
export const DEFAULT_EXPLORE_PAGE_SIZE = 60;

/**
 * URL 的 `page_size` → 实际 limit。白名单之外的取值（手改 URL、旧链接、垃圾参数）
 * 一律回落到默认值，不把非法数字转给服务端换一个 400 错误页。
 */
export function parsePageSize(raw: string | null | undefined): number {
  const n = Number(raw);
  return (EXPLORE_PAGE_SIZES as readonly number[]).includes(n) ? n : DEFAULT_EXPLORE_PAGE_SIZE;
}
