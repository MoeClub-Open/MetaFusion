import { fetchApi } from "./client";

export type CatalogObject = Record<string, unknown>;
export type WorkspaceTarget = "entity" | "relation";
export interface WorkspaceObject {
  target: WorkspaceTarget;
  id: string;
  base_version: number;
  base: CatalogObject | null;
  committed: CatalogObject | null;
  document: CatalogObject | null;
  dirty: boolean;
}
export interface Workspace {
  base_commit_id: string | null;
  id: string;
  owner_id: string;
  name: string;
  version: number;
  head_commit_id: string | null;
  state: "active" | "archived";
  members: string[];
  objects: WorkspaceObject[];
  dirty_count: number;
  pending_commits: number;
  created_at: string;
  updated_at: string;
}
export interface WorkspaceOperation {
  target: WorkspaceTarget;
  action: "create" | "update" | "delete" | "content";
  id?: string;
  ref?: string;
  document?: unknown;
  patch?: { path: string; value?: unknown; remove?: boolean }[];
  content_change?: { action: "add" | "replace" | "delete"; position: number; inclusion?: unknown };
}
export interface WorkspaceChange {
  target: WorkspaceTarget;
  id: string;
  base_version: number;
  base: CatalogObject | null;
  before: CatalogObject | null;
  after: CatalogObject | null;
}
export interface WorkspaceCommit {
  id: string;
  workspace_id: string | null;
  parent_id: string | null;
  merge_parent_id: string | null;
  operation: string;
  source_ids: string[];
  replaced_by: string[];
  snapshot_scope: "catalog" | "known_records" | "tracked_records";
  author: { id: string; name: string };
	committer: { id: string; name: string };
  authors: { id: string; name: string }[];
  message: string;
  kind: "edit" | "rebase";
  created_at: string;
  merged_at: string | null;
  merged_by: string | null;
  pull_request_id: string | null;
  changes: WorkspaceChange[];
}
export interface PullRequest {
  id: string;
  workspace_id: string;
  author_id: string;
  title: string;
  status: "open" | "merged" | "closed";
  source_head_id: string;
  source_base_id: string | null;
  version: number;
  merged_at: string | null;
  merged_by: string | null;
  created_at: string;
  updated_at: string;
  commits: WorkspaceCommit[];
  changes: WorkspaceObject[];
}
export interface PushResult {
  applied: boolean;
  workspace: Workspace | null;
  commits: WorkspaceCommit[];
  pull_request: PullRequest | null;
}
export interface WorkspaceConflict {
	 source_commit_id?: string;
  target: WorkspaceTarget;
  id: string;
  base_version: number;
  current_version: number;
  pointer: string;
  base: unknown;
  current: unknown;
  proposed: unknown;
  base_present: boolean;
  current_present: boolean;
  proposed_present: boolean;
  remove: boolean;
  kind: string;
}
export interface ConflictResolution {
	 commit_id?: string;
  target: WorkspaceTarget;
  id: string;
  pointer: string;
  current_version: number;
  value?: unknown;
  remove?: boolean;
}

export interface CollaborationPage<T> { items: T[]; total: number; limit: number; offset: number }
const workspacePath = (id: string) => `/catalog/workspaces/${encodeURIComponent(id)}`;
const pullRequestPath = (id: string) => `/catalog/pull-requests/${encodeURIComponent(id)}`;
function jsonRequest<T>(path: string, method: string, body: unknown, signal?: AbortSignal): Promise<T> {
  return fetchApi<T>(path, { method, body: JSON.stringify(body), signal });
}

export const listWorkspaces = (input: { limit?: number; offset?: number } = {}, signal?: AbortSignal) => {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(input)) if (value !== undefined) query.set(key, String(value));
  return fetchApi<CollaborationPage<Workspace>>(`/catalog/workspaces${query.size ? `?${query}` : ""}`, { signal });
};
export const createWorkspace = (input: { id?: string; name?: string; entity_ids?: string[]; relation_ids?: string[] } = {}, signal?: AbortSignal) =>
  jsonRequest<Workspace>("/catalog/workspaces", "POST", input, signal);
