package catalog

import (
	"context"
	"database/sql"
	"sort"

	"github.com/lib/pq"
)

func identityWriteDocument(e Entity) map[string]any {
	translations := map[string]map[string]any{}
	for locale, t := range e.Translations {
		row := map[string]any{"title": t.Title, "aliases": t.Aliases}
		if len(t.NonofficialAliases) > 0 {
			row["nonofficial_aliases"] = t.NonofficialAliases
		}
		translations[locale] = row
	}
	doc := map[string]any{"title": e.Title, "translations": translations, "external_links": e.ExternalLinks}
	if fn, ok := e.Attributes["former_names"]; ok && fn != nil {
		doc["attributes"] = map[string]any{"former_names": fn}
	}
	return doc
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

// addIdentityWriteKeysBatch computes identity_candidate_terms for a batch of
// entities in one SQL round-trip instead of N individual queries.
func addIdentityWriteKeysBatch(ctx context.Context, tx *sql.Tx, entities []Entity, keys map[string]bool) error {
	if len(entities) == 0 {
		return nil
	}
	docs := make([]string, len(entities))
	prefixes := make([]string, len(entities))
	for i, e := range entities {
		q := commitIdentityQuery(e)
		docs[i] = encode(identityWriteDocument(e))
		prefixes[i] = q.Kind + ":" + q.WorkID + ":" + q.ReleaseID + ":" + q.MediumID + ":"
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.ord, term
		FROM unnest($1::text[]) WITH ORDINALITY AS t(doc, ord)
		CROSS JOIN LATERAL unnest(catalog.identity_candidate_terms(t.doc::jsonb)) AS term`, pq.Array(docs))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ord int
		var term string
		if err = rows.Scan(&ord, &term); err != nil {
			return err
		}
		if ord >= 1 && ord <= len(prefixes) {
			keys[prefixes[ord-1]+term] = true
		}
	}
	return rows.Err()
}

func lockIdentityWriteKeys(ctx context.Context, tx *sql.Tx, keys map[string]bool) error {
	ordered := keysOf(keys)
	if len(ordered) == 0 {
		return nil
	}
	sort.Strings(ordered)
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(740207,hashtext(k))
		FROM unnest($1::text[]) WITH ORDINALITY AS t(k, ord) ORDER BY ord`, pq.Array(ordered))
	return err
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
