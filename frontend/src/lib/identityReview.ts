import type { Entity } from "@/components/catalog/api";

export type IdentityCandidateQuery = {
  kind: string;
  titles: string[];
  external_ids: { provider: string; value: string }[];
  work_id?: string;
  release_id?: string;
  medium_id?: string;
};

export type IdentityCandidateEntity = {
  id: string;
  kind: string;
  title: string;
  status: string;
  version: number;
  translations?: Entity["translations"];
  external_ids?: Entity["external_ids"];
  attributes?: Entity["attributes"];
  work_id?: string;
  content_unit_id?: string;
  release_id?: string;
  medium_id?: string;
  parent_id?: string;
};

export type IdentityCandidateMatch = {
  matched: IdentityCandidateEntity;
  canonical: IdentityCandidateEntity;
};

export type IdentityReviewData = {
  items: IdentityCandidateMatch[];
  candidateIDs: string[];
};

export type IdentityReviewState = {
  key: string;
  expectedIDs: string[] | null;
  status: "loading" | "failed" | "ready";
  data?: IdentityReviewData;
  confirmed: boolean;
  error?: string;
};

/** Keep lookup criteria and ownership identical to the server's commitIdentityQuery. */
export function identityCandidateQuery(entity: Entity): IdentityCandidateQuery {
  const query: IdentityCandidateQuery = {
    kind: entity.kind,
    titles: [entity.title, ...Object.keys(entity.translations || {}).sort()
      .map((locale) => entity.translations[locale].title).filter(Boolean)],
    external_ids: Object.entries(entity.external_ids || {}).sort(([a], [b]) => a.localeCompare(b))
      .map(([provider, value]) => ({ provider, value })),
  };
  if (entity.kind === "expression" || entity.kind === "content_unit") query.work_id = entity.work_id;
  if (entity.kind === "medium") query.release_id = entity.release_id;
  if (entity.kind === "track") query.medium_id = entity.medium_id;
  return query;
}

/** Evidence edits preserve confirmation; changes to names, identifiers or ownership revoke it. */
export function identityReviewKey(entity: Entity): string {
  return JSON.stringify({
    id: entity.id,
    query: identityCandidateQuery(entity),
    aliases: Object.keys(entity.translations || {}).sort()
      .map((locale) => [locale, entity.translations[locale].aliases || []]),
  });
}

function record(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function canonicalUUID(value: unknown): value is string {
  return typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);
}

function candidateEntity(value: unknown): value is IdentityCandidateEntity {
  if (!record(value) || !canonicalUUID(value.id) || typeof value.kind !== "string" ||
      typeof value.title !== "string" || typeof value.status !== "string" ||
      typeof value.version !== "number" || !Number.isSafeInteger(value.version) || value.version < 0) return false;
  for (const field of ["translations", "external_ids", "attributes"]) {
    if (value[field] != null && !record(value[field])) return false;
  }
  if (record(value.external_ids) && Object.values(value.external_ids).some((id) => typeof id !== "string")) return false;
  if (record(value.translations) && Object.values(value.translations).some((translation) =>
    !record(translation) || typeof translation.title !== "string" ||
    (translation.aliases != null && (!Array.isArray(translation.aliases) || translation.aliases.some((alias) => typeof alias !== "string"))))) return false;
  for (const field of ["work_id", "content_unit_id", "release_id", "medium_id", "parent_id"]) {
    if (value[field] !== undefined && !canonicalUUID(value[field])) return false;
  }
  return true;
}

/** Read the stable 409 review receipt, without treating it as a user acknowledgement. */
export function identityConflictIDs(details: unknown): string[] | null {
  if (!record(details) || details.error !== "identity_candidates_changed" || !record(details.identity_review)) return null;
  const ids = details.identity_review.candidate_ids;
  if (!Array.isArray(ids) || ids.length > 1000 || ids.some((id) => !canonicalUUID(id)) || new Set(ids).size !== ids.length) return null;
  return [...ids].sort();
}

/** An incomplete or unresolved lookup must never become an empty, confirmable result. */
export function readIdentityCandidates(value: unknown, entity: Pick<Entity, "id" | "kind">): IdentityReviewData {
  if (!record(value) || !Array.isArray(value.items) || typeof value.total !== "number" ||
      !Number.isSafeInteger(value.total) || value.total < value.items.length || typeof value.complete !== "boolean") {
    throw new Error("invalid_identity_review");
  }
  if (!value.complete) throw new Error("identity_candidates_incomplete");
  if (value.total !== value.items.length) throw new Error("identity_candidates_incomplete");
  const items: IdentityCandidateMatch[] = [];
  const candidateIDs = new Set<string>();
  const matchedIDs = new Set<string>();
  for (const item of value.items) {
    if (!record(item) || !candidateEntity(item.matched) || matchedIDs.has(item.matched.id)) throw new Error("invalid_identity_review");
    matchedIDs.add(item.matched.id);
    if (item.resolution_error || !candidateEntity(item.canonical) || item.canonical.kind !== entity.kind ||
        item.matched.kind !== entity.kind || item.canonical.status === "deleted" || item.canonical.status === "merged") {
      throw new Error("identity_candidate_unresolved");
    }
    if (item.canonical.id === entity.id) continue;
    // Multiple historical aliases may resolve to one identity. Show every matched alias,
    // but acknowledge each canonical identity exactly once.
    items.push({ matched: item.matched, canonical: item.canonical });
    candidateIDs.add(item.canonical.id);
  }
  return { items, candidateIDs: Array.from(candidateIDs).sort() };
}

/** IDs enter the write body only after an explicit confirmation for the current identity. */
export function reviewedIdentityIDs(review: IdentityReviewState | null, currentKey: string): string[] | undefined {
  return review?.key === currentKey && review.status === "ready" && review.confirmed && review.data
    ? [...review.data.candidateIDs] : undefined;
}
