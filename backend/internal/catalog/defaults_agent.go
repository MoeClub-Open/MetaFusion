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
				"end":   {Names: names4("结束日期", "結束日期", "終了日", "End date"), Type: "date", RangeStart: "begin", Enabled: true},
				"ended": {Names: names4("已结束", "已結束", "終了済み", "Ended"), Type: "boolean", Enabled: true},
				"note":  {Names: names4("备注", "備註", "備考", "Note"), Type: "text", Enabled: true},
			}}}
	d.Fields["former_names"] = Field{Names: names4("曾用名 / 历史名义", "曾用名 / 歷史名義", "旧名義・改称履歴", "Former names"), Type: "list", Enabled: true, Searchable: true, Comparable: true,
		ApplicableKinds: append([]string{}, Kinds...), Items: &Field{Names: names4("曾用名", "曾用名", "旧名義", "Former name"), Type: "group", Enabled: true,
			Fields: map[string]Field{
				"name":           {Names: names4("名称", "名稱", "名称", "Name"), Type: "text", Required: true, Enabled: true},
				"language":       {Names: names4("语种", "語種", "言語", "Language"), Type: "text", Enabled: true},
				"nonofficial":    {Names: names4("非官方译名", "非官方譯名", "非公式訳題", "Unofficial title"), Type: "boolean", Enabled: true},
				"begin_date":     {Names: names4("启用日期", "啟用日期", "使用開始日", "Begin date"), Type: "date", Enabled: true},
				"end_date":       {Names: names4("停用日期", "停用日期", "使用終了日", "End date"), Type: "date", RangeStart: "begin_date", Enabled: true},
				"valid_from":     {Names: names4("生效日期", "生效日期", "有効開始日", "Valid from"), Type: "date", Enabled: true},
				"valid_to":       {Names: names4("失效日期", "失效日期", "有効終了日", "Valid to"), Type: "date", RangeStart: "valid_from", Enabled: true},
				"ended":          {Names: names4("已停用", "已停用", "使用終了", "Ended"), Type: "boolean", Enabled: true},
				"date_precision": {Names: names4("时间精度", "時間精度", "日付精度", "Date precision"), Type: "enum", Vocabulary: "date_precision", Enabled: true},
				"earliest_date":  {Names: names4("不早于", "不早於", "最早日", "Earliest date"), Type: "date", Enabled: true},
				"latest_date":    {Names: names4("不晚于", "不晚於", "最遅日", "Latest date"), Type: "date", Enabled: true},
				"region":         {Names: names4("适用地区", "適用地區", "対象地域", "Applicable region"), Type: "text", Enabled: true},
				"platform":       {Names: names4("平台", "平台", "プラットフォーム", "Platform"), Type: "text", Enabled: true},
				"channel":        {Names: names4("渠道明细", "通路明細", "配信・販売チャネル", "Channel detail"), Type: "text", Enabled: true},
				"scoped_release": {Names: names4("关联发行版", "關聯發行版", "対象リリース", "Scoped release"), Type: "entity", Kinds: []string{"release"}, Enabled: true},
				"note":           {Names: names4("备注", "備註", "備考", "Note"), Type: "text", Enabled: true},
			}}}
	match := []TemplateCondition{}
	d.Templates["agent_profile"] = Template{Names: names4("主体资料", "主體資料", "主体情報", "Agent profile"), Directory: "tree", Kinds: []string{"agent"}, Match: &match, Priority: 10,
		BadgeFields: []string{"agent_type"}, FacetFields: []string{"agent_type"},
		Sections: []Section{
			{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type", "occupations", "former_names"}},
			{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"birth_date", "death_date", "formation_date", "dissolution_date"}},
			{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"activity_periods", "attribute_history"}},
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
				{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type", "occupations", "former_names"}},
				{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"birth_date", "death_date"}},
				{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"activity_periods", "attribute_history"}},
			},
		},
		{
			code: "agent_group", term: "group", names: agentTypes.Terms["group"].Names, primary: "formation_date",
			sections: []Section{
				{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type", "occupations", "former_names"}},
				{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"formation_date", "dissolution_date"}},
				{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"activity_periods", "attribute_history"}},
			},
		},
		{
			code: "agent_organization", term: "organization", names: agentTypes.Terms["organization"].Names, primary: "formation_date",
			sections: []Section{
				{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type", "former_names"}},
				{Names: names4("日期", "日期", "日付", "Dates"), Fields: []string{"formation_date", "dissolution_date"}},
				{Names: names4("活动", "活動", "活動", "Activity"), Fields: []string{"activity_periods", "attribute_history"}},
			},
		},
		{
			code: "agent_character", term: "character", names: agentTypes.Terms["character"].Names, primary: "birth_date",
			sections: []Section{
				{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"agent_type", "occupations", "former_names", "attribute_history"}},
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
