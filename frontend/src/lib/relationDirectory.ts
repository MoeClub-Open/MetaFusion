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
