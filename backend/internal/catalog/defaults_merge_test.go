package catalog

import "testing"

// 合并必须只增不改：已有键（含被人工改过的）原样保留，只补种子新增的键；重复执行不再产生新增。
func TestMergeSeedDefinitionsIsAdditiveOnly(t *testing.T) {
	seed := Defaults()
	// 现状：只保留一半关系，并把其中一个关系改名（模拟后台的人工调整）
	cur := Definitions{
		Fields:       map[string]Field{},
		Vocabularies: map[string]Vocabulary{},
		Relations:    map[string]RelationDefinition{},
		Templates:    map[string]Template{},
		Schemes:      map[string]Scheme{},
	}
	kept := seed.Relations["directed_by"]
	kept.Names = names4("被人工改过的导演名", "被人工改過的導演名", "手で直した監督名", "Edited by hand")
	cur.Relations["directed_by"] = kept

	merged, added := mergeSeedDefinitions(cur, seed)
	if len(added) == 0 {
		t.Fatal("应当补入缺失的关系码，实际没有新增")
	}
	if got := merged.Relations["directed_by"].Names["zh-CN"]; got != "被人工改过的导演名" {
		t.Fatalf("已存在的关系被覆盖了：%s", got)
	}
	if _, ok := merged.Relations["member_of"]; !ok {
		t.Fatal("种子里新增的 member_of 没有被补进当前定义")
	}
	// 幂等：把合并结果再合并一次，不应再有任何新增
	_, again := mergeSeedDefinitions(merged, seed)
	if len(again) != 0 {
		t.Fatalf("第二次合并不应有新增，实际 %v", again)
	}
}

func TestMergeSeedDefinitionsUpgradesLegacyAgentProfile(t *testing.T) {
	seed := Defaults()
	legacy := Defaults()
	delete(legacy.Fields, "agent_type")
	delete(legacy.Vocabularies, "agent_type")
	delete(legacy.Templates, "agent_person")
	delete(legacy.Templates, "agent_group")
	delete(legacy.Templates, "agent_organization")
	delete(legacy.Templates, "agent_character")
	emptyMatch := []TemplateCondition{}
	legacy.Templates["agent_profile"] = Template{
		Names:          names4("主体资料", "主體資料", "主体情報", "Agent profile"),
		Directory:      "tree",
		Kinds:          []string{"agent"},
		Match:          &emptyMatch,
		Priority:       10,
		RelationGroups: []string{"credits", "creative", "membership"},
		Sections: []Section{
			{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"birth_date", "death_date", "formation_date", "dissolution_date"}},
			{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"occupations", "activity_periods"}},
		},
	}

	merged, added := mergeSeedDefinitions(legacy, seed)
	if err := merged.Validate(); err != nil {
		t.Fatalf("合并后定义校验失败：%v", err)
	}
	if len(added) == 0 {
		t.Fatal("应当补入 agent_type 字段与细分模板")
	}
	profile := merged.Templates["agent_profile"]
	if !contains(profile.BadgeFields, "agent_type") || !contains(profile.FacetFields, "agent_type") {
		t.Fatalf("存量 agent_profile 未补入 agent_type 徽章/分面：%#v", profile)
	}
	if len(profile.Sections) != 3 || !contains(profile.Sections[0].Fields, "agent_type") {
		t.Fatalf("存量 agent_profile 未前置 Basics(agent_type) 分区：%#v", profile.Sections)
	}
	if contains(profile.Sections[0].Fields, "occupations") {
		t.Fatalf("occupations 已在 Activity 分区，不应被重复补入 Basics：%#v", profile.Sections)
	}

	if _, again := mergeSeedDefinitions(merged, seed); len(again) != 0 {
		t.Fatalf("第二次合并不应再产生变更：%v", again)
	}

	// 后台管理员后续主动从 agent_profile 移除 agent_type 时，合并不得再夺回。
	customized := merged
	editedProfile := customized.Templates["agent_profile"]
	editedProfile.BadgeFields = nil
	editedProfile.FacetFields = nil
	editedProfile.Sections = editedProfile.Sections[1:]
	customized.Templates["agent_profile"] = editedProfile
	afterCustom, third := mergeSeedDefinitions(customized, seed)
	if len(third) != 0 || len(afterCustom.Templates["agent_profile"].BadgeFields) != 0 {
		t.Fatalf("管理员移除后的模板配置不应被再次覆盖：%v", third)
	}
}
