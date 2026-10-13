package catalog

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDefaultsCreditRelations(t *testing.T) {
	d := Defaults()
	if err := d.Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	for _, code := range []string{"composed_by", "lyricist_of", "arranged_by", "directed_by", "written_by", "illustrated_by", "narrated_by"} {
		r, ok := d.Relations[code]
		if !ok {
			t.Errorf("missing credit relation %s", code)
			continue
		}
		if r.Group != "credits" || len(r.TargetKinds) != 1 || r.TargetKinds[0] != "agent" || !r.Enabled {
			t.Errorf("%s: group=%q targets=%v enabled=%v", code, r.Group, r.TargetKinds, r.Enabled)
		}
		if len(r.SourceKinds) == 0 {
			t.Errorf("%s has no source kinds", code)
		}
	}
}

// stringScalarMap 若只认 float64 会静默丢弃外部 ID。
func TestBangumiCreditRelationMapping(t *testing.T) {
	cases := map[string]string{
		"导演":    "directed_by",
		"CG 导演": "directed_by",
		"脚本":    "written_by",
		"系列构成":  "written_by",
		"插入歌作词": "lyricist_of",
		"插入歌作曲": "composed_by",
		"編曲":    "arranged_by",
		"人物设定":  "illustrated_by",
		"摄影监督":  "photographed_by",
		"旁白":    "narrated_by",
		"配音":    "voiced_by",
		// 无对应语义关系：保持为空，不虚构
		"製片人": "",
		"企画":  "",
		"制作":  "",
	}
	for in, want := range cases {
		if got := bangumiCreditRelation(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

// persons 端点 type：1=个人 2=公司 3=组合。
func TestBangumiPersonTypeAgent(t *testing.T) {
	for in, want := range map[int]string{1: "person", 2: "organization", 3: "group", 9: "person"} {
		if got := bangumiPersonTypeAgent(in); got != want {
			t.Errorf("type %d: got %q want %q", in, got, want)
		}
	}
}

// 角色番位映射到 character_rank 词表（main/supporting/guest/ensemble/narrator），
// 而不是 role（载体用途/收录内容词表）；未覆盖的原文返回空，由 credit_role 承载。
func TestBangumiCharacterRankRole(t *testing.T) {
	for in, want := range map[string]string{
		"主角": "main", "主人公": "main",
		"配角": "supporting", "副角": "supporting",
		"客串": "guest", "闲角": "ensemble", "路人": "ensemble",
		"旁白": "narrator",
		"":   "",
	} {
		if got := bangumiCharacterRankRole(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

// 关联项去重键：必须与落库的 metafusion_import 格式一致（否则 findImported 查不到
// 已存在实体，重复导入会不断新建副本）；其次名称+类型。同人物跨职位应合并为一个 agent。
// 键由服务端派生：载荷自带的 metafusion_import 同值时结果不变，冲突时不得覆盖派生键。
func TestPreviewSubjectRelationsDegradeWhenAbsent(t *testing.T) {
	stubBangumi(t) // 该桩只覆盖 /subjects/7 等三个端点，无关联端点
	ctx := context.Background()
	s := &Store{}
	pv, err := s.Preview(ctx, "bangumi", "7", "work")
	if err != nil {
		t.Fatalf("preview must not fail when relation endpoints are absent: %v", err)
	}
	if len(pv.Artists) != 0 {
		t.Fatalf("expected no related artists, got %d", len(pv.Artists))
	}
}

// infobox 解析：值既可能是标量字符串，也可能是 [{"v":...}] 列表；
// 别名要去掉与主标题/原题名重复的项。
func TestBangumiInfoboxParsing(t *testing.T) {
	raw := `{
		"id": 428735, "type": 2, "name": "BanG Dream! It's MyGO!!!!!", "name_cn": "",
		"infobox": [
			{"key": "别名", "value": [{"k": "大陆版权译", "v": "迷途之子!!!!!"}, {"v": "BanG Dream! 迷途之子!!!!!"}]},
			{"key": "话数", "value": "13"},
			{"key": "官方网站", "value": "https://anime.bang-dream.com/mygo/"},
			{"key": "商品编号", "value": "KSLA-0004～0005"}
		]
	}`
	var sub bangumiSubject
	if err := json.Unmarshal([]byte(raw), &sub); err != nil {
		t.Fatal(err)
	}
	if got := sub.infoboxString("话数"); got != "13" {
		t.Errorf("scalar infobox: got %q", got)
	}
	if got := sub.infoboxString("官方网站"); got != "https://anime.bang-dream.com/mygo/" {
		t.Errorf("website: got %q", got)
	}
	aliases := sub.bangumiInfoboxAliases("BanG Dream! It's MyGO!!!!!", "BanG Dream! It's MyGO!!!!!")
	if len(aliases) != 2 || aliases[0] != "迷途之子!!!!!" {
		t.Errorf("aliases: %v", aliases)
	}
	// 与主标题重复的别名应被剔除
	if got := sub.bangumiInfoboxAliases("迷途之子!!!!!", ""); len(got) != 1 || got[0] != "BanG Dream! 迷途之子!!!!!" {
		t.Errorf("alias dedup failed: %v", got)
	}
}

// 假名是日文原文的可靠信号；纯汉字/拉丁不猜。
func TestDetectJapaneseScript(t *testing.T) {
	for in, want := range map[string]string{
		"とある魔術の禁書目録":                 "ja",
		"BanG Dream! It's MyGO!!!!!": "",
		"魔法禁书目录":                     "",
		"アイドルマスター":                   "ja",
		"":                           "",
	} {
		if got := detectJapaneseScript(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

// 增量补录：只填补缺失元数据，绝不覆盖已有值（保护人工编辑）。
func TestBangumiPersonAgentType(t *testing.T) {
	orgSummary := "株式会社ブシロードは、東京都中野区に所在する企業。"
	voiceSummary := "尾崎由香（おざき ゆか）は、日本の女性声優。研音所属。"
	bandSummary := "现实与虚拟同步的全新乐队。"
	cases := []struct {
		name    string
		typeID  int
		summary string
		want    string
	}{
		{"上游标公司", 2, "", "organization"},
		{"上游标组合", 3, "", "group"},
		{"企业自述('株式会社Xは、' 模式)", 1, orgSummary, "organization"},
		{"企业自述('を主な事業内容とする')", 1, "アニメーションの企画・制作を主な事業内容とする日本の企業。", "organization"},
		{"企业自述('是一家…公司')", 1, "于1990年成立，是日本一家专门从事动画美术背景的公司。", "organization"},
		// 声优简介常出现"所属"，不得误判为公司
		{"声优(含'所属')", 1, voiceSummary, "person"},
		{"乐队描述但不是 type=3", 1, bandSummary, "person"},
		{"空简介", 1, "", "person"},
	}
	for _, c := range cases {
		if got := bangumiPersonAgentType(c.typeID, c.summary); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// 语言推断：名称优先，名称无信号时看简介；都无信号留空。
func TestDetectEntityLanguage(t *testing.T) {
	cases := []struct{ name, summary, want string }{
		{"とある魔術の禁書目録", "", "ja"},
		{"BanG Dream! It's MyGO!!!!!", "现实与虚拟同步的乐队", ""},       // 标题拉丁、简介中文 → 不猜日文
		{"AIR Original SoundTrack", "ゲーム中に使用されたＢＧＭ全23曲", "ja"}, // 标题拉丁、简介日文
		{"魔法禁书目录", "", ""},                                     // 纯汉字不猜
		{"ブシロード", "株式会社ブシロード", "ja"},
	}
	for _, c := range cases {
		if got := detectEntityLanguage(c.name, c.summary); got != c.want {
			t.Errorf("name=%q summary=%q: got %q want %q", c.name, c.summary, got, c.want)
		}
	}
}

// 已存在 agent 的补齐：补简介/语言/封面，不覆盖已有值。
