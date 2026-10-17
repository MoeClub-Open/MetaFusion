"use client";

import { useId } from "react";
import Link from "next/link";
import MarkdownRenderer from "@/components/MarkdownRenderer";
import { Card, CardTitle } from "@/components/ui/Card";
import { parseGeneratedBlocks, type GeneratedBlock } from "@/lib/generatedBlocks";

export type { GeneratedBlock } from "@/lib/generatedBlocks";
export { parseGeneratedBlocks } from "@/lib/generatedBlocks";

type ChartBlock = Extract<GeneratedBlock, { type: "chart" }>;
const COLORS = ["text-primary", "text-success", "text-warn", "text-violet-500", "text-rose-500", "text-cyan-600", "text-orange-500", "text-text-muted"];

function DataTable({ columns, rows, caption, rowHeadings = false }: { columns: string[]; rows: string[][]; caption?: string; rowHeadings?: boolean }) {
  return (
    <div className="overflow-x-auto">
      <table className="min-w-full divide-y divide-line text-left text-xs sm:text-sm">
        {caption && <caption className="sr-only">{caption}</caption>}
        <thead><tr>{columns.map((column, index) => <th key={index} scope="col" className="whitespace-nowrap px-3 py-2 font-medium text-text-strong">{column}</th>)}</tr></thead>
        <tbody className="divide-y divide-line-subtle">{rows.map((row, rowIndex) => (
          <tr key={rowIndex}>{row.map((cell, cellIndex) => rowHeadings && cellIndex === 0
            ? <th key={cellIndex} scope="row" className="whitespace-pre-wrap px-3 py-2 font-medium text-text-strong">{cell}</th>
            : <td key={cellIndex} className="max-w-sm whitespace-pre-wrap break-words px-3 py-2 text-text-body">{cell}</td>)}</tr>
        ))}</tbody>
      </table>
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

export default function GeneratedBlocks({ blocks, className = "", chartLabel, tableLabel, timelineLabel }: {
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
