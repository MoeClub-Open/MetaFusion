"use client";

import { useId, useMemo, useState } from "react";
import Link from "next/link";
import { ArrowUpRight, Search } from "lucide-react";
import { useI18n } from "@/i18n/I18nProvider";
import { getKindName, useDefinitions } from "@/lib/definitions";
import MarkdownRenderer from "@/components/MarkdownRenderer";
import { Card, CardTitle } from "@/components/ui/Card";
import { parseGeneratedBlocks, type GeneratedBlock } from "@/lib/generatedBlocks";

export type { GeneratedBlock } from "@/lib/generatedBlocks";
export { parseGeneratedBlocks } from "@/lib/generatedBlocks";

type ChartBlock = Extract<GeneratedBlock, { type: "chart" }>;
type EntitiesBlock = Extract<GeneratedBlock, { type: "entities" }>;
const COLORS = ["text-primary", "text-success", "text-warn", "text-violet-500", "text-rose-500", "text-cyan-600", "text-orange-500", "text-text-muted"];

function DataTable({ columns, rows, caption, rowHeadings = false }: { columns: string[]; rows: string[][]; caption?: string; rowHeadings?: boolean }) {
  const { tr } = useI18n();
  const [query, setQuery] = useState("");
  const [sortCol, setSortCol] = useState<number | null>(null);
  const [sortAsc, setSortAsc] = useState(true);
  const visibleRows = useMemo(() => {
    const q = query.trim().toLocaleLowerCase();
    const filtered = q ? rows.filter((r) => r.some((cell) => cell.toLocaleLowerCase().includes(q))) : rows;
    if (sortCol === null) return filtered;
    return [...filtered].sort((a, b) => {
      const av = a[sortCol] ?? "";
      const bv = b[sortCol] ?? "";
      const cmp = av.localeCompare(bv, undefined, { numeric: true, sensitivity: "base" });
      return sortAsc ? cmp : -cmp;
    });
  }, [rows, query, sortCol, sortAsc]);
  return (
    <div className="space-y-2">
      {rows.length > 4 && !rowHeadings && (
        <label className="flex items-center gap-2 rounded-control border border-line bg-background px-2.5 py-1 text-xs">
          <Search className="size-3.5 shrink-0 text-text-muted" />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={tr("assistantUI.filterTable", "Filter items…")}
            className="w-full bg-transparent text-xs text-text-strong outline-none"
          />
        </label>
      )}
      <div className="overflow-x-auto">
      <table className="min-w-full divide-y divide-line text-left text-xs sm:text-sm">
        {caption && <caption className="sr-only">{caption}</caption>}
        <thead><tr>{columns.map((column, index) => <th key={index} scope="col" onClick={!rowHeadings && rows.length > 1 ? () => { if (sortCol === index) setSortAsc((v) => !v); else { setSortCol(index); setSortAsc(true); } } : undefined} className={"whitespace-nowrap px-3 py-2 font-medium text-text-strong" + (!rowHeadings && rows.length > 1 ? " cursor-pointer select-none hover:text-primary" : "")}>{column}{sortCol === index ? (sortAsc ? " ↑" : " ↓") : ""}</th>)}</tr></thead>
        <tbody className="divide-y divide-line-subtle">{visibleRows.map((row, rowIndex) => (
          <tr key={rowIndex}>{row.map((cell, cellIndex) => rowHeadings && cellIndex === 0
            ? <th key={cellIndex} scope="row" className="whitespace-pre-wrap px-3 py-2 font-medium text-text-strong">{cell}</th>
            : <td key={cellIndex} className="max-w-sm whitespace-pre-wrap break-words px-3 py-2 text-text-body">{cell}</td>)}</tr>
        ))}</tbody>
      </table>
      </div>
    </div>
  );
}

