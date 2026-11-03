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

/** 已知可读副行：Bangumi 短码、EAN/UPC、含空白/非 ASCII 的标题。 */
function isReadableExternalSecondary(value: string): boolean {
  const v = value.trim();
  if (!v) return false;
  if (/^(ep|subject|character|person|prsn)\d+$/i.test(v)) return true;
  if (/^\d{8}$|^\d{12}$|^\d{13}$|^\d{14}$/.test(v)) return true;
  if (/[\u0080-\uFFFF]/.test(v) || /\s/.test(v)) return true;
  return false;
}

/**
 * 读者面外链副行是否算「不透明平台 ID」。
 * 在 UUID / Q-id / hex32 之外，再收 Spotify / YouTube / ISNI，
 * 以及 /artist|/channel|… 路径下的纯机码末段（Apple / Deezer 等）。
 * allowOpaque 编辑者仍可直出。
 */
export function isReaderOpaqueExternalId(value: string | undefined | null, url?: string): boolean {
  const v = (value || "").trim();
  if (!v) return false;
  if (isOpaqueAuthorityId(v)) return true;

  // ISNI：15–16 位，末位可为 X，可带空格/连字符
  const isniCompact = v.replace(/[\s-]/g, "");
  if (/^\d{15}[\dX]$/i.test(isniCompact) && /^[\d\s-]+[\dX]?$/i.test(v)) return true;

  // YouTube channel id
  if (/^UC[\w-]{21,24}$/.test(v)) return true;

  // Spotify 风格 base62（22 位）
  if (/^[0-9A-Za-z]{22}$/.test(v)) return true;

  if (isReadableExternalSecondary(v)) return false;

  if (url && /^https?:\/\//i.test(url)) {
    try {
      const parsed = new URL(url);
      const segments = decodeDisplayText(parsed.pathname || "")
        .split("/")
        .map((s) => s.trim())
        .filter(Boolean);
      const last = segments[segments.length - 1] || "";
      const idMatchesLast = last === v;
      // /artist|/channel|/user|/album|/track|/show|/playlist|/isni/…/<id>
      if (
        idMatchesLast &&
        /^[A-Za-z0-9_-]+$/.test(v) &&
        /\/(artist|channel|user|album|track|show|playlist|isni)(?:\/|$)/i.test(parsed.pathname)
      ) {
        return true;
      }
      // Apple Music: /artist/<slug>/<numeric-id>
      if (segments.length >= 2) {
        const prev = segments[segments.length - 2] || "";
        if (
          idMatchesLast &&
          /^(artist|channel|user|album|track)$/i.test(prev) &&
          /^\d{6,14}$/.test(v)
        ) {
          return true;
        }
      }
    } catch {
      /* ignore */
    }
  }

  return false;
}

/** 缺权威库定义时，用主机品牌 / 主机名做人话主标签；禁止 UPPER_SNAKE 枚举闪屏。 */
const HOST_BRAND: Array<{ test: RegExp; label: string }> = [
  { test: /(^|\.)spotify\.com$/i, label: "Spotify" },
  { test: /(^|\.)apple\.com$/i, label: "Apple Music" },
  { test: /(^|\.)youtube\.com$/i, label: "YouTube" },
  { test: /(^|\.)youtu\.be$/i, label: "YouTube" },
  { test: /(^|\.)deezer\.com$/i, label: "Deezer" },
  { test: /(^|\.)isni\.org$/i, label: "ISNI" },
  { test: /(^|\.)musicbrainz\.org$/i, label: "MusicBrainz" },
  { test: /(^|\.)wikidata\.org$/i, label: "Wikidata" },
  { test: /(^|\.)twitter\.com$/i, label: "X" },
  { test: /(^|\.)x\.com$/i, label: "X" },
  { test: /(^|\.)bgm\.tv$/i, label: "Bangumi" },
  { test: /(^|\.)bilibili\.com$/i, label: "Bilibili" },
  { test: /(^|\.)moegirl\.org\.cn$/i, label: "Moegirl" },
  { test: /(^|\.)fandom\.com$/i, label: "Fandom" },
];

/**
 * 主标签回落（defs 未就绪 / 无匹配）：
 * official_website → 本地化「官方网站」；
 * 否则主机品牌或 hostname；
 * 再不行用 genericLabel。
 * 绝不 `kind.toUpperCase()`。
 */
export function externalLinkPrimaryFallback(
  kind: string,
  url: string,
  options?: { officialWebsiteLabel?: string; genericLabel?: string },
): string {
  const key = (kind || "").trim().toLowerCase();
  if (key === "official_website") {
    return options?.officialWebsiteLabel || options?.genericLabel || "Website";
  }

  const candidate = (url || "").trim();
  try {
    if (/^https?:\/\//i.test(candidate)) {
      const host = new URL(candidate).hostname.replace(/^www\./i, "");
      for (const row of HOST_BRAND) {
        if (row.test.test(host)) return row.label;
      }
      if (host) return host;
    }
  } catch {
    /* fall through */
  }

  return options?.genericLabel || "Link";
}

/**
 * 副行文案：有 external_id 优先（解码后）；否则从 url 取短域名，
 * 有可读路径段时附上最后一段（同样解码），避免把整段百分号编码 URL 塞进窄栏。
 *
 * 读者面默认不直出 UUID / 平台机码等不透明权威 ID（allowOpaque 给编辑者）。
 * 可读产品码（EAN、Bangumi ep…）仍可作为副行。
 */
export function externalLinkSecondary(
  externalId: string | undefined,
  url: string,
  options?: { allowOpaque?: boolean },
): string {
  const id = decodeDisplayText(externalId || "");
  if (id && !/^https?:\/\//i.test(id)) {
    if (options?.allowOpaque || !isReaderOpaqueExternalId(id, url)) return id;
    // 不透明 ID：落到 host / 路径短标签，不把机码当副行
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
      // 路径末段若仍是不透明机码，只露主机名
      if (
        last &&
        last.length <= 48 &&
        (options?.allowOpaque || !isReaderOpaqueExternalId(last, candidate))
      ) {
        return last;
      }
      return parsed.hostname;
    }
  } catch {
    /* fall through */
  }

  const stripped = candidate.replace(/^https?:\/\//i, "");
  if (!options?.allowOpaque && isReaderOpaqueExternalId(stripped, url)) return "";
  return stripped;
}
