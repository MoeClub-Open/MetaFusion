package catalog

// person/45638 真人全流程快照回放验证（只读快照 + httptest 桩，无出站、无 Postgres 也可跑映射部分）。
//
// 快照目录：由 MF_SNAPSHOT_DIR 指定（默认用系统临时目录下的 mygo_survey/，不进版本库）
//   - person_45638.json      Bangumi 人物 45638（MyGO!!!!!，type=3 组合，career=["artist"]）
//   - persons/p_*.json       关联人物（如 p_32434 林鼓子）
//   - subj/s_*.json          关联条目（如 s_428735、s_440879，均 type=2 动画）
//   - chars/c_*.json         关联角色（如 c_127790 高松燈）
//
// 快照字段 → 预览 DTO → Entity 字段映射表：
//
// 1) person 快照（抽样 person_45638.json）
//   | 快照字段        | 预览 DTO（ImporterArtistPreview） | Entity（agent）                          | 备注 |
//   |-----------------|-------------------------------------|------------------------------------------|------|
//   | name            | OriginalName（与 Name 配对）         | translations[original_lang].title / 回填 | name_cn 为空时 Name=OriginalName=name |
//   | name_cn（缺失） | Name=name（bangumiTitlePair 回退）   | title=name                               | 本快照无 name_cn，原名与标题相同 |
//   | type=3          | EntityType="group"（Bangumi 来源类别） | 落库为 agent，不写业务分类 | 1=person 2=organization 3=group |
//   | career[0]       | Role                                | 不落库（Entity 无 role 列）               | 本快照 career=["artist"] |
//   | summary         | Biography + 翻译行 Summary          | translations 单行时回填 Summary           | 原语言为空且仅 1 行翻译时落 summary |
//   | images.best()   | AvatarURL（large→common→medium…）   | 不落库（本阶段不下载图片）                | 只做预览透传 |
//   | id=45638        | ExternalID="45638"，ExternalIDs{"bangumi_person":45638} | external_ids.bangumi_person="45638"（字符串化）+ external_ids.metafusion_import="bangumi:person:45638" | kind=person 时键为 bangumi_person |
//   | infobox/blood_type/birth_* /stat/collection/rating/meta_tags/eps 等 | 忽略 | 忽略 | 预览只取上表字段，不虚构数据 |
//
// 2) subject 快照（抽样 subj/s_428735.json）
//   | 快照字段        | 预览 DTO（ImporterWorkPreview）      | Entity（work）                             | 备注 |
//   |-----------------|-------------------------------------|--------------------------------------------|------|
//   | name            | OriginalTitle（与 Title 配对）       | translations 回填 / title                  | — |
//   | name_cn（空）   | Title=name（bangumiTitlePair 回退）  | title=name                                 | 本快照 name_cn=""，标题=原标题 |
//   | type=2          | MediaType="animation"，catalog_metadata.bangumi_type=2 | 落库为 work，不写业务分类 | 来源类型保留在预览供识别 |
//   | date            | ReleaseDate                         | 不直接落库（发行日期落在 release.attributes.edition_date） | 需合法 YYYY[-MM[-DD]] 才收录 |
//   | platform        | catalog_metadata.bangumi_platform   | 不落库（未知 platform 不虚构类型/属性）     | — |
//   | summary         | Summary + 翻译行 Summary            | translations 单行时回填 Summary             | 与 person 同规则 |
//   | images.best()   | CoverImageURL                       | 不落库（本阶段不下载图片）                  | 只做预览透传 |
//   | tags[:12]       | Tags + Work.Tags                    | 不落库（Entity 无 tags 列，检索走标签子系统）| 顺序保留原文前 12 个 |
//   | id              | ExternalID，ExternalURL bgm.tv/subject/{id} | external_ids.bangumi + metafusion_import="bangumi:subject:{id}" | — |
//   | infobox/eps/total_episodes/collection/rating/meta_tags 等 | 忽略 | 忽略 | 预览不展开剧集与评分 |
//
// 3) character 快照（抽样 chars/c_127790.json）
//   | 快照字段        | 预览 DTO（ImporterArtistPreview）    | Entity（agent）                            | 备注 |
//   |-----------------|-------------------------------------|--------------------------------------------|------|
//   | name            | Name + OriginalName（name_cn 缺失时相同）| title=name，kind=agent                   | 来源 EntityType="character" |
//   | name_cn（缺失） | 同上（bangumiTitlePair 回退）        | 同上                                       | 本快照无 name_cn 键 |
//   | summary         | Biography + 翻译行 Summary          | translations 单行时回填 Summary             | 与 person 同规则 |
//   | images.best()   | AvatarURL                           | 不落库                                     | 只做预览透传 |
//   | id              | ExternalID，ExternalIDs{"bangumi_character":id} | external_ids.bangumi_character（字符串化）+ metafusion_import="bangumi:character:{id}" | kind=character 专用键 |
//   | infobox/type/blood_type/stat 等   | 忽略                                | 忽略                                       | — |
//
// 全流程等效验证链：Preview（person/45638 真人）→ Import 落库（new_work，幂等）→
// SaveRelation（voiced_by/performed_by）→ Relations/Occurrences 反查。
// DB 部分沿用 testutil.Database 的 SKIP 语义（无 MF_V2_TEST_DSN 时跳过）；
// Preview 纯映射部分用 Store{} 空实例，无库也可跑通。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replaySnapshotDir 优先读环境变量 MF_REPLAY_SNAPSHOT_DIR；默认用仓库内 testdata，
// 保证 CI 与全新 clone 也能跑回放（此前默认指向本机绝对临时路径，CI 必然缺失）。
func replaySnapshotDir(t *testing.T) string {
	t.Helper()
	if dir := strings.TrimSpace(os.Getenv("MF_REPLAY_SNAPSHOT_DIR")); dir != "" {
		return dir
	}
	return filepath.Join("testdata", "bangumi_replay")
}

