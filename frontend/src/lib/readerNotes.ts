/**
 * 读者面 vs 运维备注：未登录阅读面（关系行、修订历史）不得露出迁移/批处理
 * edit_note、词表说明、字段路径与机器码。不靠 CSS 隐藏——直接过滤。
 *
 * Claude 定案：机器码规则与 #73 运维散文关键词**并存**（OR），不互相替代：
 * 1) 运维散文：迁移 / 批处理 / 词表说明 / migration / batch import 等
 * 2) 字段路径 / 机器码：attributes.* / AUX-* / snake_case（含 *_artist / *_id）
 * 只有可读散文摘要放行；有编辑权的编辑者仍看原文。
 */

/** #73 运维散文关键词（与机器码规则 OR）。 */
const OPS_PROSE_RE =
  /迁移|批处理|词表说明|itunes|musicbrainz\s*import|external_ids|origin\s*=\s*migration|migration\b|batch\s*(import|migrate|process)|imported?\s+from/i;

/** 单段是否像字段路径 / 机器码（非散文）。 */
export function isMachineToken(token: string | undefined | null): boolean {
  const t = (token || "").trim();
  if (!t) return false;
  // attributes.publisher / attributes.foo.bar
  if (/^attributes(?:\.[A-Za-z0-9_]+)+$/.test(t)) return true;
  // AUX-P2 / AUX-FOO_1
  if (/^AUX-[A-Za-z0-9_-]+$/i.test(t)) return true;
  // snake_case：至少一个下划线（含 *_artist / *_id）
  if (/^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$/.test(t)) return true;
  return false;
}

/**
 * 判定一段备注是否像运维/迁移/批处理说明，不该进读者面。
 * 运维散文关键词 OR 整段/夹带机器码 → 隐藏。
 */
export function isOpsNote(text: string | undefined | null): boolean {
  const value = (text || "").trim();
  if (!value) return false;

  // #73 运维散文关键词
  if (OPS_PROSE_RE.test(value)) return true;

  if (isMachineToken(value)) return true;

  // 散文里夹带字段路径 / AUX 码 → 仍当运维备注
  if (/\battributes(?:\.[A-Za-z0-9_]+)+\b/.test(value)) return true;
  if (/\bAUX-[A-Za-z0-9_-]+\b/i.test(value)) return true;

  // 多 token 时若全是机器码也藏
  const tokens = value.split(/[\s,;|/]+/).map((s) => s.trim()).filter(Boolean);
  if (tokens.length > 0 && tokens.every(isMachineToken)) return true;

  return false;
}

export type RevisionNoteInput = {
  edit_note?: string;
  summary?: string;
};

/**
 * 读者可见修订摘要。
 * - allowOps：登录且可编辑时，可看原始 edit_note
 * - 否则优先 summary；edit_note 若是运维备注则丢弃，回落 generic
 */
export function readerRevisionNote(
  rev: RevisionNoteInput,
  fallback: string,
  options?: { allowOps?: boolean },
): string {
  const summary = (rev.summary || "").trim();
  const editNote = (rev.edit_note || "").trim();
  if (options?.allowOps) {
    return editNote || summary || fallback;
  }
  if (summary && !isOpsNote(summary)) return summary;
  if (editNote && !isOpsNote(editNote)) return editNote;
  return fallback;
}

export type ReaderSource = {
  kind?: string;
  citation?: string;
  url?: string;
  key?: string;
  pointers?: string[];
  scope?: string;
};

/** 读者面 pointers：丢掉字段路径 / 机器码 / 运维散文，不直出 attributes.* 等。 */
export function readerVisiblePointers(
  pointers: string[] | undefined,
  options?: { allowOps?: boolean },
): string[] {
  if (!pointers?.length) return [];
  if (options?.allowOps) return pointers.slice();
  return pointers.filter((p) => {
    const value = (p || "").trim();
    if (!value) return false;
    return !isOpsNote(value) && !isMachineToken(value);
  });
}

/**
 * 读者面来源列表：丢掉运维词表说明 / 机器码 citation；保留带真实 URL 的证据链接
 * （citation 仍可显示，但若整段是运维备注则用 host 短标签）。
 */
export function readerVisibleSources<T extends ReaderSource>(
  sources: T[] | undefined,
  options?: { allowOps?: boolean },
): T[] {
  if (!sources?.length) return [];
  if (options?.allowOps) return sources.slice();
  return sources.filter((source) => {
    const citation = (source.citation || "").trim();
    const url = (source.url || "").trim();
    if (!citation && !url) return false;
    if (isOpsNote(citation) && !url) return false;
    if (isOpsNote(citation) && url) return true; // 有 URL 时仍展示链接，citation 可在渲染时替换
    return true;
  });
}

/** 读者面来源标题：运维 / 机器码 citation 不直出，改用短 host 或空。 */
export function readerSourceLabel(source: ReaderSource): string {
  const citation = (source.citation || "").trim();
  if (citation && !isOpsNote(citation)) return citation;
  const url = (source.url || "").trim();
  if (!url) return "";
  try {
    return new URL(url).hostname;
  } catch {
    return url;
  }
}
