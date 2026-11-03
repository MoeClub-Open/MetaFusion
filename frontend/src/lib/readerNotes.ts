/**
 * 读者面 vs 运维备注：未登录阅读面（关系行、修订历史）不得露出迁移/批处理
 * edit_note、词表说明、字段路径与机器码。不靠 CSS 隐藏——直接过滤。
 *
 * Claude 定案：机器码规则与 #73 运维散文关键词**并存**（OR），不互相替代：
 * 1) 运维散文：迁移 / 批处理 / 词表说明 / migration / batch import 等
 * 2) 字段路径 / 机器码：attributes.* / AUX-* / snake_case（含 *_artist / *_id）
 * 只有可读散文摘要放行；有编辑权的编辑者仍看原文。
 */

/** #73 运维散文关键词（与机器码规则 OR）。P1 加：Agent/batch/不虚构该ID/版权 unknown 等。 */
const OPS_PROSE_RE =
  /迁移|批处理|词表说明|itunes|musicbrainz\s*import|external_ids|origin\s*=\s*migration|migration\b|batch\s*(import|migrate|process|job|run)?|imported?\s+from|\bAgent\b|不虚构(?:该)?ID|版权\s*unknown|Spotify\s*核对|artist_id|wikidata\s*Q?\d*|ISNI\b|字段路径|实体层级|entity\s*hierarchy/i;

/** 权威库 / 平台短码：无下划线但读者面也不该当「种类」直出。 */
const OPAQUE_AUTHORITY_KIND_RE = /^(isni|wikidata|viaf|orcid|discogs|spotify|musicbrainz|mbid|imdb|tmdb|tvdb)$/i;

/** UUID / Wikidata Q-id / 32 位 hex（MB 无连字符）。 */
export function isOpaqueAuthorityId(value: string | undefined | null): boolean {
  const v = (value || "").trim();
  if (!v) return false;
  if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(v)) return true;
  if (/^Q\d+$/i.test(v)) return true;
  if (/^[0-9a-f]{32}$/i.test(v)) return true;
  return false;
}

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
  // key=value 机器赋值（edition_type=standard）
  if (/^[a-z][a-z0-9_]*=[A-Za-z0-9_.-]+$/.test(t)) return true;
  return false;
}

/**
 * 判定一段备注是否像运维/迁移/批处理说明，不该进读者面。
 * 运维散文关键词 OR 任一机器码 token（含散文夹带）→ 隐藏。
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

  // 任一 token 是机器码（含 snake_case）→ 整条藏（Kimi：混写备注不能 every）
  const tokens = value.split(/[\s,;|/]+/).map((s) => s.trim()).filter(Boolean);
  if (tokens.some(isMachineToken)) return true;

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

/**
 * 简介 / 描述里的机器码清理（edition_type=standard、（physical）等）。
 * 整段若已是运维备注则返回空，让上层回落；否则只剥机器片段，保留可读散文。
 */
export function readerVisibleProse(
  text: string | undefined | null,
  options?: { allowOps?: boolean },
): string {
  const raw = (text || "").trim();
  if (!raw) return "";
  if (options?.allowOps) return raw;
  // 纯运维备注（无读者散文）整段丢掉
  if (isOpsNote(raw) && !/[。．.！？!?]/.test(raw) && raw.length < 120) return "";

  let out = raw;
  // key=value 赋值（含前后顿号/逗号）
  out = out.replace(/(?:^|[、,，；;]\s*|\s+)[a-z][a-z0-9_]*=[A-Za-z0-9_.-]+/gi, "");
  // 括号内单独的 snake / 英文机器词：（physical）、(standard)
  out = out.replace(/[（(]\s*[a-z][a-z0-9_]*(?:_[a-z0-9]+)*\s*[）)]/gi, "");
  // 残留「故 、」类连接词空档
  out = out.replace(/故\s*[、,，]\s*/g, "故");
  out = out.replace(/[、,，]{2,}/g, "、");
  out = out.replace(/\s{2,}/g, " ").trim();
  // 收尾脏标点
  out = out.replace(/[、,，；;]\s*([。．.！？!?])/g, "$1");
  out = out.replace(/[、,，；;]\s*$/g, "");
  return out.trim();
}

/**
 * 署名职位：自由文本优先；若整段是英文机器码（performer / vocal 等）则回落关系本地化名。
 * 「摄影监督」「Vo.」等人类可读原文保留。
 */
export function readerCreditRole(
  creditRole: string | undefined | null,
  relationLabel: string | undefined | null,
  fallback: string,
  options?: { allowOps?: boolean },
): string {
  const role = (creditRole || "").trim();
  const label = (relationLabel || "").trim();
  if (options?.allowOps) return role || label || fallback;
  if (!role) return label || fallback;
  if (isMachineToken(role) || isOpaqueAuthorityId(role)) return label || fallback;
  // 纯拉丁小写职位码（performer / vocalist），无空格无标点 → 当机器码
  if (/^[a-z][a-z0-9]{1,31}$/.test(role)) return label || fallback;
  return role;
}

/**
 * 修订差异面板里的运维键名。
 * 只藏明确的运维元数据键；不要用整段 snake_case 误伤 parent_id /
 * external_links / version_label 等有可读标签的 schema 字段。
 * 机器码取值由 isOpsDiffValue 处理。
 */
export function isOpsDiffKey(key: string | undefined | null): boolean {
  const k = (key || "").trim();
  if (!k) return false;
  // 迁移痕迹 / 外部 ID 元数据（bug 复现里直出的那些）
  if (
    k === "origin" ||
    k === "migration" ||
    k === "external_id" ||
    k === "entity_hierarchy" ||
    k === "hierarchy" ||
    k === "hierarchy_path" ||
    k === "isni" ||
    k === "wikidata"
  ) {
    return true;
  }
  // 字段路径 / AUX 码作键名
  if (/^attributes(?:\.[A-Za-z0-9_]+)+$/.test(k)) return true;
  if (/^AUX-[A-Za-z0-9_-]+$/i.test(k)) return true;
  return false;
}

/**
 * 修订差异面板里的运维 / 机器码取值（如 netease_music_artist、migration、isni、Q42）。
 * 非字符串不判定；读者面不直出。
 */
export function isOpsDiffValue(value: unknown): boolean {
  if (typeof value !== "string") return false;
  const v = value.trim();
  if (!v) return false;
  // 证据 URL 一律保留（路径里的 /isni/、wikidata.org 不能触发运维散文命中）。
  if (/^https?:\/\//i.test(v)) return false;
  if (v === "origin" || v === "migration") return true;
  if (OPAQUE_AUTHORITY_KIND_RE.test(v)) return true;
  if (isOpaqueAuthorityId(v)) return true;
  return isMachineToken(v) || isOpsNote(v);
}

function sanitizeDiffNode(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sanitizeDiffNode);
  if (value && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
      if (isOpsDiffKey(key)) continue;
      if (isOpsDiffValue(child)) continue;
      out[key] = sanitizeDiffNode(child);
    }
    return out;
  }
  return value;
}

/**
 * 读者面差异树：丢掉运维键（external_id / origin / isni 等）与机器码取值。
 * allowOps 时原样返回（编辑者看原文）。
 */
export function readerVisibleDiffData<T>(value: T, options?: { allowOps?: boolean }): T {
  if (options?.allowOps) return value;
  return sanitizeDiffNode(value) as T;
}
