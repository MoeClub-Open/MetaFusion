package catalog

import (
	"context"

	"github.com/lib/pq"
)

// filterVisibleTrackContents keeps public read projections consistent with the
// release TOC. Unpublishing an expression does not remove its structural facts;
// its ID, locator and attributes must nevertheless disappear from readers who
// cannot see it. Write validation continues to use get/fillStructural directly.
// All tracks share one visibility query, including large release comparisons.
func filterVisibleTrackContents(ctx context.Context, q queryer, entities map[string]Entity, u *User) error {
	ids := map[string]bool{}
	for _, e := range entities {
		if e.Kind == "track" {
			for _, c := range e.Contents {
				ids[c.ExpressionID] = true
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.QueryContext(ctx, `SELECT id::text,kind,state,created_by::text
		FROM catalog.entities WHERE id = ANY($1::uuid[])`, pq.Array(keysOf(ids)))
	if err != nil {
		return err
	}
	readable := map[string]bool{}
	for rows.Next() {
		var e Entity
		if err = rows.Scan(&e.ID, &e.Kind, &e.State, &e.CreatedBy); err != nil {
			rows.Close()
			return err
		}
		if e.Kind == "expression" && visible(e, u) {
			readable[e.ID] = true
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for id, e := range entities {
		if e.Kind != "track" {
			continue
		}
		contents := make([]Inclusion, 0, len(e.Contents))
		for _, c := range e.Contents {
			if readable[c.ExpressionID] {
				contents = append(contents, c)
			}
		}
		e.Contents = contents
		entities[id] = e
	}
	return nil
}

func visibleEntityContents(ctx context.Context, q queryer, e Entity, u *User) (Entity, error) {
	if e.Kind != "track" || len(e.Contents) == 0 {
		return e, nil
	}
	entities := map[string]Entity{e.ID: e}
	if err := filterVisibleTrackContents(ctx, q, entities, u); err != nil {
		return Entity{}, err
	}
	return entities[e.ID], nil
}

// A whole-entity PUT built from a filtered read must not silently erase facts
// its editor could not inspect. Reviewers may remove such facts explicitly;
// other editors can only preserve their exact historical inclusion records.
func preserveHiddenTrackContents(ctx context.Context, q queryer, old, next Entity, u User) error {
	if old.Kind != "track" || len(old.Contents) == 0 || u.Can(PermissionLifecycleManage) {
		return nil
	}
	readable, err := visibleEntityContents(ctx, q, old, &u)
	if err != nil || len(readable.Contents) == len(old.Contents) {
		return err
	}
	known := map[string]bool{}
	for _, c := range readable.Contents {
		known[encode(c)] = true
	}
	retained := map[string]bool{}
	for _, c := range next.Contents {
		retained[encode(c)] = true
	}
	for _, c := range old.Contents {
		key := encode(c)
		if !known[key] && !retained[key] {
			return errForbidden
		}
	}
	return nil
}
