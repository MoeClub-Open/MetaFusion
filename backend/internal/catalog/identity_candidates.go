package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type IdentityExternalCriterion struct {
	Kind  string `json:"kind"`
	Value string `json:"value"` // matches link.external_id or link.url
}
type IdentityAttributeCriterion struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type IdentityCandidateQuery struct {
	Kind          string                       `json:"kind"`
	Titles        []string                     `json:"titles,omitempty"`
	ExternalLinks []IdentityExternalCriterion  `json:"external_links,omitempty"`
	Attributes    []IdentityAttributeCriterion `json:"attributes,omitempty"`
	WorkID        string                       `json:"work_id,omitempty"`
	ReleaseID     string                       `json:"release_id,omitempty"`
	MediumID      string                       `json:"medium_id,omitempty"`
	Limit         *int                         `json:"limit,omitempty"`
	// Context only adjusts ranking/confidence; it never shrinks the candidate set.
	Context *IdentityContext `json:"context,omitempty"`
}

// A candidate summary carries identity evidence and scope, never editable
// contents, subjects or pictures. Matching does not establish real identity.
type IdentityCandidateEntity struct {
	ID            string                 `json:"id"`
	Kind          string                 `json:"kind"`
	Version       int64                  `json:"version"`
	Title         string                 `json:"title"`
	Translations  map[string]Translation `json:"translations"`
	ExternalLinks []ExternalLink         `json:"external_links"`
	Attributes    map[string]any         `json:"attributes"`
	Status        string                 `json:"status"`
	WorkID        string                 `json:"work_id,omitempty"`
	ContentUnitID string                 `json:"content_unit_id,omitempty"`
	ReleaseID     string                 `json:"release_id,omitempty"`
	MediumID      string                 `json:"medium_id,omitempty"`
	ParentID      string                 `json:"parent_id,omitempty"`
}
type IdentityCandidateMatch struct {
	Matched         IdentityCandidateEntity  `json:"matched"`
	Canonical       *IdentityCandidateEntity `json:"canonical"`
	ResolutionError string                   `json:"resolution_error,omitempty"`
	// Assessment is advisory (reuse|review|distinct); the server never auto-merges.
	Assessment *CandidateAssessment `json:"assessment,omitempty"`
}
type IdentityCandidateResponse struct {
	Items    []IdentityCandidateMatch `json:"items"`
	Total    int                      `json:"total"`
	Complete bool                     `json:"complete"`
	Basis    string                   `json:"basis"`
}

func identityCandidateSummary(e Entity) IdentityCandidateEntity {
	return IdentityCandidateEntity{ID: e.ID, Kind: e.Kind, Version: e.Version, Title: e.Title,
		Translations: e.Translations, ExternalLinks: e.ExternalLinks, Attributes: e.Attributes, Status: e.Status,
		WorkID: e.WorkID, ContentUnitID: e.ContentUnitID, ReleaseID: e.ReleaseID, MediumID: e.MediumID, ParentID: e.ParentID}
}

func normalizeIdentityCandidateQuery(in IdentityCandidateQuery) (IdentityCandidateQuery, []map[string]any, int, error) {
	if !contains(Kinds, in.Kind) {
		return in, nil, 0, fmt.Errorf("invalid_kind")
	}
	count := len(in.Titles) + len(in.ExternalLinks) + len(in.Attributes)
	if count == 0 || count > 60 || len(in.Titles) > 20 || len(in.ExternalLinks) > 20 || len(in.Attributes) > 20 {
		return in, nil, 0, fmt.Errorf("invalid_identity_criteria")
	}
	limit := 1000
	if in.Limit != nil {
		limit = *in.Limit
	}
	if limit < 1 || limit > 1000 {
		return in, nil, 0, fmt.Errorf("invalid_limit")
	}
	clean := func(value string) (string, error) {
		if err := checkQueryText(value); err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("invalid_identity_criteria")
		}
		return value, nil
	}
	docs := []map[string]any{}
	for _, title := range in.Titles {
		value, err := clean(title)
		if err != nil {
			return in, nil, 0, err
		}
		docs = append(docs, map[string]any{"title": value})
	}
	for _, criterion := range in.ExternalLinks {
		key, err := clean(criterion.Kind)
		if err != nil {
			return in, nil, 0, err
		}
		value, err := clean(criterion.Value)
		if err != nil {
			return in, nil, 0, err
		}
		docs = append(docs, map[string]any{"external_links": []map[string]string{{"kind": key, "external_id": value}}})
	}
	for _, criterion := range in.Attributes {
		key, err := clean(criterion.Key)
		if err != nil {
			return in, nil, 0, err
		}
		value, err := clean(criterion.Value)
		if err != nil {
			return in, nil, 0, err
		}
		docs = append(docs, map[string]any{"attributes": map[string]string{key: value}})
	}
	if in.WorkID != "" {
		id, err := uuid.Parse(in.WorkID)
		if err != nil || !contains([]string{"expression", "content_unit"}, in.Kind) {
			return in, nil, 0, fmt.Errorf("invalid_work_id")
		}
		in.WorkID = id.String()
	}
	for _, scope := range []struct {
		value *string
		kind  string
		code  string
	}{{&in.ReleaseID, "medium", "invalid_release_id"}, {&in.MediumID, "track", "invalid_medium_id"}} {
		if *scope.value != "" {
			id, err := uuid.Parse(*scope.value)
			if err != nil || in.Kind != scope.kind {
				return in, nil, 0, fmt.Errorf("%s", scope.code)
			}
			*scope.value = id.String()
		}
	}
	ctx, err := normalizeIdentityContext(in.Context)
	if err != nil {
		return in, nil, 0, err
	}
	in.Context = ctx
	return in, docs, limit, nil
}

