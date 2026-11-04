import { api, type Entity, type Relation } from "@/components/catalog/api";

/** Only semantic facts have editable versions; reference keys are read-only. */
export type RelationshipRecord = Omit<Relation, "version" | "id"> & {
  id: string;
  rule_code: string;
  class: "semantic" | "reference";
  version?: number;
  field?: string;
  via?: string[];
};

export interface RelationshipLink {
  key: string;
  rule_code: string;
  class: string;
  source_id: string;
  target_id: string;
  position: number;
  attributes?: Record<string, unknown>;
  version?: number;
  field?: string;
  via?: string[];
}

export interface EntityLinksPage {
  subject_id: string;
  items: RelationshipLink[];
  entities: Record<string, Entity>;
  limit: number;
  has_more: boolean;
}

export type EntityRelationships = {
  subject_id: string;
  items: RelationshipRecord[];
  entities: Record<string, Entity>;
  /** More visible links exist beyond the loaded window. */
  truncated: boolean;
};

/** Complete, uncached read for editors and explicit "load all" actions. */
export function fetchEntityRelationships(subjectId: string) {
  return loadEntityRelationships(subjectId, (offset) => readLinksPage(subjectId, offset));
}

const PREVIEW_LIMIT = 300;
const PREVIEW_TTL = 5_000;
const previews = new Map<string, { at: number; promise: Promise<EntityRelationships> }>();

/**
 * Detail sections share one bounded read per subject. Entities with thousands
 * of links render the first window immediately and load the rest on request.
 */
export function fetchEntityRelationshipPreview(subjectId: string, viewerId = ""): Promise<EntityRelationships> {
  const cacheKey = `${viewerId}:${subjectId}`;
  const cached = previews.get(cacheKey);
  if (cached && Date.now() - cached.at < PREVIEW_TTL) return cached.promise;
  const promise = loadEntityRelationships(subjectId, (offset) => readLinksPage(subjectId, offset), PREVIEW_LIMIT);
  previews.set(cacheKey, { at: Date.now(), promise });
  promise.catch(() => previews.delete(cacheKey));
  return promise;
}

function readLinksPage(subjectId: string, offset: number) {
  return api<EntityLinksPage>(`/catalog/entities/${encodeURIComponent(subjectId)}/links?limit=100&offset=${offset}`);
}

export async function loadEntityRelationships(
  subjectId: string,
  readPage: (offset: number) => Promise<EntityLinksPage>,
  maxItems = Infinity,
): Promise<EntityRelationships> {
  const records = new Map<string, RelationshipRecord>();
  const entities: Record<string, Entity> = {};
  let truncated = false;
  for (let offset = 0; ; ) {
    const page = await readPage(offset);
    if (page.subject_id !== subjectId || !Array.isArray(page.items) || !page.entities
      || typeof page.has_more !== "boolean" || !Number.isInteger(page.limit) || page.limit < 1) {
      throw new Error("invalid_response: catalog.links");
    }
    Object.assign(entities, page.entities);
    for (const link of page.items) {
      // Fixed ownership and inclusions already have their own detail sections.
      if (link.class !== "semantic" && link.class !== "reference") continue;
      if (link.class === "semantic" && (!Number.isSafeInteger(link.version) || link.version! < 1)) {
        throw new Error("invalid_response: catalog.links.version");
      }
      records.set(link.key, {
        id: link.key,
        type: link.rule_code.startsWith("relation:") ? link.rule_code.slice("relation:".length) : link.rule_code,
        rule_code: link.rule_code, class: link.class,
        source_id: link.source_id, target_id: link.target_id, position: link.position,
        attributes: link.attributes || {}, version: link.version, field: link.field, via: link.via,
      });
    }
    if (!page.has_more) break;
    offset += page.limit;
    if (offset >= maxItems) {
      truncated = true;
      break;
    }
  }
  return { subject_id: subjectId, items: [...records.values()], entities, truncated };
}

export function editableRelations(records: RelationshipRecord[]): (Relation & { id: string })[] {
  return records.filter((record) => record.class === "semantic").map((record) => {
    if (!record.id || !Number.isSafeInteger(record.version) || record.version! < 1) throw new Error("invalid_response: catalog.links.version");
    return { id: record.id, version: record.version!, type: record.type, source_id: record.source_id,
      target_id: record.target_id, position: record.position, attributes: record.attributes };
  });
}
