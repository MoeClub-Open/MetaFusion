package catalog

// Test fixtures bootstrap authoritative records through the same materialization
// functions as workspace merge. No direct single-record writer is compiled into
// the application.
import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

func (s *Store) Save(ctx context.Context, input Edit, u User) (Entity, error) {
	if !u.CanBypassReview() {
		return Entity{}, fmt.Errorf("commit_required")
	}
	var e Entity
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		var err error
		e, err = s.saveEntityTx(ctx, tx, input, u)
		return err
	})
	return e, err
}

func (s *Store) SaveRelation(ctx context.Context, input RelationEdit, u User) (Relation, error) {
	if !u.CanBypassReview() {
		return Relation{}, fmt.Errorf("commit_required")
	}
	var r Relation
	err := s.writeCatalog(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = s.saveRelationTx(ctx, tx, input, u)
		return err
	})
	return r, err
}

func (s *Store) DeleteRelation(ctx context.Context, id string, expected int64, note string, sources []Source, u User) error {
	if !u.CanBypassReview() {
		return fmt.Errorf("commit_required")
	}
	return s.writeCatalog(ctx, func(tx *sql.Tx) error { return s.deleteRelationTx(ctx, tx, id, expected, note, sources, u) })
}

type TrackContentEdit struct {
	Inclusion       *Inclusion `json:"inclusion,omitempty"`
	ExpectedVersion int64      `json:"expected_version"`
	EditNote        string     `json:"edit_note"`
	Sources         []Source   `json:"sources"`
}

func (s *Store) EditTrackContent(ctx context.Context, id, action string, position int, in TrackContentEdit, u User) (Entity, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Entity{}, fmt.Errorf("invalid_id")
	}
	if !contains([]string{"add", "replace", "delete"}, action) || action != "add" && position < 0 ||
		action != "delete" && in.Inclusion == nil {
		return Entity{}, fmt.Errorf("invalid_payload")
	}
	e, err := s.Save(ctx, Edit{
		Entity: Entity{ID: id, Kind: "track"}, ExpectedVersion: in.ExpectedVersion, EditNote: in.EditNote, Sources: in.Sources,
		contentPatch: &trackContentPatch{action: action, position: position, inclusion: in.Inclusion},
	}, u)
	if err != nil {
		return Entity{}, err
	}
	return visibleEntityContents(ctx, s.DB, e, &u)
}