func (s *Store) FindIdentityCandidates(ctx context.Context, in IdentityCandidateQuery, u *User) (IdentityCandidateResponse, error) {
	in, docs, limit, err := normalizeIdentityCandidateQuery(in)
	if err != nil {
		return IdentityCandidateResponse{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return IdentityCandidateResponse{}, err
	}
	defer tx.Rollback()
	out, err := identityCandidatesFrom(ctx, tx, in, docs, limit, u)
	if err != nil {
		return IdentityCandidateResponse{}, err
	}
	if err = tx.Commit(); err != nil {
		return IdentityCandidateResponse{}, err
	}
	return out, nil
}

func identityCandidatesFrom(ctx context.Context, q queryer, in IdentityCandidateQuery, docs []map[string]any, limit int, u *User, excludeIDs ...string) (IdentityCandidateResponse, error) {
	out := IdentityCandidateResponse{Items: []IdentityCandidateMatch{}, Basis: "postgres_repeatable_read"}
	encoded, err := json.Marshal(docs)
	if err != nil {
		return out, err
	}
	userID, manage := "00000000-0000-0000-0000-000000000000", false
	if u != nil {
		userID, manage = u.ID, u.Can(PermissionLifecycleManage)
	}
	excludeID := ""
	if len(excludeIDs) > 0 {
		excludeID = excludeIDs[0]
	}
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE excluded(id) AS (
            SELECT id FROM catalog.entities WHERE id=NULLIF($9::text,'')::uuid
            UNION
            SELECT e.id FROM catalog.entities e JOIN excluded x ON e.document->>'redirect_id'=x.id::text
                WHERE e.status='merged'
        )
        SELECT id::text,count(*) OVER () FROM catalog.entities
        WHERE kind=$1 AND status <> 'deleted'
        AND ($3::boolean OR status='published' OR created_by=$4::uuid)
        AND ($5::text='' OR EXISTS(SELECT 1 FROM catalog.expressions x WHERE x.id=entities.id AND x.work_id::text=$5) OR EXISTS(SELECT 1 FROM catalog.content_units x WHERE x.id=entities.id AND x.work_id::text=$5))
        AND ($7::text='' OR EXISTS(SELECT 1 FROM catalog.mediums m WHERE m.id=entities.id AND m.release_id::text=$7))
        AND ($8::text='' OR EXISTS(SELECT 1 FROM catalog.tracks t WHERE t.id=entities.id AND t.medium_id::text=$8))
        AND NOT EXISTS(SELECT 1 FROM excluded WHERE excluded.id=entities.id)
        AND catalog.identity_candidate_terms(document) && ARRAY(
            SELECT DISTINCT unnest(catalog.identity_candidate_terms(value)) FROM jsonb_array_elements($2::jsonb))
        ORDER BY id LIMIT $6`, in.Kind, string(encoded), manage, userID, in.WorkID, limit+1, in.ReleaseID, in.MediumID, excludeID)
	if err != nil {
		return out, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id, &out.Total); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Complete = len(ids) <= limit
	if len(ids) > limit {
		ids = ids[:limit]
	}
	originals, err := getManyFrom(ctx, q, ids, u)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		original, ok := originals[id]
		if !ok {
			return out, sql.ErrNoRows
		}
		item := IdentityCandidateMatch{Matched: identityCandidateSummary(original)}
		canonical := original
		var err error
		if original.Status == "merged" {
			canonical, err = resolveFrom(ctx, q, id, u)
		}
		if err != nil {
			if !isIdentityNotFound(err) {
				return out, err
			}
			item.ResolutionError = "canonical_identity_unverified"
		} else if canonical.Kind != in.Kind || canonical.Status == "deleted" || canonical.Status == "merged" {
			item.ResolutionError = "canonical_identity_unverified"
		} else {
			summary := identityCandidateSummary(canonical)
			item.Canonical = &summary
		}
		out.Items = append(out.Items, item)
	}
	if err = attachAssessments(ctx, q, in, &out); err != nil {
		return out, err
	}
	return out, nil
}
