// 后端 catalog 列表/搜索的文本参数上限（backend/internal/catalog/query_params.go 的
// listTextParamLimit = 256），按 UTF-8 字节计：超出直接 400 query_too_long。
export const SEARCH_QUERY_MAX_BYTES = 256;

/** 把搜索词截到后端允许的字节数以内，不切断多字节字符。 */
export function clampSearchQuery(q: string, maxBytes = SEARCH_QUERY_MAX_BYTES): string {
  const encoder = new TextEncoder();
  if (encoder.encode(q).length <= maxBytes) return q;
  let out = "";
  let bytes = 0;
  for (const ch of q) {
    const n = encoder.encode(ch).length;
    if (bytes + n > maxBytes) break;
    out += ch;
    bytes += n;
  }
  return out;
}
