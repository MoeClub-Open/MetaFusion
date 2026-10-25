package catalog

// Suggested labels supplement the open tags field; they are not a whitelist.
func addMusicTagDefinitions(d *Definitions) {
	vocabulary := Vocabulary{Names: names4("标签", "標籤", "タグ", "Tags"), Terms: map[string]Term{}}
	for _, item := range []termSeed{
		{"pop", names4("流行音乐", "流行音樂", "ポップ", "Pop")},
		{"j_pop", names4("日本流行音乐", "日本流行音樂", "J-POP", "J-pop")},
		{"jazz", names4("爵士乐", "爵士樂", "ジャズ", "Jazz")},
		{"hip_hop", names4("嘻哈", "嘻哈", "ヒップホップ", "Hip hop")},
		{"electronic", names4("电子音乐", "電子音樂", "電子音楽", "Electronic")},
		{"alternative", names4("另类音乐", "另類音樂", "オルタナティブ", "Alternative")},
		{"rnb", names4("节奏布鲁斯", "節奏藍調", "リズム・アンド・ブルース", "R&B")},
		{"country_music", names4("乡村音乐", "鄉村音樂", "カントリー", "Country music")},
		{"folk", names4("民谣", "民謠", "フォーク", "Folk")},
		{"blues", names4("布鲁斯", "藍調", "ブルース", "Blues")},
		{"metal", names4("金属乐", "金屬樂", "メタル", "Metal")},
		{"hard_rock", names4("硬摇滚", "硬式搖滾", "ハードロック", "Hard rock")},
		{"punk", names4("朋克", "龐克", "パンク", "Punk")},
		{"latin", names4("拉丁音乐", "拉丁音樂", "ラテン音楽", "Latin")},
		{"latin_pop", names4("拉丁流行音乐", "拉丁流行音樂", "ラテンポップ", "Latin pop")},
		{"reggae", names4("雷鬼", "雷鬼", "レゲエ", "Reggae")},
		{"soul", names4("灵魂乐", "靈魂樂", "ソウル", "Soul")},
		{"funk", names4("放克", "放克", "ファンク", "Funk")},
		{"ambient", names4("氛围音乐", "氛圍音樂", "アンビエント", "Ambient")},
		{"new_age", names4("新世纪音乐", "新世紀音樂", "ニューエイジ", "New age")},
		{"enka", names4("演歌", "演歌", "演歌", "Enka")},
		{"singer_songwriter", names4("创作歌手", "創作歌手", "シンガーソングライター", "Singer-songwriter")},
		{"children_music", names4("儿童音乐", "兒童音樂", "子供向け音楽", "Children's music")},
		{"spoken_word", names4("口述作品", "口述作品", "スポークンワード", "Spoken word")},
		{"asmr", names4("ASMR音频", "ASMR音訊", "ASMR音声", "ASMR")},
	} {
		vocabulary.Terms[item.code] = Term{Names: item.names, Enabled: true}
	}
	d.Vocabularies["tags"] = vocabulary
}
