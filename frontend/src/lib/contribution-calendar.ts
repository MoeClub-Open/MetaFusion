import type { ContributionDay } from "./api/users";

const DAY_MS = 24 * 60 * 60 * 1000;

/** 按 UTC 日历排成周；缺失数据保留为空格，不补造零贡献。 */
export function buildContributionWeeks(year: number, days: readonly ContributionDay[]): Array<Array<ContributionDay | null>> {
  if (!Number.isInteger(year) || year < 1970 || year > 9999 || days.length === 0) return [];

  const byDate = new Map<string, ContributionDay>();
  for (const day of days) {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(day.date) || !day.date.startsWith(`${year}-`)) continue;
    const date = new Date(`${day.date}T00:00:00Z`);
    if (!Number.isFinite(date.getTime()) || date.toISOString().slice(0, 10) !== day.date) continue;
    byDate.set(day.date, day);
  }
  if (byDate.size === 0) return [];

  const first = Date.UTC(year, 0, 1);
  const last = Date.UTC(year, 11, 31);
  const firstSunday = first - new Date(first).getUTCDay() * DAY_MS;
  const lastSaturday = last + (6 - new Date(last).getUTCDay()) * DAY_MS;
  const weeks: Array<Array<ContributionDay | null>> = [];

  for (let weekStart = firstSunday; weekStart <= lastSaturday; weekStart += 7 * DAY_MS) {
    const week: Array<ContributionDay | null> = [];
    for (let weekday = 0; weekday < 7; weekday += 1) {
      const time = weekStart + weekday * DAY_MS;
      const date = new Date(time).toISOString().slice(0, 10);
      week.push(time >= first && time <= last ? byDate.get(date) ?? null : null);
    }
    weeks.push(week);
  }

  return weeks;
}
