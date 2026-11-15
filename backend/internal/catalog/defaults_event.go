package catalog

func addEventDefinitions(d *Definitions) {
	eventTypes := Vocabulary{
		Names: names4("活动类型", "活動類型", "イベント種別", "Event type"),
		Terms: map[string]Term{
			"concert":         {Names: names4("演唱会 / 音乐会", "演唱會 / 音樂會", "コンサート・ライブ", "Concert / live"), Enabled: true},
			"tour":            {Names: names4("巡回演出", "巡迴演出", "ツアー", "Tour"), Enabled: true},
			"festival":        {Names: names4("音乐节 / 庆典", "音樂節 / 慶典", "フェス・音楽祭", "Festival"), Enabled: true},
			"convention":      {Names: names4("展会 / 同人即卖会", "展會 / 同人即賣會", "展示会・即売会", "Convention / doujin fair"), Enabled: true},
			"stage":           {Names: names4("舞台剧 / 朗读剧", "舞台劇 / 朗讀劇", "舞台・朗読劇", "Stage / reading drama"), Enabled: true},
			"broadcast_event": {Names: names4("特别节目 / 直播活动", "特別節目 / 直播活動", "特別番組・配信イベント", "Broadcast / streaming event"), Enabled: true},
			"award":           {Names: names4("奖项 / 颁奖礼", "獎項 / 頒獎禮", "賞・授賞式", "Award / ceremony"), Enabled: true},
			"exhibition":      {Names: names4("展览 / 联动展", "展覽 / 聯動展", "展覧会・コラボ展示", "Exhibition"), Enabled: true},
		},
	}
	d.Vocabularies["event_type"] = eventTypes
	d.Fields["event_type"] = Field{
		Names: eventTypes.Names, Type: "enum", Vocabulary: "event_type",
		Enabled: true, Searchable: true, Comparable: true, ApplicableKinds: []string{"event"},
	}
	d.Fields["venue"] = Field{
		Names: names4("场馆 / 会场", "場館 / 會場", "会場", "Venue"), Type: "text",
		Enabled: true, Searchable: true, Comparable: true, ApplicableKinds: []string{"event"},
	}
	d.Fields["location"] = Field{
		Names: names4("举办地点", "舉辦地點", "開催地", "Location"), Type: "text",
		Enabled: true, Searchable: true, Comparable: true, ApplicableKinds: []string{"event"},
	}
	d.Fields["cancelled"] = Field{
		Names: names4("已取消", "已取消", "開催中止", "Cancelled"), Type: "boolean",
		Enabled: true, Searchable: true, Comparable: true, ApplicableKinds: []string{"event"},
	}

	empty := []TemplateCondition{}
	eventSections := []Section{
		{Names: names4("基本信息", "基本資訊", "基本情報", "Basics"), Fields: []string{"event_type", "venue", "location", "country", "language", "cancelled"}},
		{Names: names4("时间范围", "時間範圍", "開催期間", "Schedule"), Fields: []string{"begin_date", "end_date", "ended", "date_precision", "earliest_date", "latest_date", "time_zone"}},
		{Names: names4("权利信息", "權利資訊", "権利情報", "Rights"), Fields: []string{"copyright"}},
	}
	d.Templates["event_profile"] = Template{
		Names: names4("活动资料", "活動資料", "イベント情報", "Event profile"), Directory: "tree",
		Kinds: []string{"event"}, Match: &empty, Priority: 10,
		PrimaryDateField: "begin_date",
		BadgeFields:      []string{"event_type", "venue", "location", "cancelled"},
		FacetFields:      []string{"event_type", "country", "cancelled"},
		Columns:          []string{"event_type", "venue", "begin_date"},
		Sections:         eventSections,
		RelationGroups:   []string{"credits", "creative", "membership"},
	}
	for _, sub := range []struct {
		code string
		term string
	}{
		{"event_concert", "concert"},
		{"event_tour", "tour"},
		{"event_festival", "festival"},
		{"event_convention", "convention"},
		{"event_stage", "stage"},
		{"event_broadcast_event", "broadcast_event"},
		{"event_award", "award"},
		{"event_exhibition", "exhibition"},
	} {
		m := []TemplateCondition{{Field: "event_type", Operator: "equals", Value: sub.term}}
		d.Templates[sub.code] = Template{
			Names: eventTypes.Terms[sub.term].Names, Directory: "tree",
			Kinds: []string{"event"}, Match: &m, Priority: 20,
			PrimaryDateField: "begin_date",
			BadgeFields:      []string{"event_type", "venue", "location", "cancelled"},
			FacetFields:      []string{"event_type", "country", "cancelled"},
			Columns:          []string{"event_type", "venue", "begin_date"},
			Sections:         eventSections,
			RelationGroups:   []string{"credits", "creative", "membership"},
		}
	}
}
