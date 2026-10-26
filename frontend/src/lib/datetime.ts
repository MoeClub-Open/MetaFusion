// 时间戳展示口径（审计 2026-09-19 第 16 条）。
//
// 站点其它位置的日期是 ISO 写法的字符串（2018-09-12 这类），而用户页的修订时间走
// toLocaleString(locale)，zh-CN 下渲染成 2026/9/17 01:32:13——同一个站点出现两套
// 日期族。这里统一成 ISO 8601：正文展示 ISO，本地化写法只放进 title 供悬停查看，
// 既统一了正文口径，也不丢"本地时间更好读"的那点便利。
//
// 无效值一律返回空串：不渲染 Invalid Date，也不让一个假日期混进时间轴。

function toDate(value?: string | number | Date | null): Date | null {
  if (value === undefined || value === null || value === "") return null;
  const d = value instanceof Date ? value : new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** ISO 8601 日期（YYYY-MM-DD，UTC）。 */
export function isoDate(value?: string | number | Date | null): string {
  const d = toDate(value);
  return d ? d.toISOString().slice(0, 10) : "";
}

/** ISO 8601 时间戳（YYYY-MM-DDTHH:MM:SSZ，UTC，秒精度）。 */
export function isoTimestamp(value?: string | number | Date | null): string {
  const d = toDate(value);
  return d ? d.toISOString().slice(0, 19) + "Z" : "";
}

/** 本地化写法；无效值返回空串。zh-* 固定 Asia/Shanghai，避免中文界面仍出 en-US 的 AM/PM。 */
export function localDateTime(value: string | number | Date | null | undefined, locale: string): string {
  const d = toDate(value);
  if (!d) return "";
  const opts: Intl.DateTimeFormatOptions = {};
  if (locale.toLowerCase().startsWith("zh")) opts.timeZone = "Asia/Shanghai";
  return d.toLocaleString(locale, opts);
}
