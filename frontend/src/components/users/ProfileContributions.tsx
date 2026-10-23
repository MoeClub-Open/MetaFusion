"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { AlertCircle, ChevronLeft, ChevronRight, GitCommit, GitPullRequest, Layers, Plus, RefreshCw } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { catalogEntityHref, fetchUserContributions, type ContributionActor, type ContributionDay, type UserContributions } from "@/lib/api";
import { buildContributionWeeks } from "@/lib/contribution-calendar";
import { getKindName, useDefinitions } from "@/lib/definitions";
import { isoTimestamp, localDateTime } from "@/lib/datetime";

const DAY_COLORS = ["bg-surfaceSubtle border-line", "bg-primary/20 border-primary/20", "bg-primary/40 border-primary/30", "bg-primary/65 border-primary/50", "bg-primary border-primary"];

function contributionLevel(count: number): number {
  if (count === 0) return 0;
  if (count < 2) return 1;
  if (count < 4) return 2;
  return count < 8 ? 3 : 4;
}

function actorLabel(actor: ContributionActor): string {
  return actor.name && actor.name !== actor.id ? actor.name : actor.id.slice(0, 8);
}

function ContributionCalendar({ year, days, selected, onSelect }: { year: number; days: ContributionDay[]; selected: string; onSelect: (date:string)=>void }) {
  const { t, locale } = useI18n();
  const weeks = buildContributionWeeks(year, days);
  const scrollRef=useRef<HTMLDivElement>(null);
  useEffect(()=>{
    const box=scrollRef.current;
    const day=selected||(year===new Date().getUTCFullYear()?new Date().toISOString().slice(0,10):`${year}-12-31`);
    const cell=box?.querySelector<HTMLElement>(`[data-day="${day}"]`);
    if(box&&cell&&box.scrollWidth>box.clientWidth)box.scrollLeft+=cell.getBoundingClientRect().left-box.getBoundingClientRect().left-box.clientWidth*0.7;
  },[year,selected]);
  const monthFormat = new Intl.DateTimeFormat(locale, { month: "short", timeZone: "UTC" });
  const weekdayFormat = new Intl.DateTimeFormat(locale, { weekday: "short", timeZone: "UTC" });
  let previousMonth = -1;
  const monthLabels = weeks.map((week) => {
    const day = week.find((value) => value !== null);
    if (!day) return "";
    const date = new Date(`${day.date}T00:00:00Z`);
    const month = date.getUTCMonth();
    if (month === previousMonth) return "";
    previousMonth = month;
    return monthFormat.format(date);
  });
  return (
    <div>
      <div ref={scrollRef} className="relative overflow-x-auto pb-1">
        <table className="w-full min-w-[680px] table-fixed border-separate" style={{ borderSpacing: "3px" }}>
          <caption className="sr-only">{t("users.profile.contributions.calendarLabel", { year })}</caption>
          <thead>
            <tr className="text-[10px] text-text-muted">
              <th className="w-9" aria-hidden="true" />
              {monthLabels.map((month, index) => <th key={index} className="h-5 overflow-visible text-left font-normal whitespace-nowrap">{month}</th>)}
            </tr>
          </thead>
          <tbody>
            {Array.from({ length: 7 }, (_, weekday) => (
              <tr key={weekday}>
                <th scope="row" className="text-[10px] text-text-muted font-normal text-left pr-1">
                  {weekday % 2 === 1 ? weekdayFormat.format(new Date(Date.UTC(2023, 0, 1 + weekday))) : <span className="sr-only">{weekdayFormat.format(new Date(Date.UTC(2023, 0, 1 + weekday)))}</span>}
                </th>
                {weeks.map((week, index) => {
                  const day = week[weekday];
                  if (!day) return <td key={index} />;
                  const label = t("users.profile.contributions.day", { date: day.date, count: day.count });
                  return (
                    <td key={index} className="p-0">
                      <button type="button" data-day={day.date} title={label} aria-label={label} aria-pressed={selected===day.date} onClick={()=>onSelect(day.date)} className={`block aspect-square w-full rounded-chip border focus-visible:outline-primary ${selected===day.date?"ring-2 ring-primary ring-offset-1 ring-offset-surface":""} ${DAY_COLORS[contributionLevel(day.count)]}`}>
                        <span className="sr-only">{label}</span>
                      </button>
                    </td>
                  );
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mt-2 flex items-center justify-end gap-1.5 text-[10px] text-text-muted">
        <span>{t("users.profile.contributions.less")}</span>
        {DAY_COLORS.map((color) => <span key={color} aria-hidden="true" className={`h-2.5 w-2.5 rounded-chip border ${color}`} />)}
        <span>{t("users.profile.contributions.more")}</span>
      </div>
    </div>
  );
}

export function ProfileContributions({ userId }: { userId: string }) {
  const { t, tr, locale } = useI18n();
  const { kinds } = useDefinitions();
  const currentYear = new Date().getUTCFullYear();
  const [year, setYear] = useState(currentYear);
  const [page, setPage] = useState(1);
  const [range,setRange]=useState({from:"",to:""});
  const [draft,setDraft]=useState({from:"",to:""});
  const [reloadKey, setReloadKey] = useState(0);
  const [loaded, setLoaded] = useState<{ scope: string; result: UserContributions } | null>(null);
  const [failedScope, setFailedScope] = useState("");
  const scope = `${userId}:${year}:${page}:${reloadKey}:${range.from}:${range.to}`;
  const result = loaded?.scope.startsWith(`${userId}:${year}:`) ? loaded.result : null;
  const refreshing = !!result && loaded?.scope !== scope;
  const failed = failedScope === scope;

  useEffect(() => {
    let alive = true;
    setFailedScope("");
    fetchUserContributions(userId, { year, page, pageSize: 20, ...range })
      .then((value) => { if (alive) setLoaded({ scope, result: value }); })
      .catch(() => { if (alive) setFailedScope(scope); });
    return () => { alive = false; };
  }, [userId, year, page, reloadKey, scope, range]);

  const changeYear = (value: number) => { setYear(value); setPage(1);setRange({from:"",to:""});setDraft({from:"",to:""}); };
  const numberFormat = new Intl.NumberFormat(locale);
  const summary = result?.summary;
  const stats = [
    { key: "mergedCommits", value: summary?.merged_commits, icon: GitCommit },
    { key: "changedEntities", value: summary?.changed_entities, icon: Layers },
    { key: "createdEntities", value: summary?.created_entities, icon: Plus },
    { key: "mergedPullRequests", value: summary?.merged_pull_requests, icon: GitPullRequest },
    ...(summary?.legacy_revisions ? [{ key: "legacyRevisions", value: summary.legacy_revisions, icon: GitCommit }] : []),
  ];

  return (
    <section className="space-y-4" aria-busy={refreshing} aria-labelledby="profile-contributions-title">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="profile-contributions-title" className="text-base font-semibold text-text-strong">{t("users.profile.contributions.title", { year })}</h2>
        <div className="flex items-center gap-1">
          <button type="button" onClick={() => changeYear(year - 1)} disabled={year <= 1970} aria-label={t("users.profile.contributions.previousYear")} className="p-1.5 rounded-control border border-line hover:bg-surfaceSubtle disabled:opacity-40">
            <ChevronLeft className="h-3.5 w-3.5" />
          </button>
          <label className="sr-only" htmlFor="profile-contribution-year">{t("users.profile.contributions.year")}</label>
          <select id="profile-contribution-year" value={year} onChange={(event) => changeYear(Number(event.target.value))} className="h-8 rounded-control border border-line bg-surface px-2 text-xs text-text-body">
            {Array.from({ length: currentYear - 1969 }, (_, index) => currentYear - index).map((value) => <option key={value} value={value}>{value}</option>)}
          </select>
          <button type="button" onClick={() => changeYear(year + 1)} disabled={year >= currentYear} aria-label={t("users.profile.contributions.nextYear")} className="p-1.5 rounded-control border border-line hover:bg-surfaceSubtle disabled:opacity-40">
            <ChevronRight className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>
      {failed ? (
        <div className="rounded-control border border-line bg-surface p-8 text-center space-y-3">
          <AlertCircle className="h-5 w-5 mx-auto text-warn" />
          <p className="text-sm text-text-body">{t("users.profile.listFailed")}</p>
          <button type="button" onClick={() => setReloadKey((value) => value + 1)} className="inline-flex items-center gap-1.5 rounded-control border border-line px-3 py-1.5 text-xs hover:text-primary">
            <RefreshCw className="h-3 w-3" />{t("common.retry")}
          </button>
        </div>
      ) : !result ? (
        <div className="rounded-control border border-line bg-surface p-12 text-center text-xs text-text-muted" aria-live="polite">{t("common.loading")}</div>
      ) : (
        <>
          <div className={`grid grid-cols-2 ${stats.length === 5 ? "xl:grid-cols-5" : "xl:grid-cols-4"} gap-2`}>
            {stats.map(({ key, value, icon: Icon }) => (
              <div key={key} className="rounded-control border border-line bg-surface px-3 py-3">
                <div className="flex items-center gap-1.5 text-[11px] text-text-muted"><Icon className="h-3.5 w-3.5" />{t(`users.profile.contributions.${key}`)}</div>
                <div className="mt-2 text-xl font-semibold tabular-nums text-text-strong">{numberFormat.format(value ?? 0)}</div>
              </div>
            ))}
          </div>
          <div className="rounded-control border border-line bg-surface p-3.5 sm:p-4">
            <div className="mb-2 flex items-center justify-between gap-3 text-xs">
              <p className="font-medium text-text-body">{t("users.profile.contributions.total", { count: result.summary.merged_commits })}</p>
              <span className="text-[10px] text-text-muted font-mono">UTC</span>
            </div>
            <ContributionCalendar year={result.year} days={result.calendar} selected={range.from===range.to?range.from:""} onSelect={date=>{setRange({from:date,to:date});setDraft({from:date,to:date});setPage(1);}} />
          </div>
          <form className="flex flex-wrap items-end gap-2 border-t border-line-subtle pt-4" onSubmit={event=>{event.preventDefault();setRange(draft);setPage(1);}}>
            {(["from","to"] as const).map(field=><label key={field} className="space-y-1 text-xs text-text-muted"><span className="block">{t(`accountUI.${field}`)} (UTC)</span><input type="date" aria-label={t(`accountUI.${field}`)} min={field==="to"&&draft.from?draft.from:`${year}-01-01`} max={field==="from"&&draft.to?draft.to:`${year}-12-31`} value={draft[field]} onChange={event=>setDraft({...draft,[field]:event.target.value})} className="h-8 rounded-control border border-line bg-surface px-2 text-xs"/></label>)}
            <button type="submit" className="h-8 rounded-control border border-line px-3 text-xs">{t("accountUI.filter")}</button>
            {(range.from||range.to)&&<button type="button" onClick={()=>{setRange({from:"",to:""});setDraft({from:"",to:""});setPage(1);}} className="h-8 px-2 text-xs text-primary">{t("accountUI.reset")}</button>}
          </form>
          <div>
            <h3 className="mb-3 flex items-center gap-2 text-sm font-semibold text-text-strong">{t("users.profile.contributions.activity")}{refreshing&&<span role="status" className="text-xs font-normal text-text-muted">{t("common.loading")}</span>}</h3>
            {result.items.length === 0 ? (
              <p className="rounded-control border border-line bg-surface p-8 text-center text-xs text-text-muted">{t("users.profile.contributions.empty")}</p>
            ) : (
              <ol className="rounded-control border border-line bg-surface divide-y divide-line">
                {result.items.map((item) => {
                  const href = item.commit_id ? `/commits/${encodeURIComponent(item.commit_id)}` : item.entities[0] ? `${catalogEntityHref(item.entities[0].id)}#revisions` : null;
                  const title = item.message || t("users.profile.contributions.untitledCommit");
                  const Icon = item.pull_request_id ? GitPullRequest : GitCommit;
                  return (
                    <li key={item.id} className="p-3.5 sm:p-4 flex items-start gap-3">
                      <span className="mt-0.5 shrink-0 rounded-full border border-line bg-surfaceSubtle p-1.5 text-primary"><Icon className="h-3.5 w-3.5" /></span>
                      <div className="min-w-0 flex-1 space-y-2">
                        <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                          {href ? <Link href={href} className="text-sm font-medium text-text-strong hover:text-primary break-words">{title}</Link> : <span className="text-sm font-medium text-text-strong break-words">{title}</span>}
                          <time dateTime={isoTimestamp(item.merged_at)} title={`${t("users.profile.contributions.mergedAt")} ${localDateTime(item.merged_at, locale)}`} className="shrink-0 text-[10px] text-text-muted font-mono">{isoTimestamp(item.merged_at).slice(0, 10)}</time>
                        </div>
                        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-text-muted">
                          {item.legacy ? <span>{t("users.profile.contributions.legacyRevision")}</span> : item.commit_id ? <Link href={`/commits/${encodeURIComponent(item.commit_id)}`} className="font-mono hover:text-primary">{t("users.profile.contributions.commit", { id: item.commit_id.slice(0, 8) })}</Link> : null}
                          {item.pull_request_id && <Link href={`/pull-requests/${encodeURIComponent(item.pull_request_id)}`} className="hover:text-primary">{t("users.profile.contributions.pullRequest", { id: item.pull_request_id.slice(0, 8) })}</Link>}
                          <span>{t("users.profile.contributions.changes", { count: item.changes_count })}</span>
                        </div>
                        <div className="flex flex-wrap gap-1.5">
                          {item.entities.map((entity) => (
                            <Link key={entity.id} href={catalogEntityHref(entity.id)} className="inline-flex max-w-full items-center gap-1.5 rounded-control border border-line px-2 py-1 text-[11px] hover:border-primary/40 hover:text-primary">
                              <span className="truncate">{entity.title || entity.id.slice(0, 8)}</span>
                              <span className="shrink-0 text-[9px] text-text-muted">{getKindName(kinds, entity.kind, locale, tr(`catalog.kind.${entity.kind}`, entity.kind))}</span>
                            </Link>
                          ))}
                        </div>
                        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] text-text-muted">
                          <span className="inline-flex flex-wrap items-center gap-x-1.5 gap-y-1">
                            <span>{t("users.profile.contributions.author")}</span>
                            {item.authors.map((actor) => <Link key={actor.id} href={`/users/${encodeURIComponent(actor.id)}`} title={localDateTime(item.authored_at, locale)} className="text-text-body hover:text-primary">{actorLabel(actor)}</Link>)}
                          </span>
                          {item.merged_by && <span>{t("users.profile.contributions.mergedBy")} <Link href={`/users/${encodeURIComponent(item.merged_by.id)}`} className="text-text-body hover:text-primary">{actorLabel(item.merged_by)}</Link></span>}
                        </div>
                      </div>
                    </li>
                  );
                })}
              </ol>
            )}
          </div>
          {result.total > result.page_size && (
            <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-text-muted">
              <span>{t("users.profile.pagination", { total: result.total, page: result.page })}</span>
              <div className="flex gap-1.5">
                <button type="button" disabled={page <= 1} onClick={() => setPage((value) => Math.max(1, value - 1))} className="rounded-control border border-line px-2.5 py-1.5 hover:text-primary disabled:opacity-40">{t("users.profile.prevPage")}</button>
                <button type="button" disabled={page * result.page_size >= result.total} onClick={() => setPage((value) => value + 1)} className="rounded-control border border-line px-2.5 py-1.5 hover:text-primary disabled:opacity-40">{t("users.profile.nextPage")}</button>
              </div>
            </div>
          )}
        </>
      )}
    </section>
  );
}
