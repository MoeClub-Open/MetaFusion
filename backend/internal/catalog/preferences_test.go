package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func shelfFixtures() []Shelf {
	return []Shelf{
		{Slug: "music", SortOrder: 10},
		{Slug: "anime", SortOrder: 20},
		{Slug: "films", SortOrder: 30},
		{Slug: "novels", SortOrder: 40},
	}
}

func slugsOf(in []Shelf) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.Slug)
	}
	return out
}

// 默认（未设置偏好）时保持 sort_order 次序，不做任何裁剪。
func TestApplyHomePreferencesDefault(t *testing.T) {
	got := slugsOf(applyHomePreferences(shelfFixtures(), HomePreferences{}))
	want := []string{"music", "anime", "films", "novels"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 用户排定的分区置前，未列出的按默认序追加在后。
func TestApplyHomePreferencesOrder(t *testing.T) {
	got := slugsOf(applyHomePreferences(shelfFixtures(), HomePreferences{Order: []string{"novels", "films"}}))
	want := []string{"novels", "films", "music", "anime"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 隐藏项被移除，其余保持默认序。
func TestApplyHomePreferencesHidden(t *testing.T) {
	got := slugsOf(applyHomePreferences(shelfFixtures(), HomePreferences{Hidden: []string{"films"}}))
	want := []string{"music", "anime", "novels"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 管理台删除分区后，偏好里残留的旧 slug 必须被忽略而不是造成首页缺内容。
func TestApplyHomePreferencesUnknownSlugIgnored(t *testing.T) {
	got := slugsOf(applyHomePreferences(shelfFixtures(), HomePreferences{
		Order:  []string{"removed-shelf", "anime"},
		Hidden: []string{"also-removed"},
	}))
	want := []string{"anime", "music", "films", "novels"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// dedupeSlugs 去空去重并保持首次出现顺序。
func TestDedupeSlugs(t *testing.T) {
	got := dedupeSlugs([]string{"a", "", "b", "a", "b", "c"})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if len(dedupeSlugs(nil)) != 0 {
		t.Fatal("nil input should produce empty result")
	}
}

// shelfSectionFixtures 是带名称与收录规则的货架夹具：验证覆盖/自建合并需要真实的
// names/query/sort/icon，前面几组纯排序用例用的 shelfFixtures 只有 slug。
func shelfSectionFixtures() []Shelf {
	return []Shelf{
		{Slug: "music", Names: names4("音乐", "音樂", "音楽", "Music"), Query: ShelfQuery{Tags: []string{"music"}}, Sort: "updated", Icon: "Disc", Enabled: true, SortOrder: 10},
		{Slug: "films", Names: names4("电影", "電影", "映画", "Films"), Query: ShelfQuery{Tags: []string{"film"}}, Sort: "updated", Icon: "Film", Enabled: true, SortOrder: 30},
		{Slug: "games", Names: names4("游戏", "遊戲", "ゲーム", "Games"), Query: ShelfQuery{Tags: []string{"game"}}, Sort: "updated", Icon: "Gamepad2", Enabled: true, SortOrder: 50},
	}
}

// sections 命中系统货架 slug 是"只给本人覆盖"，不是冲突：names 逐语种覆盖（未提及语种保留
// 系统名，否则其它语种用户只能退回 slug）、query/sort 整体替换、留空 icon 保留系统图标，
// 来源标 system，位置仍跟系统 sort_order。
func TestApplyHomePreferencesOverridesSystemShelf(t *testing.T) {
	prefs := HomePreferences{Sections: []HomeSection{{
		Slug:  "films",
		Names: Names{"zh-CN": "我的电影"},
		Query: ShelfQuery{Tags: []string{"film", "animation"}},
		Sort:  "created",
	}}}
	got := applyHomePreferences(shelfSectionFixtures(), prefs)
	if want := []string{"music", "films", "games"}; !reflect.DeepEqual(slugsOf(got), want) {
		t.Fatalf("覆盖不应增删或移动分区：got %v want %v", slugsOf(got), want)
	}
	films := got[1]
	if films.Source != ShelfSourceSystem {
		t.Fatalf("覆盖同名系统货架的来源应为 system，实际 %q", films.Source)
	}
	if films.Names["zh-CN"] != "我的电影" || films.Names["zh-TW"] != "電影" || films.Names["en-US"] != "Films" {
		t.Fatalf("names 应逐语种覆盖并保留未提及语种，实际 %v", films.Names)
	}
	if !reflect.DeepEqual(films.Query.Tags, []string{"film", "animation"}) || films.Sort != "created" {
		t.Fatalf("query/sort 应整体替换，实际 query=%+v sort=%q", films.Query, films.Sort)
	}
	if films.Icon != "Film" {
		t.Fatalf("留空 icon 应保留系统图标，实际 %q", films.Icon)
	}
	if got[0].Source != ShelfSourceSystem || got[2].Source != ShelfSourceSystem {
		t.Fatalf("未覆盖的系统货架也必须是 system，实际 %q/%q", got[0].Source, got[2].Source)
	}
}

// 不命中系统货架即追加只属于本人的分区：排在系统货架之后、按声明顺序，来源 custom，
// sort_order 取基准值（客户端自行按 sort_order 排序也不会插进系统分区之间）。
func TestApplyHomePreferencesAppendsCustomSections(t *testing.T) {
	prefs := HomePreferences{Sections: []HomeSection{
		{Slug: "my-indie", Names: Names{"zh-CN": "独立游戏"}, Query: ShelfQuery{Tags: []string{"game"}, VocabTerms: map[string][]string{"tags": {"indie"}}}, Sort: "created", Icon: "Gamepad2"},
		{Slug: "my-photos", Names: Names{"zh-CN": "我的写真"}},
	}}
	got := applyHomePreferences(shelfSectionFixtures(), prefs)
	if want := []string{"music", "films", "games", "my-indie", "my-photos"}; !reflect.DeepEqual(slugsOf(got), want) {
		t.Fatalf("got %v want %v", slugsOf(got), want)
	}
	indie := got[3]
	if indie.Source != ShelfSourceCustom || indie.ID != 0 || !indie.Enabled {
		t.Fatalf("自建分区应无库内 id、来源 custom、默认启用：%+v", indie)
	}
	if indie.SortOrder != customShelfSortBase || got[4].SortOrder != customShelfSortBase+1 {
		t.Fatalf("自建分区排序位应取基准值+声明顺序：%d/%d", indie.SortOrder, got[4].SortOrder)
	}
	// 收录规则原样交给求值层：标签与词表项都保留。
	if !contains(indie.Query.Tags, "game") || indie.Query.VocabTerms["tags"][0] != "indie" {
		t.Fatalf("自建分区应保留完整收录规则：%+v", indie.Query)
	}
	// 系统货架为空（例如全部停用）时自建分区仍要出现。
	if out := applyHomePreferences(nil, prefs); len(out) != 2 || out[0].Slug != "my-indie" {
		t.Fatalf("无系统货架时仍应返回自建分区：%v", slugsOf(out))
	}
}

// order 把系统与自建 slug 混排，hidden 对两类都生效；未列出的按"系统 sort_order 优先、
// 自建按声明顺序"排后；未知 slug（管理员删货架后的残留）静默忽略。
func TestApplyHomePreferencesOrderAndHiddenAcrossKinds(t *testing.T) {
	prefs := HomePreferences{
		Order:  []string{"my-indie", "games", "my-photos", "removed-shelf"},
		Hidden: []string{"films", "my-photos"},
		Sections: []HomeSection{
			{Slug: "my-indie", Names: Names{"zh-CN": "独立游戏"}},
			{Slug: "my-photos", Names: Names{"zh-CN": "我的写真"}},
		},
	}
	got := applyHomePreferences(shelfSectionFixtures(), prefs)
	if want := []string{"my-indie", "games", "music"}; !reflect.DeepEqual(slugsOf(got), want) {
		t.Fatalf("got %v want %v", slugsOf(got), want)
	}
	if got[0].Source != ShelfSourceCustom || got[1].Source != ShelfSourceSystem {
		t.Fatalf("来源标记应与分区来源一致：%q/%q", got[0].Source, got[1].Source)
	}
	// 匿名视角（零值偏好）永远只有 system，且保持 sort_order 默认序。
	anon := applyHomePreferences(shelfSectionFixtures(), HomePreferences{})
	if want := []string{"music", "films", "games"}; !reflect.DeepEqual(slugsOf(anon), want) {
		t.Fatalf("匿名视角不应出现用户分区：%v", slugsOf(anon))
	}
	for _, sh := range anon {
		if sh.Source != ShelfSourceSystem {
			t.Fatalf("匿名视角的来源只能是 system，实际 %q", sh.Source)
		}
	}
}

// 分区校验只给稳定错误码（前端按码提示）：slug/名称/排序/收录规则逐项检查，条数上限 20。
func TestNormalizeHomePreferencesValidatesSections(t *testing.T) {
	valid := HomeSection{Slug: "my-indie", Names: Names{"zh-CN": "独立游戏"}, Query: ShelfQuery{Tags: []string{"game"}}, Sort: "created", Icon: "Gamepad2"}
	section := func(mutate func(*HomeSection)) HomeSection {
		sec := valid
		mutate(&sec)
		return sec
	}
	for _, tc := range []struct {
		name string
		sec  HomeSection
		want string
	}{
		{"大写 slug", section(func(s *HomeSection) { s.Slug = "My-Indie" }), "invalid_slug"},
		{"过短 slug", section(func(s *HomeSection) { s.Slug = "a" }), "invalid_slug"},
		{"缺中文名", section(func(s *HomeSection) { s.Names = Names{"en-US": "Indie"} }), "invalid_name"},
		{"中文名为空白", section(func(s *HomeSection) { s.Names = Names{"zh-CN": "  "} }), "invalid_name"},
		{"语种码非法", section(func(s *HomeSection) { s.Names = Names{"zh-CN": "独立游戏", "not a locale": "x"} }), "invalid_locale"},
		{"排序不在白名单", section(func(s *HomeSection) { s.Sort = "newest" }), "invalid_sort"},
		{"类型码为空白", section(func(s *HomeSection) { s.Query = ShelfQuery{Tags: []string{" "}} }), "invalid_tags"},
		{"字段码非法", section(func(s *HomeSection) { s.Query = ShelfQuery{Fields: map[string][]string{"Not-A-Code": {"x"}}} }), "invalid_fields"},
		{"词表取值为空白", section(func(s *HomeSection) { s.Query = ShelfQuery{VocabTerms: map[string][]string{"tags": {""}}} }), "invalid_vocab_terms"},
		{"关系码为空白", section(func(s *HomeSection) { s.Query = ShelfQuery{Relations: []string{" "}} }), "invalid_relations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := normalizeHomePreferences(HomePreferences{Sections: []HomeSection{tc.sec}})
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v want %s", err, tc.want)
			}
		})
	}
	// 上限 20 条：21 条拒绝，20 条放行。
	many := make([]HomeSection, 0, maxHomeSections+1)
	for i := 0; i <= maxHomeSections; i++ {
		sec := valid
		sec.Slug = fmt.Sprintf("section-%d", i)
		many = append(many, sec)
	}
	if _, err := normalizeHomePreferences(HomePreferences{Sections: many}); err == nil || err.Error() != "too_many_sections" {
		t.Fatalf("21 条分区必须以 too_many_sections 拒绝，实际 %v", err)
	}
	if _, err := normalizeHomePreferences(HomePreferences{Sections: many[:maxHomeSections]}); err != nil {
		t.Fatalf("20 条分区必须放行，实际 %v", err)
	}
}

// 归一化：空白清理、空语种值丢弃、空 sort 落成 updated、同 slug 只留首次声明、
// order/hidden 去空去重且未知 slug 原样保留（不报 unknown_shelf，见 SaveHomePreferences 注释）。
func TestNormalizeHomePreferencesNormalizesValues(t *testing.T) {
	got, err := normalizeHomePreferences(HomePreferences{
		Order:  []string{"films", "", "films", "my-indie", "removed-shelf"},
		Hidden: []string{"", "games", "games"},
		Sections: []HomeSection{
			{Slug: " films ", Names: Names{"zh-CN": " 我的电影 ", "en-US": "  ", "zh-TW": "我的電影"}, Icon: " Film "},
			{Slug: "films", Names: Names{"zh-CN": "重复声明"}},
			{Slug: "my-indie", Names: Names{"zh-CN": "独立游戏"}},
		},
	})
	if err != nil {
		t.Fatalf("合法偏好被拒：%v", err)
	}
	if !reflect.DeepEqual(got.Order, []string{"films", "my-indie", "removed-shelf"}) {
		t.Fatalf("order 应去空去重并保留未知 slug，实际 %v", got.Order)
	}
	if !reflect.DeepEqual(got.Hidden, []string{"games"}) {
		t.Fatalf("hidden 应去空去重，实际 %v", got.Hidden)
	}
	if len(got.Sections) != 2 {
		t.Fatalf("同 slug 只应保留首次声明，实际 %v", got.Sections)
	}
	first := got.Sections[0]
	if first.Slug != "films" || first.Sort != "updated" || first.Icon != "Film" {
		t.Fatalf("slug/icon 应去空白、空 sort 应落成 updated：%+v", first)
	}
	if !reflect.DeepEqual(first.Names, Names{"zh-CN": "我的电影", "zh-TW": "我的電影"}) {
		t.Fatalf("名称应去空白并丢弃空语种值，实际 %v", first.Names)
	}
	if got.Sections[1].Sort != "updated" {
		t.Fatalf("自建分区同样落成显式排序键，实际 %q", got.Sections[1].Sort)
	}
	// 空偏好归一成三个空数组，不是 null（前端不必判空）。
	empty, err := normalizeHomePreferences(HomePreferences{})
	if err != nil || empty.Order == nil || empty.Hidden == nil || empty.Sections == nil || len(empty.Sections) != 0 {
		t.Fatalf("空偏好应归一成三个空数组：%+v err=%v", empty, err)
	}
}

// 当前偏好契约的三个数组都显式存在；内部归一化仍保证返回非 nil 数组。
func TestHomePreferencesCanonicalPayload(t *testing.T) {
	var current HomePreferences
	if err := json.Unmarshal([]byte(`{"order":["films","removed-shelf"],"hidden":[],"sections":[]}`), &current); err != nil {
		t.Fatal(err)
	}
	out, err := normalizeHomePreferences(current)
	if err != nil || out.Sections == nil || !reflect.DeepEqual(out.Order, []string{"films", "removed-shelf"}) {
		t.Fatalf("当前偏好形状应保留未知 slug 并输出三个数组：%+v err=%v", out, err)
	}
	// query 里的空子条件不落 JSON（omitempty）：前端可以整份回传给 PUT。
	payload, err := json.Marshal(HomeSection{Slug: "my-indie", Names: Names{"zh-CN": "独立游戏"}, Query: ShelfQuery{Tags: []string{"game"}}, Sort: "updated", Icon: "Gamepad2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"query":{"tags":["game"]}`) {
		t.Fatalf("分区 JSON 形状应与契约一致：%s", payload)
	}
}

// 真库用例：偏好整份存 catalog.user_preferences.home_shelves（JSONB，无需迁移），
// 含 sections 与未知 order slug；输入输出均使用当前形状。
func TestPostgresHomePreferencesRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	const userID = "11111111-1111-1111-1111-111111111111"
	got, err := f.s.GetHomePreferences(ctx, userID)
	if err != nil || got.Order == nil || got.Hidden == nil || got.Sections == nil || len(got.Sections) != 0 {
		t.Fatalf("未设置偏好时应返回三个空数组：%+v err=%v", got, err)
	}
	saved, err := f.s.SaveHomePreferences(ctx, userID, HomePreferences{
		Order:  []string{"my-indie", "removed-shelf"},
		Hidden: []string{"games"},
		Sections: []HomeSection{
			{Slug: "films", Names: Names{"zh-CN": "我的电影"}, Query: ShelfQuery{Tags: []string{"film"}}, Sort: "created"},
			{Slug: "my-indie", Names: Names{"zh-CN": "独立游戏"}, Query: ShelfQuery{Tags: []string{"game"}, VocabTerms: map[string][]string{"tags": {"indie"}}}, Sort: "updated", Icon: "Gamepad2"},
		},
	})
	if err != nil {
		t.Fatalf("保存偏好：%v", err)
	}
	got, err = f.s.GetHomePreferences(ctx, userID)
	if err != nil {
		t.Fatalf("读回偏好：%v", err)
	}
	if !reflect.DeepEqual(saved, got) {
		t.Fatalf("往返不一致：saved=%+v got=%+v", saved, got)
	}
	// 当前契约要求三组数组；缺少 sections 的旧载荷必须拒绝且不覆盖已保存偏好。
	if _, err := f.s.SaveHomePreferences(ctx, userID, HomePreferences{Order: []string{}, Hidden: []string{}}); err == nil {
		t.Fatal("缺少 sections 的载荷应被拒绝")
	}
	got, err = f.s.GetHomePreferences(ctx, userID)
	if err != nil || !reflect.DeepEqual(saved, got) {
		t.Fatalf("无效载荷不应覆盖已保存偏好：got=%+v err=%v", got, err)
	}
}

// feedEntry 只取 feed 用例关心的部分：分区头（含 source）与求值后的条目数。
type feedEntry struct {
	Shelf Shelf             `json:"shelf"`
	Items []json.RawMessage `json:"items"`
	Total int               `json:"total"`
}

// feedShelves 取 feed 条目里的分区头，复用 slugsOf 断言顺序。
func feedShelves(items []feedEntry) []Shelf {
	out := make([]Shelf, 0, len(items))
	for _, it := range items {
		out = append(out, it.Shelf)
	}
	return out
}

// 真库 + HTTP 端到端：sections 覆盖同名系统货架只影响本人（匿名视角仍是系统默认），
// 自建分区追加在后并标 custom，order/hidden 对两类都生效，未知 order slug 不再让保存失败。
func TestPostgresShelfFeedMergesHomeSections(t *testing.T) {
	f := newFixture(t)
	f.save(Entity{Kind: "work", Title: "首页分区电影", Attributes: map[string]any{"tags": []any{"film"}}})
	f.save(Entity{Kind: "work", Title: "首页分区独立游戏", Attributes: map[string]any{"tags": []any{"indie_game"}}})

	key := testKey(t)
	f.s.Verifier = testVerifier(t, key)
	engine := gin.New()
	HTTP{Store: f.s}.Register(engine)
	token := signTestToken(t, key, nil)

	feed := func(authorization string) []feedEntry {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/catalog/shelves/feed?per_shelf=5", nil)
		if authorization != "" {
			req.Header.Set("Authorization", "Bearer "+authorization)
		}
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("feed = %d: %s", res.Code, res.Body.String())
		}
		var out struct {
			Items []feedEntry `json:"items"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
			t.Fatalf("feed 解码：%v", err)
		}
		return out.Items
	}

	anonymous := feed("")
	if len(anonymous) != 6 {
		t.Fatalf("匿名 feed 应是六个系统货架，实际 %d", len(anonymous))
	}
	for _, entry := range anonymous {
		if entry.Shelf.Source != ShelfSourceSystem {
			t.Fatalf("匿名视角的来源只能是 system，实际 %q（%s）", entry.Shelf.Source, entry.Shelf.Slug)
		}
	}
	if got := anonymous[2].Shelf.Names["zh-CN"]; got != "电影" {
		t.Fatalf("匿名视角的电影分区名应是系统默认，实际 %q", got)
	}
	// total 是不受 per_shelf 限制的真实筛选数：电影分区只命中 1 部。
	if anonymous[2].Total != 1 {
		t.Fatalf("电影分区 total=%d，应 1", anonymous[2].Total)
	}
	if len(anonymous[2].Items) != 1 {
		t.Fatalf("电影分区 items=%d，应 1", len(anonymous[2].Items))
	}

	payload, err := json.Marshal(HomePreferences{
		Order:  []string{"my-indie", "films", "removed-shelf"},
		Hidden: []string{"games"},
		Sections: []HomeSection{
			{Slug: "films", Names: Names{"zh-CN": "我的电影"}, Query: ShelfQuery{Tags: []string{"film"}}, Sort: "updated", Icon: "Film"},
			{Slug: "my-indie", Names: Names{"zh-CN": "独立游戏"}, Query: ShelfQuery{Tags: []string{"indie_game"}}, Sort: "updated", Icon: "Gamepad2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	put := httptest.NewRequest(http.MethodPut, "/api/catalog/me/home-preferences", strings.NewReader(string(payload)))
	put.Header.Set("Content-Type", "application/json")
	put.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	engine.ServeHTTP(res, put)
	if res.Code != http.StatusOK {
		t.Fatalf("保存偏好 = %d: %s", res.Code, res.Body.String())
	}
	var savedPrefs HomePreferences
	if err := json.Unmarshal(res.Body.Bytes(), &savedPrefs); err != nil {
		t.Fatalf("偏好响应解码：%v", err)
	}
	if len(savedPrefs.Sections) != 2 || len(savedPrefs.Order) != 3 {
		t.Fatalf("保存响应应回显归一化后的偏好（未知 slug 保留）：%+v", savedPrefs)
	}

	mine := feed(token)
	if want := []string{"my-indie", "films", "music", "anime", "novels", "creations"}; !reflect.DeepEqual(slugsOf(feedShelves(mine)), want) {
		t.Fatalf("个人视角顺序：got %v want %v", slugsOf(feedShelves(mine)), want)
	}
	bySlug := map[string]feedEntry{}
	for _, entry := range mine {
		bySlug[entry.Shelf.Slug] = entry
	}
	if bySlug["my-indie"].Shelf.Source != ShelfSourceCustom {
		t.Fatalf("自建分区来源应为 custom，实际 %q", bySlug["my-indie"].Shelf.Source)
	}
	if bySlug["films"].Shelf.Source != ShelfSourceSystem {
		t.Fatalf("覆盖系统货架后来源仍是 system，实际 %q", bySlug["films"].Shelf.Source)
	}
	if bySlug["films"].Shelf.Names["zh-CN"] != "我的电影" || bySlug["films"].Shelf.Names["en-US"] != "Films" {
		t.Fatalf("覆盖应逐语种生效并保留其它语种：%v", bySlug["films"].Shelf.Names)
	}
	if n := len(bySlug["films"].Items); n != 1 {
		t.Fatalf("覆盖后的电影分区应求值出 1 条，实际 %d", n)
	}
	// 自建分区走的是同一套服务端标签求值，不是前端近似匹配。
	if n := len(bySlug["my-indie"].Items); n != 1 {
		t.Fatalf("自建分区应求值出 1 条，实际 %d", n)
	}
	if _, ok := bySlug["games"]; ok {
		t.Fatal("hidden 里的系统货架不应出现")
	}
	// 覆盖只影响本人：保存后匿名视角仍是系统默认名与默认序。
	after := feed("")
	if len(after) != 6 || after[2].Shelf.Names["zh-CN"] != "电影" {
		t.Fatalf("覆盖不应影响匿名视角：%d 个分区，电影名 %q", len(after), after[2].Shelf.Names["zh-CN"])
	}
	// 缺少 sections 的旧接口载荷不再接受。
	old := httptest.NewRequest(http.MethodPut, "/api/catalog/me/home-preferences", strings.NewReader(`{"order":[],"hidden":[]}`))
	old.Header.Set("Content-Type", "application/json")
	old.Header.Set("Authorization", "Bearer "+token)
	res = httptest.NewRecorder()
	engine.ServeHTTP(res, old)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("旧载荷应拒绝，实际 = %d: %s", res.Code, res.Body.String())
	}
}

// 空 query 的货架按规则文档应收录全部作品，而不是无结果。
func TestShelfFilterEmptyQueryFallsBackToWorkKind(t *testing.T) {
	args := []any{}
	parts := shelfFilter(Shelf{}, &args, "e")
	if len(parts) != 1 || parts[0] != "e.kind='work'" {
		t.Fatalf("got %v want [e.kind='work']", parts)
	}
	if len(args) != 0 {
		t.Fatalf("empty query should bind no args, got %v", args)
	}
}

// tags 走 JSONB 存在性判断；fields / vocab_terms 走 attributes 取值比较；
// relations 走 EXISTS 子查询。子条件之间 AND，同数组内 OR。
func TestShelfFilterCompilesAllConditionKinds(t *testing.T) {
	sh := Shelf{Query: ShelfQuery{
		Tags:       []string{"music", "album"},
		Fields:     map[string][]string{"edition_date": {"2024"}},
		VocabTerms: map[string][]string{"edition_type": {"deluxe"}},
		Relations:  []string{"performed_by"},
	}}
	args := []any{}
	parts := shelfFilter(sh, &args, "e")
	if len(parts) != 5 {
		t.Fatalf("expected 5 predicates, got %d: %v", len(parts), parts)
	}
	// 空白项应被剔除，且空数组条件不入 SQL。
	sh2 := Shelf{Query: ShelfQuery{Tags: []string{"  ", ""}, Fields: map[string][]string{"x": {""}}}}
	args2 := []any{}
	parts2 := shelfFilter(sh2, &args2, "e")
	if len(parts2) != 1 || parts2[0] != "e.kind='work'" {
		t.Fatalf("blank conditions should collapse to work kind, got %v", parts2)
	}
}

func TestTrimAll(t *testing.T) {
	if got := trimAll([]string{" a ", "", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	if got := trimAll([]string{"", "  "}); got != nil {
		t.Fatalf("all-blank should return nil, got %v", got)
	}
}

// infobox 原文摊平：保序、去空键、同名多值展开为多行。
func TestBangumiInfoboxEntries(t *testing.T) {
	s := bangumiSubject{Infobox: []bangumiInfoItem{
		{Key: "话数", Value: json.RawMessage(`"12"`)},
		{Key: "别名", Value: json.RawMessage(`[{"v":"A"},{"v":"B"}]`)},
		{Key: "", Value: json.RawMessage(`"ignored"`)},
		{Key: "Copyright", Value: json.RawMessage(`"© test"`)},
	}}
	got := s.infoboxEntries()
	want := []string{"话数=12", "别名=A", "别名=B", "Copyright=© test"}
	flat := make([]string, 0, len(got))
	for _, e := range got {
		flat = append(flat, fmt.Sprint(e["key"])+"="+fmt.Sprint(e["value"]))
	}
	if !reflect.DeepEqual(flat, want) {
		t.Fatalf("got %v want %v", flat, want)
	}
}

// infobox 键映射只产出有值的已声明字段码。ISBN 是产品标识，映射到发行层字段
// barcode，不再产出作品层的 isbn（defaults 已不给 Work 声明该字段）。
func TestBangumiInfoboxValues(t *testing.T) {
	s := bangumiSubject{Infobox: []bangumiInfoItem{
		{Key: "话数", Value: json.RawMessage(`"24"`)},
		{Key: "ISBN", Value: json.RawMessage(`"978-4-00-000000-0"`)},
		{Key: "放送星期", Value: json.RawMessage(`"星期六"`)},
	}}
	got := s.infoboxValues()
	if got["episodes"] != 24 || got["barcode"] != "9784000000000" || got["broadcast_weekday"] != "星期六" {
		t.Fatalf("got %v", got)
	}
	if _, ok := got["isbn"]; ok {
		t.Fatalf("isbn must not be produced for the work level: %v", got)
	}
	if _, ok := got["volume_count"]; ok {
		t.Fatalf("absent key must not be produced: %v", got)
	}
}

// 上游数字/日期是自由文本，必须归一化为字段类型可接受的值，
// 否则会被规格校验以 invalid_number / invalid_date 拒绝整次导入。
func TestNormalizeInfoboxValue(t *testing.T) {
	cases := []struct {
		raw, kind string
		want      any
	}{
		{"13", "number", 13},
		{"24(22+2)卷完结", "number", 24},
		{"全12话", "number", 12},
		{"暂无", "number", nil},
		{"2023年6月29日", "date", "2023-06-29"},
		{"2023/6/9", "date", "2023-06-09"},
		{"2023-06", "date", "2023-06"},
		{"2023年", "date", "2023"},
		{"待定", "date", nil},
		{"TOKYO MX", "text", "TOKYO MX"},
	}
	for _, c := range cases {
		if got := normalizeInfoboxValue(c.raw, c.kind); got != c.want {
			t.Errorf("normalize(%q,%s) = %v (%T), want %v", c.raw, c.kind, got, got, c.want)
		}
	}
}

// 动态字段值必须保类型：整数不能被 stringify 成 "13"，否则 number 字段校验必失败。
func TestDynamicFieldValueKeepsType(t *testing.T) {
	if v, ok := dynamicFieldValue(13); !ok || v != 13 {
		t.Fatalf("int must stay int, got %#v ok=%v", v, ok)
	}
	if v, ok := dynamicFieldValue("  2023-06-29  "); !ok || v != "2023-06-29" {
		t.Fatalf("string must be trimmed, got %#v", v)
	}
	if _, ok := dynamicFieldValue("   "); ok {
		t.Fatal("blank string must be dropped")
	}
}

// 映射表里的字段码必须在 defaults 中已声明，否则写入会被校验拒绝（unknown_field）。
func TestInfoboxFieldKeysAreDeclared(t *testing.T) {
	d := Defaults()
	for _, m := range infoboxFieldKeys {
		if _, ok := d.Fields[m.field]; !ok {
			t.Fatalf("infobox field %q is not declared in defaults", m.field)
		}
	}
}

// 标签过滤必须编译成 jsonb 容器包含（@>），才能命中 entities_attribute_tags
// 函数索引；若退化为展开比较就等于全表扫描。
func TestListFilterTagContainerMatch(t *testing.T) {
	s := &Store{}
	args := []any{}
	parts, err := listFilter(context.Background(), s, ListOptions{Tags: []string{"动画", "音乐"}}, nil, &args)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(parts, " AND ")
	if !strings.Contains(joined, "@>") {
		t.Fatalf("tag filter must use container match (@>), got: %s", joined)
	}
	if !strings.Contains(joined, " OR ") {
		t.Fatalf("multiple tags must be OR-ed, got: %s", joined)
	}
	if len(args) != 2 {
		t.Fatalf("expected 2 bound args, got %v", args)
	}
	// 空白标签被剔除后不应产生任何谓词。
	args2 := []any{}
	parts2, err := listFilter(context.Background(), s, ListOptions{Tags: []string{"", "  "}}, nil, &args2)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts2 {
		if strings.Contains(p, "@>") {
			t.Fatalf("blank tags must add no tag predicate, got %v", parts2)
		}
	}
}

// 关系编辑器对端的候选 kind 约束必须落在 SQL 侧，用 = ANY，
// 否则前端先取固定条数再过滤会把合法候选截断丢弃。
func TestListFilterSupportsMultiValueKind(t *testing.T) {
	args := []any{}
	parts, err := listFilter(context.Background(), &Store{}, ListOptions{
		Kinds: []string{"work", "collection"},
	}, nil, &args)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(parts, " AND ")
	if !strings.Contains(joined, "kind = ANY(") {
		t.Fatalf("multi-kind filter missing: %s", joined)
	}
	if strings.Contains(joined, "document->'types'") {
		t.Fatalf("kind filter must not query removed types: %s", joined)
	}
	// 空切片不应产生任何谓词。
	empty := []any{}
	parts, err = listFilter(context.Background(), &Store{}, ListOptions{}, nil, &empty)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(parts, " AND "); strings.Contains(joined, "ANY(") || strings.Contains(joined, "?|") {
		t.Fatalf("empty kinds must add no predicate, got %s", joined)
	}
}
