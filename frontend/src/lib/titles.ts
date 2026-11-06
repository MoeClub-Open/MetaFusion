import {
  canonicalLanguageCode,
  findLanguage,
  languageAliases,
  languageRank,
  sameLanguage,
} from "./languages";

/** Show original_title only when it differs from the primary display title. */
export function isDistinctOriginalTitle(
  originalTitle?: string | null,
  displayTitle?: string | null,
): boolean {
  const original = (originalTitle ?? "").trim();
  if (!original) return false;
  const display = (displayTitle ?? "").trim();
  if (!display) return true;
  return original.toLocaleLowerCase() !== display.toLocaleLowerCase();
}

export interface LocaleTitleGroup {
  locale: string;
  /** 该语种主标题（翻译行 title/name） */
  primary: string;
  /** 主标题是否标为非官方译名 */
  nonofficial?: boolean;
  /** 该语种并列标题（翻译行 aliases） */
  aliases: string[];
  /** 该语种非官方别名（翻译行 nonofficial_aliases） */
  nonofficialAliases: string[];
  /** 是否为原始语言（由 original_language 推导） */
  isOriginal: boolean;
}

/**
 * 取某语种代码的等价写法（不含自身）：ja ↔ ja-JP、zh ↔ zh-CN、zh-Hant ↔ zh-TW、jpn ↔ ja…
 * 等价关系只有语言单一来源一处定义（见 @/lib/languages），本文件不再维护自己的别名表。
 */
export function localeAliases(loc: string): string[] {
  return languageAliases(loc);
}

/** 语种在默认回退链里的秩；不在链内的语种排在最后（ja 与 ja-JP 同秩）。 */
function localeRank(locale: string): number {
  return languageRank(locale);
}

/**
 * 实体内容语言（ISO 639-1，也接受 jpn / zh-Hant / ko-KR 这类写法）归一到编目语种。
 *
 * 旧实现自带一张只覆盖 zh/en/ja/ko/fr/de 的小表，其它语种一律返回空串——原语言是 cy、eo、
 * pt-BR 的实体就失去原语言标记、也不进标题回退链。现在交给语言单一来源解析，
 * 解析不到才返回空串（调用方按"没有原语言"处理）。
 */
function normalizeOriginalLocale(originalLanguage?: string | null): string {
  const v = (originalLanguage ?? "").trim();
  if (!v) return "";
  const found = findLanguage(v);
  return found ? found.code : "";
}

/**
 * 将实体多语言标题按语种归并：每语种一组（主标题 + 同语种并列标题），
 * 原始语言仅作组内标记。调用方用 original_language 判定哪一组是原始语言，
 * 用 displayTitle 判定主标题行已展示过的标题不再重复。
 * 输入为统一 DTO 的 translations（按 locale 分组的对象：{loc:{title,aliases}}）。
 */
export function groupTitlesByLocale(
  translations: Record<string, { title?: string; name?: string; nonofficial?: boolean; aliases?: string[]; nonofficial_aliases?: string[] }> | undefined,
  originalLanguage?: string | null,
): LocaleTitleGroup[] {
  const origLocale = normalizeOriginalLocale(originalLanguage);
  const groups: LocaleTitleGroup[] = [];
  const seen = new Set<string>();
  for (const [locale, row] of Object.entries(translations || {})) {
    const loc = (locale || "").trim() || "zh-CN";
    const primary = (row?.title || row?.name || "").trim();
    const aliases = Array.isArray(row?.aliases)
      ? row!.aliases!.map((a) => String(a ?? "").trim()).filter(Boolean)
      : [];
    const nonofficialAliases = Array.isArray(row?.nonofficial_aliases)
      ? row!.nonofficial_aliases!.map((a) => String(a ?? "").trim()).filter(Boolean)
      : [];
    if (!primary && aliases.length === 0 && nonofficialAliases.length === 0) continue;
    const key = `${loc}\u0000${primary.toLocaleLowerCase()}`;
    if (seen.has(key)) continue;
    seen.add(key);
    groups.push({
      locale: loc,
      primary: primary || aliases[0] || "",
      nonofficial: !!row?.nonofficial && !!primary,
      aliases: primary ? aliases : aliases.slice(1),
      nonofficialAliases,
      isOriginal: !!origLocale && sameLanguage(loc, origLocale),
    });
  }
  groups.sort((a, b) => {
    if (a.isOriginal !== b.isOriginal) return a.isOriginal ? -1 : 1;
    return localeRank(a.locale) - localeRank(b.locale);
  });
  return groups;
}

