import type { Entity, Relation } from "@/components/catalog/api";

type ReadableRelation = Omit<Relation, "version">;

export type RelationDirectoryRow = {
  key: string;
  type: string;
  kind: string;
  direction: "outgoing" | "incoming" | "reference";
  entityId: string;
  entity?: Entity;
  source?: Entity;
  target?: Entity;
  records: ReadableRelation[];
};

export type RelationTemporalFilter = {
  activeOnly?: boolean;
  asOf?: string;
};

export function validPartialISODate(raw: string): boolean {
  const text = raw.trim();
  const m = /^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?$/.exec(text);
  if (!m) return false;
  const year = Number(m[1]);
  if (m[2] === undefined) return year >= 1;
  const month = Number(m[2]);
  if (month < 1 || month > 12) return false;
  if (m[3] === undefined) return true;
  const day = Number(m[3]);
  const maxDay = new Date(Date.UTC(year, month, 0)).getUTCDate();
  return day >= 1 && day <= maxDay;
}

function partialDateStartMs(raw: string): number {
  const [y, m, d] = raw.trim().split("-").map(Number);
  return Date.UTC(y, (m || 1) - 1, d || 1, 0, 0, 0, 0);
}

function partialDateEndMs(raw: string): number {
  const parts = raw.trim().split("-").map(Number);
  if (parts.length === 1) return Date.UTC(parts[0], 11, 31, 23, 59, 59, 999);
  if (parts.length === 2) return Date.UTC(parts[0], parts[1], 0, 23, 59, 59, 999);
  return Date.UTC(parts[0], parts[1] - 1, parts[2], 23, 59, 59, 999);
}

function dateAttr(attrs: Record<string, unknown> | undefined, key: string): string {
  const val = attrs?.[key];
  if (typeof val !== "string") return "";
  const s = val.trim();
  return validPartialISODate(s) ? s : "";
}

export function hasTemporalQualifiers(rows: readonly RelationDirectoryRow[]): boolean {
  return rows.some((row) =>
    row.records.some((record) => {
      const attrs = record.attributes;
      if (!attrs) return false;
      return (
        attrs.ended === true ||
        Boolean(dateAttr(attrs, "begin_date") || dateAttr(attrs, "end_date") || dateAttr(attrs, "earliest_date") || dateAttr(attrs, "latest_date"))
      );
    }),
  );
}

export function recordMatchesTemporal(
  attrs: Record<string, unknown> | undefined,
  filter: RelationTemporalFilter,
  nowMs = Date.now(),
): boolean {
  const asOf = (filter.asOf || "").trim();
  const activeOnly = Boolean(filter.activeOnly);
  if (!activeOnly && !asOf) return true;
  if (asOf && !validPartialISODate(asOf)) return false;

  const ended = attrs?.ended === true;
  const beginDate = dateAttr(attrs, "begin_date");
  const endDate = dateAttr(attrs, "end_date");
  const earliestDate = dateAttr(attrs, "earliest_date");
  const latestDate = dateAttr(attrs, "latest_date");

  if (asOf) {
    const qStart = partialDateStartMs(asOf);
    const qEnd = partialDateEndMs(asOf);
    if (beginDate && partialDateStartMs(beginDate) > qEnd) return false;
    if (earliestDate && partialDateStartMs(earliestDate) > qEnd) return false;
    if (endDate && partialDateEndMs(endDate) < qStart) return false;
    if (latestDate && partialDateEndMs(latestDate) < qStart) return false;
    if (activeOnly && ended && !endDate && !latestDate) return false;
    return true;
  }
  if (ended) return false;
  if (endDate && partialDateEndMs(endDate) < nowMs) return false;
  if (latestDate && partialDateEndMs(latestDate) < nowMs) return false;
  return true;
}

export function filterRelationDirectoryTemporal(
  rows: readonly RelationDirectoryRow[],
  filter: RelationTemporalFilter,
  nowMs = Date.now(),
): RelationDirectoryRow[] {
  const asOf = (filter.asOf || "").trim();
  if (!filter.activeOnly && !asOf) return [...rows];
  const out: RelationDirectoryRow[] = [];
  for (const row of rows) {
    const records = row.records.filter((record) => recordMatchesTemporal(record.attributes, filter, nowMs));
    if (records.length > 0) {
      out.push(records.length === row.records.length ? row : { ...row, records });
    }
  }
  return out;
}

// Coalesce statements about the same endpoints, retaining every statement and
// its qualifiers. Titles never establish identity (Work and Expression can match).
export function relationDirectoryRows(subjectId: string, relations: ReadableRelation[], entities: Record<string, Entity>) {
  const rows = new Map<string, RelationDirectoryRow>();
  for (const record of relations) {
    const direction = record.source_id === subjectId ? "outgoing" : record.target_id === subjectId ? "incoming" : "reference";
    const entityId = direction === "outgoing" ? record.target_id : record.source_id;
    const key = JSON.stringify([record.type, record.source_id, record.target_id]);
    let row = rows.get(key);
    if (!row) {
      row = { key, type: record.type, kind: entities[entityId]?.kind || "", direction, entityId,
        entity: entities[entityId], source: entities[record.source_id], target: entities[record.target_id], records: [] };
      rows.set(key, row);
    }
    row.records.push(record);
  }
  return Array.from(rows.values());
}

export function relationDirectorySearch(row: RelationDirectoryRow, query: string) {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return true;
  const entityText = (entity?: Entity) => entity ? [entity.title, ...Object.values(entity.translations || {}).flatMap((text) => [text.title, ...(text.aliases || [])])] : [];
  return JSON.stringify([row.type, row.kind, ...entityText(row.entity),
    ...(row.direction === "reference" ? entityText(row.target) : []), row.records.map((record) => record.attributes)])
    .toLocaleLowerCase().includes(needle);
}
