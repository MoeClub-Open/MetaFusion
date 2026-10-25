package catalog

// Agent dates describe the subject itself. Work-specific duties remain credits.
func addAgentDefinitions(d *Definitions) {
	for _, item := range []fieldSeed{
		{"birth_date", "date", names4("出生日期", "出生日期", "生年月日", "Birth date")},
		{"death_date", "date", names4("逝世日期", "逝世日期", "没年月日", "Death date")},
		{"formation_date", "date", names4("成立日期", "成立日期", "設立日", "Formation date")},
		{"dissolution_date", "date", names4("解散日期", "解散日期", "解散日", "Dissolution date")},
	} {
		d.Fields[item.code] = Field{Names: item.names, Type: item.typ, Enabled: true, Searchable: true, Comparable: true,
			ApplicableKinds: []string{"agent"}, AllowYearless: item.code == "birth_date"}
	}
	d.Fields["occupations"] = Field{Names: names4("职业", "職業", "職業", "Occupations"), Type: "list", Enabled: true, Searchable: true, Comparable: true,
		ApplicableKinds: []string{"agent"}, Items: &Field{Names: names4("职业", "職業", "職業", "Occupation"), Type: "text", Enabled: true}}
	d.Fields["activity_periods"] = Field{Names: names4("活动时期", "活動時期", "活動期間", "Activity periods"), Type: "list", Enabled: true, Searchable: true, Comparable: true,
		ApplicableKinds: []string{"agent"}, Items: &Field{Names: names4("活动时期", "活動時期", "活動期間", "Activity period"), Type: "group", Enabled: true,
			Fields: map[string]Field{
				"begin": {Names: names4("开始日期", "開始日期", "開始日", "Begin date"), Type: "date", Enabled: true},
				"end":   {Names: names4("结束日期", "結束日期", "終了日", "End date"), Type: "date", Enabled: true},
			}}}
	match := []TemplateCondition{}
	d.Templates["agent_profile"] = Template{Names: names4("主体资料", "主體資料", "主体情報", "Agent profile"), Directory: "tree", Kinds: []string{"agent"}, Match: &match, Priority: 10,
		Sections: []Section{
			{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"birth_date", "death_date", "formation_date", "dissolution_date"}},
			{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"occupations", "activity_periods"}},
		}, RelationGroups: []string{"credits", "creative", "membership"}}
}
