package catalog

import "testing"

// 属性缺省与空对象必须视为同一条边：否则同一逻辑边能被存两行（少写一个键即可绕过判重）。
func TestRelationAttributesNilEqualsEmpty(t *testing.T) {
	if encode(attrsOrEmpty(nil)) != encode(attrsOrEmpty(map[string]any{})) {
		t.Fatalf("归一后仍不相等: %s vs %s", encode(attrsOrEmpty(nil)), encode(attrsOrEmpty(map[string]any{})))
	}
	if encode(attrsOrEmpty(map[string]any{"role": "primary"})) == encode(attrsOrEmpty(nil)) {
		t.Fatal("有属性的边不应与空属性边判为同一条")
	}
	d := Defaults()
	a := Entity{ID: "aaaaaaaa-1111-1111-1111-111111111111", Kind: "work", Title: "A", State: "active", Translations: map[string]Translation{"ja": {Title: "A"}}}
	b := Entity{ID: "bbbbbbbb-2222-2222-2222-222222222222", Kind: "work", Title: "B", State: "active", Translations: map[string]Translation{"ja": {Title: "B"}}}
	// 库里已有的是"属性缺省"那条，写入的是"显式空对象"，两者是同一条边。
	existing := []Relation{{ID: "r1", Type: "adaptation_of", SourceID: a.ID, TargetID: b.ID}}
	candidate := Relation{Type: "adaptation_of", SourceID: a.ID, TargetID: b.ID, Attributes: map[string]any{}}
	ref := func(string, []string) error { return nil }
	if err := validateRelation(d, candidate, a, b, existing, ref, false); err == nil || err.Error() != "duplicate_relation" {
		t.Fatalf("缺省属性与空对象应判为重复边，实际: %v", err)
	}
}