export const TITLE_DISPLAY_ORDER_KEY = "metafusion_title_display_order";
export const TITLE_ORDER_CHANGED_EVENT = "mf:title-display-order-changed";

/**
 * localStorage 里的语种码归一：命中语言表就走规范码（ja → ja-JP，与选择器写进去的一致），
 * 表外的合法 BCP-47 写法原样保留——cy、eo 这些不在候选表里的语种必须能存下来并显示；
 * 既非表内也不是合法 BCP-47 的垃圾值丢弃，避免列表里出现一行空白标签。
 */
function normalizeLocaleCode(input: unknown): string {
  return canonicalLanguageCode(typeof input === "string" ? input : String(input ?? ""));
}

/** 用户自定义的标题显示语言优先级（localStorage，未设置返回空数组即默认回退链）。 */
export function getTitleDisplayOrder(): string[] {
  if (typeof window === "undefined" || !window.localStorage) return [];
  try {
    const raw = window.localStorage.getItem(TITLE_DISPLAY_ORDER_KEY);
    if (!raw) return [];
    const arr = JSON.parse(raw);
    if (!Array.isArray(arr)) return [];
    const seen = new Set<string>();
    const out: string[] = [];
    for (const v of arr) {
      const code = normalizeLocaleCode(v);
      if (!code || seen.has(code)) continue;
      seen.add(code);
      out.push(code);
    }
    return out;
  } catch {
    return [];
  }
}

export function setTitleDisplayOrder(order: string[]): void {
  if (typeof window === "undefined" || !window.localStorage) return;
  try {
    const seen = new Set<string>();
    const clean: string[] = [];
    for (const v of order || []) {
      const code = normalizeLocaleCode(v);
      if (!code || seen.has(code)) continue;
      seen.add(code);
      clean.push(code);
    }
    window.localStorage.setItem(TITLE_DISPLAY_ORDER_KEY, JSON.stringify(clean));
    window.dispatchEvent(new CustomEvent(TITLE_ORDER_CHANGED_EVENT));
  } catch {
    /* 存储不可用时保持默认回退链 */
  }
}

export function resetTitleDisplayOrder(): void {
  if (typeof window === "undefined" || !window.localStorage) return;
  try {
    window.localStorage.removeItem(TITLE_DISPLAY_ORDER_KEY);
    window.dispatchEvent(new CustomEvent(TITLE_ORDER_CHANGED_EVENT));
  } catch {
    /* 存储不可用时保持默认回退链 */
  }
}

/**
 * 语种 → 界面文案键：只有字典里真配了本地化名字的语种才在这里（en-US 界面要显示 "Japanese"
 * 而不是 "日本語"）。这里的键是语言单一来源的规范码，不是另一份语种清单——等价写法
 * （ja → ja-JP）先经语言表归一后再查。
 */
const TITLE_LOCALE_LABEL_KEYS: Record<string, string> = {
  "zh-CN": "editor.core.langZhHans",
  "zh-TW": "editor.core.langZhHant",
  "ja-JP": "editor.core.langJa",
  "ko": "editor.core.langKo",
  "en-US": "editor.core.langEn",
};

/** 语种展示标签的 i18n 键；字典没配的语种返回 null，调用方用语言表的自称兜底。 */
export function titleLocaleLabelKey(locale: string): string | null {
  const found = findLanguage(locale);
  if (!found) return null;
  return TITLE_LOCALE_LABEL_KEYS[found.code] ?? null;
}

/** 内容语言（ISO 639-1/639-2 或带变体的写法）映射到编目语种。 */
export function mapOriginalLanguageToLocale(originalLanguage?: string | null): string {
  return normalizeOriginalLocale(originalLanguage);
}

export interface TitlePickOptions {
  /** 用户自定义优先级；缺省时读取 localStorage，未设置则走默认回退链。 */
  order?: string[];
  /** 实体内容语言（ISO 639-1），参与回退链。 */
  originalLanguage?: string | null;
}

/**
 * 标题/简介选取链：
 * 用户优先级（含等价写法）→ 界面语言（含等价写法）→ 原始语言（含等价写法）
 * → en-US → zh-CN → zh-TW → ja-JP → 行内剩余语种（按语种秩）。
 * original_language 经 mapOriginalLanguageToLocale 归一化后参与回退；等价写法
 * （ISO 639-1 的 ja/jpn、zh、en、ko 与编目语种的 ja-JP、zh-CN…）由语言单一来源解析。
 */
