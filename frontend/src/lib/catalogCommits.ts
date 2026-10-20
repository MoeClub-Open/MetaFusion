import { emptyEntity, type Entity } from "@/components/catalog/api";
import { ApiError } from "@/lib/api/client";
import {
  createWorkspace, getWorkspace, listWorkspaces, saveWorkspaceChanges,
  type Workspace, type WorkspaceOperation,
} from "@/lib/api/collaboration";

const currentWorkspace = new Map<string, string>();
const openingWorkspace = new Map<string, { promise: Promise<Workspace>; signal?: AbortSignal }>();
const creationWorkspaceID=new Map<string,string>();

/** Explicit links never fall back to another copy when access fails. */
export async function ensureEditingWorkspace(userId: string, requestedId?: string | null, signal?: AbortSignal): Promise<Workspace> {
  signal?.throwIfAborted();
  if (!userId) throw new Error("unauthenticated");
  if (requestedId) {
    const workspace = await getWorkspace(requestedId, signal);
    if (workspace.state !== "active") throw new Error("workspace_archived");
    return workspace;
  }
  const pending = openingWorkspace.get(userId);
  if (pending && !pending.signal?.aborted) return pending.promise;
  const opening = (async () => {
    const selectedId = currentWorkspace.get(userId);
    if (selectedId) {
      const selected = await getWorkspace(selectedId, signal);
      if (selected.state === "active" && selected.owner_id === userId) return selected;
      currentWorkspace.delete(userId);
    }
    const { items } = await listWorkspaces({}, signal);
    signal?.throwIfAborted();
    if(!creationWorkspaceID.has(userId))creationWorkspaceID.set(userId,crypto.randomUUID());
    const selected = items.find((item) => item.owner_id === userId && item.state === "active")
      ?? await createWorkspace({id:creationWorkspaceID.get(userId)!}, signal);
    if (selected.owner_id !== userId) throw new Error("workspace_owner_changed");
    currentWorkspace.set(userId, selected.id);
    creationWorkspaceID.delete(userId);
    return selected;
  })();
  openingWorkspace.set(userId, { promise: opening, signal });
  try { return await opening; }
  finally { if (openingWorkspace.get(userId)?.promise === opening) openingWorkspace.delete(userId); }
}

function canonical(value: unknown): string {
  return JSON.stringify(value, (_key, item) => item && typeof item === "object" && !Array.isArray(item)
    ? Object.fromEntries(Object.entries(item).sort(([a], [b]) => a.localeCompare(b))) : item);
}

/** Unrelated saves may proceed; an object changed by another editor must be resolved. */
export async function stageEditingWorkspace(snapshot: Workspace, operations: WorkspaceOperation[], signal?: AbortSignal) {
  signal?.throwIfAborted();
  const latest = await getWorkspace(snapshot.id, signal);
  if (latest.state !== "active") throw new Error("workspace_archived");
  if (operations.some((operation) => operation.action === "create") && latest.version !== snapshot.version) {
    throw new ApiError("workspace_conflict", 409);
  }
  for (const operation of operations) {
    if (!operation.id) continue;
    const before = snapshot.objects.find((object) => object.target === operation.target && object.id === operation.id);
    const current = latest.objects.find((object) => object.target === operation.target && object.id === operation.id);
    if (!before || !current || canonical(before.document) !== canonical(current.document)) {
      throw new ApiError("workspace_conflict", 409);
    }
  }
  signal?.throwIfAborted();
  try {
    return await saveWorkspaceChanges(snapshot.id, { expected_version: latest.version, operations }, signal);
  } catch (error) {
    if (operations.some((operation) => operation.action === "create") &&
      (!(error instanceof ApiError) || error.status >= 500)) {
      throw new ApiError("workspace_write_uncertain", 0);
    }
    throw error;
  }
}

const entityFactFields = ["title","original_language","translations","attributes","external_links","pictures",
  "content_unit_id","parent_id","position","number","contents","subjects"] as const;

