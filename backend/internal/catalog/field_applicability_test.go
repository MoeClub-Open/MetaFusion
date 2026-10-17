package catalog

import (
	"context"
	"testing"
)

func allowAllRef(string, []string) error { return nil }

func TestFieldApplicabilityUsesKindNotPublicationCategory(t *testing.T) {
	d := Defaults()
	for _, e := range []Entity{
		{Kind: "work", Title: "小说", State: "active", Attributes: map[string]any{"language": "ja", "volume_count": float64(3), "tags": []any{"连载"}}},
		{Kind: "work", Title: "歌曲", State: "active", Attributes: map[string]any{"duration": float64(200), "broadcast_start": "2024-01-01"}},
		{Kind: "release", Title: "发行", State: "active", Attributes: map[string]any{"catalog_number": "ABC-001", "edition_type": "limited", "attachments": []any{map[string]any{"label": map[string]any{"en-US": "Booklet"}, "quantity": float64(1)}}}},
		{Kind: "medium", Title: "载体", State: "active", Attributes: map[string]any{"format": "cd"}},
		{Kind: "track", Title: "条目", State: "active", Attributes: map[string]any{"duration": float64(180), "role": "primary"}},
		{Kind: "expression", Title: "表达", State: "active", Attributes: map[string]any{"duration": float64(90)}},
		{Kind: "content_unit", Title: "章节", State: "active", Attributes: map[string]any{"entry_role": "main", "air_date": "2024-04-05"}},
	} {
		if err := d.validateEntityContent(e, allowAllRef, false); err != nil {
			t.Errorf("kind %s 的适用字段应放行：%v", e.Kind, err)
		}
	}
}

func TestFieldApplicabilityRejectsForeignAndUnscopedFields(t *testing.T) {
	d := Defaults()
	for _, tc := range []struct{ kind, key string }{
		{"work", "catalog_number"},
		{"expression", "catalog_number"},
		{"release", "entry_role"},
		{"track", "isbn"},
		{"medium", "broadcast_start"},
		{"agent", "duration"},
	} {
		e := Entity{Kind: tc.kind, Title: "X", State: "active", Attributes: map[string]any{tc.key: "value"}}
		if err := d.validateEntityContent(e, allowAllRef, false); err == nil || err.Error() != "unknown_field: "+tc.key {
			t.Errorf("kind %s 的 %s 应报 unknown_field，实际 %v", tc.kind, tc.key, err)
		}
	}
	for _, kind := range Kinds {
		if err := d.validateEntityContent(Entity{Kind: kind, Title: "X", State: "active", Attributes: map[string]any{"tags": []any{"开放标签"}}}, allowAllRef, false); err != nil {
			t.Errorf("kind %s 的自由标签应放行：%v", kind, err)
		}
	}
	d.Fields["relation_only"] = Field{Names: names("仅关系", "Relation only"), Type: "text", Enabled: true}
	if err := d.validateEntityContent(Entity{Kind: "work", Title: "X", State: "active", Attributes: map[string]any{"relation_only": "x"}}, allowAllRef, false); err == nil || err.Error() != "unknown_field: relation_only" {
		t.Fatalf("没有 applicable_kinds 的字段不得直接写入实体：%v", err)
	}
}

func TestNewFieldApplicabilityChangesWithoutCodeChange(t *testing.T) {
	d := Defaults()
	d.Fields["custom_mood"] = Field{Names: names("自定义心情", "Custom mood"), Type: "text", ApplicableKinds: []string{"work"}, Enabled: true, Searchable: true}
	if !contains(d.attributeKeys("work"), "custom_mood") || contains(d.attributeKeys("release"), "custom_mood") {
		t.Fatalf("字段适用层级未按声明生效：work=%v release=%v", d.attributeKeys("work"), d.attributeKeys("release"))
	}
	if err := d.validateEntityContent(Entity{Kind: "work", Title: "W", State: "active", Attributes: map[string]any{"custom_mood": "blue"}}, allowAllRef, false); err != nil {
		t.Fatalf("新字段应随定义自动可写：%v", err)
	}
	if err := d.validateEntityContent(Entity{Kind: "release", Title: "R", State: "active", Attributes: map[string]any{"custom_mood": "blue"}}, allowAllRef, false); err == nil || err.Error() != "unknown_field: custom_mood" {
		t.Fatalf("非适用 kind 必须拒绝新字段：%v", err)
	}
}

