import type { Entity } from "@/components/catalog/api";

function hasValue(value: unknown): boolean {
  if (value === undefined || value === null || value === "") return false;
  if (Array.isArray(value)) return value.some(hasValue);
  if (typeof value === "object") return Object.values(value).some(hasValue);
  return true;
}

/** These values cannot safely be carried into another entity skeleton. */
export function hasKindSpecificDraftData(entity: Entity, queuedRelations: number): boolean {
  return queuedRelations > 0
    || Object.entries(entity.attributes || {}).some(([key, value]) => key !== "tags" && hasValue(value))
    || [entity.work_id, entity.content_unit_id, entity.release_id, entity.medium_id, entity.parent_id, entity.number].some(hasValue)
    || entity.position !== 0
    || hasValue(entity.contents)
    || hasValue(entity.subjects);
}

/** Keep common identity/evidence fields; use the target skeleton's defaults for everything else. */
export function changeDraftKind(entity: Entity, targetDefaults: Entity): Entity {
  if (entity.kind === targetDefaults.kind) return entity;
  return {
    ...targetDefaults,
    title: entity.title,
    state: entity.state,
    original_language: entity.original_language,
    translations: structuredClone(entity.translations),
    external_links: structuredClone(entity.external_links || []),
    pictures: structuredClone(entity.pictures),
    attributes: Object.prototype.hasOwnProperty.call(entity.attributes || {}, "tags")
      ? { tags: structuredClone(entity.attributes.tags) }
      : {},
  };
}
