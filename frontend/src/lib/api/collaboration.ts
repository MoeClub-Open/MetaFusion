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
  id: string;
  owner_id: string;
  name: string;
  version: number;
  head_commit_id: string | null;
  state: "active" | "archived";
  members: string[];
  objects: WorkspaceObject[];
  dirty_count: number;
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
  author: { id: string; name: string };
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
export const createWorkspace = (input: { name?: string; entity_ids?: string[]; relation_ids?: string[] } = {}, signal?: AbortSignal) =>
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
export const listWorkspaceCommits = (id: string, signal?: AbortSignal) => fetchApi<{ items: WorkspaceCommit[] }>(`${workspacePath(id)}/commits`, { signal });
export const getWorkspaceCommit = (id: string, signal?: AbortSignal) => fetchApi<WorkspaceCommit>(`/catalog/commits/${encodeURIComponent(id)}`, { signal });
export const createWorkspaceCommit = (id: string, input: { id: string; expected_version: number; message: string }, signal?: AbortSignal) =>
  jsonRequest<WorkspaceCommit>(`${workspacePath(id)}/commits`, "POST", input, signal);
export const pushWorkspace = (id: string, input: { expected_version: number; expected_head: string; title?: string }, signal?: AbortSignal) =>
  jsonRequest<PushResult>(`${workspacePath(id)}/push`, "POST", input, signal);
export const rebaseWorkspace = (id: string, input: { expected_version: number; expected_head: string; id: string; message?: string; resolutions?: ConflictResolution[] }, signal?: AbortSignal) =>
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
