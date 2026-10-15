package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/google/uuid"
)

type CompositionItem struct {
	Relation Relation `json:"relation"`
	Entity   Entity   `json:"entity"`
}
type ExpressionComposition struct {
	Expression     Entity            `json:"expression"`
	Parts          []CompositionItem `json:"parts"`
	Wholes         []CompositionItem `json:"wholes"`
	DefinitionETag string            `json:"definition_etag"`
}
type ReleaseEditions struct {
	Release        Entity   `json:"release"`
	Group          *Entity  `json:"group"`
	Editions       []Entity `json:"editions"`
	DefinitionETag string   `json:"definition_etag"`
}

func usageRelations(ctx context.Context, q queryer, d Definitions, usage, id string, incoming bool) ([]Relation, error) {
	codes := []string{}
	for code, r := range d.Relations {
		if r.Usage == usage {
			codes = append(codes, code)
		}
	}
	if len(codes) == 0 {
		return []Relation{}, nil
	}
	column := "source_id"
	if incoming {
		column = "target_id"
	}
	args := append(entityArgs(codes), id)
	rows, err := q.QueryContext(ctx, "SELECT document FROM catalog.relations WHERE type IN ("+
		entityPlaceholders(codes, 1)+") AND "+column+"=$"+fmt.Sprint(len(args))+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items, err := scanRelationDocuments(rows)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Position != items[j].Position {
			return items[i].Position < items[j].Position
		}
		return items[i].ID < items[j].ID
	})
	return items, err
}

func projectionRoot(ctx context.Context, tx *sql.Tx, id, kind string, u *User) (Entity, DefinitionConfig, error) {
	e, err := get(ctx, tx, id)
	if err != nil {
		return e, DefinitionConfig{}, err
	}
	if !visible(e, u) || e.State == "deleted" || e.State == "merged" {
		return e, DefinitionConfig{}, sql.ErrNoRows
	}
	if e.Kind != kind {
		return e, DefinitionConfig{}, fmt.Errorf("not_%s", kind)
	}
	d, err := definitions(ctx, tx)
	return e, d, err
}

func visibleUsagePeers(ctx context.Context, q queryer, d Definitions, items []Relation, incoming bool, u *User) ([]CompositionItem, error) {
	ids := []string{}
	for _, r := range items {
		id := r.TargetID
		if incoming {
			id = r.SourceID
		}
		ids = append(ids, id)
	}
	entities, err := getManyFrom(ctx, q, ids, u)
	if err != nil {
		return nil, err
	}
	out := []CompositionItem{}
	for _, r := range items {
		id := r.TargetID
		if incoming {
			id = r.SourceID
		}
		e, ok := entities[id]
		if !ok || e.State == "deleted" || e.State == "merged" {
			continue
		}
		if err := d.attributes(d.Relations[r.Type].Fields, r.Attributes, reference(ctx, q, u), true); err != nil {
			continue
		}
		out = append(out, CompositionItem{Relation: r, Entity: e})
	}
	return out, nil
}

func (s *Store) ExpressionComposition(ctx context.Context, id string, u *User) (ExpressionComposition, error) {
	out := ExpressionComposition{Parts: []CompositionItem{}, Wholes: []CompositionItem{}}
	if _, err := uuid.Parse(id); err != nil {
		return out, fmt.Errorf("invalid_id")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	e, d, err := projectionRoot(ctx, tx, id, "expression", u)
	if err != nil {
		return out, err
	}
	out.Expression, out.DefinitionETag = e, d.ETag
	for _, incoming := range []bool{false, true} {
		items, err := usageRelations(ctx, tx, d.Document, "expression_composition", id, incoming)
		if err != nil {
			return out, err
		}
		peers, err := visibleUsagePeers(ctx, tx, d.Document, items, incoming, u)
		if err != nil {
			return out, err
		}
		if incoming {
			out.Wholes = peers
		} else {
			out.Parts = peers
		}
	}
	return out, tx.Commit()
}

func (s *Store) ReleaseEditions(ctx context.Context, id string, u *User) (ReleaseEditions, error) {
	out := ReleaseEditions{Editions: []Entity{}}
	if _, err := uuid.Parse(id); err != nil {
		return out, fmt.Errorf("invalid_id")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	e, d, err := projectionRoot(ctx, tx, id, "release", u)
	if err != nil {
		return out, err
	}
	out.Release, out.DefinitionETag = e, d.ETag
	links, err := usageRelations(ctx, tx, d.Document, "release_group", id, false)
	if err != nil {
		return out, err
	}
	groups, err := visibleUsagePeers(ctx, tx, d.Document, links, false, u)
	if err != nil {
		return out, err
	}
	if len(groups) > 1 {
		return out, fmt.Errorf("edition_group_conflict")
	}
	if len(groups) == 0 {
		return out, tx.Commit()
	}
	out.Group = &groups[0].Entity
	links, err = usageRelations(ctx, tx, d.Document, "release_group", out.Group.ID, true)
	if err != nil {
		return out, err
	}
	editions, err := visibleUsagePeers(ctx, tx, d.Document, links, true, u)
	if err != nil {
		return out, err
	}
	for _, edition := range editions {
		out.Editions = append(out.Editions, edition.Entity)
	}
	return out, tx.Commit()
}