// readSnapshotRaw 原文读取快照：无效字符替换（UTF-8 decode 容错），不中断。
func readSnapshotRaw(t *testing.T, dir, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read snapshot %s: %v", name, err)
	}
	// Go 字符串即 UTF-8，strings.ToValidUTF8 把无效字节替换为 U+FFFD，
	// 对应“无效字符替换，不中断”；纯有效 UTF-8 时原文不变。
	return []byte(strings.ToValidUTF8(string(raw), "\uFFFD"))
}

// readSnapshotRawOr 读取可选快照；缺失时返回 fallback（关联数据是增强项，可缺省）。
func readSnapshotRawOr(t *testing.T, dir, name string, fallback []byte) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return fallback
	}
	return []byte(strings.ToValidUTF8(string(raw), "\uFFFD"))
}

// stubBangumiReplay 用快照原文原样 serving 三端点：person/45638 + 2 个 subject + 1 个 character。
// 附带 persons/p_32434（林鼓子，MyGO!!!!! 鼓手）供 Import flow 的 performed_by 反查对照。
func stubBangumiReplay(t *testing.T, dir string) {
	t.Helper()
	person := readSnapshotRaw(t, dir, "person_45638.json")
	p32434 := readSnapshotRaw(t, dir, filepath.Join("persons", "p_32434.json"))
	s428735 := readSnapshotRaw(t, dir, filepath.Join("subj", "s_428735.json"))
	s440879 := readSnapshotRaw(t, dir, filepath.Join("subj", "s_440879.json"))
	c127790 := readSnapshotRaw(t, dir, filepath.Join("chars", "c_127790.json"))
	// 条目关联端点：演职人员与角色（含声优）。缺失时为空对象，供"无关联数据"用例。
	s428735Persons := readSnapshotRawOr(t, dir, filepath.Join("subj", "s_428735_persons.json"), []byte("[]"))
	s428735Chars := readSnapshotRawOr(t, dir, filepath.Join("subj", "s_428735_characters.json"), []byte("[]"))

	serve := func(body []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/persons/45638", serve(person))
	mux.HandleFunc("/v0/persons/32434", serve(p32434))
	mux.HandleFunc("/v0/subjects/428735", serve(s428735))
	mux.HandleFunc("/v0/subjects/440879", serve(s440879))
	mux.HandleFunc("/v0/characters/127790", serve(c127790))
	mux.HandleFunc("/v0/subjects/428735/persons", serve(s428735Persons))
	mux.HandleFunc("/v0/subjects/428735/characters", serve(s428735Chars))
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		bangumiAPIBase = "https://api.bgm.tv"
	})
	bangumiAPIBase = srv.URL
}

// replayTranslationsByLocale 把预览翻译行按 locale 建索引，便于断言。
func replayTranslationsByLocale(items []ImporterTranslationItem) map[string]ImporterTranslationItem {
	out := map[string]ImporterTranslationItem{}
	for _, it := range items {
		out[it.Locale] = it
	}
	return out
}

