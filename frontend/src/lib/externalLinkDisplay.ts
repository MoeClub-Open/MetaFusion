/** 外链侧栏展示：平台名优先完整显示；副行才是 ID / 短域名，百分号编码要解码。 */

import { isOpaqueAuthorityId } from "@/lib/readerNotes";

/** 尽量 decodeURIComponent；非法序列保持原样。 */
export function decodeDisplayText(value: string): string {
  const raw = value.trim();
  if (!raw) return "";
  try {
    return decodeURIComponent(raw.replace(/\+/g, " "));
  } catch {
    return raw;
  }
}

/**
 * 副行文案：有 external_id 优先（解码后）；否则从 url 取短域名，
 * 有可读路径段时附上最后一段（同样解码），避免把整段百分号编码 URL 塞进窄栏。
 *
 * 读者面默认不直出 UUID / Wikidata Q-id 等不透明权威 ID（allowOpaque 给编辑者）。
 * 可读产品码（EAN、Bangumi ep…）仍可作为副行。
 */
export function externalLinkSecondary(
  externalId: string | undefined,
  url: string,
  options?: { allowOpaque?: boolean },
): string {
  const id = decodeDisplayText(externalId || "");
  if (id && !/^https?:\/\//i.test(id)) {
    if (options?.allowOpaque || !isOpaqueAuthorityId(id)) return id;
    // 不透明 ID：落到 host / 路径短标签，不把 UUID 当副行
  }

  const candidate = (options?.allowOpaque ? id : "") || decodeDisplayText(url);
  if (!candidate) return "";

  try {
    if (/^https?:\/\//i.test(candidate)) {
      const parsed = new URL(candidate);
      const segments = decodeDisplayText(parsed.pathname || "")
        .split("/")
        .map((s) => s.trim())
        .filter(Boolean);
      const last = segments[segments.length - 1] || "";
      // 路径末段若仍是 UUID/Q-id，只露主机名
      if (last && last.length <= 48 && (options?.allowOpaque || !isOpaqueAuthorityId(last))) {
        return last;
      }
      return parsed.hostname;
    }
  } catch {
    /* fall through */
  }

  const stripped = candidate.replace(/^https?:\/\//i, "");
  if (!options?.allowOpaque && isOpaqueAuthorityId(stripped)) return "";
  return stripped;
}
