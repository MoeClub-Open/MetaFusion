package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

type entityLookup func(string) (Entity, error)

func scopeKinds(scope string) []string {
	switch scope {
	case "work":
		return []string{"work", "content_unit", "expression"}
	case "release":
		return []string{"release", "medium", "track"}
	case "medium":
		return []string{"medium", "track"}
	}
	return nil
}

func kindsWithin(kinds, allowed []string) bool {
	for _, kind := range kinds {
		if !contains(allowed, kind) {
			return false
		}
	}
	return len(kinds) > 0
}

func (d Definitions) validateRelationConstraints(code string, r RelationDefinition) error {
	if r.Scope != "" && (!kindsWithin(r.SourceKinds, scopeKinds(r.Scope)) || !kindsWithin(r.TargetKinds, scopeKinds(r.Scope))) {
		return fmt.Errorf("invalid_relation_scope: %s", code)
	}
	if r.CycleGroup != "" && (!codePattern.MatchString(r.CycleGroup) || !r.Acyclic) {
		return fmt.Errorf("invalid_cycle_group: %s", code)
	}
	switch r.Usage {
	case "":
	case "expression_composition":
		if !kindsWithin(r.SourceKinds, []string{"expression"}) || !kindsWithin(r.TargetKinds, []string{"expression"}) ||
			r.Scope != "work" || !r.Acyclic || r.CycleGroup != "expression_composition" || !r.Aggregate || !r.UniquePosition || r.Symmetric {
			return fmt.Errorf("invalid_relation_usage: %s", code)
		}
	case "release_group":
		if !kindsWithin(r.SourceKinds, []string{"release"}) || !kindsWithin(r.TargetKinds, []string{"work", "collection"}) ||
			r.MaxOutgoing != 1 || r.Symmetric {
			return fmt.Errorf("invalid_relation_usage: %s", code)
		}
	default:
		return fmt.Errorf("invalid_relation_usage: %s", code)
	}
	for field, rule := range r.ReferenceScopes {
		endpoint, scope, ok := strings.Cut(rule, "_")
		f, present := d.Fields[field]
		kinds := r.SourceKinds
		if endpoint == "target" {
			kinds = r.TargetKinds
		}
		if !ok || (endpoint != "source" && endpoint != "target") || !present || f.Type != "entity" ||
			!contains(r.Fields, field) || !kindsWithin(kinds, scopeKinds(scope)) || !kindsWithin(f.Kinds, scopeKinds(scope)) {
			return fmt.Errorf("invalid_reference_scope: %s.%s", code, field)
		}
	}
	return nil
}

// Scopes are single ownership domains. A compilation's subjects do not make it
// a member of one Work, and cannot be used to infer a version group.
func relationScope(e Entity, scope string, lookup entityLookup) (string, error) {
	switch scope {
	case "work":
		if e.Kind == "work" {
			return e.ID, nil
		}
		if e.Kind == "content_unit" || e.Kind == "expression" {
			return e.WorkID, nil
		}
	case "release":
		if e.Kind == "release" {
			return e.ID, nil
		}
		if e.Kind == "medium" {
			return e.ReleaseID, nil
		}
		if e.Kind == "track" && lookup != nil {
			medium, err := lookup(e.MediumID)
			if err != nil {
				return "", err
			}
			if medium.Kind == "medium" {
				return medium.ReleaseID, nil
			}
		}
	case "medium":
		if e.Kind == "medium" {
			return e.ID, nil
		}
		if e.Kind == "track" {
			return e.MediumID, nil
		}
	}
	return "", nil
}

func validateRelationScopes(rt RelationDefinition, r Relation, src, tgt Entity, lookup entityLookup) error {
	if rt.Scope != "" {
		a, err := relationScope(src, rt.Scope, lookup)
		if err != nil {
			return err
		}
		b, err := relationScope(tgt, rt.Scope, lookup)
		if err != nil {
			return err
		}
		if a == "" || a != b {
			return fmt.Errorf("relation_scope_mismatch")
		}
	}
	for field, rule := range rt.ReferenceScopes {
		id, _ := r.Attributes[field].(string)
		if strings.TrimSpace(id) == "" {
			continue
		}
		if lookup == nil {
			return fmt.Errorf("invalid_reference")
		}
		referenced, err := lookup(id)
		if err != nil {
			return err
		}
		endpoint, scope, _ := strings.Cut(rule, "_")
		owner := src
		if endpoint == "target" {
			owner = tgt
		}
		a, err := relationScope(owner, scope, lookup)
		if err != nil {
			return err
		}
		b, err := relationScope(referenced, scope, lookup)
		if err != nil {
			return err
		}
		if a == "" || a != b {
			return fmt.Errorf("relation_reference_scope_mismatch: %s", field)
		}
	}
	return nil
}