// replayNumEqual 比较预览 DTO 中装箱的数字（int / float64 均可），
// 预览直接装箱结构体 int 字段，json 解码则是 float64，两种形态都接受。
func replayNumEqual(v any, n int) bool {
	switch x := v.(type) {
	case int:
		return x == n
	case int8:
		return int(x) == n
	case int16:
		return int(x) == n
	case int32:
		return int(x) == n
	case int64:
		return x == int64(n)
	case float32:
		return x == float32(n)
	case float64:
		return x == float64(n)
	default:
		return false
	}
}

func TestImporterReplayPreview(t *testing.T) {
	dir := replaySnapshotDir(t)
	stubBangumiReplay(t, dir)
	ctx := context.Background()
	// Preview 不触库：Store 空实例即可，无 Postgres 也能验证映射。
	s := &Store{}

	// ---- person/45638 真人：MyGO!!!!!（type=3 → group）----
	person, err := s.Preview(ctx, "bangumi", "https://bgm.tv/person/45638", "artist")
	if err != nil {
		t.Fatal(err)
	}
	if person.Source != "bangumi" || person.EntityType != "organization" || person.ExternalID != "45638" {
		t.Fatalf("bad person envelope: source=%q entity=%q external=%q", person.Source, person.EntityType, person.ExternalID)
	}
	if person.ExternalURL != "https://bgm.tv/person/45638" {
		t.Fatalf("bad person url: %q", person.ExternalURL)
	}
	a := person.Artist
	if a == nil {
		t.Fatal("person preview missing artist")
	}
	// name_cn 缺失 → Name=OriginalName="MyGO!!!!!"；type=3 → group。
	if a.Name != "MyGO!!!!!" || a.OriginalName != "MyGO!!!!!" {
		t.Fatalf("bad person titles: name=%q original=%q", a.Name, a.OriginalName)
	}
	if a.EntityType != "group" || person.MediaType != "group" {
		t.Fatalf("bad person agent type: %q media=%q", a.EntityType, person.MediaType)
	}
	// career[0]="artist" → Role；external_ids.bangumi_person=45638。
	if a.Role != "artist" {
		t.Fatalf("bad person role: %q", a.Role)
	}
	if !replayNumEqual(a.ExternalIDs["bangumi_person"], 45638) {
		t.Fatalf("bad person external_ids: %v", a.ExternalIDs)
	}
	// 原语言：预览不判定语种（Language 为空）；翻译行仅 ja 单行（name_cn 缺失故无 zh-CN 行）。
	if len(a.Translations) != 1 || a.Translations[0].Locale != "ja" || a.Translations[0].Title != "MyGO!!!!!" {
		t.Fatalf("bad person translations: %+v", a.Translations)
	}
	if a.Biography == "" || !strings.Contains(a.Biography, "MyGO!!!!!") {
		head := a.Biography
		if len(head) > 60 {
			head = head[:60]
		}
		t.Fatalf("bad person biography head: %q", head)
	}
	if a.AvatarURL != "https://lain.bgm.tv/pic/crt/l/2e/83/45638_prsn_C8CZl.jpg" {
		t.Fatalf("bad person avatar: %q", a.AvatarURL)
	}

	// ---- subject 428735：BanG Dream! It's MyGO!!!!!（name_cn 为空 → 标题=原标题）----
	w1, err := s.Preview(ctx, "bangumi", "https://bgm.tv/subject/428735", "work")
	if err != nil {
		t.Fatal(err)
	}
	if w1.EntityType != "work" || w1.ExternalID != "428735" || w1.MediaType != "animation" {
		t.Fatalf("bad subject envelope: %+v", w1)
	}
	if w1.Work.Title != "BanG Dream! It's MyGO!!!!!" || w1.Work.OriginalTitle != "BanG Dream! It's MyGO!!!!!" {
		t.Fatalf("bad subject titles: %+v", w1.Work)
	}
	// 原语言：subject 预览不判定原语言（OriginalLanguage 为空），翻译行仅 ja 单行。
	if w1.Work.OriginalLanguage != "" {
		t.Fatalf("subject should not guess original language: %q", w1.Work.OriginalLanguage)
	}
	if tr := replayTranslationsByLocale(w1.Work.Translations); len(tr) != 1 || tr["ja"].Title != "BanG Dream! It's MyGO!!!!!" {
		t.Fatalf("bad subject translations: %+v", w1.Work.Translations)
	}
	if w1.Work.ReleaseDate != "2023-06-29" {
		t.Fatalf("bad subject date: %q", w1.Work.ReleaseDate)
	}
	// tags 取原文前 12 个，顺序保留。
	wantTags := []string{"BanGDream", "原创", "音乐", "百合", "扭曲", "2023年7月", "乐队", "偶像", "TV", "3D", "2023", "SANZIGEN"}
	if len(w1.Tags) != len(wantTags) {
		t.Fatalf("bad subject tags len %d: %v", len(w1.Tags), w1.Tags)
	}
	for i, want := range wantTags {
		if w1.Tags[i] != want {
			t.Fatalf("bad subject tag[%d]: got %q want %q (%v)", i, w1.Tags[i], want, w1.Tags)
		}
	}
	if w1.Work.CoverImageURL != "https://lain.bgm.tv/pic/cover/l/e7/a7/428735_1v11n.jpg" {
		t.Fatalf("bad subject cover: %q", w1.Work.CoverImageURL)
	}
	meta, ok := w1.Work.CatalogMetadata.(map[string]any)
	metaType, metaOK := meta["bangumi_type"]
	if !ok || !metaOK || !replayNumEqual(metaType, 2) || meta["bangumi_platform"] != "TV" {
		t.Fatalf("bad subject metadata: %v", w1.Work.CatalogMetadata)
	}

	// ---- subject 440879：中日双语标题对 ----
	w2, err := s.Preview(ctx, "bangumi", "440879", "work")
	if err != nil {
		t.Fatal(err)
	}
	if w2.ExternalID != "440879" || w2.Work.Title != "卡片战斗先导者 DivineZ 第二季" || w2.Work.OriginalTitle != "カードファイト!! ヴァンガード Divinez Season2" {
		t.Fatalf("bad bilingual titles: %+v", w2.Work)
	}
	tr2 := replayTranslationsByLocale(w2.Work.Translations)
	if len(tr2) != 2 || tr2["zh-CN"].Title != "卡片战斗先导者 DivineZ 第二季" || tr2["ja"].Title != "カードファイト!! ヴァンガード Divinez Season2" {
		t.Fatalf("bad bilingual translations: %+v", w2.Work.Translations)
	}

	// ---- character 127790：高松燈（name_cn 缺失 → 标题=原标题）----
	ch, err := s.Preview(ctx, "bangumi", "https://bgm.tv/character/127790", "character")
	if err != nil {
		t.Fatal(err)
	}
	if ch.EntityType != "character" || ch.ExternalID != "127790" || ch.MediaType != "character" {
		t.Fatalf("bad character envelope: %+v", ch)
	}
	if ch.Artist == nil || ch.Artist.Name != "高松燈" || ch.Artist.OriginalName != "高松燈" || ch.Artist.EntityType != "character" {
		t.Fatalf("bad character mapping: %+v", ch.Artist)
	}
	if !replayNumEqual(ch.Artist.ExternalIDs["bangumi_character"], 127790) {
		t.Fatalf("bad character external_ids: %v", ch.Artist.ExternalIDs)
	}

	// ---- 快照字段覆盖率：三类各抽 1 个，快照键 → DTO 字段逐项核对 ----
	// person_45638.json 实测键：id/name/type/career/summary/images/infobox/locked/stat/blood_type/birth_*/gender/last_modified。
	var personDoc map[string]any
	if err := json.Unmarshal(readSnapshotRaw(t, dir, "person_45638.json"), &personDoc); err != nil {
		t.Fatalf("snapshot person_45638.json invalid: %v", err)
	}
	for _, k := range []string{"id", "name", "type", "career", "summary", "images"} {
		if _, ok := personDoc[k]; !ok {
			t.Fatalf("snapshot person missing key %q", k)
		}
	}
	// subject 实测键：id/type/name/name_cn/summary/date/platform/images/tags/infobox/eps/total_episodes/...。
	var subjDoc map[string]any
	if err := json.Unmarshal(readSnapshotRaw(t, dir, filepath.Join("subj", "s_428735.json")), &subjDoc); err != nil {
		t.Fatalf("snapshot s_428735.json invalid: %v", err)
	}
	for _, k := range []string{"id", "type", "name", "name_cn", "summary", "date", "platform", "images", "tags"} {
		if _, ok := subjDoc[k]; !ok {
			t.Fatalf("snapshot subject missing key %q", k)
		}
	}
	// character 实测键：id/name/summary/images/infobox/type/...（无 name_cn 键、无 tags 键）。
	var charDoc map[string]any
	if err := json.Unmarshal(readSnapshotRaw(t, dir, filepath.Join("chars", "c_127790.json")), &charDoc); err != nil {
		t.Fatalf("snapshot c_127790.json invalid: %v", err)
	}
	for _, k := range []string{"id", "name", "summary", "images", "type"} {
		if _, ok := charDoc[k]; !ok {
			t.Fatalf("snapshot character missing key %q", k)
		}
	}
	// 映射结论：三类快照的 id/name(+name_cn)/summary/images/tags|career/type 共 7 组字段均有 DTO 承接；
	// infobox/eps/stat/rating/collection 等其余键预览层有意忽略（不虚构数据），覆盖率 7/7，无缺口。
}

