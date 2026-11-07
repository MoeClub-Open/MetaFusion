package curator

import (
	"encoding/json"
	"time"

	"github.com/metafusion/metafusion-app/internal/assistant"
)

const (
	statusOpen     = "open"
	statusDone     = "done"
	statusFailed   = "failed"
	statusResolved = "resolved"

	severityWarning = "warning"
	severityInfo    = "info"

	kindAgent   = "agent"
	kindSweep   = "sweep"
	kindQuality = "quality"

	triggerSchedule = "schedule"

	maxAttempts = 5
)

const (
	CodeDanglingReference     = "dangling_reference"
	CodeMissingRequiredField  = "missing_required_field"
	CodeReleaseMissingMedia   = "release_missing_media"
	CodeMediumMissingTracks   = "medium_missing_tracks"
	CodeTrackEmptyContents    = "track_empty_contents"
	CodeTrackPositionGap      = "track_position_gap"
	CodeDuplicateTitle        = "duplicate_title"
	CodeWorkMissingForm       = "work_missing_creation_form"
	CodeTVAnimeWithoutCredits = "tv_anime_without_agent"
	CodeReleaseWithoutSubject = "release_without_subject"
	CodeTranslationZHKana     = "translation_zh_kana"
	CodeDuplicateTagValues    = "duplicate_tag_values"
	CodeDuplicatePictureURLs  = "duplicate_picture_values"
	CodeMissingPicture        = "missing_picture_with_links"
)

type Settings struct {
	Enabled                bool       `json:"enabled"`
	ModelID                string     `json:"model_id"`
	IntervalSeconds        int        `json:"interval_seconds"`
	MaxRunsPerHour         int        `json:"max_runs_per_hour"`
	MonthlyCreditAllowance int64      `json:"monthly_credit_allowance"`
	AutoMergeLowRisk       bool       `json:"auto_merge_low_risk"`
	AllowStructures        bool       `json:"allow_structures"`
	AllowSources           bool       `json:"allow_sources"`
	AllowNames             bool       `json:"allow_names"`
	AllowDedupe            bool       `json:"allow_dedupe"`
	SystemUserID           string     `json:"system_user_id"`
	ReviewerUserID         string     `json:"reviewer_user_id"`
	Paused                 bool       `json:"paused"`
	PausedReason           string     `json:"paused_reason"`
	ConsecutiveFailures    int        `json:"consecutive_failures"`
	SweepCursorUpdatedAt   *time.Time `json:"sweep_cursor_updated_at,omitempty"`
	SweepCursorID          string     `json:"sweep_cursor_id,omitempty"`
	SweepDetector          int        `json:"sweep_detector"`
	LastSweepAt            *time.Time `json:"last_sweep_at,omitempty"`
	LastRunAt              *time.Time `json:"last_run_at,omitempty"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type Finding struct {
	ID            string          `json:"id"`
	EntityID      string          `json:"entity_id"`
	EntityKind    string          `json:"entity_kind"`
	EntityTitle   string          `json:"entity_title,omitempty"`
	Code          string          `json:"code"`
	Severity      string          `json:"severity"`
	Priority      int             `json:"priority"`
	Signature     string          `json:"signature"`
	Details       json.RawMessage `json:"details"`
	Status        string          `json:"status"`
	Decision      string          `json:"decision"`
	Reason        string          `json:"reason"`
	Sources       json.RawMessage `json:"sources"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt time.Time       `json:"next_attempt_at"`
	LastError     string          `json:"last_error"`
	LastRunID     string          `json:"last_run_id,omitempty"`
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	PullRequestID string          `json:"pull_request_id,omitempty"`
	Merged        bool            `json:"merged"`
	AutoMerged    bool            `json:"auto_merged"`
	LastSeenAt    time.Time       `json:"last_seen_at"`
	ResolvedAt    *time.Time      `json:"resolved_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Run struct {
	ID            string          `json:"id"`
	FindingID     string          `json:"finding_id,omitempty"`
	EntityID      string          `json:"entity_id,omitempty"`
	Kind          string          `json:"kind"`
	Trigger       string          `json:"trigger"`
	Status        string          `json:"status"`
	ModelID       string          `json:"model_id,omitempty"`
	Decision      string          `json:"decision"`
	Reason        string          `json:"reason"`
	Sources       json.RawMessage `json:"sources"`
	ToolTrace     json.RawMessage `json:"tool_trace"`
	Detail        json.RawMessage `json:"detail"`
	WorkspaceID   string          `json:"workspace_id,omitempty"`
	PullRequestID string          `json:"pull_request_id,omitempty"`
	Merged        bool            `json:"merged"`
	AutoMerged    bool            `json:"auto_merged"`
	Error         string          `json:"error"`
	InputTokens   int64           `json:"input_tokens"`
	OutputTokens  int64           `json:"output_tokens"`
	Credits       int64           `json:"credits"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type FindingFilter struct {
	Status string
	Code   string
	Kind   string
}

type RunFilter struct {
	Kind string
}

type Page[T any] struct {
	Items  []T   `json:"items"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

type Summary struct {
	Settings         Settings          `json:"settings"`
	AssistantEnabled bool              `json:"assistant_enabled"`
	Model            *assistant.Model  `json:"model,omitempty"`
	Balance          assistant.Balance `json:"balance"`
	SpentCredits     int64             `json:"spent_credits"`
	RemainingCredits int64             `json:"remaining_credits"`
	Counts           map[string]int64  `json:"counts"`
	Runs24h          int64             `json:"runs_24h"`
	LeaseHeld        bool              `json:"lease_held"`
}

type candidate struct {
	EntityID  string
	Kind      string
	Code      string
	Severity  string
	Priority  int
	Signature string
	Details   map[string]any
}

type decision struct {
	Decision   string   `json:"decision"`
	Reason     string   `json:"reason"`
	Sources    []string `json:"sources"`
	Confidence string   `json:"confidence"`
}