function GeneratedChart({ block, label }: { block: ChartBlock; label?: string }) {
  const titleId = useId();
  const caption = block.title || label;
  const allValues = block.series.flatMap((series) => series.values);
  // Normalizing first also keeps SVG coordinates finite for very large finite numbers.
  const magnitude = Math.max(...allValues.map(Math.abs)) || 1;
  const minimum = Math.min(0, ...allValues.map((value) => value / magnitude));
  const maximum = Math.max(0, ...allValues.map((value) => value / magnitude));
  const range = maximum - minimum || 1;
  const width = Math.max(640, block.labels.length * 56);
  const height = 260;
  const left = 48;
  const right = width - 16;
  const top = 16;
  const bottom = 216;
  const unit = (right - left) / block.labels.length;
  const y = (value: number) => bottom - ((value / magnitude - minimum) / range) * (bottom - top);
  const baseline = y(0);
  const barWidth = unit * 0.75 / block.series.length;
  return (
    <figure className="space-y-3">
      <div className="overflow-x-auto">
        <svg viewBox={`0 0 ${width} ${height}`} style={{ minWidth: width }} className="w-full text-text-muted" role={caption ? "img" : undefined} aria-labelledby={caption ? titleId : undefined} aria-hidden={caption ? undefined : true}>
          {caption && <title id={titleId}>{caption}</title>}
          <line x1={left} x2={right} y1={baseline} y2={baseline} className="stroke-line" />
          <text x={left - 8} y={baseline + 4} textAnchor="end" fontSize={10} fill="currentColor">0</text>
          {block.labels.map((label, index) => <text key={index} x={left + (index + 0.5) * unit} y={bottom + 20} textAnchor="middle" fontSize={10} fill="currentColor">{label.length > 14 ? `${label.slice(0, 13)}…` : label}</text>)}
          {block.series.map((series, seriesIndex) => (
            <g key={seriesIndex} className={COLORS[seriesIndex]}>
              {block.chart_type === "line" && <polyline fill="none" stroke="currentColor" strokeWidth={2} points={series.values.map((value, index) => `${left + (index + 0.5) * unit},${y(value)}`).join(" ")} />}
              {series.values.map((value, index) => block.chart_type === "bar" ? (
                <rect key={index} x={left + index * unit + unit * 0.125 + seriesIndex * barWidth} y={Math.min(y(value), baseline)} width={barWidth} height={Math.abs(y(value) - baseline)} fill="currentColor">
                  <title>{`${series.name}: ${block.labels[index]} — ${value}`}</title>
                </rect>
              ) : (
                <circle key={index} cx={left + (index + 0.5) * unit} cy={y(value)} r={3} fill="currentColor"><title>{`${series.name}: ${block.labels[index]} — ${value}`}</title></circle>
              ))}
            </g>
          ))}
        </svg>
      </div>
      <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs" aria-label={caption}>
        {block.series.map((series, index) => <li key={index} className={`inline-flex items-center gap-1.5 ${COLORS[index]}`}><span aria-hidden="true" className="h-2 w-2 rounded-sm bg-current" /><span className="text-text-body">{series.name}</span></li>)}
      </ul>
      <DataTable rowHeadings caption={caption} columns={["", ...block.series.map((series) => series.name)]} rows={block.labels.map((label, index) => [label, ...block.series.map((series) => String(series.values[index]))])} />
    </figure>
  );
}

function EntityCoverThumb({ url, title }: { url?: string; title: string }) {
  const [failed, setFailed] = useState(false);
  const initial = (title.trim()[0] || "?").toUpperCase();
  if (url && !failed) {
    return (
      <img
        src={url}
        alt={title}
        loading="lazy"
        onError={() => setFailed(true)}
        className="h-14 w-14 shrink-0 rounded-control border border-line-subtle bg-surfaceSubtle object-cover"
      />
    );
  }
  return (
    <div
      aria-hidden="true"
      className="flex h-14 w-14 shrink-0 items-center justify-center rounded-control border border-line-subtle bg-surfaceSubtle text-xs font-semibold text-text-muted"
    >
      {initial}
    </div>
  );
}

