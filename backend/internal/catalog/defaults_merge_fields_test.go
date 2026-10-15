package catalog

import (
	"context"
	"testing"
)

// 种子新增字段需连同适用 kind 一起补入；现有字段的人工设置不得覆盖。
func TestMergeSeedDefinitionsAddsFieldsAndApplicableKinds(t *testing.T) {
	seed := Defaults()
	cur := Defaults()
	// 当前定义缺少 air_date，且已有字段少了种子新增的适用层级。
	delete(cur.Fields, "air_date")
	role := cur.Fields["role"]
	role.ApplicableKinds = []string{"medium"}
	cur.Fields["role"] = role
	cur.Fields["custom_local_field"] = Field{Names: names("自定义字段", "Custom field"), Type: "text", ApplicableKinds: []string{"work"}, Enabled: true}

	merged, added := mergeSeedDefinitions(cur, seed)
	if _, ok := merged.Fields["air_date"]; !ok {
		t.Fatal("字段表未补入 air_date")
	}
	if !contains(merged.Fields["air_date"].ApplicableKinds, "content_unit") {
		t.Fatalf("air_date 未携带 content_unit 适用层级：%v", merged.Fields["air_date"].ApplicableKinds)
	}
	if !contains(merged.Fields["role"].ApplicableKinds, "track") || !contains(merged.Fields["custom_local_field"].ApplicableKinds, "work") {
		t.Fatalf("种子适用层级或人工字段丢失：role=%v custom=%v", merged.Fields["role"].ApplicableKinds, merged.Fields["custom_local_field"].ApplicableKinds)
	}
	found := false
	for _, a := range added {
		if a == "fields.role.applicable_kinds.track" {
			found = true
		}
	}
	if !found {
		t.Fatalf("字段适用层级的补丁未记入新增项：%v", added)
	}
	// 幂等：把合并结果再合并一次，不应再有任何新增。
	if _, again := mergeSeedDefinitions(merged, seed); len(again) != 0 {
		t.Fatalf("第二次合并不应有新增，实际 %v", again)
	}
}

// 真库：存量实例（已发布定义里没有 air_date）经 EnsureSeedDefinitions 后，
// 新字段出现在存量文档里并能真正写进篇目；重复合并不产生第二个定义版本。
func TestPostgresSeedMergeBackfillsAirDateIntoExistingDocument(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	definitionsCount := func() int {
		t.Helper()
		var n int
		if err := f.s.DB.QueryRowContext(ctx, "SELECT count(*) FROM catalog.definition_config").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// 已发布定义暂时缺少 air_date，重新合并后应补入完整字段声明。
	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := v.Document
	delete(old.Fields, "air_date")
	f.publish(old, v.ETag)

	work := f.save(Entity{Kind: "work", Title: "动画作品"})
	newUnit := func() Entity {
		return Entity{Kind: "content_unit", WorkID: work.ID, Title: "第1话", Number: "1", Position: 1,
			Status: "active", Translations: map[string]Translation{"ja": {Title: "第1话"}},
			Attributes: map[string]any{"language": "ja", "entry_role": "main", "air_date": "2017-01-21"}}
	}
	// 反证：合并之前写 air_date 必须被校验拒绝，否则本用例证明不了合并的作用。
	if _, err := f.s.Save(ctx, Edit{Entity: newUnit(), EditNote: "写未声明字段", Sources: fixtureSources()}, f.u); err == nil {
		t.Fatal("存量文档未声明 air_date 时，写入篇目属性本应被 unknown_field 拒绝")
	}

	if err := f.s.EnsureSeedDefinitions(ctx); err != nil {
		t.Fatal(err)
	}
	v2, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v2.ETag == v.ETag {
		t.Fatal("合并后应产生新的定义版本")
	}
	if _, ok := v2.Document.Fields["air_date"]; !ok {
		t.Fatal("合并后存量文档的字段表里没有 air_date")
	}
	if !contains(v2.Document.Fields["air_date"].ApplicableKinds, "content_unit") {
		t.Fatalf("合并后 air_date 未适用 content_unit：%v", v2.Document.Fields["air_date"].ApplicableKinds)
	}

	// 合并之后同一份载荷必须真的能落库并读回。
	saved, err := f.s.Save(ctx, Edit{Entity: newUnit(), EditNote: "回填放送日", Sources: fixtureSources()}, f.u)
	if err != nil {
		t.Fatalf("合并后写入带 air_date 的篇目失败：%v", err)
	}
	got, err := f.s.Get(ctx, saved.ID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attributes["air_date"] != "2017-01-21" {
		t.Fatalf("air_date 未落库：%v", got.Attributes)
	}

	// 幂等：再合并一次既没有新增项，也不产生第二个定义版本。
	before := definitionsCount()
	if err := f.s.EnsureSeedDefinitions(ctx); err != nil {
		t.Fatal(err)
	}
	v3, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v3.ETag != v2.ETag {
		t.Fatalf("重复合并换了 etag：%s -> %s", v2.ETag, v3.ETag)
	}
	if after := definitionsCount(); after != before {
		t.Fatalf("重复合并新增了定义行：%d -> %d", before, after)
	}
}
