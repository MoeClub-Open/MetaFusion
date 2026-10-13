package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// IdentityContext ranks identity candidates; it never filters the candidate set.
// Same-name entities with different artists stay in the result.
type IdentityContext struct {
	Creator       []string `json:"creator,omitempty"`
	Performer     []string `json:"performer,omitempty"`
	Label         []string `json:"label,omitempty"`
	CatalogNumber []string `json:"catalog_number,omitempty"`
}

// CandidateAssessment is advisory only. The server never auto-merges.
type CandidateAssessment struct {
	MatchedFields     []string       `json:"matched_fields"`
	Conflicts         []string       `json:"conflicts,omitempty"`
	ContextSummary    map[string]any `json:"context_summary,omitempty"`
	Confidence        float64        `json:"confidence"`
	RecommendedAction string         `json:"recommended_action"` // reuse|review|distinct
}

const (
	ActionReuse    = "reuse"
	ActionReview   = "review"
	ActionDistinct = "distinct"

	identityBatchMax = 50
)

var identitySpace = regexp.MustCompile(`[\t\n\v\f\r \x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}]+`)

func identityTitle(s string) string {
	return strings.ToLower(strings.TrimSpace(identitySpace.ReplaceAllString(s, " ")))
}

// IdentityBatchQuery is one entry in POST /entities/candidates/batch.
// Fields are flat (key + IdentityCandidateQuery) for agent-friendly payloads.
type IdentityBatchQuery struct {
	Key string `json:"key"`
	IdentityCandidateQuery
}

type IdentityBatchRequest struct {
	Queries []IdentityBatchQuery `json:"queries"`
}

type IdentityBatchResult struct {
	Key      string                   `json:"key"`
	Items    []IdentityCandidateMatch `json:"items"`
	Total    int                      `json:"total"`
	Complete bool                     `json:"complete"`
	Basis    string                   `json:"basis,omitempty"`
	Error    string                   `json:"error,omitempty"`
}

type IdentityBatchResponse struct {
	Results []IdentityBatchResult `json:"results"`
}

