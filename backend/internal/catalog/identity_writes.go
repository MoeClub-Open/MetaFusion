package catalog

import (
	"context"
	"database/sql"
	"sort"
)

func identityWriteDocument(e Entity) map[string]any {
	translations := map[string]map[string]any{}
	for locale, t := range e.Translations {
		translations[locale] = map[string]any{"title": t.Title, "aliases": t.Aliases}
	}
	return map[string]any{"title": e.Title, "translations": translations, "external_links": e.ExternalLinks}
}

// Use the same indexed terms as candidate lookup, including translated aliases
// which can become candidates for another writer's formal title. Lock both the
// old and new terms so moving away from an identity also changes its reviewed set.
func addIdentityWriteKeys(ctx context.Context, tx *sql.Tx, e Entity, keys map[string]bool) error {
	q := commitIdentityQuery(e)
	rows, err := tx.QueryContext(ctx, `SELECT unnest(catalog.identity_candidate_terms($1::jsonb))`, encode(identityWriteDocument(e)))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var term string
		if err = rows.Scan(&term); err != nil {
			return err
		}
		keys[q.Kind+":"+q.WorkID+":"+q.ReleaseID+":"+q.MediumID+":"+term] = true
	}
	return rows.Err()
}

func lockIdentityWriteKeys(ctx context.Context, tx *sql.Tx, keys map[string]bool) error {
	ordered := keysOf(keys)
	sort.Strings(ordered)
	for _, key := range ordered {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(740207,hashtext($1))`, key); err != nil {
			return err
		}
	}
	return nil
}

func lockEntityIdentityWrite(ctx context.Context, tx *sql.Tx, e Entity, u User) error {
	keys := map[string]bool{}
	if e.ID != "" {
		old, err := get(ctx, tx, e.ID)
		if err != nil {
			return err
		}
		if !visible(old, &u) || old.State == "deleted" || old.State == "merged" {
			return errForbidden
		}
		if encode(identityWriteDocument(old)) == encode(identityWriteDocument(e)) {
			return nil
		}
		if err = addIdentityWriteKeys(ctx, tx, old, keys); err != nil {
			return err
		}
	}
	if err := addIdentityWriteKeys(ctx, tx, e, keys); err != nil {
		return err
	}
	return lockIdentityWriteKeys(ctx, tx, keys)
}
