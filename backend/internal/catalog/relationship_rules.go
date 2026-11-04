package catalog

import "sort"

// RelationshipRule describes one readable edge. Fixed structural rules are
// read-only views over their existing foreign keys or inclusion tables; ordinary
// relations remain governed by the published definitions document.
var RelationshipClasses = []string{"ownership", "placement", "inclusion", "semantic", "reference"}

type RelationshipRule struct {
	Code         string   `json:"code"`
	Class        string   `json:"class"`
	Names        Names    `json:"names"`
	ReverseNames Names    `json:"reverse_names"`
	SourceKinds  []string `json:"source_kinds"`
	TargetKinds  []string `json:"target_kinds"`
	MaxIncoming  int      `json:"max_incoming"`
	MaxOutgoing  int      `json:"max_outgoing"`
	Ordered      bool     `json:"ordered"`
	Scope        string   `json:"scope,omitempty"`
	ReadOnly     bool     `json:"read_only"`
	Enabled      bool     `json:"enabled"`
}

func fixedRelationshipRules() []RelationshipRule {
	row := func(code, class, source, target string, maxIncoming int, ordered bool, scope string) RelationshipRule {
		return RelationshipRule{
			Code: "structure:" + code, Class: class,
			SourceKinds: []string{source}, TargetKinds: []string{target},
			MaxIncoming: maxIncoming, Ordered: ordered, Scope: scope, ReadOnly: true, Enabled: true,
		}
	}
	return []RelationshipRule{
		row("work_content_unit", "ownership", "work", "content_unit", 1, true, "work"),
		row("work_expression", "ownership", "work", "expression", 1, true, "work"),
		row("release_medium", "ownership", "release", "medium", 1, true, "release"),
		row("medium_track", "ownership", "medium", "track", 1, true, "medium"),
		row("content_unit_parent", "placement", "content_unit", "content_unit", 1, true, "work"),
		row("content_unit_expression", "placement", "content_unit", "expression", 1, true, "work"),
		row("medium_parent", "placement", "medium", "medium", 1, true, "release"),
		row("track_parent", "placement", "track", "track", 1, true, "medium"),
		row("release_subject", "inclusion", "release", "work", 0, true, ""),
		row("track_content", "inclusion", "track", "expression", 0, true, ""),
	}
}

// Structural facts still live in their foreign keys/inclusion tables. Only
// their wording is editable in the published definition document.
func structuralRuleNames(d Definitions, code string) (Names, Names) {
	if code == "structure:release_subject" {
		r := d.Structure["release"]
		return r.SubjectNames, r.SubjectReverseNames
	}
	if code == "structure:track_content" {
		r := d.Structure["track"]
		return r.ContentNames, r.ContentReverseNames
	}
	refs := map[string][2]string{
		"structure:work_content_unit":       {"content_unit", "work_id"},
		"structure:work_expression":         {"expression", "work_id"},
		"structure:release_medium":          {"medium", "release_id"},
		"structure:medium_track":            {"track", "medium_id"},
		"structure:content_unit_parent":     {"content_unit", "parent_id"},
		"structure:content_unit_expression": {"expression", "content_unit_id"},
		"structure:medium_parent":           {"medium", "parent_id"},
		"structure:track_parent":            {"track", "parent_id"},
	}
	ref := refs[code]
	for _, field := range d.Structure[ref[0]].Fields {
		if field.Code == ref[1] {
			return field.Names, field.ReverseNames
		}
	}
	return nil, nil
}

// RelationshipRules is the single public registry for structural and semantic
// edge codes. A structure: code cannot collide with an administrator's relation
// code; reverse names are presentation labels rather than second edge types.
func RelationshipRules(d Definitions) []RelationshipRule {
	out := fixedRelationshipRules()
	for i := range out {
		out[i].Names, out[i].ReverseNames = structuralRuleNames(d, out[i].Code)
	}
	codes := make([]string, 0, len(d.Relations))
	for code := range d.Relations {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		r := d.Relations[code]
		out = append(out, RelationshipRule{
			Code: "relation:" + code, Class: "semantic", Names: r.Names, ReverseNames: r.ReverseNames,
			SourceKinds: r.SourceKinds, TargetKinds: r.TargetKinds,
			MaxIncoming: r.MaxIncoming, MaxOutgoing: r.MaxOutgoing, Ordered: true, Scope: r.Scope, Enabled: r.Enabled,
		})
	}
	return out
}
