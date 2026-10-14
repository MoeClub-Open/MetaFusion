package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TrackStatusEdit changes no content or ownership fields. Full Track facts are
// read inside the write transaction, never reconstructed from a public view.
type TrackStatusEdit struct {
	Status          string   `json:"status"`
	ExpectedVersion int64    `json:"expected_version"`
	EditNote        string   `json:"edit_note"`
	Sources         []Source `json:"sources"`
}

func validateTrackStatusEdit(in TrackStatusEdit) error {
	if in.ExpectedVersion < 1 {
		return fmt.Errorf("invalid_payload")
	}
	if !contains([]string{"draft", "pending_review", "published"}, in.Status) {
		return errInvalidStatus
	}
	return validateSources(in.EditNote, in.Sources)
}

func (s *Store) EditTrackStatus(ctx context.Context, id string, in TrackStatusEdit, u User) (Entity, error) {
	if err := validateTrackStatusEdit(in); err != nil {
		return Entity{}, err
	}
	if _, err := uuid.Parse(id); err != nil {
		return Entity{}, fmt.Errorf("invalid_id")
	}
	var next Entity
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		if err := lockDefinitionsShared(ctx, tx); err != nil {
			return err
		}
		old, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		if !visible(old, &u) || old.Status == "deleted" || old.Status == "merged" {
			return errForbidden
		}
		if old.Version != in.ExpectedVersion {
			return errVersionConflict
		}
		if old.Kind != "track" {
			return fmt.Errorf("not_track")
		}
		next = old
		next.Status = in.Status
		if err = prepareEntityWriteStatus(old, &next, u); err != nil {
			return err
		}
		if err = lockReleaseScope(ctx, tx, next); err != nil {
			return err
		}
		if next.Status == "published" {
			ref, err := entityWriteReference(ctx, tx, next, u)
			if err != nil {
				return err
			}
			v, err := definitions(ctx, tx)
			if err != nil {
				return err
			}
			medium, err := get(ctx, tx, next.MediumID)
			if err != nil || medium.Kind != "medium" {
				return fmt.Errorf("invalid_reference")
			}
			format, _ := medium.Attributes["format"].(string)
			if err = v.Document.validateEntity(next, ref, true, format); err != nil {
				return err
			}
			if err = validateEntityStructuralReferences(next, ref); err != nil {
				return err
			}
			if err = v.Document.validateExternalLinks(next); err != nil {
				return err
			}
			if err = validateExternalLinksAgainstDB(ctx, tx, next); err != nil {
				return err
			}
			missing, err := undeclaredReleaseSubject(ctx, tx, next)
			if err != nil {
				return err
			}
			if missing {
				return fmt.Errorf("undeclared_release_subject")
			}
		}
		next.Version++
		next.UpdatedAt = time.Now().UTC()
		// Merge only these three JSON keys. Existing document keys and every
		// structural row (including hidden inclusions and their evidence) stay
		// byte-for-byte unchanged, with no DELETE/INSERT side-table churn.
		changes := map[string]any{"status": next.Status, "version": next.Version, "updated_at": next.UpdatedAt}
		var document []byte
		err = tx.QueryRowContext(ctx, `UPDATE catalog.entities
			SET version=$3,status=$4,document=document || $5::jsonb,updated_at=$6
			WHERE id=$1 AND version=$2 AND kind='track' RETURNING document`,
			next.ID, in.ExpectedVersion, next.Version, next.Status, encode(changes), next.UpdatedAt).Scan(&document)
		if errors.Is(err, sql.ErrNoRows) {
			return errVersionConflict
		}
		if err != nil {
			return err
		}
		// The revision/outbox carries the full current document plus structural
		// facts. Keep opaque document keys too, rather than silently discarding
		// them while changing an unrelated status.
		var snapshot, facts map[string]json.RawMessage
		if err = json.Unmarshal(document, &snapshot); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(encode(next)), &facts); err != nil {
			return err
		}
		for key, value := range facts {
			snapshot[key] = value
		}
		if err = audit(ctx, tx, next.ID, next.Version, u, in.EditNote, in.Sources, snapshot, "entity.saved"); err != nil {
			return err
		}
		if err = notifySaveOutcome(ctx, tx, &old, next, u); err != nil {
			return err
		}
		// A response projection is not a source of truth for this write. Any
		// filtering failure rolls back the status, revision and outbox together.
		next, err = visibleEntityContents(ctx, tx, next, &u)
		return err
	})
	return next, err
}
