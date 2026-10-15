package catalog

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// The owner Track is the concurrency and revision boundary. No second edge
// table or independently versioned copy of the inclusion is introduced.
type TrackContentEdit struct {
	Inclusion       *Inclusion `json:"inclusion,omitempty"`
	ExpectedVersion int64      `json:"expected_version"`
	EditNote        string     `json:"edit_note"`
	Sources         []Source   `json:"sources"`
}

type trackContentPatch struct {
	action    string
	position  int
	inclusion *Inclusion
}

func inclusionSources(sources []Source) []Source {
	if sources == nil {
		return []Source{}
	}
	return sources
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

func applyTrackContentPatch(ctx context.Context, q queryer, old Entity, patch *trackContentPatch, u User) (Entity, error) {
	if old.Kind != "track" {
		return Entity{}, fmt.Errorf("not_track")
	}
	next := old
	next.Contents = append([]Inclusion{}, old.Contents...)
	if patch.action != "delete" {
		reader := &u
		if old.Status == "active" {
			reader = nil
		}
		if err := reference(ctx, q, reader)(patch.inclusion.ExpressionID, []string{"expression"}); err != nil {
			return Entity{}, err
		}
	}
	if patch.action == "add" {
		next.Contents = append(next.Contents, *patch.inclusion)
		return next, nil
	}
	for i, inclusion := range next.Contents {
		if inclusion.Position != patch.position {
			continue
		}
		// An editor cannot guess a hidden record's position to remove it.
		if err := reference(ctx, q, &u)(inclusion.ExpressionID, []string{"expression"}); err != nil {
			return Entity{}, errForbidden
		}
		if patch.action == "replace" {
			next.Contents[i] = *patch.inclusion
		} else {
			next.Contents = append(next.Contents[:i], next.Contents[i+1:]...)
		}
		return next, nil
	}
	return Entity{}, sql.ErrNoRows
}

// Each inclusion carries explicit evidence or inherits this edit's evidence.
func assignInclusionSources(next *Entity, sources []Source) {
	if next.Kind != "track" {
		return
	}
	for i, c := range next.Contents {
		if c.Sources != nil {
			continue
		}
		next.Contents[i].Sources = append([]Source{}, sources...)
	}
}
