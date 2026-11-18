package catalog

import (
	"context"
	"encoding/json"
	"testing"
)

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

func TestMergeSeedDefinitionsAddsFieldsAndApplicableKinds(t *testing.T) {
	seed := Defaults()
	cur := Defaults()
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
	if !contains(added, "fields.role.applicable_kinds.track") {
		t.Fatalf("字段适用层级的补丁未记入新增项：%v", added)
	}
	if _, again := mergeSeedDefinitions(merged, seed); len(again) != 0 {
		t.Fatalf("第二次合并不应有新增，实际 %v", again)
	}
}

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

	v, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	old := v.Document
	delete(old.Fields, "air_date")
	delete(old.Templates, "content_unit_default")
	f.publish(old, v.ETag)

	work := f.save(Entity{Kind: "work", Title: "动画作品"})
	newUnit := func() Entity {
		return Entity{Kind: "content_unit", WorkID: work.ID, Title: "第1话", Number: "1", Position: 1,
			State: "active", Translations: map[string]Translation{"ja": {Title: "第1话"}},
			Attributes: map[string]any{"language": "ja", "entry_role": "main", "air_date": "2017-01-21"}}
	}
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
	if _, ok := v2.Document.Fields["air_date"]; !ok || !contains(v2.Document.Fields["air_date"].ApplicableKinds, "content_unit") {
		t.Fatalf("合并后 air_date 未补齐：%v", v2.Document.Fields["air_date"])
	}

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

	before := definitionsCount()
	if err := f.s.EnsureSeedDefinitions(ctx); err != nil {
		t.Fatal(err)
	}
	v3, err := f.s.Definitions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v3.ETag != v2.ETag || definitionsCount() != before {
		t.Fatalf("重复合并应幂等：etag=%s->%s", v2.ETag, v3.ETag)
	}
}

func TestMergeSeedDefinitionsKeepsExplicitFalse(t *testing.T) {
	seed := Defaults()
	withFlags := func(r RelationDefinition, aggregate, credit bool) RelationDefinition {
		r.Aggregate = aggregate
		r.CountsAsCredit = credit
		return r
	}
	mkCur := func() Definitions {
		return Definitions{
			Fields:       map[string]Field{},
			Vocabularies: map[string]Vocabulary{},
			Relations: map[string]RelationDefinition{
				"includes":     withFlags(seed.Relations["includes"], false, false),
				"performed_by": withFlags(seed.Relations["performed_by"], false, false),
			},
			Templates: map[string]Template{},
			Schemes:   map[string]Scheme{},
		}
	}
	merged, added := mergeSeedDefinitions(mkCur(), seed)
	if merged.Relations["includes"].Aggregate || merged.Relations["performed_by"].CountsAsCredit {
		t.Fatal("后台关闭的开关不得被种子重新打开")
	}
	if contains(added, "relations.includes.aggregate") || contains(added, "relations.performed_by.counts_as_credit") {
		t.Fatalf("显式 false 不应出现在新增项里：%v", added)
	}
	if !merged.Relations["voiced_by"].CountsAsCredit {
		t.Fatal("新增补入的关系应携带种子开关")
	}
	if _, again := mergeSeedDefinitions(merged, seed); len(again) != 0 {
		t.Fatalf("第二次合并不应有新增，实际 %v", again)
	}
	reopen := mkCur()
	rt := reopen.Relations["includes"]
	rt.Aggregate = true
	reopen.Relations["includes"] = rt
	if merged, _ := mergeSeedDefinitions(reopen, seed); !merged.Relations["includes"].Aggregate {
		t.Fatal("已打开的 Aggregate 不得被合并关掉")
	}
}