export const getWorkspace = (id: string, signal?: AbortSignal) => fetchApi<Workspace>(workspacePath(id), { signal });
export const updateWorkspace = (id: string, input: { expected_version: number; name?: string; state?: Workspace["state"] }, signal?: AbortSignal) =>
  jsonRequest<Workspace>(workspacePath(id), "PATCH", input, signal);
export const checkoutWorkspace = (id: string, input: { expected_version: number; entity_ids: string[]; relation_ids: string[] }, signal?: AbortSignal) =>
  jsonRequest<Workspace>(`${workspacePath(id)}/checkout`, "POST", input, signal);
export const saveWorkspaceChanges = (id: string, input: { expected_version: number; operations: WorkspaceOperation[] }, signal?: AbortSignal) =>
  jsonRequest<{ workspace: Workspace; refs: Record<string, string> }>(`${workspacePath(id)}/changes`, "PUT", input, signal);
export const updateWorkspaceMembers = (id: string, input: { expected_version: number; user_ids: string[] }, signal?: AbortSignal) =>
  jsonRequest<Workspace>(`${workspacePath(id)}/members`, "PUT", input, signal);
export const listWorkspaceCommits = (id: string, signal?: AbortSignal, page: { limit?: number; offset?: number } = {}) => fetchApi<CollaborationPage<WorkspaceCommit>>(`${workspacePath(id)}/commits?${new URLSearchParams(Object.entries(page).filter(([,value])=>value!==undefined).map(([key,value])=>[key,String(value)]))}`, { signal });
export const getWorkspaceCommit = (id: string, signal?: AbortSignal) => fetchApi<WorkspaceCommit>(`/catalog/commits/${encodeURIComponent(id)}`, { signal });
export const createWorkspaceCommit = (id: string, input: { id: string; expected_version: number; message: string }, signal?: AbortSignal) =>
  jsonRequest<WorkspaceCommit>(`${workspacePath(id)}/commits`, "POST", input, signal);
export const pushWorkspace = (id: string, input: { expected_version: number; expected_head: string; title?: string }, signal?: AbortSignal) =>
  jsonRequest<PushResult>(`${workspacePath(id)}/push`, "POST", input, signal);
export interface RebaseAction { expected_version: number; expected_head: string; id: string; onto_commit_id?: string; order?: string[]; message?: string; resolutions?: ConflictResolution[] }
export const rebaseWorkspace = (id: string, input: RebaseAction, signal?: AbortSignal) =>
  jsonRequest<{ workspace: Workspace; commit: WorkspaceCommit | null }>(`${workspacePath(id)}/rebase`, "POST", input, signal);
export const createPullRequest = (input: { workspace_id: string; expected_version: number; expected_head: string; title: string }, signal?: AbortSignal) =>
  jsonRequest<PullRequest>("/catalog/pull-requests", "POST", input, signal);
export const listPullRequests = (input: { status?: PullRequest["status"]; limit?: number; offset?: number } = {}, signal?: AbortSignal) => {
  const query = new URLSearchParams();
  for (const [key, value] of Object.entries(input)) if (value !== undefined) query.set(key, String(value));
  return fetchApi<CollaborationPage<PullRequest>>(`/catalog/pull-requests${query.size ? `?${query}` : ""}`, { signal });
};
export const getPullRequest = (id: string, signal?: AbortSignal) => fetchApi<PullRequest>(pullRequestPath(id), { signal });
export const updatePullRequest = (id: string, input: { expected_version: number; title?: string; status?: "open" | "closed" }, signal?: AbortSignal) =>
  jsonRequest<PullRequest>(pullRequestPath(id), "PATCH", input, signal);
export const mergePullRequest = (id: string, input: { expected_version: number; expected_head: string }, signal?: AbortSignal) =>
  jsonRequest<PushResult>(`${pullRequestPath(id)}/merge`, "POST", input, signal);

