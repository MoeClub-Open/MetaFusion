package catalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/lib/pq"
)

type attributeReferenceSegment struct {
	code string
	list bool
}

type attributeReferenceProjection struct {
	rule     RelationshipRule
	field    Field
	segments []attributeReferenceSegment
}

// ReadRelationshipRules derives attribute references from the same published
// field definitions that govern entity writes. These are read-only views of
// existing JSONB facts, never a second editable relationship store.
func ReadRelationshipRules(d Definitions) []RelationshipRule {
	out := RelationshipRules(d)
	for _, projection := range attributeReferenceProjections(d) {
		out = append(out, projection.rule)
	}
	return out
}

func attributeReferenceProjections(d Definitions) []attributeReferenceProjection {
	keys := make([]string, 0, len(d.Fields))
	for code := range d.Fields {
		keys = append(keys, code)
	}
	sort.Strings(keys)
	out := []attributeReferenceProjection{}
	var walk func(Field, string, []attributeReferenceSegment, []string, bool, bool)
	walk = func(f Field, path string, segments []attributeReferenceSegment, sourceKinds []string, enabled, ordered bool) {
		enabled = enabled && f.Enabled
		switch f.Type {
		case "entity":
			maxOutgoing := 1
			if ordered {
				maxOutgoing = 0
			}
			out = append(out, attributeReferenceProjection{
				rule: RelationshipRule{Code: "attribute:" + path, Class: "reference", Names: f.Names, ReverseNames: f.Names,
					SourceKinds: sourceKinds, TargetKinds: f.Kinds, MaxOutgoing: maxOutgoing, Ordered: ordered, ReadOnly: true, Enabled: enabled},
				field: f, segments: segments,
			})
		case "group":
			for _, code := range sortedFieldKeys(f) {
				next := append(append([]attributeReferenceSegment{}, segments...), attributeReferenceSegment{code: code})
				walk(f.Fields[code], path+"."+code, next, sourceKinds, enabled, ordered)
			}
		case "list":
			if f.Items != nil {
				next := append(append([]attributeReferenceSegment{}, segments...), attributeReferenceSegment{list: true})
				walk(*f.Items, path+"[]", next, sourceKinds, enabled, true)
			}
		}
	}
	for _, code := range keys {
		f := d.Fields[code]
		if len(f.ApplicableKinds) == 0 {
			continue
		}
		walk(f, code, []attributeReferenceSegment{{code: code}}, f.ApplicableKinds, true, false)
	}
	return out
}

func attributeEntityLinksSQL(d Definitions, filters relationshipFilters, args *[]any) string {
	bind := func(value any) string {
		*args = append(*args, value)
		return "$" + strconv.Itoa(len(*args))
	}
	branches := []string{}
	for _, projection := range attributeReferenceProjections(d) {
		if len(filters.RuleCodes) > 0 && !contains(filters.RuleCodes, projection.rule.Code) {
			continue
		}
		value, path, position := "e.document->'attributes'", "", "0"
		joins := []string{}
		for i, segment := range projection.segments {
			if segment.list {
				alias := "attribute_item_" + strconv.Itoa(i)
				joins = append(joins, "CROSS JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof("+value+")='array' THEN "+value+" ELSE '[]'::jsonb END) WITH ORDINALITY AS "+alias+"(value,ordinality)")
				value = alias + ".value"
				position = "(" + alias + ".ordinality-1)::int"
				path = "(" + path + " || '[' || (" + alias + ".ordinality-1)::text || ']')"
			} else {
				code := bind(segment.code)
				value = "(" + value + "->" + code + "::text)"
				if path == "" {
					path = code + "::text"
				} else {
					path = "(" + path + " || '.' || " + code + "::text)"
				}
			}
		}
		// The guard precedes the cast: malformed historical JSON cannot turn an
		// otherwise valid public query into a database error.
		text := "(" + value + " #>> '{}')"
		targetID := "CASE WHEN jsonb_typeof(" + value + ")='string' AND " + text + " ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN " + text + "::uuid END"
		var pattern any = (*args)[0]
		for i := len(projection.segments) - 1; i >= 0; i-- {
			segment := projection.segments[i]
			if segment.list {
				pattern = []any{pattern}
			} else {
				pattern = map[string]any{segment.code: pattern}
			}
		}
		candidate := "e.id=$1::uuid"
		if filters.Direction != "outgoing" {
			incomingCandidate := "e.document->'attributes' @> " + bind(encode(pattern)) + "::jsonb"
			candidate = "(e.id=$1::uuid OR " + incomingCandidate + ")"
			if filters.Direction == "incoming" {
				candidate = incomingCandidate
			}
		}
		branches = append(branches, fmt.Sprintf(`
 UNION ALL
 SELECT %s::text,e.id::text,ref_target.id::text,%s,''::text,'{}'::jsonb,'{}'::jsonb,''::text,NULL::jsonb,%s
 FROM catalog.entities e %s
 JOIN catalog.entities ref_target ON ref_target.id=%s
 WHERE %s AND (e.id=$1::uuid OR ref_target.id=$1::uuid)
  AND e.kind=ANY(%s::text[]) AND ref_target.kind=ANY(%s::text[])
  AND ref_target.state NOT IN ('deleted','merged')`, bind(projection.rule.Code), position, path, strings.Join(joins, "\n"), targetID, candidate, bind(pq.Array(projection.rule.SourceKinds)), bind(pq.Array(projection.field.Kinds))))
	}
	return strings.Join(branches, "\n")
}
