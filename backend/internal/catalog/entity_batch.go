package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const entityBatchMax = 100

type EntityBatchRequest struct {
	IDs []string `json:"ids"`
}

type EntityBatchResponse struct {
	Items   map[string]Entity       `json:"items"`
	Covers  map[string]DisplayCover `json:"covers,omitempty"`
	Missing []string                `json:"missing"`
}

func normalizeEntityBatchIDs(raw []string, requireNonEmpty bool) ([]string, error) {
	if requireNonEmpty && len(raw) == 0 {
		return nil, fmt.Errorf("ids_required")
	}
	if len(raw) > entityBatchMax {
		return nil, fmt.Errorf("too_many_ids")
	}
	ids := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		parsed, err := uuid.Parse(strings.TrimSpace(item))
		if err != nil {
			return nil, fmt.Errorf("invalid_id")
		}
		key := parsed.String()
		if !seen[key] {
			seen[key] = true
			ids = append(ids, key)
		}
	}
	return ids, nil
}

func parseEntityQueryIDs(c *gin.Context) ([]string, error) {
	tokens := []string{}
	for _, raw := range c.QueryArray("ids") {
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				tokens = append(tokens, trimmed)
			}
		}
	}
	if len(tokens) == 0 {
		return nil, nil
	}
	return normalizeEntityBatchIDs(tokens, false)
}

// EntityBatch reads 1-100 visible entities with full structural fields, visible
// track contents and display covers in one repeatable-read snapshot.
func (s *Store) EntityBatch(ctx context.Context, in EntityBatchRequest, u *User) (EntityBatchResponse, error) {
	out := EntityBatchResponse{Items: map[string]Entity{}, Missing: []string{}}
	ids, err := normalizeEntityBatchIDs(in.IDs, true)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	got, err := getManyFrom(ctx, tx, ids, u)
	if err != nil {
		return out, err
	}
	visibleItems := make([]Entity, 0, len(got))
	for _, id := range ids {
		if e, ok := got[id]; ok {
			out.Items[id] = e
			visibleItems = append(visibleItems, e)
		} else {
			out.Missing = append(out.Missing, id)
		}
	}
	if len(visibleItems) > 0 {
		covers, err := displayCoversFrom(ctx, tx, visibleItems, u)
		if err != nil {
			return out, err
		}
		if len(covers) > 0 {
			out.Covers = covers
		}
	}
	if err = tx.Commit(); err != nil {
		return EntityBatchResponse{}, err
	}
	return out, nil
}
