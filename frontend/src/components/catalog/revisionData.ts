import type { Entity } from "./api";
import { canEditEntity } from "@/lib/permissions";

function canonical(value: any): any {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  }
  return value;
}

// 完整比较实体字段，属性和翻译按键展开；对象键顺序不构成修订。
export function revisionChanges(before: any = {}, after: any = {}) {
  const changes: Record<string, { old: any; new: any }> = {};
  const put = (key: string, old: any, next: any) => {
    if (JSON.stringify(canonical(old)) !== JSON.stringify(canonical(next))) {
      changes[key] = { old, new: next };
    }
  };
  const ignored = new Set(["id", "version", "created_by", "created_at", "updated_at"]);
  const left = before || {};
  const right = after || {};
  for (const key of Array.from(new Set([...Object.keys(left), ...Object.keys(right)]))) {
    if (ignored.has(key) || key.startsWith("localized_")) continue;
    if (key === "translations") {
      for (const locale of Array.from(new Set([...Object.keys(left[key] || {}), ...Object.keys(right[key] || {})]))) {
        const oldRow = left[key]?.[locale] || {};
        const newRow = right[key]?.[locale] || {};
        for (const field of Array.from(new Set([...Object.keys(oldRow), ...Object.keys(newRow)]))) {
          put(`${key}.${locale}.${field}`, oldRow[field], newRow[field]);
        }
      }
    } else if (key === "attributes") {
      for (const child of Array.from(new Set([...Object.keys(left[key] || {}), ...Object.keys(right[key] || {})]))) {
        put(`${key}.${child}`, left[key]?.[child], right[key]?.[child]);
      }
    } else put(key, left[key], right[key]);
  }
  return changes;
}

export function canEditRevision(
  entity: Entity,
  user?: { id: string; permissions?: string[]; groups?: string[] },
): boolean {
  // 授权口径收敛到 lib/permissions.ts（与后端 permission.go 一致）：
  // 前端不再自己比较 role 字符串，否则"给了权限组却看不到编辑入口"。
  return canEditEntity(user, entity);
}

// 载入历史内容，保留当前版本锁、实体身份与生命周期。仍由普通 PUT 执行权限、
// definitions、引用和审计校验；不修改旧修订、不直接写库或另建还原接口。
export function prepareRevisionRestore(current: Entity, snapshot: Entity): Entity {
  if (!snapshot || snapshot.id !== current.id || snapshot.kind !== current.kind ||
      ["deleted", "merged"].includes(current.state) || ["deleted", "merged"].includes((snapshot.state || (snapshot as any).status))) {
    throw new Error("revision_incompatible");
  }
  for (const key of ["work_id", "release_id", "medium_id"] as const) {
    if ((snapshot[key] || "") !== (current[key] || "")) throw new Error("revision_incompatible");
  }
  const result = structuredClone(current);
  const contentFields = ["title", "original_language", "translations", "attributes",
    "external_links", "pictures", "content_unit_id", "parent_id", "position", "number", "contents", "subjects"] as const;
  for (const key of contentFields) {
    if (Object.prototype.hasOwnProperty.call(snapshot, key)) {
      (result as any)[key] = structuredClone(snapshot[key]);
    } else if (key === "parent_id" || key === "content_unit_id") {
      delete result[key];
    }
  }
  return result;
}
