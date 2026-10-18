import type { AuditLogEntry } from "./api/audit";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const TARGET_ROUTES: Record<string, string> = {
  entity: "/catalog/", workspace: "/workspaces/", commit: "/commits/",
  pull_request: "/pull-requests/", topic: "/community/", user: "/users/",
};

export function activityTargetHref(entry: AuditLogEntry): string | undefined {
  const type = entry.target_type ?? "";
  const prefix = Object.hasOwn(TARGET_ROUTES, type) ? TARGET_ROUTES[type] : undefined;
  return prefix && UUID.test(entry.target_id ?? "") ? prefix + entry.target_id : undefined;
}

/** Prefer the name captured at the time of the action; never infer a historical title. */
export function activityTargetName(entry: AuditLogEntry): string {
  const changes = entry.changes ?? {};
  for (const key of ["target_name", "title", "name", "message", "filename", "file_name", "slug", "code"]) {
    const value = changes[key];
    if (typeof value === "string" && value.trim()) return value;
    if (value && typeof value === "object" && "after" in value && typeof value.after === "string") return value.after;
  }
  return "";
}

export interface ActivityChange {
  field: string;
  before?: unknown;
  after?: unknown;
  hasBefore: boolean;
  hasAfter: boolean;
}

/** Preserve absent-vs-null and the server's redactions while presenting summaries as fields. */
export function activityChanges(changes: AuditLogEntry["changes"]): ActivityChange[] {
  const result: ActivityChange[] = [];
  const walk = (value: Record<string, unknown>, prefix = "") => {
    if ("before" in value || "after" in value) {
      const before = value.before, after = value.after;
      if (before && after && typeof before === "object" && typeof after === "object" && !Array.isArray(before) && !Array.isArray(after)) {
        for (const field of new Set([...Object.keys(before), ...Object.keys(after)])) {
          const b = before as Record<string, unknown>, a = after as Record<string, unknown>;
          if (JSON.stringify(b[field]) !== JSON.stringify(a[field])) result.push({ field: prefix ? `${prefix}.${field}` : field, before: b[field], after: a[field], hasBefore: field in b, hasAfter: field in a });
        }
      } else result.push({ field: prefix || "value", before, after, hasBefore: "before" in value, hasAfter: "after" in value });
      return;
    }
    for (const [field, item] of Object.entries(value)) {
      if (["target_name", "_truncated"].includes(field) || field.endsWith("_id")) continue;
      const path = prefix ? `${prefix}.${field}` : field;
      if (item && typeof item === "object" && !Array.isArray(item)) walk(item as Record<string, unknown>, path);
      else result.push({ field: path, after: item, hasBefore: false, hasAfter: true });
    }
  };
  walk(changes ?? {});
  return result;
}
