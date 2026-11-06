package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// EntityLink is a read-only projection of one canonical structural or semantic
// fact. Its key is stable for rendering, but only relation: keys are writable
// relation UUIDs; callers edit fixed links through their owning entity.
type EntityLink struct {
	Key        string         `json:"key"`
	Version    *int64         `json:"version,omitempty"`
	RuleCode   string         `json:"rule_code"`
	Class      string         `json:"class"`
	Direction  string         `json:"direction"`
	SourceID   string         `json:"source_id"`
	TargetID   string         `json:"target_id"`
	Position   int            `json:"position"`
	Role       string         `json:"role,omitempty"`
	Field      string         `json:"field,omitempty"`
	Locator    Locator        `json:"locator,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	// References retain the original field path, including nested groups/lists.
	// Via identifies all paths that refer to subject_id when it is not an endpoint.
	References []RelationshipReference `json:"references,omitempty"`
	Via        []string                `json:"via,omitempty"`
}

type RelationshipReference struct {
	Field    string `json:"field"`
	EntityID string `json:"entity_id"`
}

type EntityLinksPage struct {
	SubjectID      string            `json:"subject_id"`
	DefinitionETag string            `json:"definition_etag"`
	Items          []EntityLink      `json:"items"`
	Entities       map[string]Entity `json:"entities"`
	Limit          int               `json:"limit"`
	Offset         int               `json:"offset"`
	HasMore        bool              `json:"has_more"`
}

type fixedEntityLinkBranch struct {
	code  string
	class string
	sql   string
}

// Each branch reads the existing source of truth. The endpoint never persists
// copies of these edges in catalog.relations or a second structural table.
var fixedEntityLinkBranches = []fixedEntityLinkBranch{
	{
		code: "structure:work_content_unit", class: "ownership",
		sql: `SELECT 'structure:work_content_unit' AS code, x.work_id::text AS source_id, x.id::text AS target_id,
  coalesce((e.document->>'position')::int,0) AS position, ''::text AS role, '{}'::jsonb AS locator, '{}'::jsonb AS attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.content_units x JOIN catalog.entities e ON e.id=x.id WHERE x.work_id=$1::uuid OR x.id=$1::uuid`,
	},
	{
		code: "structure:work_expression", class: "ownership",
		sql: `SELECT 'structure:work_expression' AS code, x.work_id::text AS source_id, x.id::text AS target_id,
  coalesce((e.document->>'position')::int,0) AS position, ''::text AS role, '{}'::jsonb AS locator, '{}'::jsonb AS attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.expressions x JOIN catalog.entities e ON e.id=x.id WHERE x.work_id=$1::uuid OR x.id=$1::uuid`,
	},
	{
		code: "structure:release_medium", class: "ownership",
		sql: `SELECT 'structure:release_medium' AS code, x.release_id::text AS source_id, x.id::text AS target_id,
  coalesce((e.document->>'position')::int,0) AS position, ''::text AS role, '{}'::jsonb AS locator, '{}'::jsonb AS attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.mediums x JOIN catalog.entities e ON e.id=x.id WHERE x.release_id=$1::uuid OR x.id=$1::uuid`,
	},
	{
		code: "structure:medium_track", class: "ownership",
		sql: `SELECT 'structure:medium_track' AS code, x.medium_id::text AS source_id, x.id::text AS target_id,
  coalesce((e.document->>'position')::int,0) AS position, ''::text AS role, '{}'::jsonb AS locator, '{}'::jsonb AS attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.tracks x JOIN catalog.entities e ON e.id=x.id WHERE x.medium_id=$1::uuid OR x.id=$1::uuid`,
	},
	{
		code: "structure:content_unit_parent", class: "placement",
		sql: `SELECT 'structure:content_unit_parent' AS code, x.parent_id::text AS source_id, x.id::text AS target_id,
  coalesce((e.document->>'position')::int,0) AS position, ''::text AS role, '{}'::jsonb AS locator, '{}'::jsonb AS attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.content_units x JOIN catalog.entities e ON e.id=x.id WHERE x.parent_id IS NOT NULL AND (x.parent_id=$1::uuid OR x.id=$1::uuid)`,
	},
	{
		code: "structure:content_unit_expression", class: "placement",
		sql: `SELECT 'structure:content_unit_expression' AS code, x.content_unit_id::text AS source_id, x.id::text AS target_id,
  coalesce((e.document->>'position')::int,0) AS position, ''::text AS role, '{}'::jsonb AS locator, '{}'::jsonb AS attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.expressions x JOIN catalog.entities e ON e.id=x.id WHERE x.content_unit_id IS NOT NULL AND (x.content_unit_id=$1::uuid OR x.id=$1::uuid)`,
	},
	{
		code: "structure:medium_parent", class: "placement",
		sql: `SELECT 'structure:medium_parent' AS code, x.parent_id::text AS source_id, x.id::text AS target_id,
  coalesce((e.document->>'position')::int,0) AS position, ''::text AS role, '{}'::jsonb AS locator, '{}'::jsonb AS attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.mediums x JOIN catalog.entities e ON e.id=x.id WHERE x.parent_id IS NOT NULL AND (x.parent_id=$1::uuid OR x.id=$1::uuid)`,
	},
	{
		code: "structure:track_parent", class: "placement",
		sql: `SELECT 'structure:track_parent' AS code, x.parent_id::text AS source_id, x.id::text AS target_id,
  coalesce((e.document->>'position')::int,0) AS position, ''::text AS role, '{}'::jsonb AS locator, '{}'::jsonb AS attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.tracks x JOIN catalog.entities e ON e.id=x.id WHERE x.parent_id IS NOT NULL AND (x.parent_id=$1::uuid OR x.id=$1::uuid)`,
	},
	{
		code: "structure:release_subject", class: "inclusion",
		sql: `SELECT 'structure:release_subject' AS code, x.release_id::text AS source_id, x.work_id::text AS target_id,
  x.position, x.role, '{}'::jsonb AS locator, x.attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.release_subjects x WHERE x.release_id=$1::uuid OR x.work_id=$1::uuid OR (/* subject references */ false)`,
	},
	{
		code: "structure:track_content", class: "inclusion",
		sql: `SELECT 'structure:track_content' AS code, x.track_id::text AS source_id, x.expression_id::text AS target_id,
  x.position, ''::text AS role, x.locator, x.attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path
  FROM catalog.track_contents x WHERE x.track_id=$1::uuid OR x.expression_id=$1::uuid OR (/* content references */ false)`,
	},
}

const semanticEntityLinksBranchSQL = `SELECT 'relation:' || r.type AS code, r.source_id::text AS source_id, r.target_id::text AS target_id,
  coalesce((r.document->>'position')::int,0) AS position, ''::text AS role, '{}'::jsonb AS locator,
  coalesce(r.document->'attributes','{}'::jsonb) AS attributes, r.id::text AS relation_id, r.document AS relation_document, ''::text AS field_path
  FROM catalog.relations r WHERE r.source_id=$1::uuid OR r.target_id=$1::uuid
   OR (/* attribute references */ false)`

const emptyEntityLinksBranchSQL = `SELECT ''::text AS code, NULL::text AS source_id, NULL::text AS target_id,
  0::int AS position, ''::text AS role, '{}'::jsonb AS locator, '{}'::jsonb AS attributes,
  ''::text AS relation_id, NULL::jsonb AS relation_document, ''::text AS field_path WHERE false`

// Both endpoint visibility checks run before pagination. Attribute candidates
// are compiled from published field definitions, then checked against the
// actual relation rule before they can occupy a visible page slot.
const entityLinksPageSQL = `
SELECT l.code, l.source_id::text, l.target_id::text, l.position, l.role,
 l.locator, l.attributes, l.relation_id, l.relation_document,l.field_path
FROM (
 /* entity link branches */
) AS l(code, source_id, target_id, position, role, locator, attributes, relation_id, relation_document, field_path)
JOIN catalog.entities source ON source.id=l.source_id::uuid
JOIN catalog.entities target ON target.id=l.target_id::uuid
WHERE ($2::boolean OR source.state='active' OR source.created_by=$3::uuid)
 AND ($2::boolean OR target.state='active' OR target.created_by=$3::uuid)
 AND ($6='both' OR ($6='outgoing' AND l.source_id=$1::text)
  OR ($6='incoming' AND l.source_id<>$1::text AND (l.target_id=$1::text OR l.relation_id<>''
   OR l.code IN ('structure:release_subject','structure:track_content'))))
 AND ($7::text[] IS NULL OR l.code=ANY($7::text[]))
 AND ($8::text[] IS NULL OR
  (l.source_id=$1::text AND target.kind=ANY($8::text[])) OR
  (l.target_id=$1::text AND source.kind=ANY($8::text[])) OR
  (l.source_id<>$1::text AND l.target_id<>$1::text AND
   (source.kind=ANY($8::text[]) OR target.kind=ANY($8::text[]))))
ORDER BY l.code, l.position, l.source_id, l.target_id, l.role, l.relation_id,l.field_path
LIMIT $4 OFFSET $5`

func (s *Store) EntityLinks(ctx context.Context, id string, limit, offset int, u *User) (EntityLinksPage, error) {
	return s.EntityLinksFiltered(ctx, id, limit, offset, relationshipFilters{Direction: "both"}, u)
}

func (s *Store) EntityLinksFiltered(ctx context.Context, id string, limit, offset int, filters relationshipFilters, u *User) (EntityLinksPage, error) {
	if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
		return EntityLinksPage{}, fmt.Errorf("invalid_id")
	}
	var err error
	filters, err = normalizeRelationshipLinkFilters(filters)
	if err != nil {
		return EntityLinksPage{}, err
	}
	self, err := s.Get(ctx, id, u)
	if err != nil {
		return EntityLinksPage{}, err
	}
	defs, err := s.Definitions(ctx)
	if err != nil {
		return EntityLinksPage{}, err
	}
	if err = validateRelationshipRuleCodes(filters.RuleCodes, defs.Document); err != nil {
		return EntityLinksPage{}, err
	}
	if len(filters.Classes) == 0 {
		filters.Classes = nil
	}
	if len(filters.RuleCodes) == 0 {
		filters.RuleCodes = nil
	}
	if len(filters.PeerKinds) == 0 {
		filters.PeerKinds = nil
	}
	page, err := entityLinksFrom(ctx, s.DB, self.ID, limit, offset, u, defs, filters)
	if err != nil {
		return page, err
	}
	page.Entities[self.ID] = self
	ids := make([]string, 0, len(page.Items)*2)
	for _, link := range page.Items {
		ids = append(ids, link.SourceID, link.TargetID)
		for _, ref := range link.References {
			ids = append(ids, ref.EntityID)
		}
	}
	peers, err := s.GetManyVisible(ctx, ids, u)
	if err != nil {
		return page, err
	}
	// Guard against a visibility change between the SQL page and endpoint fetch.
	filtered := page.Items[:0]
	for _, link := range page.Items {
		source, sourceOK := peers[link.SourceID]
		target, targetOK := peers[link.TargetID]
		if !sourceOK || !targetOK {
			continue
		}
		allReferencesVisible := true
		for _, ref := range link.References {
			if _, ok := peers[ref.EntityID]; !ok {
				allReferencesVisible = false
				break
			}
		}
		if !allReferencesVisible {
			continue
		}
		filtered = append(filtered, link)
		page.Entities[link.SourceID] = source
		page.Entities[link.TargetID] = target
		for _, ref := range link.References {
			page.Entities[ref.EntityID] = peers[ref.EntityID]
		}
	}
	page.Items = filtered
	return page, nil
}

type relationshipFilters struct {
	Direction string
	Classes   []string
	RuleCodes []string
	PeerKinds []string
}

// entityLinksFrom is shared by single-subject links and the bounded Agent query.
// Filters and endpoint visibility are applied in SQL before visible pagination.
func entityLinksFrom(ctx context.Context, q queryer, id string, limit, offset int, u *User, defs DefinitionConfig, filters relationshipFilters) (EntityLinksPage, error) {
	page := EntityLinksPage{SubjectID: id, Items: []EntityLink{}, Entities: map[string]Entity{}, Limit: limit, Offset: offset}
	page.DefinitionETag = defs.ETag
	manage := u != nil && u.Can(PermissionLifecycleManage)
	userID := "00000000-0000-0000-0000-000000000000"
	if u != nil {
		userID = u.ID
	}
	// Only record-bearing links and semantic links need reference validation.
	// Plain structural pages can apply their visible offset in SQL directly.
	// The general path still counts after validation so hidden references cannot
	// occupy a page slot; a per-query cache batches references for each window.
	const window = 200
	seen, dbOffset := 0, 0
	batchSize := window
	if sqlPaginatedRelationshipFilters(filters) {
		seen, dbOffset, batchSize = offset, offset, limit+1
	}
	refCache := relationshipReferenceCache{entities: map[string]Entity{}, known: map[string]bool{}}
	for len(page.Items) <= limit {
		args := []any{id, manage, userID, batchSize, dbOffset, filters.Direction, pq.Array(filters.RuleCodes), pq.Array(filters.PeerKinds)}
		query := entityLinksQuery(defs.Document, id, filters, &args)
		rows, err := q.QueryContext(ctx, query, args...)
		if err != nil {
			return page, err
		}
		type candidate struct {
			link       EntityLink
			relationID string
			relation   Relation
			refSites   []refSite
		}
		candidates := make([]candidate, 0, batchSize)
		allRefSites := []refSite{}
		for rows.Next() {
			var link EntityLink
			var loc, attrs, relationDoc []byte
			var relationID string
			if err = rows.Scan(&link.RuleCode, &link.SourceID, &link.TargetID, &link.Position, &link.Role, &loc, &attrs, &relationID, &relationDoc, &link.Field); err != nil {
				rows.Close()
				return page, err
			}
			if err = json.Unmarshal(loc, &link.Locator); err != nil {
				rows.Close()
				return page, err
			}
			if err = json.Unmarshal(attrs, &link.Attributes); err != nil {
				rows.Close()
				return page, err
			}
			item := candidate{link: link, relationID: relationID}
			if relationID != "" {
				if err = json.Unmarshal(relationDoc, &item.relation); err != nil {
					rows.Close()
					return page, err
				}
				for _, site := range defs.Document.relationRefSites(item.relation) {
					if site.kind == kindRelationAttribute {
						item.refSites = append(item.refSites, site)
					}
				}
			} else {
				switch link.RuleCode {
				case "structure:release_subject":
					collectRefSites(defs.Document.Fields["subject_attributes"], "attributes", link.Attributes, "entity", link.SourceID, kindAttribute, &item.refSites)
				case "structure:track_content":
					collectRefSites(defs.Document.Fields["locator"], "locator", map[string]any(link.Locator), "entity", link.SourceID, kindAttribute, &item.refSites)
					collectRefSites(defs.Document.Fields["inclusion_attributes"], "attributes", link.Attributes, "entity", link.SourceID, kindAttribute, &item.refSites)
				}
			}
			allRefSites = append(allRefSites, item.refSites...)
			candidates = append(candidates, item)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return page, err
		}
		rows.Close()
		if err := refCache.load(ctx, q, allRefSites, u); err != nil {
			return page, err
		}
		for _, candidate := range candidates {
			link, relationID := candidate.link, candidate.relationID
			if relationID != "" {
				relation := candidate.relation
				rule, ok := defs.Document.Relations[relation.Type]
				if !ok || defs.Document.attributes(rule.Fields, relation.Attributes, refCache.validate, true) != nil {
					continue
				}
				link.Class, link.Key = "semantic", relationID
				version := relation.Version
				link.Version = &version
			} else if strings.HasPrefix(link.RuleCode, "attribute:") {
				link.Class = "reference"
				link.Key = link.RuleCode + ":" + link.SourceID + ":" + link.Field + ":" + link.TargetID
				link.References = []RelationshipReference{{Field: link.Field, EntityID: link.TargetID}}
			} else {
				var ok bool
				link.Class, ok = fixedLinkClass(link.RuleCode)
				if !ok {
					return page, fmt.Errorf("unknown fixed relationship rule: %s", link.RuleCode)
				}
				link.Key = fixedLinkKey(link)
				// Record-level references can become hidden after a structural fact
				// was saved. Neither their IDs nor their locators may leak on read.
				ref := refCache.validate
				switch link.RuleCode {
				case "structure:release_subject":
					if defs.Document.value(defs.Document.Fields["subject_attributes"], link.Attributes, ref, true) != nil {
						continue
					}
				case "structure:track_content":
					if defs.Document.value(defs.Document.Fields["locator"], map[string]any(link.Locator), ref, true) != nil ||
						defs.Document.value(defs.Document.Fields["inclusion_attributes"], link.Attributes, ref, true) != nil {
						continue
					}
				}
			}
			if link.SourceID != id && link.TargetID != id {
				for _, site := range candidate.refSites {
					if parsed, parseErr := uuid.Parse(site.value); parseErr == nil && parsed.String() == id {
						link.Via = append(link.Via, site.field)
					}
				}
				// Containment only locates candidates. The actual declared reference
				// paths must confirm a semantic or record-level third participant.
				if len(link.Via) == 0 {
					continue
				}
			}
			for _, site := range candidate.refSites {
				parsed, _ := uuid.Parse(site.value) // All reference sites passed validation.
				link.References = append(link.References, RelationshipReference{Field: site.field, EntityID: parsed.String()})
			}
			if link.SourceID == id {
				link.Direction = "outgoing"
			} else {
				link.Direction = "incoming"
			}
			if seen >= offset {
				page.Items = append(page.Items, link)
			}
			seen++
			if len(page.Items) > limit {
				page.HasMore = true
				page.Items = page.Items[:limit]
				break
			}
		}
		if page.HasMore || len(candidates) < batchSize {
			break
		}
		dbOffset += len(candidates)
	}
	return page, nil
}

// Reference containment shapes preserve groups and list elements and can use
// the existing relations_document_attributes_gin index. Arbitrary JSON strings
// are not references; only paths declared by a live relation definition qualify.
func entityLinksQuery(d Definitions, id string, filters relationshipFilters, args *[]any) string {
	recordReferences := func(column string, f Field) string {
		clauses := []string{}
		for _, pattern := range relationshipReferencePatterns(f, id) {
			*args = append(*args, encode(pattern))
			clauses = append(clauses, column+" @> $"+strconv.Itoa(len(*args))+"::jsonb")
		}
		if len(clauses) == 0 {
			return "false"
		}
		return strings.Join(clauses, " OR ")
	}
	branches := []string{}
	for _, branch := range fixedEntityLinkBranches {
		if len(filters.Classes) > 0 && !contains(filters.Classes, branch.class) {
			continue
		}
		if len(filters.RuleCodes) > 0 && !contains(filters.RuleCodes, branch.code) {
			continue
		}
		branchSQL := branch.sql
		switch branch.code {
		case "structure:release_subject":
			branchSQL = strings.Replace(branchSQL, "/* subject references */ false", recordReferences("x.attributes", d.Fields["subject_attributes"]), 1)
		case "structure:track_content":
			contentReferences := recordReferences("x.locator", d.Fields["locator"]) + " OR " + recordReferences("x.attributes", d.Fields["inclusion_attributes"])
			branchSQL = strings.Replace(branchSQL, "/* content references */ false", contentReferences, 1)
		}
		branches = append(branches, branchSQL)
	}
	semanticAllowed := len(filters.Classes) == 0 || contains(filters.Classes, "semantic")
	if semanticAllowed && len(filters.RuleCodes) > 0 {
		semanticAllowed = false
		for _, code := range filters.RuleCodes {
			if strings.HasPrefix(code, "relation:") {
				semanticAllowed = true
				break
			}
		}
	}
	if semanticAllowed {
		patterns := map[string]bool{}
		for _, rule := range d.Relations {
			for _, code := range rule.Fields {
				for _, value := range relationshipReferencePatterns(d.Fields[code], id) {
					patterns[encode(map[string]any{code: value})] = true
				}
			}
		}
		ordered := make([]string, 0, len(patterns))
		for pattern := range patterns {
			ordered = append(ordered, pattern)
		}
		sort.Strings(ordered)
		clauses := []string{}
		for _, pattern := range ordered {
			*args = append(*args, pattern)
			clauses = append(clauses, "r.document->'attributes' @> $"+strconv.Itoa(len(*args))+"::jsonb")
		}
		condition := "false"
		if len(clauses) > 0 {
			condition = strings.Join(clauses, " OR ")
		}
		branches = append(branches, strings.Replace(semanticEntityLinksBranchSQL, "/* attribute references */ false", condition, 1))
	}
	unionSQL := emptyEntityLinksBranchSQL
	attrSQL := attributeEntityLinksSQL(d, filters, args)
	if len(branches) > 0 {
		unionSQL = strings.Join(branches, "\n UNION ALL\n ") + attrSQL
	} else if attrSQL != "" {
		unionSQL = strings.TrimPrefix(strings.TrimSpace(attrSQL), "UNION ALL")
	}
	return strings.Replace(entityLinksPageSQL, "/* entity link branches */", unionSQL, 1)
}

func relationshipReferencePatterns(f Field, id string) []any {
	switch f.Type {
	case "entity":
		return []any{id}
	case "group":
		out := []any{}
		for _, code := range sortedFieldKeys(f) {
			for _, pattern := range relationshipReferencePatterns(f.Fields[code], id) {
				out = append(out, map[string]any{code: pattern})
			}
		}
		return out
	case "list":
		if f.Items != nil {
			out := []any{}
			for _, pattern := range relationshipReferencePatterns(*f.Items, id) {
				out = append(out, []any{pattern})
			}
			return out
		}
	}
	return nil
}

func sqlPaginatedRelationshipFilters(filters relationshipFilters) bool {
	if len(filters.Classes) > 0 {
		direct := true
		for _, class := range filters.Classes {
			if class != "ownership" && class != "placement" && class != "reference" {
				direct = false
				break
			}
		}
		if direct {
			return true
		}
	}
	if len(filters.RuleCodes) == 0 {
		return false
	}
	for _, code := range filters.RuleCodes {
		if strings.HasPrefix(code, "attribute:") {
			continue // Definition-derived leaves are fully filtered in SQL.
		}
		if _, ok := fixedLinkClass(code); !ok || code == "structure:release_subject" || code == "structure:track_content" {
			return false
		}
	}
	return true
}

// The cache holds only identity/visibility columns. Validation never loads the
// full structural record or performs one database call per referenced field.
type relationshipReferenceCache struct {
	entities map[string]Entity
	known    map[string]bool
}

func (c *relationshipReferenceCache) load(ctx context.Context, q queryer, sites []refSite, u *User) error {
	ids := []string{}
	for _, site := range sites {
		if c.known[site.value] {
			continue
		}
		c.known[site.value] = true
		if _, err := uuid.Parse(site.value); err == nil {
			ids = append(ids, site.value)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.QueryContext(ctx, "SELECT id::text,kind,state,created_by::text FROM catalog.entities WHERE id=ANY($1::uuid[])", pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entity
		if err = rows.Scan(&e.ID, &e.Kind, &e.State, &e.CreatedBy); err != nil {
			return err
		}
		if visible(e, u) && e.State != "deleted" && e.State != "merged" {
			c.entities[e.ID] = e
		}
	}
	return rows.Err()
}

func (c *relationshipReferenceCache) validate(id string, kinds []string) error {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("invalid_reference")
	}
	e, ok := c.entities[parsed.String()]
	if !ok || !contains(kinds, e.Kind) {
		return fmt.Errorf("invalid_reference")
	}
	return nil
}

func fixedLinkClass(code string) (string, bool) {
	for _, rule := range fixedRelationshipRules() {
		if rule.Code == code {
			return rule.Class, true
		}
	}
	return "", false
}

func fixedLinkKey(link EntityLink) string {
	key := link.RuleCode + ":" + link.SourceID + ":" + link.TargetID
	switch link.RuleCode {
	case "structure:release_subject":
		return key + ":" + link.Role // primary key includes role
	case "structure:track_content":
		return fmt.Sprintf("%s:%s:%d", link.RuleCode, link.SourceID, link.Position) // primary key is track + position
	default:
		return key // moving a sibling does not change the edge's identity
	}
}
