package catalog

import (
	"encoding/json"
	"time"
)

type WorkspaceObject struct {
	Target       string            `json:"target"`
	ID           string            `json:"id"`
	BaseVersion  int64             `json:"base_version"`
	Base         json.RawMessage   `json:"base"`
	Committed    json.RawMessage   `json:"committed"`
	Document     json.RawMessage   `json:"document"`
	Dirty        bool              `json:"dirty"`
	Contributors map[string]string `json:"contributors,omitempty"`
}
type Workspace struct {
	BaseCommitID   *string           `json:"base_commit_id"`
	ID             string            `json:"id"`
	OwnerID        string            `json:"owner_id"`
	Name           string            `json:"name"`
	Version        int64             `json:"version"`
	HeadCommitID   *string           `json:"head_commit_id"`
	State          string            `json:"state"`
	Members        []string          `json:"members"`
	Objects        []WorkspaceObject `json:"objects"`
	DirtyCount     int               `json:"dirty_count"`
	PendingCommits int64             `json:"pending_commits"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}
type WorkspacePage struct {
	Items  []Workspace `json:"items"`
	Total  int64       `json:"total"`
	Limit  int         `json:"limit"`
	Offset int         `json:"offset"`
}
type WorkspaceCreate struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	EntityIDs   []string `json:"entity_ids"`
	RelationIDs []string `json:"relation_ids"`
}
type WorkspaceEdit struct {
	ExpectedVersion int64   `json:"expected_version"`
	Name            *string `json:"name,omitempty"`
	State           *string `json:"state,omitempty"`
}
type WorkspaceCheckout struct {
	ExpectedVersion int64    `json:"expected_version"`
	EntityIDs       []string `json:"entity_ids"`
	RelationIDs     []string `json:"relation_ids"`
}
type WorkspaceOperation struct {
	Target        string               `json:"target"`
	Action        string               `json:"action"`
	ID            string               `json:"id,omitempty"`
	Ref           string               `json:"ref,omitempty"`
	Document      json.RawMessage      `json:"document,omitempty"`
	Patch         []CommitPatch        `json:"patch,omitempty"`
	ContentChange *CommitContentChange `json:"content_change,omitempty"`
}
type WorkspaceChanges struct {
	ExpectedVersion int64                `json:"expected_version"`
	Operations      []WorkspaceOperation `json:"operations"`
}
type WorkspaceChangesResult struct {
	Workspace Workspace         `json:"workspace"`
	Refs      map[string]string `json:"refs"`
}
type WorkspaceMembersEdit struct {
	ExpectedVersion int64    `json:"expected_version"`
	UserIDs         []string `json:"user_ids"`
}
type WorkspaceCommitCreate struct {
	ID              string `json:"id"`
	ExpectedVersion int64  `json:"expected_version"`
	Message         string `json:"message"`
}
type CollaborationAuthor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type CommitObject struct {
	Target      string          `json:"target"`
	ID          string          `json:"id"`
	BaseVersion int64           `json:"base_version"`
	Base        json.RawMessage `json:"base"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
}
type CollaborationCommit struct {
	ID            string                `json:"id"`
	WorkspaceID   *string               `json:"workspace_id"`
	ParentID      *string               `json:"parent_id"`
	MergeParentID *string               `json:"merge_parent_id"`
	Operation     string                `json:"operation"`
	SourceIDs     []string              `json:"source_ids"`
	ReplacedBy    []string              `json:"replaced_by"`
	SnapshotScope string                `json:"snapshot_scope"`
	Author        CollaborationAuthor   `json:"author"`
	Committer     CollaborationAuthor   `json:"committer"`
	Authors       []CollaborationAuthor `json:"authors"`
	Message       string                `json:"message"`
	Kind          string                `json:"kind"`
	CreatedAt     time.Time             `json:"created_at"`
	MergedAt      *time.Time            `json:"merged_at"`
	MergedBy      *string               `json:"merged_by"`
	PullRequestID *string               `json:"pull_request_id"`
	Changes       []CommitObject        `json:"changes"`
}
type WorkspacePush struct {
	ExpectedVersion int64  `json:"expected_version"`
	ExpectedHead    string `json:"expected_head"`
	Title           string `json:"title,omitempty"`
}
type PullRequest struct {
	ID           string                `json:"id"`
	WorkspaceID  string                `json:"workspace_id"`
	AuthorID     string                `json:"author_id"`
	Title        string                `json:"title"`
	Status       string                `json:"status"`
	SourceHeadID string                `json:"source_head_id"`
	SourceBaseID *string               `json:"source_base_id"`
	Version      int64                 `json:"version"`
	MergedAt     *time.Time            `json:"merged_at"`
	MergedBy     *string               `json:"merged_by"`
	CreatedAt    time.Time             `json:"created_at"`
	UpdatedAt    time.Time             `json:"updated_at"`
	Commits      []CollaborationCommit `json:"commits"`
	Changes      []WorkspaceObject     `json:"changes"`
}
type PullRequestPage struct {
	Items  []PullRequest `json:"items"`
	Total  int64         `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}
type PullRequestCreate struct {
	WorkspaceID string `json:"workspace_id"`
	WorkspacePush
}
type PullRequestEdit struct {
	ExpectedVersion int64   `json:"expected_version"`
	Title           *string `json:"title,omitempty"`
	Status          *string `json:"status,omitempty"`
}
type PullRequestMerge struct {
	ExpectedVersion int64  `json:"expected_version"`
	ExpectedHead    string `json:"expected_head"`
}
type WorkspacePushResult struct {
	Applied     bool                  `json:"applied"`
	Workspace   *Workspace            `json:"workspace"`
	Commits     []CollaborationCommit `json:"commits"`
	PullRequest *PullRequest          `json:"pull_request"`
}
type WorkspaceConflict struct {
	SourceCommitID  string `json:"source_commit_id,omitempty"`
	Target          string `json:"target"`
	ID              string `json:"id"`
	BaseVersion     int64  `json:"base_version"`
	CurrentVersion  int64  `json:"current_version"`
	Pointer         string `json:"pointer"`
	Base            any    `json:"base"`
	Current         any    `json:"current"`
	Proposed        any    `json:"proposed"`
	BasePresent     bool   `json:"base_present"`
	CurrentPresent  bool   `json:"current_present"`
	ProposedPresent bool   `json:"proposed_present"`
	Remove          bool   `json:"remove,omitempty"`
	Kind            string `json:"kind"`
}
type workspaceConflictError struct{ Conflicts []WorkspaceConflict }

func (e *workspaceConflictError) Error() string { return "workspace_conflict" }

type WorkspaceResolution struct {
	CommitID       string          `json:"commit_id,omitempty"`
	Target         string          `json:"target"`
	ID             string          `json:"id"`
	Pointer        string          `json:"pointer"`
	CurrentVersion int64           `json:"current_version"`
	Value          json.RawMessage `json:"value,omitempty"`
	Remove         bool            `json:"remove,omitempty"`
}
type WorkspaceRebase struct {
	OntoCommitID    string                `json:"onto_commit_id,omitempty"`
	Order           *[]string             `json:"order,omitempty"`
	ExpectedVersion int64                 `json:"expected_version"`
	ExpectedHead    string                `json:"expected_head"`
	ID              string                `json:"id"`
	Message         string                `json:"message,omitempty"`
	Resolutions     []WorkspaceResolution `json:"resolutions,omitempty"`
}
type WorkspaceRebaseResult struct {
	Workspace Workspace            `json:"workspace"`
	Commit    *CollaborationCommit `json:"commit"`
}
