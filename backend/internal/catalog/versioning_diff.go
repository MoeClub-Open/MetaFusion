package catalog

import (
	"context"
	"database/sql"
	"fmt"
)

type RecordChangePage struct {
	Changes []CommitObject `json:"changes"`
	Total   int64          `json:"total"`
	Limit   int            `json:"limit"`
	Offset  int            `json:"offset"`
}

type WorkspaceDiff struct {
	RecordChangePage
	WorkspaceID string  `json:"workspace_id"`
	Version     int64   `json:"version"`
	HeadID      *string `json:"head_id"`
	Basis       string  `json:"basis"`
}

type CommitDiff struct {
	RecordChangePage
	CommitID string  `json:"commit_id"`
	ParentID *string `json:"parent_id"`
}

type PullRequestDiff struct {
	RecordChangePage
	PullRequestID string  `json:"pull_request_id"`
	Version       int64   `json:"version"`
	SourceHeadID  string  `json:"source_head_id"`
	SourceBaseID  *string `json:"source_base_id"`
}

type PullRequestCommitPage struct {
	CommitPage
	PullRequestID string  `json:"pull_request_id"`
	Version       int64   `json:"version"`
	SourceHeadID  string  `json:"source_head_id"`
	SourceBaseID  *string `json:"source_base_id"`
}

func recordChangePage(changes []CommitObject, q VersionReadQuery) RecordChangePage {
	items := []CommitObject{}
	for _, co := range changes {
		if q.matches(co.Target, co.ID) {
			items = append(items, co)
		}
	}
	out := RecordChangePage{Changes: []CommitObject{}, Total: int64(len(items)), Limit: q.Limit, Offset: q.Offset}
	if q.Offset < len(items) {
		out.Changes = items[q.Offset : q.Offset+min(len(items)-q.Offset, q.Limit)]
	}
	return out
}

func (s *Store) WorkspaceDiff(ctx context.Context, id, basis string, q VersionReadQuery, u User) (WorkspaceDiff, error) {
	out := WorkspaceDiff{}
	q, err := q.normalized()
	if err != nil {
		return out, err
	}
	if basis == "" {
		basis = "head"
	}
	if !contains([]string{"head", "base"}, basis) {
		return out, fmt.Errorf("invalid_diff_basis")
	}
	w, err := s.GetWorkspace(ctx, id, u)
	if err != nil {
		return out, err
	}
	changes := []CommitObject{}
	for _, o := range w.Objects {
		before := o.Committed
		if basis == "base" {
			before = o.Base
		}
		if !workspaceSameFact(before, o.Document, o.Target) {
			changes = append(changes, CommitObject{Target: o.Target, ID: o.ID, BaseVersion: versionDocumentNumber(before, o.BaseVersion), Base: o.Base, Before: before, After: o.Document})
		}
	}
	return WorkspaceDiff{RecordChangePage: recordChangePage(changes, q), WorkspaceID: w.ID, Version: w.Version, HeadID: w.HeadCommitID, Basis: basis}, nil
}

func (s *Store) CommitDiff(ctx context.Context, id string, q VersionReadQuery, u User) (CommitDiff, error) {
	q, err := q.normalized()
	if err != nil {
		return CommitDiff{}, err
	}
	c, err := s.GetCollaborationCommit(ctx, id, u)
	if err != nil {
		return CommitDiff{}, err
	}
	return CommitDiff{RecordChangePage: recordChangePage(c.Changes, q), CommitID: c.ID, ParentID: c.ParentID}, nil
}

func (s *Store) PullRequestDiff(ctx context.Context, id string, q VersionReadQuery, u User) (PullRequestDiff, error) {
	q, err := q.normalized()
	if err != nil {
		return PullRequestDiff{}, err
	}
	p, err := s.GetPullRequest(ctx, id, u)
	if err != nil {
		return PullRequestDiff{}, err
	}
	changes := []CommitObject{}
	for _, o := range p.Changes {
		changes = append(changes, CommitObject{Target: o.Target, ID: o.ID, BaseVersion: o.BaseVersion, Base: o.Base, Before: o.Base, After: o.Document})
	}
	return PullRequestDiff{RecordChangePage: recordChangePage(changes, q), PullRequestID: p.ID, Version: p.Version, SourceHeadID: p.SourceHeadID, SourceBaseID: p.SourceBaseID}, nil
}

func (s *Store) PullRequestCommitHistory(ctx context.Context, id string, q VersionHistoryQuery, u User) (PullRequestCommitPage, error) {
	q, err := q.normalized(false)
	if err != nil {
		return PullRequestCommitPage{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return PullRequestCommitPage{}, err
	}
	defer tx.Rollback()
	p, err := pullRequestFrom(ctx, tx, id, u, false)
	if err != nil {
		return PullRequestCommitPage{}, err
	}
	out := PullRequestCommitPage{CommitPage: CommitPage{Items: []CollaborationCommit{}, Limit: q.Limit, Offset: q.Offset}, PullRequestID: p.ID, Version: p.Version, SourceHeadID: p.SourceHeadID, SourceBaseID: p.SourceBaseID}
	args := []any{id}
	where := ` FROM catalog.pull_request_commits pc JOIN catalog.commits c ON c.id=pc.commit_id WHERE pc.pull_request_id=$1` + q.sqlFilters(&args, "c.created_at")
	if err = tx.QueryRowContext(ctx, `SELECT count(*)`+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	args = append(args, q.Limit, q.Offset)
	ids, err := collaborationIDs(ctx, tx, `SELECT c.id::text`+where+fmt.Sprintf(` ORDER BY pc.position DESC LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		c, err := collaborationCommitFrom(ctx, tx, id)
		if err != nil {
			return out, err
		}
		if err = visibleVersionLinks(ctx, tx, &c, u); err != nil {
			return out, err
		}
		out.Items = append(out.Items, c)
	}
	return out, tx.Commit()
}