export function buildTitleChain(
  uiLocale: string,
  opts?: TitlePickOptions,
  rowLocales?: Array<string | null | undefined>,
): string[] {
  const chain: string[] = [];
  const push = (loc?: string | null) => {
    const v = (loc ?? "").trim();
    if (v && !chain.includes(v)) chain.push(v);
  };
  const pushWithAliases = (loc?: string | null) => {
    const v = (loc ?? "").trim();
    if (!v) return;
    push(v);
    for (const a of localeAliases(v)) push(a);
  };
  const order = Array.isArray(opts?.order) ? opts!.order! : getTitleDisplayOrder();
  for (const loc of order) pushWithAliases(loc);
  pushWithAliases(uiLocale);
  pushWithAliases(mapOriginalLanguageToLocale(opts?.originalLanguage));
  pushWithAliases("en-US");
  pushWithAliases("zh-CN");
  push("zh-TW");
  // ja 与 ja-JP 是同一语种：pushWithAliases 会把两种写法都放进链里，不必各写一行。
  pushWithAliases("ja-JP");
  const rest = (rowLocales || [])
    .map((l) => (l ?? "").trim())
    .filter(Boolean)
    .sort((a, b) => localeRank(a) - localeRank(b));
  for (const loc of rest) pushWithAliases(loc);
  return chain;
}

/** 在翻译行数组中按链定位：精确匹配优先，其次等价写法（ja-JP↔ja 等）。 */
export function findRowForLocale<
  T extends { locale?: string | null },
>(rows: T[], loc: string): T | undefined {
  const exact = rows.find((r) => (r.locale || "").trim() === loc);
  if (exact) return exact;
  const aliases = localeAliases(loc);
  for (const a of aliases) {
    const hit = rows.find((r) => (r.locale || "").trim() === a);
    if (hit) return hit;
  }
  // 大小写/短码兜底：ja-JP 行 vs ja 链等
  const low = loc.trim().toLowerCase();
  const short = low.split("-")[0];
  for (const r of rows) {
    const rl = ((r.locale || "").trim().toLowerCase());
    if (rl === low || rl === short) return r;
  }
  return undefined;
}

/** 翻译行为 Record<string, {title/name/summary/biography}> 形态时的统一标题选取。 */
export function pickRecordTitle(
  uiLocale: string,
  translations: Record<string, { title?: string; name?: string; nonofficial?: boolean; summary?: string; biography?: string } | undefined | null> | undefined,
  fallbackTitle: string,
  opts?: TitlePickOptions,
): string {
  const rec = translations || {};
  const rows = Object.entries(rec)
    .filter(([loc, row]) => loc && row && (((row.title || row.name || "").trim())))
    .map(([loc, row]) => ({ locale: loc, title: (row!.title || row!.name || "").trim(), nonofficial: !!row!.nonofficial }));
  const chain = buildTitleChain(uiLocale, opts, rows.map((r) => r.locale));
  const officialRows = rows.filter((r) => !r.nonofficial);
  for (const loc of chain) {
    const row = findRowForLocale(officialRows, loc);
    if (row && row.title) return row.title;
  }
  const base = (fallbackTitle || "").trim();
  if (base) return base;
  for (const loc of chain) {
    const row = findRowForLocale(rows, loc);
    if (row && row.title) return row.title;
  }
  return "";
}

/** Record 形态翻译行的标题+简介选取（详情/预览链共用）。 */
export function pickRecordEntry(
  uiLocale: string,
  translations: Record<string, { title?: string; name?: string; nonofficial?: boolean; summary?: string; biography?: string } | undefined | null> | undefined,
  fallbackTitle: string,
  fallbackBody?: string,
  opts?: TitlePickOptions,
): { title: string; body: string } {
  const rec = translations || {};
  const rows = Object.entries(rec)
    .filter(([loc, row]) => loc && row)
    .map(([loc, row]) => ({
      locale: loc,
      title: ((row!.title || row!.name || "").trim()),
      body: ((row!.summary || row!.biography || "").trim()),
    }));
  const chain = buildTitleChain(uiLocale, opts, rows.map((r) => r.locale));
  const resolvedTitle = pickRecordTitle(uiLocale, translations, fallbackTitle, opts);
  for (const loc of chain) {
    const row = findRowForLocale(rows, loc);
    if (row && row.body) {
      return { title: resolvedTitle, body: row.body };
    }
  }
  return {
    title: resolvedTitle,
    body: (fallbackBody || "").trim(),
  };
}
