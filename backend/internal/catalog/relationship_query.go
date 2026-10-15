package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// RelationshipQueryRequest reads only direct neighbors; it never recursively
// expands a graph or stores duplicate structural relationships.
type RelationshipQueryRequest struct {
	IDs       []string `json:"ids"`
	Direction string   `json:"direction,omitempty"`
	RuleCodes []string `json:"rule_codes,omitempty"`
	PeerKinds []string `json:"peer_kinds,omitempty"`
	Limit     *int     `json:"limit,omitempty"`
	Offset    *int     `json:"offset,omitempty"`
}

// RelationshipEntity is deliberately a summary, not an editable Entity: full
// structural records and dynamic attributes can contain unrelated hidden IDs.
type RelationshipEntity struct {
	ID               string                 `json:"id"`
	Kind             string                 `json:"kind"`
	Version          int64                  `json:"version"`
	Title            string                 `json:"title"`
	OriginalLanguage string                 `json:"original_language"`
	Translations     map[string]Translation `json:"translations"`
}

type RelationshipQueryPage struct {
	SubjectID string       `json:"subject_id"`
	Items     []EntityLink `json:"items"`
	Limit     int          `json:"limit"`
	Offset    int          `json:"offset"`
	HasMore   bool         `json:"has_more"`
}

type RelationshipQueryResponse struct {
	DefinitionETag string                        `json:"definition_etag"`
	Pages          []RelationshipQueryPage       `json:"pages"`
	Entities       map[string]RelationshipEntity `json:"entities"`
	UnavailableIDs []string                      `json:"unavailable_ids"`
}

func normalizeRelationshipQuery(in RelationshipQueryRequest) (RelationshipQueryRequest, int, int, error) {
	if len(in.IDs) == 0 || len(in.IDs) > 20 {
		return in, 0, 0, fmt.Errorf("invalid_ids")
	}
	ids, seen := []string{}, map[string]bool{}
	for _, raw := range in.IDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return in, 0, 0, fmt.Errorf("invalid_id")
		}
		key := id.String()
		if !seen[key] {
			seen[key] = true
			ids = append(ids, key)
		}
	}
	in.IDs = ids
	if in.Direction == "" {
		in.Direction = "both"
	}
	if !contains([]string{"both", "outgoing", "incoming"}, in.Direction) {
		return in, 0, 0, fmt.Errorf("invalid_direction")
	}
	for _, kind := range in.PeerKinds {
		if !contains(Kinds, kind) {
			return in, 0, 0, fmt.Errorf("invalid_peer_kind")
		}
	}
	in.RuleCodes = uniqueRelationshipFilters(in.RuleCodes)
	in.PeerKinds = uniqueRelationshipFilters(in.PeerKinds)
	limit, offset := 25, 0
	if in.Limit != nil {
		limit = *in.Limit
	}
	if in.Offset != nil {
		offset = *in.Offset
	}
	if limit < 1 || limit > 100 {
		return in, 0, 0, fmt.Errorf("invalid_limit")
	}
	if offset < 0 {
		return in, 0, 0, fmt.Errorf("invalid_offset")
	}
	return in, limit, offset, nil
}

func uniqueRelationshipFilters(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func validateRelationshipQueryRules(in RelationshipQueryRequest, d Definitions) error {
	rules := map[string]bool{}
	for _, rule := range ReadRelationshipRules(d) {
		rules[rule.Code] = true
	}
	for _, code := range in.RuleCodes {
		if !rules[code] {
			return fmt.Errorf("invalid_rule_code")
		}
	}
	return nil
}

func relationshipEntitiesFrom(ctx context.Context, q queryer, ids []string, u *User) (map[string]RelationshipEntity, error) {
	out := map[string]RelationshipEntity{}
	if len(ids) == 0 {
		return out, nil
	}
	manage := u != nil && u.Can(PermissionLifecycleManage)
	userID := "00000000-0000-0000-0000-000000000000"
	if u != nil {
		userID = u.ID
	}
	rows, err := q.QueryContext(ctx, `SELECT id::text,kind,coalesce((document->>'version')::bigint,0),
	 coalesce(document->>'title',''),coalesce(document->>'original_language',''),coalesce(document->'translations','{}'::jsonb)
	 FROM catalog.entities WHERE id=ANY($1::uuid[])
	 AND ($2::boolean OR state='active' OR created_by=$3::uuid)`, pq.Array(ids), manage, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e RelationshipEntity
		var translations []byte
		if err = rows.Scan(&e.ID, &e.Kind, &e.Version, &e.Title, &e.OriginalLanguage, &translations); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(translations, &e.Translations); err != nil {
			return nil, err
		}
		if e.Translations == nil {
			e.Translations = map[string]Translation{}
		}
		out[e.ID] = e
	}
	return out, rows.Err()
}

func (s *Store) QueryRelationships(ctx context.Context, in RelationshipQueryRequest, u *User) (RelationshipQueryResponse, error) {
	out := RelationshipQueryResponse{Pages: []RelationshipQueryPage{}, Entities: map[string]RelationshipEntity{}, UnavailableIDs: []string{}}
	in, limit, offset, err := normalizeRelationshipQuery(in)
	if err != nil {
		return out, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = queryRelationshipsFrom(ctx, tx, in, limit, offset, u)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return RelationshipQueryResponse{}, err
	}
	return out, nil
}

func queryRelationshipsFrom(ctx context.Context, q queryer, in RelationshipQueryRequest, limit, offset int, u *User) (RelationshipQueryResponse, error) {
	out := RelationshipQueryResponse{Pages: []RelationshipQueryPage{}, Entities: map[string]RelationshipEntity{}, UnavailableIDs: []string{}}
	defs, err := definitions(ctx, q)
	if err != nil {
		return out, err
	}
	if err = validateRelationshipQueryRules(in, defs.Document); err != nil {
		return out, err
	}
	out.DefinitionETag = defs.ETag
	subjects, err := relationshipEntitiesFrom(ctx, q, in.IDs, u)
	if err != nil {
		return out, err
	}
	filters := relationshipFilters{Direction: in.Direction, RuleCodes: in.RuleCodes, PeerKinds: in.PeerKinds}
	// Empty lists mean no filter, including explicitly supplied [] arrays.
	if len(filters.RuleCodes) == 0 {
		filters.RuleCodes = nil
	}
	if len(filters.PeerKinds) == 0 {
		filters.PeerKinds = nil
	}
	endpointIDs := []string{}
	for _, id := range in.IDs {
		self, ok := subjects[id]
		if !ok {
			out.UnavailableIDs = append(out.UnavailableIDs, id)
			continue
		}
		out.Entities[id] = self
		page, err := entityLinksFrom(ctx, q, id, limit, offset, u, defs, filters)
		if err != nil {
			return out, err
		}
		out.Pages = append(out.Pages, RelationshipQueryPage{SubjectID: id, Items: page.Items, Limit: limit, Offset: offset, HasMore: page.HasMore})
		for _, link := range page.Items {
			endpointIDs = append(endpointIDs, link.SourceID, link.TargetID)
			for _, ref := range link.References {
				endpointIDs = append(endpointIDs, ref.EntityID)
			}
		}
	}
	peers, err := relationshipEntitiesFrom(ctx, q, endpointIDs, u)
	if err != nil {
		return out, err
	}
	for id, e := range peers {
		out.Entities[id] = e
	}
	return out, nil
}