func TestOptionalGroupDoesNotRequireUnfilledMetadata(t *testing.T) {
	for _, tc := range []struct {
		name     string
		required bool
		value    any
		want     string
	}{
		{name: "optional group omitted"},
		{name: "optional group null map", value: map[string]any(nil)},
		{name: "optional group supplied", value: map[string]any{"label": "First edition"}},
		{name: "supplied group requires its child", value: map[string]any{}, want: "required_field"},
		{name: "supplied nested group requires its child", value: map[string]any{"label": "First edition", "details": map[string]any{}}, want: "required_field"},
		{name: "required group omitted", required: true, want: "required_field"},
		{name: "group still requires an object", value: "First edition", want: "expected_object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Defaults()
			d.Fields["publication_statement"] = Field{
				Names: names("出版说明", "Publication statement"), Type: "group",
				ApplicableKinds: []string{"work"}, Enabled: true, Required: tc.required,
				Fields: map[string]Field{
					"label": {Names: names("名称", "Label"), Type: "text", Enabled: true, Required: true},
					"details": {Names: names("详情", "Details"), Type: "group", Enabled: true, Fields: map[string]Field{
						"note": {Names: names("说明", "Note"), Type: "text", Enabled: true, Required: true},
					}},
				},
			}
			e := Entity{Kind: "work", Title: "Work", State: "active"}
			if tc.value != nil {
				e.Attributes = map[string]any{"publication_statement": tc.value}
			}
			err := d.validateEntityContent(e, allowAllRef, false)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("editing unrelated metadata failed: %v", err)
				}
			} else if err == nil || err.Error() != "publication_statement: "+tc.want {
				t.Fatalf("got %v, want publication_statement: %s", err, tc.want)
			}
		})
	}
}

func TestSchemeMatchingUsesKindAndMediumFormat(t *testing.T) {
	d := Defaults()
	d.Schemes["test_track_path"] = Scheme{Names: names("测试定位", "Test locator"), Slot: "locator", Kinds: []string{"track"}, Fields: []string{"relative_to", "path"}, Enabled: true}
	if got := d.matchSchemes("locator", "track"); len(got) != 1 {
		t.Fatalf("track 应命中定位方案：%v", got)
	}
	if got := d.matchSchemes("locator", "work"); len(got) != 0 {
		t.Fatalf("work 不应命中 track 定位方案：%v", got)
	}
}

func TestPostgresGenericFieldsRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	w := f.save(Entity{Kind: "work", Title: "混合形态作品", Attributes: map[string]any{"language": "ja", "duration": float64(200), "volume_count": float64(3), "tags": []any{"跨媒介"}}})
	r := f.save(Entity{Kind: "release", Title: "发行", Subjects: []Subject{{WorkID: w.ID, Role: "primary"}}, Attributes: map[string]any{"catalog_number": "XYZ-001", "edition_type": "limited", "attachments": []any{map[string]any{"label": map[string]any{"en-US": "Booklet"}, "quantity": float64(2)}}}})
	m := f.save(Entity{Kind: "medium", Title: "CD", ReleaseID: r.ID, Attributes: map[string]any{"format": "cd"}})
	x := f.save(Entity{Kind: "expression", Title: "录音", WorkID: w.ID, Attributes: map[string]any{"duration": float64(200)}})
	tr := f.save(Entity{Kind: "track", Title: "第一轨", MediumID: m.ID, Number: "A1", Contents: []Inclusion{{ExpressionID: x.ID, Position: 0, Locator: Locator{"relative_to": "track", "time_start_ms": float64(0), "time_end_ms": float64(30000)}}}, Attributes: map[string]any{"duration": float64(200)}})
	gw, err := f.s.Get(ctx, w.ID, &f.u)
	if err != nil || gw.Attributes["language"] != "ja" || gw.Attributes["volume_count"] != float64(3) || gw.Attributes["duration"] != float64(200) {
		t.Fatalf("作品字段回读不一致：%v %+v", err, gw.Attributes)
	}
	gr, err := f.s.Get(ctx, r.ID, &f.u)
	if err != nil || gr.Attributes["catalog_number"] != "XYZ-001" || len(gr.Subjects) != 1 || gr.Subjects[0].WorkID != w.ID {
		t.Fatalf("发行字段或收录回读不一致：%v %+v", err, gr)
	}
	gt, err := f.s.Get(ctx, tr.ID, &f.u)
	if err != nil || len(gt.Contents) != 1 || gt.Contents[0].ExpressionID != x.ID || gt.Contents[0].Locator["relative_to"] != "track" {
		t.Fatalf("收录定位回读不一致：%v %+v", err, gt)
	}
}
