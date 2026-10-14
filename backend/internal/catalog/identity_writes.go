package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Replacing an ancestor can change a title even when its name is absent from
// the pointer. Summary-only translation patches do not require identity review.
func commitNeedsIdentityReview(op CommitOperation) bool {
	if op.Target != "entity" {
		return false
	}
	if op.Action == "create" {
		return true
	}
	for _, patch := range op.Patch {
		parts, err := pointerParts(patch.Path)
		if err != nil {
			continue
		}
		switch parts[0] {
		case "title", "external_links":
			return true
		case "translations":
			if len(parts) <= 2 || parts[2] == "title" {
				return true
			}
		}
	}
	return false
}

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
		if !visible(old, &u) || old.Status == "deleted" || old.Status == "merged" {
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

func entityNeedsIdentityReview(old, e Entity) bool {
	if old.ID == "" || old.Title != e.Title || encode(old.ExternalLinks) != encode(e.ExternalLinks) {
		return true
	}
	titles := func(translations map[string]Translation) map[string]string {
		out := map[string]string{}
		for locale, translation := range translations {
			if translation.Title != "" {
				out[locale] = translation.Title
			}
		}
		return out
	}
	return encode(titles(old.Translations)) != encode(titles(e.Translations))
}

func reviewEntityIdentity(ctx context.Context, tx *sql.Tx, e Entity, reviewedIDs []string, u User, refs map[string]string, operation int) error {
	if len(reviewedIDs) > 1000 {
		return fmt.Errorf("invalid_identity_review")
	}
	reviewed := map[string]bool{}
	for _, id := range reviewedIDs {
		if strings.HasPrefix(id, "@local:") {
			id = refs[strings.TrimPrefix(id, "@local:")]
		}
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id || reviewed[id] {
			return fmt.Errorf("invalid_identity_review")
		}
		reviewed[id] = true
	}
	query, docs, limit, err := normalizeIdentityCandidateQuery(commitIdentityQuery(e))
	if err != nil {
		return err
	}
	candidates, err := identityCandidatesFrom(ctx, tx, query, docs, limit, &u, e.ID)
	if err != nil {
		return err
	}
	if !candidates.Complete {
		return fmt.Errorf("identity_candidates_incomplete")
	}
	actual := map[string]bool{}
	for _, candidate := range candidates.Items {
		if candidate.Canonical == nil || candidate.ResolutionError != "" {
			return fmt.Errorf("identity_candidate_unresolved")
		}
		if candidate.Canonical.ID != e.ID {
			actual[candidate.Canonical.ID] = true
		}
	}
	ids := keysOf(actual)
	sort.Strings(ids)
	if len(reviewed) != len(actual) {
		return &commitIdentityError{operation, ids}
	}
	for id := range actual {
		if !reviewed[id] {
			return &commitIdentityError{operation, ids}
		}
	}
	return nil
}

func identityPatchedEntity(e Entity, patch []CommitPatch, refs map[string]string) (Entity, error) {
	doc := commitMap(e)
	for _, p := range patch {
		parts, err := pointerParts(p.Path)
		if err != nil {
			return Entity{}, err
		}
		value := p.Value
		if !p.Remove {
			value, err = resolveCommitRefs(value, refs)
			if err != nil {
				return Entity{}, err
			}
		}
		if err = putPointer(doc, parts, value, p.Remove); err != nil {
			return Entity{}, err
		}
	}
	var out Entity
	err := strictCommitDocument([]byte(encode(doc)), &out)
	return out, err
}