function EntitiesGrid({ block }: { block: EntitiesBlock }) {
  const { tr, locale } = useI18n();
  const { kinds } = useDefinitions();
  const [query, setQuery] = useState("");
  const filtered = useMemo(() => {
    const q = query.trim().toLocaleLowerCase();
    if (!q) return block.items;
    return block.items.filter((item) =>
      item.title.toLocaleLowerCase().includes(q) ||
      (item.subtitle || "").toLocaleLowerCase().includes(q) ||
      (item.summary || "").toLocaleLowerCase().includes(q) ||
      (item.kind || "").toLocaleLowerCase().includes(q)
    );
  }, [block.items, query]);
  return (
    <div className="space-y-2.5">
      {block.items.length > 4 && (
        <label className="flex items-center gap-2 rounded-control border border-line bg-background px-2.5 py-1.5 text-xs">
          <Search className="size-3.5 shrink-0 text-text-muted" />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={tr("assistantUI.filterTable", "Filter items…")}
            className="w-full bg-transparent text-xs text-text-strong outline-none"
          />
          <span className="shrink-0 tabular-nums text-[11px] text-text-muted">{filtered.length} / {block.items.length}</span>
        </label>
      )}
      <div className="grid gap-2 sm:grid-cols-2">
        {filtered.map((item) => (
          <Link
            key={item.id}
            href={`/catalog/${item.id}`}
            className="group flex items-start justify-between gap-2.5 rounded-control border border-line bg-background p-2.5 transition-colors hover:border-primary/40 hover:bg-primary/5"
          >
            <EntityCoverThumb url={item.cover_url} title={item.title} />
            <div className="min-w-0 flex-1 space-y-0.5">
              <div className="flex items-center gap-1.5">
                {item.kind && (
                  <span className="shrink-0 rounded bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-primary">
                    {getKindName(kinds, item.kind, locale, tr(`catalog.kind.${item.kind}`, item.kind))}
                  </span>
                )}
                <span className="truncate text-xs font-semibold text-text-strong group-hover:text-primary">
                  {item.title}
                </span>
              </div>
              {item.subtitle && <p className="truncate text-[11px] text-text-muted">{item.subtitle}</p>}
              {item.summary && <p className="line-clamp-2 text-[11px] text-text-body">{item.summary}</p>}
              <p className="font-mono text-[10px] text-text-faint">/catalog/{item.id.slice(0, 8)}…</p>
            </div>
            <ArrowUpRight className="mt-0.5 size-3.5 shrink-0 text-text-faint transition-transform group-hover:translate-x-0.5 group-hover:-translate-y-0.5 group-hover:text-primary" />
          </Link>
        ))}
      </div>
    </div>
  );
}

export function GeneratedBlocksView({ blocks, className = "", chartLabel, tableLabel, timelineLabel }: {
  blocks: unknown;
  className?: string;
  chartLabel?: string;
  tableLabel?: string;
  timelineLabel?: string;
}) {
  const headingPrefix = useId();
  const parsed = parseGeneratedBlocks(blocks);
  if (parsed.length === 0) return null;
  return (
    <div className={`space-y-3 ${className}`}>
      {parsed.map((block, index) => (
        <Card key={index} className="min-w-0">
          {block.title && <CardTitle>{block.title}</CardTitle>}
          {block.type === "markdown" && <MarkdownRenderer content={block.markdown} headingPrefix={`${headingPrefix}-ai-block-${index}`} />}
          {block.type === "table" && <DataTable columns={block.columns} rows={block.rows} caption={block.title || tableLabel} />}
          {block.type === "chart" && <GeneratedChart block={block} label={chartLabel} />}
          {block.type === "entities" && <EntitiesGrid block={block} />}
          {block.type === "timeline" && <ol className="ml-2 border-l border-line pl-5" aria-label={block.title || timelineLabel}>
            {block.events.map((event, eventIndex) => <li key={eventIndex} className="relative pb-5 last:pb-0">
              <span aria-hidden="true" className="absolute -left-[1.6rem] top-1.5 h-2 w-2 rounded-full bg-primary" />
              <p className="text-xs text-text-muted">{event.date}</p>
              <p className="text-sm font-medium text-text-strong">{event.entity_id ? <Link href={`/catalog/${event.entity_id}`} className="text-primary hover:underline">{event.title}</Link> : event.title}</p>
              {event.description && <p className="mt-1 whitespace-pre-wrap text-sm text-text-body">{event.description}</p>}
            </li>)}
          </ol>}
        </Card>
      ))}
    </div>
  );
}

export default GeneratedBlocksView;
