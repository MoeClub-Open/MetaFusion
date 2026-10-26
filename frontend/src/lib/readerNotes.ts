/**
 * 读者面 vs 运维备注：未登录阅读面（关系行、修订历史）不得露出迁移/批处理
 * edit_note 与词表说明。不靠 CSS 隐藏——直接过滤出读者可见文案。
 *
 * Claude 定案：运维备注与读者可见修订摘要分开；「官方」只能 commit 明确设上。
 */

const OPS_NOTE_RE =
  /迁移|批处理|词表说明|itunes|musicbrainz\s*import|external_ids|origin\s*=\s*migration|migration\b|batch\s*(import|migrate|process)|imported?\s+from/i;

/** 判定一段备注是否像运维/迁移/批处理说明，不该进读者面。 */
export function isOpsNote(text: string | undefined | null): boolean {
  const value = (text || "").trim();
  if (!value) return false;
  return OPS_NOTE_RE.test(value);
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

/**
 * 读者面来源列表：丢掉运维词表说明；保留带真实 URL 的证据链接
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

/** 读者面来源标题：运维 citation 不直出，改用短 host 或空。 */
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
