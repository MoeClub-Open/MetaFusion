import { test } from "node:test";
import assert from "node:assert/strict";
import type { ContributionDay } from "../src/lib/api/users";
import { buildContributionWeeks } from "../src/lib/contribution-calendar";

function fullYear(year: number): ContributionDay[] {
  const days: ContributionDay[] = [];
  for (let date = Date.UTC(year, 0, 1); date < Date.UTC(year + 1, 0, 1); date += 24 * 60 * 60 * 1000) {
    days.push({ date: new Date(date).toISOString().slice(0, 10), count: 0 });
  }
  return days;
}

test("contribution weeks retain every day of a leap year including February 29", () => {
  const days = fullYear(2024);
  const weeks = buildContributionWeeks(2024, days);
  const actualDays = weeks.flat().filter((day) => day !== null);
  assert.equal(weeks.length, 53);
  assert.ok(weeks.every((week) => week.length === 7));
  assert.equal(actualDays.length, 366);
  assert.deepEqual(actualDays, days);
  assert.deepEqual(actualDays.find((day) => day.date === "2024-02-29"), { date: "2024-02-29", count: 0 });
});

test("contribution weeks align Sunday through Saturday and pad year boundaries with null", () => {
  const weeks = buildContributionWeeks(2026, fullYear(2026));
  assert.deepEqual(weeks[0]?.map((day) => day?.date ?? null), [
    null, null, null, null, "2026-01-01", "2026-01-02", "2026-01-03",
  ]);
  assert.deepEqual(weeks.at(-1)?.map((day) => day?.date ?? null), [
    "2026-12-27", "2026-12-28", "2026-12-29", "2026-12-30", "2026-12-31", null, null,
  ]);
  for (const week of weeks) {
    for (let weekday = 0; weekday < week.length; weekday += 1) {
      const day = week[weekday];
      if (day) assert.equal(new Date(`${day.date}T00:00:00Z`).getUTCDay(), weekday);
    }
  }
});

test("empty contribution calendars stay empty without inventing zero-filled days", () => {
  assert.deepEqual(buildContributionWeeks(2026, []), []);
});

test("unordered sparse calendars retain true zero counts and leave missing days empty", () => {
  const days = [
    { date: "2026-01-05", count: 3 },
    { date: "2026-01-01", count: 0 },
  ];
  const weeks = buildContributionWeeks(2026, days);
  assert.deepEqual(weeks.flat().filter((day) => day !== null), [days[1], days[0]]);
  assert.equal(weeks[0]?.[5], null);
  assert.deepEqual(days.map((day) => day.date), ["2026-01-05", "2026-01-01"]);
});

test("a contribution year excludes dates from other years and invalid calendar dates", () => {
  const selected = { date: "2026-10-18", count: 2 };
  const days = [
    { date: "2025-12-31", count: 1 },
    selected,
    { date: "2027-01-01", count: 3 },
    { date: "2026-02-29", count: 4 },
  ];
  assert.deepEqual(buildContributionWeeks(2026, days).flat().filter((day) => day !== null), [selected]);
  assert.deepEqual(buildContributionWeeks(2028, days), []);
});
