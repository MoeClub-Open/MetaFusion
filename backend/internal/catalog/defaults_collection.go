package catalog

func addCollectionAndStructuralDefinitions(d *Definitions) {
	collectionTypes := Vocabulary{
		Names: names4("集合类型", "集合類型", "コレクション種別", "Collection type"),
		Terms: map[string]Term{
			"franchise":     {Names: names4("跨媒体企划", "跨媒體企劃", "メディアミックス企画", "Media franchise"), Enabled: true},
			"series":        {Names: names4("作品系列", "作品系列", "作品シリーズ", "Work series"), Enabled: true},
			"anthology":     {Names: names4("选集 / 丛书", "選集 / 叢書", "アンソロジー・叢書", "Anthology / imprint series"), Enabled: true},
			"event_catalog": {Names: names4("展会 / 活动特辑", "展會 / 活動特輯", "イベント・特集", "Event / special collection"), Enabled: true},
		},
	}
	d.Vocabularies["collection_type"] = collectionTypes
	d.Fields["collection_type"] = Field{
		Names: collectionTypes.Names, Type: "enum", Vocabulary: "collection_type",
		Enabled: true, Searchable: true, Comparable: true, ApplicableKinds: []string{"collection"},
	}

	empty := []TemplateCondition{}
	collectionSections := []Section{
		{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"collection_type", "language"}},
		{Names: names4("时间范围", "時間範圍", "期間", "Period"), Fields: []string{"begin_date", "end_date"}},
	}
	d.Templates["collection_profile"] = Template{
		Names: names4("集合资料", "集合資料", "コレクション情報", "Collection profile"), Directory: "tree",
		Kinds: []string{"collection"}, Match: &empty, Priority: 10,
		PrimaryDateField: "begin_date", BadgeFields: []string{"collection_type"}, FacetFields: []string{"collection_type"},
		Sections: collectionSections, RelationGroups: []string{"credits", "creative", "membership"},
	}
	for _, sub := range []struct {
		code string
		term string
	}{
		{"collection_franchise", "franchise"},
		{"collection_series", "series"},
		{"collection_anthology", "anthology"},
		{"collection_event_catalog", "event_catalog"},
	} {
		m := []TemplateCondition{{Field: "collection_type", Operator: "equals", Value: sub.term}}
		d.Templates[sub.code] = Template{
			Names: collectionTypes.Terms[sub.term].Names, Directory: "tree",
			Kinds: []string{"collection"}, Match: &m, Priority: 20,
			PrimaryDateField: "begin_date", BadgeFields: []string{"collection_type"}, FacetFields: []string{"collection_type"},
			Sections: collectionSections, RelationGroups: []string{"credits", "creative", "membership"},
		}
	}

	cuEmpty := []TemplateCondition{}
	d.Templates["content_unit_default"] = Template{
		Names: names4("内容单元", "內容單元", "コンテンツ単位", "Content unit"), Directory: "tree",
		Kinds: []string{"content_unit"}, Match: &cuEmpty, Priority: 10,
		PrimaryDateField: "air_date", BadgeFields: []string{"entry_role"}, FacetFields: []string{"entry_role"},
		Sections: []Section{
			{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"entry_role", "air_date", "language"}},
		},
		RelationGroups: []string{"credits", "creative", "membership"},
	}

	exprEmpty := []TemplateCondition{}
	d.Templates["expression_default"] = Template{
		Names: names4("内容表达", "內容表達", "内容表現", "Expression"), Directory: "tree",
		Kinds: []string{"expression"}, Match: &exprEmpty, Priority: 10,
		BadgeFields: []string{"version_label", "isrc"},
		Sections: []Section{
			{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"version_label", "language", "duration", "isrc", "events"}},
		},
		RelationGroups: []string{"credits", "creative", "membership"},
	}

	mediumEmpty := []TemplateCondition{}
	d.Templates["medium_default"] = Template{
		Names: names4("载体", "載體", "キャリア", "Medium"), Directory: "discs",
		Kinds: []string{"medium"}, Match: &mediumEmpty, Priority: 10,
		BadgeFields: []string{"format", "role", "catalog_number"}, FacetFields: []string{"format", "role"},
		Sections: []Section{
			{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"format", "role", "catalog_number"}},
		},
		RelationGroups: []string{"credits", "creative", "membership"},
	}

	trackEmpty := []TemplateCondition{}
	d.Templates["track_default"] = Template{
		Names: names4("收录位置", "收錄位置", "収録位置", "Track"), Directory: "list",
		Kinds: []string{"track"}, Match: &trackEmpty, Priority: 10,
		BadgeFields: []string{"role"}, FacetFields: []string{"role"},
		Sections: []Section{
			{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"role", "duration"}},
		},
		RelationGroups: []string{"credits", "creative", "membership"},
	}
}
