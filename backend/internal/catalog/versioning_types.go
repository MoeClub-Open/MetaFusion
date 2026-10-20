package catalog

import (
	"encoding/json"
	"time"
)

type RevisionQuery struct {
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Version  int64  `json:"version,omitempty"`
	AuthorID string `json:"author_id,omitempty"`
	Since    string `json:"since,omitempty"`
	Until    string `json:"until,omitempty"`
}

type RevisionPage struct {
	Items  []map[string]any `json:"items"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

type WorkspaceFork struct {
	EntityIDs   []string `json:"entity_ids,omitempty"`
	RelationIDs []string `json:"relation_ids,omitempty"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	CommitID    string   `json:"commit_id"`
}

// A version action always names an immutable request ID and the branch head
// that was reviewed. No version action writes catalog facts.
type WorkspaceVersionAction struct {
	ID              string                `json:"id"`
	ExpectedVersion int64                 `json:"expected_version"`
	ExpectedHead    string                `json:"expected_head"`
	Operation       string                `json:"operation"`
	CommitIDs       []string              `json:"commit_ids,omitempty"`
	Message         string                `json:"message,omitempty"`
	Mode            string                `json:"mode,omitempty"`
	Resolutions     []WorkspaceResolution `json:"resolutions,omitempty"`
}

type WorkspaceVersionResult struct {
	Workspace Workspace            `json:"workspace"`
	Commit    *CollaborationCommit `json:"commit"`
}

type WorkspaceStatus struct {
	Version        int64               `json:"version"`
	Head           *string             `json:"head"`
	DirtyCount     int                 `json:"dirty_count"`
	PendingCommits int64               `json:"pending_commits"`
	Mergeable      bool                `json:"mergeable"`
	Conflicts      []WorkspaceConflict `json:"conflicts"`
}

type CommitSnapshot struct {
	CommitID string            `json:"commit_id"`
	Scope    string            `json:"scope"`
	Objects  []WorkspaceObject `json:"objects"`
	Total    int64             `json:"total"`
	Limit    int               `json:"limit"`
	Offset   int               `json:"offset"`
}

type CommitPage struct {
	Items  []CollaborationCommit `json:"items"`
	Total  int64                 `json:"total"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

type VersionComparison struct {
	Total   int64          `json:"total"`
	Limit   int            `json:"limit"`
	Offset  int            `json:"offset"`
	BaseID  string         `json:"base_id"`
	HeadID  string         `json:"head_id"`
	Changes []CommitObject `json:"changes"`
}

type WorkspaceEvent struct {
	ID        int64           `json:"id"`
	RequestID string          `json:"request_id"`
	ActorID   string          `json:"actor_id"`
	Action    string          `json:"action"`
	OldHead   *string         `json:"old_head"`
	NewHead   *string         `json:"new_head"`
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"created_at"`
}

type WorkspaceEventPage struct {
	Items  []WorkspaceEvent `json:"items"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

type WorkspaceTag struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CommitID  string    `json:"commit_id"`
	Message   string    `json:"message"`
	Version   int64     `json:"version"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkspaceTagEdit struct {
	ID              string `json:"id"`
	TagID           string `json:"tag_id,omitempty"`
	Delete          bool   `json:"delete,omitempty"`
	ExpectedVersion int64  `json:"expected_version"`
	ExpectedHead    string `json:"expected_head"`
	Name            string `json:"name"`
	CommitID        string `json:"commit_id"`
	Message         string `json:"message"`
}

type WorkspaceTagPage struct {
	Items  []WorkspaceTag `json:"items"`
	Total  int64          `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}
type WorkspaceStashPage struct {
	Items  []WorkspaceStash `json:"items"`
	Total  int64            `json:"total"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

type WorkspaceStash struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type WorkspaceStashAction struct {
	ID              string                `json:"id"`
	ExpectedVersion int64                 `json:"expected_version"`
	ExpectedHead    string                `json:"expected_head"`
	Operation       string                `json:"operation"`
	StashID         string                `json:"stash_id,omitempty"`
	Name            string                `json:"name,omitempty"`
	Resolutions     []WorkspaceResolution `json:"resolutions,omitempty"`
}