func TestMergeSeedDefinitionsBackfillsOnlyPlaceholderTranslations(t *testing.T) {
	seed := Defaults()
	cur := Definitions{
		Fields:       map[string]Field{},
		Vocabularies: map[string]Vocabulary{},
		Relations:    map[string]RelationDefinition{},
		Templates:    map[string]Template{},
		Schemes:      map[string]Scheme{},
	}
	cur.Fields["character"] = Field{Names: Names{"zh-CN": "所饰角色", "en-US": "Character"}, Type: "entity", Enabled: true}
	cur.Fields["locale"] = Field{Names: Names{"zh-CN": "语种", "en-US": "Locale", "ja-JP": "手で直した語種名"}, Type: "text", Enabled: true}

	merged, added := mergeSeedDefinitions(cur, seed)
	got := merged.Fields["character"].Names
	if got["ja-JP"] == "" || got["ja-JP"] == "Character" || got["zh-TW"] == "" || got["zh-TW"] == "Character" {
		t.Fatalf("character 的日文/繁中应被补成真实译文，实际 %#v", got)
	}
	if kept := merged.Fields["locale"].Names["ja-JP"]; kept != "手で直した語種名" {
		t.Fatalf("人工改过的译文被覆盖了：%q", kept)
	}
	if !contains(added, "fields.character.names.ja-JP") {
		t.Fatalf("应在补丁清单里记录 fields.character.names.ja-JP：%v", added)
	}
	_, again := mergeSeedDefinitions(merged, seed)
	if contains(again, "fields.character.names.ja-JP") {
		t.Fatal("第二次合并不应再报 fields.character.names.ja-JP")
	}
}

func TestMergeSeedBackfillsMissingSchemeMediumFormats(t *testing.T) {
	seed := Defaults()
	vinyl := seed.Schemes["vinyl_track_locator"]
	vinyl.MediumFormats = nil
	current := seed
	current.Schemes = map[string]Scheme{"vinyl_track_locator": vinyl}

	merged, added := mergeSeedDefinitions(current, seed)
	formats := merged.Schemes["vinyl_track_locator"].MediumFormats
	if formats == nil || len(*formats) != 1 || (*formats)[0] != "vinyl" {
		t.Fatalf("legacy scheme should inherit the seeded medium format constraint, got %#v", formats)
	}
	if !contains(added, "schemes.vinyl_track_locator.medium_formats") {
		t.Fatalf("backfilled setting should be listed in the merge report: %v", added)
	}
	if _, again := mergeSeedDefinitions(merged, seed); len(again) != 0 {
		t.Fatalf("repeated merge should be idempotent, added %v", again)
	}
}

func TestMergeSeedPreservesExplicitEmptySchemeMediumFormats(t *testing.T) {
	seed := Defaults()
	vinyl := seed.Schemes["vinyl_track_locator"]
	empty := []string{}
	vinyl.MediumFormats = &empty
	current := seed
	current.Schemes = map[string]Scheme{"vinyl_track_locator": vinyl}

	merged, added := mergeSeedDefinitions(current, seed)
	formats := merged.Schemes["vinyl_track_locator"].MediumFormats
	if formats == nil || len(*formats) != 0 {
		t.Fatalf("explicit empty list should be preserved, got %#v", formats)
	}
	encoded, err := json.Marshal(merged.Schemes["vinyl_track_locator"])
	if err != nil {
		t.Fatalf("marshal merged scheme: %v", err)
	}
	var decoded Scheme
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal merged scheme: %v", err)
	}
	if decoded.MediumFormats == nil || len(*decoded.MediumFormats) != 0 {
		t.Fatalf("explicit empty list should survive JSON persistence, got %#v", decoded.MediumFormats)
	}
	if contains(added, "schemes.vinyl_track_locator.medium_formats") {
		t.Fatalf("explicit empty list must not be reported as a backfill: %v", added)
	}
	if _, again := mergeSeedDefinitions(merged, seed); len(again) != 0 {
		t.Fatalf("repeated merge should be idempotent, added %v", again)
	}
}

