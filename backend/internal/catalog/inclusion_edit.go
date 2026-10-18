package catalog

import (
	"context"
	"database/sql"
	"fmt"
)

// The owner Track is the concurrency and revision boundary. No second edge
// table or independently versioned copy of the inclusion is introduced.
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

func applyTrackContentPatch(ctx context.Context, q queryer, old Entity, patch *trackContentPatch, u User) (Entity, error) {
	if old.Kind != "track" {
		return Entity{}, fmt.Errorf("not_track")
	}
	next := old
	next.Contents = append([]Inclusion{}, old.Contents...)
	if patch.action != "delete" {
		reader := &u
		if old.State == "active" {
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
