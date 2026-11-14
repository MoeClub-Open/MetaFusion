package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lib/pq"
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
	Kind               string                       `json:"kind"`
	Titles             []string                     `json:"titles,omitempty"`
	ExternalLinks      []IdentityExternalCriterion  `json:"external_links,omitempty"`
	Attributes         []IdentityAttributeCriterion `json:"attributes,omitempty"`
	WorkID             string                       `json:"work_id,omitempty"`
	ReleaseID          string                       `json:"release_id,omitempty"`
	MediumID           string                       `json:"medium_id,omitempty"`
	Limit              *int                         `json:"limit,omitempty"`
	IncludeApproximate bool                         `json:"include_approximate,omitempty"`
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
	State         string                 `json:"state"`
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
		Translations: e.Translations, ExternalLinks: e.ExternalLinks, Attributes: e.Attributes, State: e.State,
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
	likeEscape := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	approxCores := []string{}
	approxLikes := []string{}
	approxSubCores := []string{}
	approxRevLikes := []string{}
	approxTrgms := []string{}
	if in.IncludeApproximate {
		seenCore := map[string]bool{}
		seenSubCore := map[string]bool{}
		seenRevLike := map[string]bool{}
		for _, rawTitle := range in.Titles {
			core := identityCoreTitle(rawTitle)
			runes := []rune(core)
			n := len(runes)
			if n < 2 || seenCore[core] {
				continue
			}
			seenCore[core] = true
			approxCores = append(approxCores, core)
			approxLikes = append(approxLikes, "%"+likeEscape.Replace(core)+"%")
			for _, subLen := range []int{2, 3} {
				if subLen >= n {
					continue
				}
				for i := 0; i+subLen <= n; i++ {
					sub := string(runes[i : i+subLen])
					if !seenSubCore[sub] {
						seenSubCore[sub] = true
						approxSubCores = append(approxSubCores, sub)
					}
				}
			}
			if n >= 5 {
				for i := 0; i+4 <= n; i++ {
					pat := "%" + likeEscape.Replace(string(runes[i:i+4])) + "%"
					if !seenRevLike[pat] {
						seenRevLike[pat] = true
						approxRevLikes = append(approxRevLikes, pat)
					}
				}
			}
			if n >= 4 {
				approxTrgms = append(approxTrgms, core)
			}
		}
	}
	_ = utf8.RuneError
	var rows *sql.Rows
	if len(approxCores) == 0 {
		rows, err = q.QueryContext(ctx, `WITH RECURSIVE excluded(id) AS (
            SELECT id FROM catalog.entities WHERE id=NULLIF($9::text,'')::uuid
            UNION
            SELECT e.id FROM catalog.entities e JOIN excluded x ON e.document->>'redirect_id'=x.id::text
                WHERE e.state='merged'
        ),
        query_terms AS (
            SELECT
                COALESCE(ARRAY(SELECT DISTINCT unnest(catalog.identity_exact_terms(value)) FROM jsonb_array_elements($2::jsonb)), ARRAY[]::text[]) AS strong_terms,
                COALESCE(ARRAY(SELECT DISTINCT md5(jsonb_build_array('core_title', c)::text) FROM jsonb_array_elements($2::jsonb) elem, LATERAL unnest(catalog.identity_core_titles(elem)) c), ARRAY[]::text[]) AS core_terms,
                COALESCE(ARRAY(SELECT DISTINCT unnest(catalog.identity_candidate_terms(elem)) FROM jsonb_array_elements($2::jsonb) elem WHERE elem ? 'attributes'), ARRAY[]::text[]) AS attr_terms,
                COALESCE(ARRAY(SELECT DISTINCT unnest(catalog.identity_candidate_terms(value)) FROM jsonb_array_elements($2::jsonb)), ARRAY[]::text[]) AS all_terms
        )
        SELECT e.id::text,count(*) OVER () FROM catalog.entities e, query_terms qt, LATERAL (SELECT catalog.identity_candidate_terms(e.document) AS terms) ct
        WHERE e.kind=$1 AND e.state <> 'deleted'
        AND ($3::boolean OR e.state='active' OR e.created_by=$4::uuid)
        AND ($5::text='' OR EXISTS(SELECT 1 FROM catalog.expressions x WHERE x.id=e.id AND x.work_id::text=$5) OR EXISTS(SELECT 1 FROM catalog.content_units x WHERE x.id=e.id AND x.work_id::text=$5))
        AND ($7::text='' OR EXISTS(SELECT 1 FROM catalog.mediums m WHERE m.id=e.id AND m.release_id::text=$7))
        AND ($8::text='' OR EXISTS(SELECT 1 FROM catalog.tracks t WHERE t.id=e.id AND t.medium_id::text=$8))
        AND NOT EXISTS(SELECT 1 FROM excluded WHERE excluded.id=e.id)
        AND catalog.identity_candidate_terms(e.document) && qt.all_terms
        ORDER BY (
            CASE
                WHEN cardinality(qt.strong_terms) > 0 AND ct.terms && qt.strong_terms THEN
                    CASE WHEN cardinality(qt.attr_terms) > 0 AND ct.terms && qt.attr_terms THEN 0 ELSE 1 END
                WHEN cardinality(qt.core_terms) > 0 AND ct.terms && qt.core_terms THEN
                    CASE WHEN cardinality(qt.attr_terms) > 0 AND ct.terms && qt.attr_terms THEN 2 ELSE 3 END
                ELSE 4
            END
        ) ASC, e.id ASC
        LIMIT $6`, in.Kind, string(encoded), manage, userID, in.WorkID, limit+1, in.ReleaseID, in.MediumID, excludeID)
	} else {
		rows, err = q.QueryContext(ctx, `WITH RECURSIVE excluded(id) AS (
            SELECT id FROM catalog.entities WHERE id=NULLIF($9::text,'')::uuid
            UNION
            SELECT e.id FROM catalog.entities e JOIN excluded x ON e.document->>'redirect_id'=x.id::text
                WHERE e.state='merged'
        ),
        query_terms AS (
            SELECT
                COALESCE(ARRAY(SELECT DISTINCT unnest(catalog.identity_exact_terms(value)) FROM jsonb_array_elements($2::jsonb)), ARRAY[]::text[]) AS strong_terms,
                COALESCE(ARRAY(SELECT DISTINCT md5(jsonb_build_array('core_title', c)::text) FROM jsonb_array_elements($2::jsonb) elem, LATERAL unnest(catalog.identity_core_titles(elem)) c), ARRAY[]::text[]) AS core_terms,
                COALESCE(ARRAY(SELECT DISTINCT unnest(catalog.identity_candidate_terms(elem)) FROM jsonb_array_elements($2::jsonb) elem WHERE elem ? 'attributes'), ARRAY[]::text[]) AS attr_terms,
                COALESCE(ARRAY(SELECT DISTINCT unnest(catalog.identity_candidate_terms(value)) FROM jsonb_array_elements($2::jsonb)), ARRAY[]::text[]) AS all_terms,
                COALESCE(ARRAY(SELECT DISTINCT md5(jsonb_build_array('core_title', s)::text) FROM unnest($12::text[]) s), ARRAY[]::text[]) AS sub_core_terms
        ),
        matched_ids(id, tier) AS (
            SELECT e.id,
                CASE
                    WHEN cardinality(qt.strong_terms) > 0 AND ct.terms && qt.strong_terms THEN
                        CASE WHEN cardinality(qt.attr_terms) > 0 AND ct.terms && qt.attr_terms THEN 0 ELSE 1 END
                    WHEN cardinality(qt.core_terms) > 0 AND ct.terms && qt.core_terms THEN
                        CASE WHEN cardinality(qt.attr_terms) > 0 AND ct.terms && qt.attr_terms THEN 2 ELSE 3 END
                    ELSE 6
                END
            FROM catalog.entities e, query_terms qt, LATERAL (SELECT catalog.identity_candidate_terms(e.document) AS terms) ct
            WHERE e.kind=$1 AND e.state <> 'deleted'
              AND catalog.identity_candidate_terms(e.document) && qt.all_terms
            UNION ALL
            SELECT e.id, 4
            FROM catalog.entities e
            WHERE cardinality($10::text[]) > 0
              AND e.kind=$1 AND e.state <> 'deleted'
              AND catalog.identity_search_text(e.document) LIKE ANY($10::text[])
              AND EXISTS (
                  SELECT 1 FROM unnest($11::text[]) qc
                  CROSS JOIN unnest(catalog.identity_core_titles(e.document)) ct
                  WHERE strpos(ct, qc) > 0
              )
            UNION ALL
            SELECT e.id, 4
            FROM catalog.entities e, query_terms qt
            WHERE cardinality(qt.sub_core_terms) > 0
              AND e.kind=$1 AND e.state <> 'deleted'
              AND catalog.identity_candidate_terms(e.document) && qt.sub_core_terms
            UNION ALL
            SELECT e.id, 4
            FROM catalog.entities e
            WHERE cardinality($13::text[]) > 0
              AND e.kind=$1 AND e.state <> 'deleted'
              AND catalog.identity_search_text(e.document) LIKE ANY($13::text[])
              AND EXISTS (
                  SELECT 1 FROM unnest($11::text[]) qc
                  CROSS JOIN unnest(catalog.identity_core_titles(e.document)) ct
                  WHERE strpos(qc, ct) > 0
              )
            UNION ALL
            SELECT e.id, 5
            FROM unnest($14::text[]) q
            JOIN catalog.entities e
              ON e.kind=$1 AND e.state <> 'deleted'
             AND catalog.identity_search_text(e.document) %> q
            WHERE word_similarity(q, catalog.identity_search_text(e.document)) >= 0.6
        ),
        ranked AS (
            SELECT e.id, MIN(m.tier) AS best_tier
            FROM matched_ids m
            JOIN catalog.entities e ON e.id=m.id
            WHERE e.kind=$1 AND e.state <> 'deleted'
            AND ($3::boolean OR e.state='active' OR e.created_by=$4::uuid)
            AND ($5::text='' OR EXISTS(SELECT 1 FROM catalog.expressions x WHERE x.id=e.id AND x.work_id::text=$5) OR EXISTS(SELECT 1 FROM catalog.content_units x WHERE x.id=e.id AND x.work_id::text=$5))
            AND ($7::text='' OR EXISTS(SELECT 1 FROM catalog.mediums md WHERE md.id=e.id AND md.release_id::text=$7))
            AND ($8::text='' OR EXISTS(SELECT 1 FROM catalog.tracks t WHERE t.id=e.id AND t.medium_id::text=$8))
            AND NOT EXISTS(SELECT 1 FROM excluded WHERE excluded.id=e.id)
            GROUP BY e.id
        )
        SELECT id::text,count(*) OVER ()
        FROM ranked
        ORDER BY best_tier ASC, id ASC
        LIMIT $6`, in.Kind, string(encoded), manage, userID, in.WorkID, limit+1, in.ReleaseID, in.MediumID, excludeID, pq.Array(approxLikes), pq.Array(approxCores), pq.Array(approxSubCores), pq.Array(approxRevLikes), pq.Array(approxTrgms))
	}
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
		if original.State == "merged" {
			canonical, err = resolveFrom(ctx, q, id, u)
		}
		if err != nil {
			if !isIdentityNotFound(err) {
				return out, err
			}
			item.ResolutionError = "canonical_identity_unverified"
		} else if canonical.Kind != in.Kind || canonical.State == "deleted" || canonical.State == "merged" {
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