func TestDefaultsDeclareParticipantSlotForEveryRelation(t *testing.T) {
	d := Defaults()
	valid := map[string]bool{"person": true, "character": true, "peer": true}
	for code, rel := range d.Relations {
		if !valid[rel.ParticipantSlot] {
			t.Errorf("关系 %q 的 participant_slot=%q 不是合法槽位", code, rel.ParticipantSlot)
		}
	}
	for _, code := range []string{"voiced_by", "created_by", "composed_by", "credit_for", "translated_by", "illustrated_by"} {
		if got := d.Relations[code].ParticipantSlot; got != "person" {
			t.Errorf("%q 应是 person 槽位，得到 %q", code, got)
		}
	}
	if got := d.Relations["character_in"].ParticipantSlot; got != "character" {
		t.Errorf("character_in 应是 character 槽位，得到 %q", got)
	}
	for _, code := range []string{"adaptation_of", "sequel_of", "translation_of", "cover_of", "pressing_of", "includes", "member_of"} {
		if got := d.Relations[code].ParticipantSlot; got != "peer" {
			t.Errorf("%q 应是 peer 槽位，得到 %q", code, got)
		}
	}
}

func TestMergeSeedBackfillsFixedStructureAdditions(t *testing.T) {
	seed := Defaults()
	current := seed
	current.Structure = make(map[string]StructureRule, len(seed.Structure))
	for kind, rule := range seed.Structure {
		current.Structure[kind] = rule
	}
	track := current.Structure["track"]
	track.Contents = false
	track.Resources = true
	track.Fields = append([]StructureField{}, track.Fields[:1]...)
	current.Structure["track"] = track

	merged, added := mergeSeedDefinitions(current, seed)
	track = merged.Structure["track"]
	if !track.Contents || !track.Resources {
		t.Fatal("track.contents 应补齐且不得覆盖 resources 开关")
	}
	if len(track.Fields) != 2 || track.Fields[1].Code != "parent_id" {
		t.Fatalf("缺失固定结构字段应按种子顺序插入，实际 %#v", track.Fields)
	}
	if !contains(added, "structure.track.contents") || !contains(added, "structure.track.fields.parent_id") {
		t.Fatalf("结构补齐项应写入合并变更清单：%v", added)
	}
	if err := merged.Validate(); err != nil {
		t.Fatalf("合并后的定义应满足固定结构校验：%v", err)
	}
	if _, again := mergeSeedDefinitions(merged, seed); len(again) != 0 {
		t.Fatalf("重复合并应幂等，实际新增：%v", again)
	}
}

func TestMergeSeedDefinitionsFillsMissingBonusSemantic(t *testing.T) {
	seed := Defaults()
	role := seed.Vocabularies["role"]
	supplement := role.Terms["supplement"]
	if supplement.IsBonus == nil || !*supplement.IsBonus {
		t.Fatal("seed supplement role must be marked as bonus content")
	}

	legacyTerms := make(map[string]Term, len(role.Terms))
	for code, term := range role.Terms {
		term.IsBonus = nil
		legacyTerms[code] = term
	}
	current := Definitions{
		Fields:       map[string]Field{},
		Vocabularies: map[string]Vocabulary{"role": {Names: role.Names, Terms: legacyTerms}},
		Relations:    map[string]RelationDefinition{},
		Templates:    map[string]Template{},
		Schemes:      map[string]Scheme{},
	}

	merged, added := mergeSeedDefinitions(current, seed)
	got := merged.Vocabularies["role"].Terms["supplement"].IsBonus
	if got == nil || !*got {
		t.Fatal("missing legacy semantic should be filled from seed")
	}
	if !contains(added, "vocabularies.role.terms.supplement.is_bonus") {
		t.Fatalf("semantic update should be reported: %v", added)
	}
	_, again := mergeSeedDefinitions(merged, seed)
	if contains(again, "vocabularies.role.terms.supplement.is_bonus") {
		t.Fatalf("semantic backfill should be idempotent: %v", again)
	}

	falseValue := false
	explicit := current
	explicitRole := role
	explicitRole.Terms = make(map[string]Term, len(legacyTerms))
	for code, term := range legacyTerms {
		explicitRole.Terms[code] = term
	}
	term := explicitRole.Terms["supplement"]
	term.IsBonus = &falseValue
	explicitRole.Terms["supplement"] = term
	explicit.Vocabularies = map[string]Vocabulary{"role": explicitRole}
	kept, _ := mergeSeedDefinitions(explicit, seed)
	got = kept.Vocabularies["role"].Terms["supplement"].IsBonus
	if got == nil || *got {
		t.Fatal("explicit false semantic must not be overwritten")
	}
}
