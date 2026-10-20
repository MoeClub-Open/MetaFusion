import type { ConflictResolution, WorkspaceConflict } from "./api/collaboration";

export interface WorkspaceResolutionChoice {
  choice: "current" | "proposed" | "custom" | "remove";
  custom: string;
}

export function workspaceConflictResolution(conflict: WorkspaceConflict, choice: WorkspaceResolutionChoice): ConflictResolution {
  const target = { ...(conflict.source_commit_id ? { commit_id: conflict.source_commit_id } : {}), target: conflict.target, id: conflict.id, pointer: conflict.pointer, current_version: conflict.current_version };
  if (choice.choice === "remove"
    || choice.choice === "current" && !conflict.current_present
    || choice.choice === "proposed" && !conflict.proposed_present) return { ...target, remove: true };
  return { ...target, remove: false, value: choice.choice === "custom" ? (typeof conflict.proposed === "string" ? choice.custom : JSON.parse(choice.custom))
    : choice.choice === "current" ? conflict.current : conflict.proposed };
}