export type VersionOperation = "merge" | "cherry_pick" | "revert" | "restore" | "amend" | "squash" | "reset" | "discard";
export interface VersionAction { id: string; expected_version: number; expected_head: string; operation: VersionOperation; commit_ids?: string[]; message?: string; mode?: "soft" | "hard"; resolutions?: ConflictResolution[] }
export interface VersionEvent { id: number; request_id: string; actor_id: string; action: string; old_head: string | null; new_head: string | null; detail: Record<string, unknown>; created_at: string }
export interface VersionTag { id: string; name: string; commit_id: string; message: string; version: number; created_by: string; created_at: string }
export interface VersionStash { id: string; name: string; created_by: string; created_at: string }
export interface VersionSnapshot { commit_id: string; scope: "catalog" | "known_records" | "tracked_records"; objects: WorkspaceObject[]; total: number; limit: number; offset: number }
export interface VersionStatus { version: number; head: string | null; dirty_count: number; pending_commits: number; mergeable: boolean; conflicts: WorkspaceConflict[] }
export const forkWorkspace = (input: { id: string; name: string; commit_id: string;entity_ids?:string[];relation_ids?:string[] }, signal?: AbortSignal) => jsonRequest<Workspace>("/catalog/workspaces/fork", "POST", input, signal);
export const applyWorkspaceVersion = (id: string, input: VersionAction, signal?: AbortSignal) => jsonRequest<{ workspace: Workspace; commit: WorkspaceCommit | null }>(`${workspacePath(id)}/version`, "POST", input, signal);
export const getWorkspaceStatus = (id: string, signal?: AbortSignal) => fetchApi<VersionStatus>(`${workspacePath(id)}/status`, { signal });
export const getCommitSnapshot = (id: string, offset = 0, signal?: AbortSignal) => fetchApi<VersionSnapshot>(`/catalog/commits/${encodeURIComponent(id)}/snapshot?limit=20&offset=${offset}`, { signal });
export const compareCommits = (base: string, head: string, signal?: AbortSignal, offset=0) => fetchApi<{base_id:string;head_id:string;changes:WorkspaceChange[];total:number;limit:number;offset:number}>(`/catalog/commits/${encodeURIComponent(head)}/compare?base=${encodeURIComponent(base)}&offset=${offset}`, { signal });
export const getCatalogHistory=(offset=0,signal?:AbortSignal)=>fetchApi<CollaborationPage<WorkspaceCommit>>(`/catalog/history?limit=20&offset=${offset}`,{signal});
export const getCatalogHead=(signal?:AbortSignal)=>fetchApi<{commit_id:string;sequence:number}>("/catalog/head",{signal});
export const getVersionFieldAuthor = (head: string, target: WorkspaceTarget, recordId: string, pointer: string, signal?: AbortSignal) => fetchApi<WorkspaceCommit>(`/catalog/commits/${encodeURIComponent(head)}/blame?${new URLSearchParams({target,record_id:recordId,pointer})}`, { signal });
export const getWorkspaceEvents = (id: string, offset = 0, signal?: AbortSignal) => fetchApi<CollaborationPage<VersionEvent>>(`${workspacePath(id)}/events?limit=20&offset=${offset}`, { signal });
export const getWorkspaceTags = (id: string, offset = 0, signal?: AbortSignal) => fetchApi<CollaborationPage<VersionTag>>(`${workspacePath(id)}/tags?limit=20&offset=${offset}`, { signal });
export const editWorkspaceTag = (id: string, input: {id:string;expected_version:number;expected_head:string;tag_id?:string;delete?:boolean;name?:string;commit_id?:string;message?:string}, signal?: AbortSignal) => jsonRequest<Workspace>(`${workspacePath(id)}/tags`, "POST", input, signal);
export const getWorkspaceStashes = (id: string, offset = 0, signal?: AbortSignal) => fetchApi<CollaborationPage<VersionStash>>(`${workspacePath(id)}/stashes?limit=20&offset=${offset}`, { signal });
export interface StashAction {id:string;expected_version:number;expected_head:string;operation:"save"|"apply"|"pop"|"drop";stash_id?:string;name?:string;resolutions?:ConflictResolution[]}
export const applyWorkspaceStash = (id: string, input: StashAction, signal?: AbortSignal) => jsonRequest<Workspace>(`${workspacePath(id)}/stashes`, "POST", input, signal);
