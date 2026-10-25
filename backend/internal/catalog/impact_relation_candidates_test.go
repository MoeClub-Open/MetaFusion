package catalog

import (
	"fmt"
	"testing"
)

func TestRelationReplayCandidatesPreserveConstraints(t *testing.T) {
	base := RelationDefinition{SourceKinds: []string{"work"}, TargetKinds: []string{"work"}, Enabled: true}
	symmetric, outgoing, incoming, ordered, cycle, usage := base, base, base, base, base, base
	symmetric.Symmetric = true
	outgoing.MaxOutgoing, incoming.MaxIncoming = 1, 1
	ordered.UniquePosition = true
	cycle.Acyclic, cycle.CycleGroup = true, "composition"
	usage.MaxOutgoing, usage.Usage = 1, "release_group"
	d := Definitions{Relations: map[string]RelationDefinition{
		"plain": base, "symmetric": symmetric, "outgoing": outgoing, "incoming": incoming,
		"ordered": ordered, "cycle": cycle, "cycle_peer": cycle, "usage": usage, "usage_peer": usage,
	}}
	edge := func(id, typ, source, target string) Relation {
		return Relation{ID: id, Type: typ, SourceID: source, TargetID: target, Position: 1}
	}
	for _, tc := range []struct {
		name, typ, want string
		existing        []Relation
	}{
		{"forward duplicate", "plain", "duplicate_relation", []Relation{edge("old", "plain", "a", "b")}},
		{"unrelated", "plain", "<nil>", []Relation{edge("old", "plain", "c", "d")}},
		{"reverse duplicate", "symmetric", "duplicate_relation", []Relation{edge("old", "symmetric", "b", "a")}},
		{"outgoing limit", "outgoing", "cardinality_exceeded", []Relation{edge("old", "outgoing", "a", "c")}},
		{"incoming limit", "incoming", "cardinality_exceeded", []Relation{edge("old", "incoming", "c", "b")}},
		{"position", "ordered", "duplicate_relation_position", []Relation{edge("old", "ordered", "a", "c")}},
		{"shared usage", "usage", "cardinality_exceeded", []Relation{edge("old", "usage_peer", "a", "c")}},
		{"cross code cycle", "cycle", "relation_cycle", []Relation{edge("one", "cycle", "b", "c"), edge("two", "cycle_peer", "c", "a")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := edge("current", tc.typ, "a", "b")
			all := append([]Relation{edge("noise", "plain", "x", "y"), r}, tc.existing...)
			src, tgt := Entity{ID: "a", Kind: "work"}, Entity{ID: "b", Kind: "work"}
			full := validateRelation(d, r, src, tgt, all, nil, true)
			selected := relationReplayCandidates(d, all)(r)
			indexed := validateRelation(d, r, src, tgt, selected, nil, true)
			if fmt.Sprint(full) != tc.want || fmt.Sprint(indexed) != tc.want {
				t.Fatalf("full=%v indexed=%v, want %s", full, indexed, tc.want)
			}
		})
	}
}
