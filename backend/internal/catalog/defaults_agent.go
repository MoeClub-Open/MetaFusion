package catalog

// Agent dates describe the subject itself. Work-specific duties remain credits.
func addAgentDefinitions(d *Definitions) {
	agentTypes := Vocabulary{
		Names: names4("主体类型", "主體類型", "主体種別", "Agent type"),
		Terms: map[string]Term{
			"person":       {Names: names4("个人", "個人", "個人", "Person"), Enabled: true},
			"group":        {Names: names4("团体 / 组合", "團體 / 組合", "グループ・ユニット", "Group / unit"), Enabled: true},
			"organization": {Names: names4("机构 / 公司", "機構 / 公司", "法人・団体", "Organization / company"), Enabled: true},
			"character":    {Names: names4("虚构角色", "虛構角色", "架空のキャラクター", "Fictional character"), Enabled: true},
		},
	}
	d.Vocabularies["agent_type"] = agentTypes
	d.Fields["agent_type"] = Field{
		Names: agentTypes.Names, Type: "enum", Vocabulary: "agent_type",
		Enabled: true, Searchable: true, Comparable: true, ApplicableKinds: []string{"agent"},
	}
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
		BadgeFields: []string{"agent_type"}, FacetFields: []string{"agent_type"},
		Sections: []Section{
			{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type", "occupations"}},
			{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"birth_date", "death_date", "formation_date", "dissolution_date"}},
			{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"activity_periods"}},
		}, RelationGroups: []string{"credits", "creative", "membership"}}
	for _, sub := range []struct {
		code     string
		term     string
		names    Names
		primary  string
		sections []Section
	}{
		{
			code: "agent_person", term: "person", names: agentTypes.Terms["person"].Names, primary: "birth_date",
			sections: []Section{
				{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type", "occupations"}},
				{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"birth_date", "death_date"}},
				{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"activity_periods"}},
			},
		},
		{
			code: "agent_group", term: "group", names: agentTypes.Terms["group"].Names, primary: "formation_date",
			sections: []Section{
				{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type", "occupations"}},
				{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"formation_date", "dissolution_date"}},
				{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"activity_periods"}},
			},
		},
		{
			code: "agent_organization", term: "organization", names: agentTypes.Terms["organization"].Names, primary: "formation_date",
			sections: []Section{
				{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type"}},
				{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"formation_date", "dissolution_date"}},
				{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"activity_periods"}},
			},
		},
		{
			code: "agent_character", term: "character", names: agentTypes.Terms["character"].Names, primary: "birth_date",
			sections: []Section{
				{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type", "occupations"}},
				{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"birth_date"}},
			},
		},
	} {
		m := []TemplateCondition{{Field: "agent_type", Operator: "equals", Value: sub.term}}
		d.Templates[sub.code] = Template{
			Names: sub.names, Directory: "tree", Kinds: []string{"agent"}, Match: &m, Priority: 20,
			PrimaryDateField: sub.primary, BadgeFields: []string{"agent_type"}, FacetFields: []string{"agent_type"},
			Sections: sub.sections, RelationGroups: []string{"credits", "creative", "membership"},
		}
	}
}