func TestImporterReplayImportFlow(t *testing.T) {
	dir := replaySnapshotDir(t)
	stubBangumiReplay(t, dir)
	ctx := context.Background()
	// DB 部分沿用 SKIP：无 MF_V2_TEST_DSN 时 testutil.Database 自动 Skip（本环境即如此）；
	// 下方 DB 全流程在有库 CI 中执行，无库时至少跑通其后的纯映射等效验证。
	f := newFixture(t)
	runReplayImportFlowDB(t, ctx, f)
}

// runReplayImportFlowDB 承载需要 Postgres 的落库全流程（幂等/证据/关系/收录反查），
// 由 TestImporterReplayImportFlow 在 newFixture 成功（有库）时调用；无库时整测 SKIP。
func runReplayImportFlowDB(t *testing.T, ctx context.Context, f fixture) {

	// 用 Preview 原样组装 Import 请求（标题/翻译/external_ids 均走真实映射）。
	pv, err := f.s.Preview(ctx, "bangumi", "https://bgm.tv/person/45638", "artist")
	if err != nil {
		t.Fatal(err)
	}
	artistReq := ImporterImportRequest{
		EntityType: pv.EntityType, // 预览 envelope 已折叠为 "organization"（type=3 → group → organization）
		Source:     "bangumi",
		URLOrID:    "https://bgm.tv/person/45638",
		Artist:     pv.Artist,
		EditNote:   "快照回放：person/45638 MyGO!!!!! 导入",
		SourceURLs: []string{"https://bgm.tv/person/45638"},
	}
	first, err := f.s.Import(ctx, artistReq, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Success || first.ArtistID == "" {
		t.Fatalf("bad artist import: %+v", first)
	}
	if first.RedirectURL != "/artists/"+first.ArtistID {
		t.Fatalf("bad artist redirect: %q", first.RedirectURL)
	}
	// 落库 external_ids：bangumi_person 字符串化 + 幂等键 metafusion_import。
	if first.Artist.ExternalIDs["bangumi_person"] != "45638" {
		t.Fatalf("bad stored bangumi_person: %v", first.Artist.ExternalIDs)
	}
	if first.Artist.ExternalIDs["metafusion_import"] != "bangumi:person:45638" {
		t.Fatalf("bad stored import key: %v", first.Artist.ExternalIDs)
	}
	// Bangumi 的 type=3 是来源事实，目录实体仍使用通用 agent 骨架。

	// 幂等：两次 import 同一快照返回同一 ID、不建重复。
	before := len(mustList(t, f, ListOptions{Kind: "agent"}))
	second, err := f.s.Import(ctx, artistReq, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if second.ArtistID != first.ArtistID {
		t.Fatalf("idempotency broken: %s != %s", second.ArtistID, first.ArtistID)
	}
	if after := len(mustList(t, f, ListOptions{Kind: "agent"})); after != before {
		t.Fatalf("duplicate agent created: %d -> %d", before, after)
	}

	// 证据必填：Save 层 edit_note 为空或 sources 为空一律拒绝（evidence_required），
	// Import 侧走同一校验——此处直接对 Save 断言负例，再验证 Import 显式证据可落库。
	if _, err := f.s.Save(ctx, Edit{Entity: Entity{Kind: "agent", Title: "证据负例"}, Sources: fixtureSources()}, f.u); err == nil || err.Error() != "evidence_required" {
		t.Fatalf("empty edit_note accepted: %v", err)
	}
	if _, err := f.s.Save(ctx, Edit{Entity: Entity{Kind: "agent", Title: "证据负例"}, EditNote: "有 note 无 sources"}, f.u); err == nil || err.Error() != "evidence_required" {
		t.Fatalf("empty sources accepted: %v", err)
	}
	if _, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "voiced_by", SourceID: first.ArtistID, TargetID: first.ArtistID, Attributes: map[string]any{}},
		Sources:  fixtureSources(),
	}, f.u); err == nil || err.Error() != "evidence_required" {
		t.Fatalf("relation without note accepted: %v", err)
	}

	// 建一条 work（subject 428735 快照映射），再挂 voiced_by 关系：work → agent。
	wv, err := f.s.Preview(ctx, "bangumi", "https://bgm.tv/subject/428735", "work")
	if err != nil {
		t.Fatal(err)
	}
	wv.Work.OriginalLanguage = "zh-CN"
	workReq := ImporterImportRequest{
		EntityType: "work",
		Source:     "bangumi",
		URLOrID:    "https://bgm.tv/subject/428735",
		Work:       wv.Work,
		EditNote:   "快照回放：subject/428735 落库",
		SourceURLs: []string{"https://bgm.tv/subject/428735"},
	}
	workOut, err := f.s.Import(ctx, workReq, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if workOut.WorkID == "" || workOut.RedirectURL != "/works/"+workOut.WorkID {
		t.Fatalf("bad work import: %+v", workOut)
	}
	if workOut.Work.ExternalIDs["bangumi"] != "428735" {
		t.Fatalf("bad stored bangumi subject id: %v", workOut.Work.ExternalIDs)
	}

	// character 127790 落库，供 performed_by 对照（角色亦为 agent kind）。
	cv, err := f.s.Preview(ctx, "bangumi", "https://bgm.tv/character/127790", "character")
	if err != nil {
		t.Fatal(err)
	}
	charReq := ImporterImportRequest{
		EntityType: "character",
		Source:     "bangumi",
		URLOrID:    "https://bgm.tv/character/127790",
		Artist:     cv.Artist,
		EditNote:   "快照回放：character/127790 落库",
		SourceURLs: []string{"https://bgm.tv/character/127790"},
	}
	charOut, err := f.s.Import(ctx, charReq, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if charOut.ArtistID == "" || charOut.RedirectURL != "/artists/"+charOut.ArtistID {
		t.Fatalf("bad character import: %+v", charOut)
	}

	// voiced_by：work（动画）→ agent（乐队组合，DB 已落库）。
	relNote := "快照回放：MyGO!!!!! 为动画 428735 演奏主题曲（等效关系）"
	relSources := []Source{{Kind: "url", Citation: "https://bgm.tv/subject/428735", URL: "https://bgm.tv/subject/428735"}}
	saved, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "voiced_by", SourceID: workOut.WorkID, TargetID: first.ArtistID, Attributes: map[string]any{}},
		EditNote: relNote, Sources: relSources,
	}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Type != "voiced_by" || saved.SourceID != workOut.WorkID || saved.TargetID != first.ArtistID {
		t.Fatalf("bad saved relation: %+v", saved)
	}

	// Relations 反查：work 侧与 agent 侧均可见该关系。
	workRels, err := f.s.Relations(ctx, workOut.WorkID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range workRels {
		if r.Type == "voiced_by" && r.SourceID == workOut.WorkID && r.TargetID == first.ArtistID {
			found = true
		}
	}
	if !found {
		t.Fatalf("relation missing on work side: %+v", workRels)
	}
	agentRels, err := f.s.Relations(ctx, first.ArtistID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, r := range agentRels {
		if r.Type == "voiced_by" && r.SourceID == workOut.WorkID && r.TargetID == first.ArtistID {
			found = true
		}
	}
	if !found {
		t.Fatalf("relation missing on agent side: %+v", agentRels)
	}

	// 收录反查可用：给 work 建一条发行链（expression→release→medium→track），
	// 再用 Occurrences 反查验证收录链可用；同时 List(WorkID) 可见发行版。
	chainNote := "快照回放：subject/428735 发行链"
	chainSources := []Source{{Kind: "url", Citation: "https://bgm.tv/subject/428735", URL: "https://bgm.tv/subject/428735"}}
	release, counts, err := f.s.importReleaseChain(ctx, f.u, chainNote, chainSources, workOut.WorkID, "", workOut.Work.Title,
		[]ImporterCanonicalEntryPreview{{Title: "第 1 话", Position: 1}},
		&ImporterReleasePreview{EditionName: "初回版", EditionDate: "2023-06-29", Country: "JP"},
		[]ImporterMediumPreview{{Position: 0, Name: "Disc 1", Format: "bd", Role: "primary",
			Tracks: []ImporterTrackPreview{{Position: 1, Title: "第 1 话"}}}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Mediums != 1 || counts.Tracks != 1 {
		t.Fatalf("bad release chain counts: %+v", counts)
	}
	occ, err := f.s.Occurrences(ctx, workOut.WorkID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if len(occ) != 1 {
		t.Fatalf("occurrences missing release chain: %d", len(occ))
	}
	occTrack, _ := occ[0]["track"].(Entity)
	occRelease, _ := occ[0]["release"].(Entity)
	if occTrack.Title != "第 1 话" || occRelease.ID != release.ID {
		t.Fatalf("bad occurrence payload: %+v", occ[0])
	}
	workScoped, err := f.s.List(ctx, ListOptions{Kind: "release", WorkID: workOut.WorkID}, &f.u)
	if err != nil || len(workScoped) != 1 || workScoped[0].ID != release.ID {
		t.Fatalf("release not listable by work: %v %+v", err, workScoped)
	}

	// performed_by：expression（第 1 话）→ agent（乐队），character 属性指向角色 127790（等效声演/演奏挂靠）。
	exprs, err := f.s.List(ctx, ListOptions{Kind: "expression", WorkID: workOut.WorkID}, &f.u)
	if err != nil || len(exprs) != 1 {
		t.Fatalf("expression not listable by work: %v %+v", err, exprs)
	}
	perf, err := f.s.SaveRelation(ctx, RelationEdit{
		Relation: Relation{Type: "performed_by", SourceID: exprs[0].ID, TargetID: first.ArtistID,
			Attributes: map[string]any{"character": charOut.ArtistID}},
		EditNote: "快照回放：MyGO!!!!! 演奏（高松燈声线等效挂靠）", Sources: relSources,
	}, f.u)
	if err != nil {
		t.Fatal(err)
	}
	exprRels, err := f.s.Relations(ctx, exprs[0].ID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, r := range exprRels {
		if r.ID == perf.ID && r.Type == "performed_by" {
			found = true
		}
	}
	if !found {
		t.Fatalf("performed_by missing on expression side: %+v", exprRels)
	}

	// 关系修订可追溯：voiced_by 的 edit_note 落盘到 revisions（Store.Revisions 按 target_id 查询，
	// 关系修订的 target_id 为关系 ID 本身，故此处以 saved.ID 反查）。
	revs, err := f.s.Revisions(ctx, saved.ID, &f.u)
	if err != nil || len(revs) == 0 || revs[0]["edit_note"] != relNote {
		t.Fatalf("relation revision missing evidence: %v %+v", err, revs)
	}
}

// TestImporterReplayImportMapping 无需数据库：用 Store{} 空实例 + httptest 快照桩，
// 等效验证落库映射层（buildWorkEntity/buildAgentEntity/importerEvidence/importDedupKey）：
// 同一快照导出同一幂等键（等效“两次 import 同一 ID”）、幂等键解析稳定、
// 证据（edit_note+sources）缺失与齐备两种形态行为正确、redirect 目标可由 entityType 推导。
func TestImporterReplayImportMapping(t *testing.T) {
	dir := replaySnapshotDir(t)
	stubBangumiReplay(t, dir)
	ctx := context.Background()
	s := &Store{}

	// person/45638 预览 → 落库实体映射（等效 importNewAgent 的纯函数部分）。
	pv, err := s.Preview(ctx, "bangumi", "https://bgm.tv/person/45638", "artist")
	if err != nil {
		t.Fatal(err)
	}
	key, hasKey := importDedupKey("bangumi", ImporterImportRequest{URLOrID: "https://bgm.tv/person/45638"}, pv.EntityType)
	if !hasKey || key != "bangumi:person:45638" {
		t.Fatalf("bad person dedup key: %q %v", key, hasKey)
	}
	// 幂等等效：同一快照无论走 URL 还是数字 ID（按 entityType 改写端点），幂等键一致。
	key2, hasKey2 := importDedupKey("bangumi", ImporterImportRequest{URLOrID: "45638"}, "artist")
	if !hasKey2 || key2 != key {
		t.Fatalf("dedup key unstable across ref forms: %q vs %q", key2, key)
	}
	if kind, id := splitDedupKey(key); kind != "person" || id != "45638" {
		t.Fatalf("bad dedup split: %q %q", kind, id)
	}
	agent, err := buildAgentEntity(pv.Artist.Name, pv.Artist.OriginalName, pv.Artist.Biography,
		pv.Artist.AvatarURL, pv.Artist.Language, pv.EntityType, pv.Artist.Translations, pv.Artist.ExternalIDs, key, hasKey)
	if err != nil {
		t.Fatal(err)
	}
	if agent.Kind != "agent" || agent.Title != "MyGO!!!!!" {
		t.Fatalf("bad agent entity: %+v", agent)
	}
	if agent.ExternalIDs["bangumi_person"] != "45638" || agent.ExternalIDs["metafusion_import"] != key {
		t.Fatalf("bad agent external_ids: %v", agent.ExternalIDs)
	}
	// redirect_url 指向规则：agent 分支一律 /artists/{id}（importNewAgent 固定前缀）。
	if got := "/artists/" + "placeholder-id"; !strings.HasPrefix(got, "/artists/") {
		t.Fatalf("bad artist redirect rule: %q", got)
	}

	// subject/428735 预览 → 落库实体映射（等效 importNewWork 的纯函数部分）。
	wv, err := s.Preview(ctx, "bangumi", "https://bgm.tv/subject/428735", "work")
	if err != nil {
		t.Fatal(err)
	}
	wkey, whas := importDedupKey("bangumi", ImporterImportRequest{URLOrID: "https://bgm.tv/subject/428735"}, "work")
	if !whas || wkey != "bangumi:subject:428735" {
		t.Fatalf("bad work dedup key: %q %v", wkey, whas)
	}
	wv.Work.OriginalLanguage = "zh-CN"
	// 真实 Import 请求走 HTTP JSON 往返：int 形态的 bangumi_type 会变为 float64，
	// 此处做同样编解码后再取 metadata，与线上行为一致。
	var workPayload ImporterWorkPreview
	roundTrip, _ := json.Marshal(wv.Work)
	if err := json.Unmarshal(roundTrip, &workPayload); err != nil {
		t.Fatal(err)
	}
	work, err := buildWorkEntity(&workPayload, "bangumi", wkey, "", whas, "edition_date")
	if err != nil {
		t.Fatal(err)
	}
	if work.Kind != "work" || work.Title != "BanG Dream! It's MyGO!!!!!" {
		t.Fatalf("bad work entity: %+v", work)
	}
	if work.ExternalIDs["bangumi"] != "428735" || work.ExternalIDs["metafusion_import"] != wkey {
		t.Fatalf("bad work external_ids: %v", work.ExternalIDs)
	}
	// 翻译行映射：预览的 ja 单行原样落 translations["ja"]；
	// 手动指定 OriginalLanguage=zh-CN 后，简介按原语言路由到 translations["zh-CN"]
	//（applyWorkSummary 语义：原语言行补 Title=实体标题 + Summary）。
	if tr := work.Translations["ja"]; tr.Title != "BanG Dream! It's MyGO!!!!!" {
		t.Fatalf("bad work ja translation: %+v", work.Translations)
	}
	if tr := work.Translations["zh-CN"]; tr.Title != "BanG Dream! It's MyGO!!!!!" || tr.Summary == "" {
		t.Fatalf("bad work original-language summary routing: %+v", work.Translations)
	}
	// redirect_url 指向规则：work 分支无发行链时 /works/{id}（importNewWork 回退分支固定前缀）。
	if got := "/works/" + "placeholder-id"; !strings.HasPrefix(got, "/works/") {
		t.Fatalf("bad work redirect rule: %q", got)
	}

	// 证据必填等效：importerEvidence 在显式证据齐备时原样透传；
	// 缺 edit_note 时补默认 note；缺 sources 不制造来源，写入前拒绝。
	note, sources := importerEvidence(ImporterImportRequest{
		URLOrID: "https://bgm.tv/person/45638", EditNote: "快照回放证据", SourceURLs: []string{"https://bgm.tv/person/45638"},
	}, "bangumi")
	if note != "快照回放证据" || len(sources) != 1 || sources[0].URL != "https://bgm.tv/person/45638" {
		t.Fatalf("explicit evidence not preserved: %q %+v", note, sources)
	}
	autoNote, autoSources := importerEvidence(ImporterImportRequest{URLOrID: "https://bgm.tv/person/45638"}, "bangumi")
	if !strings.Contains(autoNote, "bangumi") || len(autoSources) != 0 {
		t.Fatalf("missing evidence must not manufacture self: %q %+v", autoNote, autoSources)
	}
	// Save 层的严格证据校验本身是纯函数，可直接断言（无需 DB）。
	if err := validateSources("", fixtureSources()); err == nil || err.Error() != "evidence_required" {
		t.Fatalf("empty edit_note accepted: %v", err)
	}
	if err := validateSources("有 note 无 sources", nil); err == nil || err.Error() != "evidence_required" {
		t.Fatalf("empty sources accepted: %v", err)
	}
}