func normalizeIdentityContext(ctx *IdentityContext) (*IdentityContext, error) {
	if ctx == nil {
		return nil, nil
	}
	out := &IdentityContext{}
	cleanIDs := func(in []string, code string) ([]string, error) {
		if len(in) > 20 {
			return nil, fmt.Errorf("%s", code)
		}
		seen := map[string]bool{}
		ids := []string{}
		for _, raw := range in {
			id := strings.TrimSpace(raw)
			if id == "" {
				return nil, fmt.Errorf("%s", code)
			}
			parsed, err := uuid.Parse(id)
			if err != nil {
				return nil, fmt.Errorf("%s", code)
			}
			id = parsed.String()
			if seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
		return ids, nil
	}
	var err error
	if out.Creator, err = cleanIDs(ctx.Creator, "invalid_context_creator"); err != nil {
		return nil, err
	}
	if out.Performer, err = cleanIDs(ctx.Performer, "invalid_context_performer"); err != nil {
		return nil, err
	}
	if out.Label, err = cleanIDs(ctx.Label, "invalid_context_label"); err != nil {
		return nil, err
	}
	if len(ctx.CatalogNumber) > 20 {
		return nil, fmt.Errorf("invalid_context_catalog_number")
	}
	for _, raw := range ctx.CatalogNumber {
		if err := checkQueryText(raw); err != nil {
			return nil, fmt.Errorf("invalid_context_catalog_number")
		}
		v := strings.TrimSpace(raw)
		if v == "" {
			return nil, fmt.Errorf("invalid_context_catalog_number")
		}
		out.CatalogNumber = append(out.CatalogNumber, v)
	}
	if len(out.Creator)+len(out.Performer)+len(out.Label)+len(out.CatalogNumber) == 0 {
		return nil, nil
	}
	return out, nil
}

func normalizeIdentityBatchRequest(in IdentityBatchRequest) (IdentityBatchRequest, error) {
	if len(in.Queries) == 0 {
		return in, fmt.Errorf("queries_required")
	}
	if len(in.Queries) > identityBatchMax {
		return in, fmt.Errorf("too_many_queries")
	}
	seen := map[string]bool{}
	for i := range in.Queries {
		key := strings.TrimSpace(in.Queries[i].Key)
		if key == "" || len(key) > 64 {
			return in, fmt.Errorf("invalid_query_key")
		}
		if seen[key] {
			return in, fmt.Errorf("duplicate_query_key")
		}
		seen[key] = true
		in.Queries[i].Key = key
		// Context is normalized again inside normalizeIdentityCandidateQuery;
		// validate early so a bad context yields a stable batch-level error.
		ctxNorm, err := normalizeIdentityContext(in.Queries[i].Context)
		if err != nil {
			return in, err
		}
		in.Queries[i].Context = ctxNorm
	}
	return in, nil
}

func (s *Store) FindIdentityCandidatesBatch(ctx context.Context, in IdentityBatchRequest, u *User) (IdentityBatchResponse, error) {
	in, err := normalizeIdentityBatchRequest(in)
	if err != nil {
		return IdentityBatchResponse{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return IdentityBatchResponse{}, err
	}
	defer tx.Rollback()
	out := IdentityBatchResponse{Results: make([]IdentityBatchResult, 0, len(in.Queries))}
	for _, q := range in.Queries {
		norm, docs, limit, err := normalizeIdentityCandidateQuery(q.IdentityCandidateQuery)
		if err != nil {
			out.Results = append(out.Results, IdentityBatchResult{Key: q.Key, Items: []IdentityCandidateMatch{}, Error: err.Error()})
			continue
		}
		res, err := identityCandidatesFrom(ctx, tx, norm, docs, limit, u)
		if err != nil {
			return IdentityBatchResponse{}, err
		}
		out.Results = append(out.Results, IdentityBatchResult{
			Key: q.Key, Items: res.Items, Total: res.Total, Complete: res.Complete, Basis: res.Basis,
		})
	}
	if err = tx.Commit(); err != nil {
		return IdentityBatchResponse{}, err
	}
	return out, nil
}

type creditPeers struct {
	Creators   []string
	Performers []string
}

func loadCreditPeers(ctx context.Context, q queryer, entityIDs []string) (map[string]creditPeers, error) {
	out := map[string]creditPeers{}
	if len(entityIDs) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT source_id::text, target_id::text, type
		FROM catalog.relations
		WHERE type IN ('created_by','performed_by') AND source_id = ANY($1::uuid[])
		ORDER BY id`, pq.Array(entityIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sourceID, targetID, typ string
		if err = rows.Scan(&sourceID, &targetID, &typ); err != nil {
			return nil, err
		}
		p := out[sourceID]
		switch typ {
		case "created_by":
			p.Creators = append(p.Creators, targetID)
		case "performed_by":
			p.Performers = append(p.Performers, targetID)
		}
		out[sourceID] = p
	}
	return out, rows.Err()
}

func formalTitleNorms(e IdentityCandidateEntity) map[string]bool {
	out := map[string]bool{}
	if n := identityTitle(e.Title); n != "" {
		out[n] = true
	}
	for _, t := range e.Translations {
		if n := identityTitle(t.Title); n != "" {
			out[n] = true
		}
	}
	return out
}

func aliasTitleNorms(e IdentityCandidateEntity) map[string]bool {
	out := map[string]bool{}
	for _, t := range e.Translations {
		for _, a := range t.Aliases {
			if n := identityTitle(a); n != "" {
				out[n] = true
			}
		}
	}
	return out
}

func attributeValues(attrs map[string]any, key string) []string {
	if attrs == nil {
		return nil
	}
	raw, ok := attrs[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, attributeScalar(item))
		}
		return out
	default:
		return []string{attributeScalar(v)}
	}
}

func attributeScalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == 0 {
			return "0"
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(v)
	}
}

func containsExact(values []string, want string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == want {
			return true
		}
	}
	return false
}

func containsFold(values []string, want string) bool {
	n := identityTitle(want)
	for _, v := range values {
		if strings.TrimSpace(v) == want || identityTitle(v) == n {
			return true
		}
	}
	return false
}

func intersectAny(have, want []string) bool {
	set := map[string]bool{}
	for _, id := range have {
		set[id] = true
	}
	for _, id := range want {
		if set[id] {
			return true
		}
	}
	return false
}

func assessMatch(in IdentityCandidateQuery, item IdentityCandidateMatch, peers creditPeers) CandidateAssessment {
	e := item.Matched
	if item.Canonical != nil {
		e = *item.Canonical
	}
	reasons := []string{}
	conflicts := []string{}
	seen := map[string]bool{}
	add := func(code string) {
		if !seen[code] {
			seen[code] = true
			reasons = append(reasons, code)
		}
	}

	formal := formalTitleNorms(e)
	aliases := aliasTitleNorms(e)
	titleExact, aliasHit := false, false
	for _, q := range in.Titles {
		n := identityTitle(q)
		if n == "" {
			continue
		}
		if formal[n] {
			titleExact = true
		} else if aliases[n] {
			aliasHit = true
		}
	}
	if titleExact {
		add("title_exact")
	} else if aliasHit {
		add("alias")
	}

	for _, ext := range in.ExternalIDs {
		if e.ExternalIDs != nil && e.ExternalIDs[ext.Provider] == ext.Value {
			add("external_id")
			break
		}
	}
	for _, attr := range in.Attributes {
		vals := attributeValues(e.Attributes, attr.Key)
		if containsFold(vals, attr.Value) {
			add("attribute")
			break
		}
	}

	summary := map[string]any{}
	if len(peers.Creators) > 0 {
		summary["creator"] = append([]string{}, peers.Creators...)
	}
	if len(peers.Performers) > 0 {
		summary["performer"] = append([]string{}, peers.Performers...)
	}
	if labels := attributeValues(e.Attributes, "publisher"); len(labels) > 0 {
		summary["label"] = labels
	}
	if nums := attributeValues(e.Attributes, "catalog_number"); len(nums) > 0 {
		if len(nums) == 1 {
			summary["catalog_number"] = nums[0]
		} else {
			summary["catalog_number"] = nums
		}
	}

	ctxHits := 0
	if in.Context != nil {
		if len(in.Context.Creator) > 0 {
			if intersectAny(peers.Creators, in.Context.Creator) {
				add("ctx_creator")
				ctxHits++
			} else if len(peers.Creators) > 0 {
				conflicts = append(conflicts, "ctx_creator_mismatch")
			}
		}
		if len(in.Context.Performer) > 0 {
			if intersectAny(peers.Performers, in.Context.Performer) {
				add("ctx_performer")
				ctxHits++
			} else if len(peers.Performers) > 0 {
				conflicts = append(conflicts, "ctx_performer_mismatch")
			}
		}
		if len(in.Context.Label) > 0 {
			labels := attributeValues(e.Attributes, "publisher")
			if intersectAny(labels, in.Context.Label) {
				add("ctx_label")
				ctxHits++
			} else if len(labels) > 0 {
				conflicts = append(conflicts, "ctx_label_mismatch")
			}
		}
		if len(in.Context.CatalogNumber) > 0 {
			nums := attributeValues(e.Attributes, "catalog_number")
			hit := false
			for _, want := range in.Context.CatalogNumber {
				if containsExact(nums, want) {
					hit = true
					break
				}
			}
			if hit {
				add("ctx_catalog_number")
				ctxHits++
			} else if len(nums) > 0 {
				conflicts = append(conflicts, "ctx_catalog_number_mismatch")
			}
		}
	}

	conf := 0.0
	if seen["external_id"] {
		conf += 0.5
	}
	if seen["title_exact"] {
		conf += 0.3
	} else if seen["alias"] {
		conf += 0.15
	}
	if seen["attribute"] {
		conf += 0.2
	}
	conf += 0.1 * float64(ctxHits)
	conf -= 0.25 * float64(len(conflicts))
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}
	conf = float64(int(conf*100+0.5)) / 100

	action := ActionReview
	switch {
	case len(conflicts) > 0:
		action = ActionDistinct
	case seen["external_id"] || (seen["title_exact"] && ctxHits >= 2):
		action = ActionReuse
	}

	a := CandidateAssessment{
		MatchedFields:     reasons,
		Conflicts:         conflicts,
		Confidence:        conf,
		RecommendedAction: action,
	}
	if len(summary) > 0 {
		a.ContextSummary = summary
	}
	return a
}

func attachAssessments(ctx context.Context, q queryer, in IdentityCandidateQuery, out *IdentityCandidateResponse) error {
	ids := make([]string, 0, len(out.Items))
	for _, item := range out.Items {
		id := item.Matched.ID
		if item.Canonical != nil {
			id = item.Canonical.ID
		}
		ids = append(ids, id)
	}
	peers, err := loadCreditPeers(ctx, q, ids)
	if err != nil {
		return err
	}
	for i := range out.Items {
		id := out.Items[i].Matched.ID
		if out.Items[i].Canonical != nil {
			id = out.Items[i].Canonical.ID
		}
		a := assessMatch(in, out.Items[i], peers[id])
		out.Items[i].Assessment = &a
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		ai, aj := out.Items[i].Assessment, out.Items[j].Assessment
		if ai == nil || aj == nil {
			return false
		}
		if ai.Confidence != aj.Confidence {
			return ai.Confidence > aj.Confidence
		}
		return out.Items[i].Matched.ID < out.Items[j].Matched.ID
	})
	return nil
}