func sharesCycleGroup(d Definitions, a, b string) bool {
	if a == b {
		return true
	}
	ar, br := d.Relations[a], d.Relations[b]
	return ar.CycleGroup != "" && ar.CycleGroup == br.CycleGroup
}

// Fetch exactly the rule's dependency set. Cross-code constraints must use the
// same set during ordinary saves, definition impact and merge replay.
func relationsForRule(ctx context.Context, q queryer, d Definitions, typ string) ([]Relation, error) {
	rt := d.Relations[typ]
	codes := []string{typ}
	for code, other := range d.Relations {
		if code != typ && (sharesCycleGroup(d, typ, code) || rt.Usage != "" && rt.Usage == other.Usage) {
			codes = append(codes, code)
		}
	}
	rows, err := q.QueryContext(ctx, "SELECT r.document FROM catalog.relations r "+
		"JOIN catalog.entities s ON s.id=r.source_id JOIN catalog.entities t ON t.id=r.target_id "+
		"WHERE s.state NOT IN ('deleted','merged') AND t.state NOT IN ('deleted','merged') AND r.type IN ("+
		entityPlaceholders(codes, 1)+") ORDER BY r.id", entityArgs(codes)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelationDocuments(rows)
}

// Ordinary saves need adjacent edges for counts/duplicates/positions, and only
// the target's reachable graph for cycle checks. Definition and merge replay
// still use relationsForRule when they intentionally validate the entire rule.
func relationsForSave(ctx context.Context, q queryer, d Definitions, r Relation) ([]Relation, error) {
	rt := d.Relations[r.Type]
	countCodes := []string{r.Type}
	cycleCodes := []string{}
	for code, other := range d.Relations {
		if code != r.Type && rt.Usage != "" && other.Usage == rt.Usage {
			countCodes = append(countCodes, code)
		}
		if rt.Acyclic && sharesCycleGroup(d, r.Type, code) {
			cycleCodes = append(cycleCodes, code)
		}
	}
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE reachable(id) AS (
		SELECT $2::uuid
		UNION
		SELECT edge.target_id FROM reachable walk
		JOIN catalog.relations edge ON edge.source_id=walk.id
		JOIN catalog.entities src ON src.id=edge.source_id
		JOIN catalog.entities tgt ON tgt.id=edge.target_id
		WHERE edge.type=ANY($5::text[]) AND edge.id IS DISTINCT FROM $3::uuid
		AND src.state NOT IN ('deleted','merged') AND tgt.state NOT IN ('deleted','merged')
	)
	SELECT edge.document FROM catalog.relations edge
	JOIN catalog.entities src ON src.id=edge.source_id
	JOIN catalog.entities tgt ON tgt.id=edge.target_id
	WHERE edge.id IS DISTINCT FROM $3::uuid
	AND src.state NOT IN ('deleted','merged') AND tgt.state NOT IN ('deleted','merged')
	AND ((edge.type=ANY($4::text[]) AND (edge.source_id=$1::uuid OR edge.target_id=$2::uuid
		OR (edge.source_id=$2::uuid AND edge.target_id=$1::uuid)))
		OR (edge.type=ANY($5::text[]) AND edge.source_id IN (SELECT id FROM reachable)))
	ORDER BY edge.id`, r.SourceID, r.TargetID, nullable(r.ID), pq.Array(countCodes), pq.Array(cycleCodes))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelationDocuments(rows)
}

func scanRelationDocuments(rows *sql.Rows) ([]Relation, error) {
	out := []Relation{}
	for rows.Next() {
		var b []byte
		var r Relation
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
