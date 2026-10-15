export type MergeIdentity = {
  id?: string;
  kind?: string;
  state?: string;
  work_id?: string;
  content_unit_id?: string;
  release_id?: string;
  medium_id?: string;
  parent_id?: string;
};

/** Mirrors catalog lifecycle validation; the server remains authoritative at submission. */
export function isCompatibleMergeTarget(source: MergeIdentity, target: MergeIdentity): boolean {
  if (!source.id || !target.id || source.id.toLowerCase() === target.id.toLowerCase()) return false;
  if (source.kind !== target.kind || target.state !== "active") return false;
  return (["work_id", "content_unit_id", "release_id", "medium_id", "parent_id"] as const)
    .every((key) => (source[key] || "").toLowerCase() === (target[key] || "").toLowerCase());
}
