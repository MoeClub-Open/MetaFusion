package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Preview freezes the proposed facts for review; it is never a materialized head.
type CommitPreview struct {
	Target string `json:"target"`
	Action string `json:"action"`
	ID     string `json:"id"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

type commitAuthor struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Permissions []string `json:"permissions"`
}
type commitReviewContextKey struct{}
type commitReviewApproval struct {
	Reviewer User
	Note     string
}

type CommitSubmission struct {
	Request    CatalogCommit `json:"request"`
	Receipt    CommitReceipt `json:"receipt"`
	AuthorID   string        `json:"author_id"`
	AuthorName string        `json:"author_name"`
	CreatedAt  time.Time     `json:"created_at"`
}
type CommitReviewEdit struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

func (s *Store) CommitSubmission(ctx context.Context, id string, u User) (CommitSubmission, error) {
	var out CommitSubmission
	if _, err := uuid.Parse(id); err != nil {
		return out, fmt.Errorf("invalid_commit_id")
	}
	var request, result, author []byte
	err := s.DB.QueryRowContext(ctx, `SELECT request,result,actor_id,author_snapshot,created_at FROM catalog.commits WHERE id=$1 AND (actor_id=$2 OR $3::boolean)`, id, u.ID, u.Can(PermissionReviewManage)).Scan(&request, &result, &out.AuthorID, &author, &out.CreatedAt)
	if err != nil {
		return out, err
	}
	var a commitAuthor
	if err = json.Unmarshal(author, &a); err != nil {
		return out, err
	}
	out.AuthorName = a.Username
	if err = json.Unmarshal(request, &out.Request); err != nil {
		return out, err
	}
	err = json.Unmarshal(result, &out.Receipt)
	return out, err
}

// Without review permission this is always the caller's own submission history.
func (s *Store) CommitSubmissions(ctx context.Context, status string, limit, offset int, u User) ([]CommitSubmission, error) {
	if status != "" && status != "pending_review" && status != "reviewed" {
		return nil, fmt.Errorf("invalid_review_status")
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT request,result,actor_id,author_snapshot->>'username',created_at FROM catalog.commits WHERE (actor_id=$1 OR $2::boolean) AND ($3='' OR review_status=$3) ORDER BY created_at DESC,id DESC LIMIT $4 OFFSET $5`, u.ID, u.Can(PermissionReviewManage), status, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommitSubmission{}
	for rows.Next() {
		var v CommitSubmission
		var request, result []byte
		var name sql.NullString
		if err = rows.Scan(&request, &result, &v.AuthorID, &name, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.AuthorName = name.String
		if err = json.Unmarshal(request, &v.Request); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(result, &v.Receipt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ReviewCommit(ctx context.Context, id string, in CommitReviewEdit, reviewer User) (CommitReceipt, error) {
	if !reviewer.Can(PermissionReviewManage) {
		return CommitReceipt{}, errForbidden
	}
	if _, err := uuid.Parse(id); err != nil {
		return CommitReceipt{}, fmt.Errorf("invalid_commit_id")
	}
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > 2000 || (in.Decision != "approve" && in.Decision != "reject") {
		return CommitReceipt{}, fmt.Errorf("invalid_review")
	}
	if in.Decision == "approve" {
		var request, author, result []byte
		if err := s.DB.QueryRowContext(ctx, `SELECT request,author_snapshot,result FROM catalog.commits WHERE id=$1`, id).Scan(&request, &author, &result); err != nil {
			return CommitReceipt{}, err
		}
		var prior CommitReceipt
		if err := json.Unmarshal(result, &prior); err != nil {
			return CommitReceipt{}, err
		}
		if prior.ReviewStatus != "pending_review" {
			return prior, nil
		}
		var commit CatalogCommit
		var a commitAuthor
		if err := json.Unmarshal(request, &commit); err != nil {
			return CommitReceipt{}, err
		}
		if err := json.Unmarshal(author, &a); err != nil {
			return CommitReceipt{}, err
		}
		if a.ID == "" {
			return s.CommitReceipt(ctx, id, reviewer)
		} // legacy receipt, already applied
		ctx = context.WithValue(ctx, commitReviewContextKey{}, commitReviewApproval{reviewer, in.Note})
		return s.PushCommit(ctx, commit, User{ID: a.ID, Username: a.Username, Permissions: a.Permissions}, false)
	}
	var out CommitReceipt
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		var raw, author []byte
		if err := tx.QueryRowContext(ctx, `SELECT result,author_snapshot FROM catalog.commits WHERE id=$1 FOR UPDATE`, id).Scan(&raw, &author); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return err
		}
		if out.ReviewStatus != "pending_review" {
			return nil
		}
		var a commitAuthor
		if err := json.Unmarshal(author, &a); err != nil {
			return err
		}
		now := time.Now().UTC()
		out.ReviewStatus, out.ReviewDecision, out.ReviewedBy, out.ReviewedAt, out.ReviewNote = "reviewed", "rejected", reviewer.ID, &now, in.Note
		if _, err := tx.ExecContext(ctx, `UPDATE catalog.commits SET result=$2,review_status='reviewed',review_decision='rejected',reviewed_by=$3,reviewed_at=$4,review_note=$5 WHERE id=$1`, id, encode(out), reviewer.ID, now, in.Note); err != nil {
			return err
		}
		return notifyCommitReview(ctx, tx, id, User{ID: a.ID}, reviewer, false, in.Note)
	})
	return out, err
}

func notifyCommitReview(ctx context.Context, tx *sql.Tx, id string, author, reviewer User, approved bool, note string) error {
	if author.ID == reviewer.ID {
		return nil
	}
	typ := NotificationReviewRejected
	if approved {
		typ = NotificationReviewApproved
	}
	return notify(ctx, tx, NotificationInput{RecipientID: author.ID, Type: typ, ActorID: reviewer.ID, ActorName: reviewer.Username, SubjectType: "commit", SubjectID: id, Payload: map[string]any{"commit_id": id, "review_note": note}, DedupeKey: typ + ":" + id})
}

func commitPreviewEntity(ctx context.Context, q queryer, id string, u User) (Entity, error) {
	e, err := get(ctx, q, id)
	if err != nil {
		return e, err
	}
	return visibleEntityContents(ctx, q, e, &u)
}

func frozenCommitPreview(v any) any {
	if v == nil {
		return nil
	}
	var out any
	_ = json.Unmarshal([]byte(encode(v)), &out)
	return out
}
