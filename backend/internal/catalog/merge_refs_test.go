package catalog

import (
	"testing"
)

func TestMergeExternalLinksDoesNotDiscardMetadata(t *testing.T) {
	base := linkFromID("bangumi", "7")
	changed := base
	changed.Role = "official"
	if _, err := mergeExternalLinks([]ExternalLink{base}, []ExternalLink{changed}); err == nil {
		t.Fatal("same URL with different metadata was silently discarded")
	}
}

// M04：内部幂等键不复制——源 merged 行保留它做别名，目标复制即撞唯一索引
// （external_links_metafusion_import_key 覆盖 merged/deleted 行）；其它键沿用
// 缺键补齐/同值幂等/异值冲突的旧口径。
func TestMergeExternalLinksSkipsInternalKey(t *testing.T) {
	got, err := mergeExternalLinks(
		[]ExternalLink{linkFromID("bangumi", "7")},
		[]ExternalLink{
			linkFromID("metafusion_import", "bangumi:subject:7"),
			linkFromID("bangumi", "7"),
			linkFromID("official_website", "https://example.com/"),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range got {
		if link.Kind == "metafusion_import" {
			t.Fatalf("内部幂等键不得复制到目标：%v", got)
		}
	}
	byKind := map[string]ExternalLink{}
	for _, link := range got {
		byKind[link.Kind] = link
	}
	if byKind["bangumi"].ExternalID != "7" || byKind["official_website"].URL != "https://example.com" {
		t.Fatalf("普通键应补齐：%v", got)
	}
	if _, err = mergeExternalLinks(got, []ExternalLink{linkFromID("bangumi", "7")}); err != nil {
		t.Fatalf("同值重复合并应幂等：%v", err)
	}
	if _, err = mergeExternalLinks(got, []ExternalLink{linkFromID("bangumi", "8")}); err == nil {
		t.Fatal("同键异值必须报 merge_external_conflict")
	}
	if out, err := mergeExternalLinks(nil, nil); err != nil || out != nil {
		t.Fatalf("空源应原样返回，实际 %v %v", out, err)
	}
}

// M03：只认 definitions 声明的 entity 引用路径（含嵌套组/列表），
// 自由文本里的巧合计入不算，未知关系类型不算。
func TestRelationReferencesID(t *testing.T) {
	d := Defaults()
	voiced := Relation{ID: "r1", Type: "voiced_by", SourceID: "w", TargetID: "p",
		Attributes: map[string]any{"character": "char-1", "scope": "为 char-1 配音"}}
	if !relationReferencesID(d, voiced, "char-1") {
		t.Fatal("character 属性引用必须命中")
	}
	if relationReferencesID(d, voiced, "char-2") {
		t.Fatal("未引用 ID 不得命中")
	}
	// scope 是自由文本：值里出现 ID 也不算引用。
	if relationReferencesID(d, Relation{Type: "voiced_by",
		Attributes: map[string]any{"scope": "char-1"}}, "char-1") {
		t.Fatal("自由文本巧合不得算引用")
	}
	if relationReferencesID(d, Relation{Type: "no_such_type",
		Attributes: map[string]any{"character": "char-1"}}, "char-1") {
		t.Fatal("未知关系类型不得命中")
	}
	// 嵌套组/列表路径同样覆盖。
	nested := Defaults()
	nested.Fields["crew"] = Field{Names: names("制作组", "Crew"), Type: "group", Enabled: true, Fields: map[string]Field{
		"lead": {Names: names("负责人", "Lead"), Type: "entity", Kinds: []string{"agent"}, Enabled: true},
		"note": {Names: names("备注", "Note"), Type: "text", Enabled: true},
	}}
	nested.Fields["teams"] = Field{Names: names("分组", "Teams"), Type: "list", Enabled: true, Items: &Field{
		Names: names("组", "Team"), Type: "group", Enabled: true, Fields: map[string]Field{
			"lead": {Names: names("负责人", "Lead"), Type: "entity", Kinds: []string{"agent"}, Enabled: true},
		}}}
	nested.Relations["test_made_by"] = RelationDefinition{
		Names: names("制作", "Made by"), ReverseNames: names("制作了", "Made"),
		SourceKinds: []string{"work"}, TargetKinds: []string{"agent"},
		Fields: []string{"crew", "teams"}, Enabled: true,
	}
	if !relationReferencesID(nested, Relation{Type: "test_made_by",
		Attributes: map[string]any{"crew": map[string]any{"lead": "agent-9"}}}, "agent-9") {
		t.Fatal("嵌套组 entity 引用必须命中")
	}
	if relationReferencesID(nested, Relation{Type: "test_made_by",
		Attributes: map[string]any{"crew": map[string]any{"note": "agent-9"}}}, "agent-9") {
		t.Fatal("嵌套组自由文本不得算引用")
	}
	if !relationReferencesID(nested, Relation{Type: "test_made_by",
		Attributes: map[string]any{"teams": []any{map[string]any{"lead": "agent-9"}}}}, "agent-9") {
		t.Fatal("列表嵌套 entity 引用必须命中")
	}
}