export function entityOperation(next: Entity, base?: Entity): WorkspaceOperation {
  if (!base?.id) {
    const document: Record<string,unknown> = {kind:next.kind};
    for (const field of [...entityFactFields,"work_id","release_id","medium_id"] as const) {
      if (next[field] !== undefined) document[field] = next[field];
    }
    return {target:"entity",action:"create",ref:"entity",document};
  }
  const patch: NonNullable<WorkspaceOperation["patch"]> = [];
  const escape = (key:string) => key.replace(/~/g,"~0").replace(/\//g,"~1");
  const put = (path:string,value:unknown) => patch.push(value===undefined ? {path,remove:true} : {path,value});
  for (const key of entityFactFields) {
    if(JSON.stringify(next[key])===JSON.stringify(base[key]))continue;
    if(key==="attributes" || key==="translations") {
      const before=base[key] || {};const after=next[key] || {};
      for (const name of Object.keys({...before,...after})) {
        const a=(before as Record<string,any>)[name];const b=(after as Record<string,any>)[name];
        const path=`/${key}/${escape(name)}`;
        if(JSON.stringify(a)===JSON.stringify(b))continue;
        if(key==="translations" && a && b) {
          for(const field of Object.keys({...a,...b})) if(JSON.stringify(a[field])!==JSON.stringify(b[field])) put(`${path}/${escape(field)}`,b[field]);
        }else put(path,b);
      }
    }else put(`/${key}`,next[key]);
  }
  return {target:"entity",action:"update",id:base.id,patch};
}

export interface LocalEditConflict {
  pointer: string;
  base: unknown;
  current: unknown;
  proposed: unknown;
  remove: boolean;
}
export interface LocalEntityRebase { entity: Entity; conflicts: LocalEditConflict[] }
export type LocalEditResolution = { choice: "proposed" | "current" | "custom" | "remove"; value?: unknown };

/** The API represents absent collections as null; editing uses concrete containers. */
export function normalizeEditorEntity(document: unknown): Entity {
  if (!document || typeof document !== "object" || Array.isArray(document)) throw new Error("invalid_workspace_entity");
  const raw = structuredClone(document) as Partial<Entity>;
  if (typeof raw.kind !== "string" || typeof raw.title !== "string") throw new Error("invalid_workspace_entity");
  const entity: Entity = { ...emptyEntity(raw.kind), ...raw,
    attributes: raw.attributes ?? {}, translations: raw.translations ?? {}, pictures: raw.pictures ?? [],
    external_links: raw.external_links ?? [], contents: raw.contents ?? [], subjects: raw.subjects ?? [] };
  if (entity.translations && typeof entity.translations === "object" && !Array.isArray(entity.translations)) {
    for (const row of Object.values(entity.translations)) if (row && typeof row === "object") {
      if (row.aliases === null) row.aliases = [];
    }
  }
  if (Array.isArray(entity.pictures)) entity.pictures = entity.pictures.map((picture) => picture && typeof picture === "object"
    ? { ...picture, caption: picture.caption ?? {}, source: picture.source ?? {kind: "", citation: ""} } : picture);
  if (Array.isArray(entity.contents)) entity.contents = entity.contents.map((content) => content && typeof content === "object"
    ? { ...content, locator: content.locator ?? {}, attributes: content.attributes ?? {} } : content);
  if (!validEditorEntity(entity)) throw new Error("invalid_workspace_entity");
  return entity;
}

function pointerParts(pointer: string): string[] {
  return pointer.slice(1).split("/").map((part) => part.replace(/~1/g, "/").replace(/~0/g, "~"));
}
function atPointer(document: unknown, pointer: string): unknown {
  let value = document;
  for (const part of pointerParts(pointer)) {
    if (!value || typeof value !== "object" || !Object.hasOwn(value, part)) return undefined;
    value = (value as Record<string, unknown>)[part];
  }
  return value;
}
function putPointer(document: Entity, pointer: string, value: unknown, remove: boolean) {
  const parts = pointerParts(pointer);
  let container = document as unknown as Record<string, unknown>;
  for (const part of parts.slice(0, -1)) {
    if (!Object.hasOwn(container, part) || !container[part] || typeof container[part] !== "object") {
      if (remove) return;
      Object.defineProperty(container, part, { value: {}, enumerable: true, writable: true, configurable: true });
    }
    container = container[part] as Record<string, unknown>;
  }
  const key = parts[parts.length - 1];
  if (remove) delete container[key];
  else Object.defineProperty(container, key, { value: structuredClone(value), enumerable: true, writable: true, configurable: true });
}

/** Refresh a shared copy without discarding the editor's unsaved fields. */
export function rebaseEntityEdit(base: Entity, proposed: Entity, current: Entity): LocalEntityRebase {
  const entity = structuredClone(current);
  const conflicts: LocalEditConflict[] = [];
  for (const patch of entityOperation(proposed, base).patch || []) {
    const before = atPointer(base, patch.path);
    const theirs = atPointer(current, patch.path);
    const mine = patch.remove ? undefined : patch.value;
    if (canonical(theirs) === canonical(before) || canonical(theirs) === canonical(mine)) {
      putPointer(entity, patch.path, mine, !!patch.remove);
    } else conflicts.push({ pointer: patch.path, base: before, current: theirs, proposed: mine, remove: !!patch.remove });
  }
  return { entity, conflicts };
}

export function resolveEntityEdit(rebase: LocalEntityRebase, resolutions: Record<string, LocalEditResolution>): Entity {
  const entity = structuredClone(rebase.entity);
  for (const conflict of rebase.conflicts) {
    const resolution = resolutions[conflict.pointer];
    if (!resolution) throw new Error("workspace_resolution_required");
    if (resolution.choice === "current") continue;
    putPointer(entity, conflict.pointer,
      resolution.choice === "proposed" ? conflict.proposed : resolution.value,
      resolution.choice === "remove" || resolution.choice === "proposed" && conflict.remove);
  }
  if (!validEditorEntity(entity)) throw new Error("workspace_resolution_invalid");
  return entity;
}

function validEditorEntity(entity: Entity): boolean {
  const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === "object" && !Array.isArray(value);
  const names = (value: unknown) => record(value) && Object.values(value).every((name) => typeof name === "string");
  if (typeof entity.title !== "string" || typeof entity.original_language !== "string" || typeof entity.number !== "string" || !Number.isFinite(entity.position)) return false;
  if (!record(entity.attributes) || !record(entity.translations)) return false;
  if (!Object.values(entity.translations).every((row) => record(row) && typeof row.title === "string" &&
    (row.body === undefined || typeof row.body === "string") && (row.summary === undefined || typeof row.summary === "string") &&
    (row.aliases === undefined || Array.isArray(row.aliases) && row.aliases.every((alias) => typeof alias === "string")))) return false;
  if (!Array.isArray(entity.pictures) || !entity.pictures.every((picture) => record(picture) && typeof picture.url === "string" && names(picture.caption) &&
    (picture.source === undefined || record(picture.source) && typeof picture.source.kind === "string" && typeof picture.source.citation === "string"))) return false;
  if (!Array.isArray(entity.external_links) || !entity.external_links.every((link) => record(link) && typeof link.kind === "string" && typeof link.url === "string")) return false;
  if (!Array.isArray(entity.contents) || !entity.contents.every((content) => record(content) && typeof content.expression_id === "string" && Number.isFinite(content.position) && record(content.locator))) return false;
  if (!Array.isArray(entity.subjects) || !entity.subjects.every((subject) => record(subject) && typeof subject.work_id === "string" && typeof subject.role === "string" && Number.isFinite(subject.position))) return false;
  return true;
}
