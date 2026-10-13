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

type LimitedTarget = { value: string };
type LimitedEvent = { currentTarget: LimitedTarget; nativeEvent?: object };

/**
 * 给受控搜索框挂上字节上限：maxLength 先按字符数卡住（256 个 ASCII 正好 256 字节），
 * onChange/onCompositionEnd 再按 UTF-8 字节截断，并把截断结果直接写回 DOM，
 * 这样中文/emoji 也无法继续输入。输入法组字过程中不截断，组字结束时再截。
 */
export function searchInputLimitProps(setValue: (v: string) => void, maxBytes = SEARCH_QUERY_MAX_BYTES) {
  const apply = (target: LimitedTarget) => {
    const clamped = clampSearchQuery(target.value, maxBytes);
    if (clamped !== target.value) target.value = clamped;
    setValue(clamped);
  };
  return {
    maxLength: maxBytes,
    onChange: (e: LimitedEvent) => {
      if ((e.nativeEvent as { isComposing?: boolean } | undefined)?.isComposing) {
        setValue(e.currentTarget.value);
        return;
      }
      apply(e.currentTarget);
    },
    onCompositionEnd: (e: LimitedEvent) => apply(e.currentTarget),
  };
}
