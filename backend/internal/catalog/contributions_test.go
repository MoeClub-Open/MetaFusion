package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

// 分页口径：越界静默收敛（与 List 的 limit 同风格），不硬拒绝。
func TestContributionPageClamping(t *testing.T) {
	for _, tc := range []struct{ page, size, wantPage, wantSize int }{
		{0, 0, 1, 20},
		{-3, -1, 1, 20},
		{2, 50, 2, 50},
		{5, 101, 5, 20},
	} {
		if page, size := contributionPage(tc.page, tc.size); page != tc.wantPage || size != tc.wantSize {
			t.Fatalf("contributionPage(%d,%d)=(%d,%d), want (%d,%d)", tc.page, tc.size, page, size, tc.wantPage, tc.wantSize)
		}
	}
}

// tab 与 kind 的映射：artists 是前端口径，骨架里对应 agent；目录不服务的 tab 必须被拒。
func TestContributionTabAndKindMapping(t *testing.T) {
	for tab, kind := range map[string]string{"works": "work", "releases": "release", "artists": "agent"} {
		if got := contributionKindOfTab(tab); got != kind {
			t.Fatalf("%s → %s, want %s", tab, got, kind)
		}
		if got := contributionKindTab(kind); got != tab {
			t.Fatalf("%s → %s, want %s", kind, got, tab)
		}
	}
	// 非创建 kind（篇目/载体/轨道）与 all/revisions 都不映射成创建项 tab。
	for _, kind := range []string{"track", "medium", "content_unit", "expression", "collection"} {
		if contributionKindTab(kind) != "" {
			t.Fatalf("%s 不该映射成创建项 tab", kind)
		}
	}
	for _, tab := range []string{"all", "revisions", "works", "releases", "artists"} {
		if !contributionTabValid(tab) {
			t.Fatalf("%s 应被接受", tab)
		}
	}
	// topics/comments/audits 属互动服务的口径：必须被拒，不能静默返回空列表。
	for _, tab := range []string{"topics", "comments", "audits", ""} {
		if contributionTabValid(tab) {
			t.Fatalf("%s 不该被目录服务接受", tab)
		}
	}
}

// 差异口径与前端 revisionData.ts 的 revisionChanges 逐条对齐：忽略每次写入都变的键，
// attributes / translations 下钻一层，其余按顶层字段比。
func TestContributionDiffMatchesRevisionChanges(t *testing.T) {
	before := []byte(`{"id":"a","kind":"work","version":1,"created_by":"u-1","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","title":"旧题名","attributes":{"tags":["a"],"duration":100},"translations":{"zh-CN":{"title":"旧题名"}}}`)
	after := []byte(`{"id":"a","kind":"work","version":2,"created_by":"u-1","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-02-01T00:00:00Z","title":"新题名","attributes":{"tags":["a","b"],"duration":100},"translations":{"zh-CN":{"title":"新题名"},"en":{"title":"New"}}}`)
	got := contributionDiff(before, after)
	keys := make([]string, 0, len(got))
	for key := range got {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := []string{"attributes.tags", "title", "translations.en", "translations.zh-CN"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("差异键 %v, want %v", keys, want)
	}
	if got["title"].Old != "旧题名" || got["title"].New != "新题名" {
		t.Fatalf("title 差异不符: %+v", got["title"])
	}
	if encode(got["attributes.tags"].New) != `["a","b"]` {
		t.Fatalf("tags 差异取了未归一的值: %s", encode(got["attributes.tags"].New))
	}
	// 只有被忽略的键变了（version/updated_at）：nil，响应里整个 diff 键省略。
	same := contributionDiff(before, []byte(`{"id":"a","kind":"work","version":9,"created_by":"u-1","created_at":"2026-03-01T00:00:00Z","updated_at":"2026-03-01T00:00:00Z","title":"旧题名","attributes":{"tags":["a"],"duration":100},"translations":{"zh-CN":{"title":"旧题名"}}}`))
	if same != nil {
		t.Fatalf("无字段变化应返回 nil: %+v", same)
	}
	// 快照解不出 JSON 时宁可没有 diff，也不编一份出来。
	if broken := contributionDiff(before, []byte("not json")); broken != nil {
		t.Fatalf("非法快照应返回 nil: %+v", broken)
	}
	// 超限值截断成 utf8 安全的前缀：差异是摘要，不是第二份快照。
	long := strings.Repeat("题", 400)
	truncated := contributionDiff([]byte(`{"title":"x"}`), []byte(`{"title":"`+long+`"}`))
	value, ok := truncated["title"].New.(string)
	if !ok || len(value) > contributionDiffMaxValue || !utf8.ValidString(value) {
		t.Fatalf("超限值应截断成 utf8 安全字符串: %#v", truncated["title"].New)
	}
}

// 项形状：创建项 id 是实体 id、带 status/updated_at；修订项 id 是修订行 id、实体 id 落在 target_id。
// 这里只喂 version=1 的行——取"上一版快照"要连库，那条路径由集成用例覆盖。
func TestContributionItemShapes(t *testing.T) {
	s := &Store{}
	entityID := "11111111-1111-1111-1111-111111111111"
	rows := []contributionRow{{
		revID: 7, version: 1, entityID: entityID, kind: "work", title: "作品", status: "active",
		editNote: "建立条目", sources: []byte(`[{"kind":"self","citation":"作者自述"}]`),
		createdAt: time.Unix(0, 0).UTC(), updatedAt: time.Unix(1, 0).UTC(),
	}}
	created, err := s.userContributionItems(context.Background(), rows, true)
	if err != nil {
		t.Fatal(err)
	}
	if created[0].ID != entityID || created[0].Tab != "works" || created[0].EditType != "create" || created[0].Status != "active" || created[0].UpdatedAt == nil {
		t.Fatalf("创建项字段不符: %+v", created[0])
	}
	if len(created[0].Sources) != 1 || created[0].Sources[0].Citation != "作者自述" {
		t.Fatalf("创建项应带回修订行的来源: %+v", created[0].Sources)
	}
	if raw, _ := json.Marshal(created[0]); strings.Contains(string(raw), "diff") {
		t.Fatalf("创建项不该带 diff 键: %s", raw)
	}
	revised, err := s.userContributionItems(context.Background(), rows, false)
	if err != nil {
		t.Fatal(err)
	}
	if revised[0].ID != "7" || revised[0].Tab != "revisions" || revised[0].TargetID != entityID || revised[0].EditType != "create" || revised[0].Status != "" {
		t.Fatalf("修订项字段不符: %+v", revised[0])
	}
	raw, _ := json.Marshal(revised[0])
	if strings.Contains(string(raw), "status") || strings.Contains(string(raw), "updated_at") {
		t.Fatalf("修订项不该带实体状态字段: %s", raw)
	}
}

// 参数校验不必连库（Store 为空也走不到查询）：非法 user id 与账号 /users/{id}、互动
// /users/{id}/stats 同口径——404 not_found（前端三路聚合按同一类降级，目录这一路回
// 400 invalid_id 会弹出"参数错误"）；目录不服务的 tab 仍是 400 invalid_tab。
func TestUserContributionsRejectsBadParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		path   string
		status int
		code   string
	}{
		{"/api/users/not-a-uuid/contributions", http.StatusNotFound, "not_found"},
		{"/api/users/11111111-1111-1111-1111-111111111111/contributions?tab=topics", http.StatusBadRequest, "invalid_tab"},
	} {
		w := httptest.NewRecorder()
		gateEngine(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%s: status=%d body=%s, want %d %s", tc.path, w.Code, w.Body.String(), tc.status, tc.code)
		}
	}
}

// 集成口径（需要隔离库，无 MF_V2_TEST_DSN 时跳过）：归属只看 actor 快照列、可见性跟随实体口径、
// total 是同谓词下的真实计数、stats 五个计数与注释里的口径逐条对上。
func TestUserContributionsFeedAndStats(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	author := fixtureUser("editor")
	admin := fixtureUser("admin")
	other := fixtureUser("editor")

	save := func(u User, e Entity) Entity {
		if e.Status == "" {
			e.Status = "active"
		}
		// 发布态要求至少一条翻译（真实契约）：在夹具里补齐，免得每个调用点各记这件事。
		if e.Status == "active" && len(e.Translations) == 0 {
			e.Translations = map[string]Translation{"en": {Title: e.Title}}
		}
		// 与 HTTP 解码同形：动态值经 JSON 往返。
		var copy Entity
		if err := json.Unmarshal([]byte(encode(e)), &copy); err != nil {
			t.Fatal(err)
		}
		out, err := f.s.Save(ctx, Edit{Entity: copy, ExpectedVersion: e.Version, EditNote: "贡献夹具", Sources: fixtureSources()}, u)
		if err != nil {
			t.Fatalf("save %s: %v", e.Title, err)
		}
		return out
	}
	work := save(author, Entity{Kind: "work", Title: "初版题名"})
	save(author, Entity{Kind: "release", Title: "发行版"})
	save(author, Entity{Kind: "agent", Title: "作者"})
	save(author, Entity{Kind: "work", Title: "普通条目", Status: "active"})
	work.Title = "初版题名2"
	work = save(author, work)
	work.Title = "初版题名3"
	work = save(author, work)

	read := func(u *User, tab string, page, pageSize int) UserContributions {
		t.Helper()
		got, err := f.s.UserContributions(ctx, author.ID, tab, page, pageSize, u)
		if err != nil {
			t.Fatalf("contributions %s: %v", tab, err)
		}
		return got
	}

	// 匿名：只看已发布条目，草稿既不进列表也不进统计。
	anon := read(nil, "works", 1, 20)
	if anon.Total != 2 || len(anon.Items) != 2 {
		t.Fatalf("匿名 works: total=%d items=%d, want 1/1", anon.Total, len(anon.Items))
	}
	item := anon.Items[0]
	for _, candidate := range anon.Items {
		if candidate.TargetID == work.ID {
			item = candidate
		}
	}
	// 作品的 title 经两轮编辑变成"初版题名3"。
	if item.Kind != "work" || item.Title != "初版题名3" || item.Tab != "works" || item.EditType != "create" {
		t.Fatalf("创建项字段不符: %+v", item)
	}
	if item.Status != "active" || item.UpdatedAt == nil || item.Version != 1 {
		t.Fatalf("创建项应带 status/updated_at 且版本为首次修订: %+v", item)
	}
	if st := anon.Stats; st.WorksCreated != 2 || st.ReleasesCreated != 1 || st.ArtistsCreated != 1 || st.AuditActions != 0 {
		t.Fatalf("统计口径不符: %+v", st)
	}
	// 修订条数：work 三版 + release 一版 + agent 一版 = 5（草稿那条被可见性过滤掉）。
	if anon.Stats.RevisionsCount != 6 {
		t.Fatalf("revisions_count=%d, want 5", anon.Stats.RevisionsCount)
	}

	// 本人与生命周期管理员看得到自己的草稿；别人看不到（与实体列表同口径）。
	if own := read(&author, "works", 1, 20); own.Total != 2 {
		t.Fatalf("本人应看到自己的草稿: total=%d", own.Total)
	}
	if adm := read(&admin, "works", 1, 20); adm.Total != 2 {
		t.Fatalf("管理员应看到草稿: total=%d", adm.Total)
	}
	if alien := read(&other, "works", 1, 20); alien.Total != 2 {
		t.Fatalf("他人不该看到草稿: total=%d", alien.Total)
	}

	// revisions tab：全部实体修订行（含首次创建），改动项带与上一版的字段差异。
	revs := read(nil, "revisions", 1, 20)
	if revs.Total != 6 || len(revs.Items) != 6 {
		t.Fatalf("匿名 revisions: total=%d items=%d, want 5/5", revs.Total, len(revs.Items))
	}
	updates := 0
	for _, it := range revs.Items {
		if it.Tab != "revisions" || it.TargetID == "" || it.Status != "" {
			t.Fatalf("修订项字段不符: %+v", it)
		}
		if it.EditType == "update" {
			updates++
			change, ok := it.Diff["title"]
			if !ok || change.Old == change.New {
				t.Fatalf("改动项缺少 title 差异: %+v", it)
			}
		}
	}
	if updates != 2 {
		t.Fatalf("改动项 %d, want 2", updates)
	}

	// all：创建项（作品/发行/艺术家）与后续改动（作品第 2、3 版）混排，同一事件不重复出现。
	all := read(nil, "all", 1, 20)
	if all.Total != 6 || len(all.Items) != 6 {
		t.Fatalf("匿名 all: total=%d items=%d, want 5/5", all.Total, len(all.Items))
	}
	tabs := map[string]int{}
	for _, it := range all.Items {
		tabs[it.Tab]++
	}
	if tabs["works"] != 2 || tabs["releases"] != 1 || tabs["artists"] != 1 || tabs["revisions"] != 2 {
		t.Fatalf("all 混排分布不符: %+v", tabs)
	}

	// 分页：total 是真实计数，不随 page_size 变；越界收敛而不是拒绝。
	last := read(nil, "all", 3, 2)
	if last.Total != 6 || last.Page != 3 || last.PageSize != 2 || len(last.Items) != 2 {
		t.Fatalf("分页口径不符: total=%d page=%d size=%d items=%d", last.Total, last.Page, last.PageSize, len(last.Items))
	}
	if clamped := read(nil, "all", 0, 500); clamped.Page != 1 || clamped.PageSize != 20 {
		t.Fatalf("越界分页应静默收敛: page=%d size=%d", clamped.Page, clamped.PageSize)
	}

	// 生命周期管理动作按"发生过"计数：目标删除后同口径统计消失，但动作仍计入 audit_actions。
	doomed := save(author, Entity{Kind: "work", Title: "待清退"})
	if _, err := f.s.Lifecycle(ctx, doomed.ID, LifecycleEdit{ExpectedVersion: doomed.Version, EditNote: "清退", Sources: fixtureSources()}, admin); err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	adminStats, err := f.s.UserContributions(ctx, admin.ID, "all", 1, 20, &admin)
	if err != nil {
		t.Fatal(err)
	}
	if adminStats.Stats.AuditActions != 1 {
		t.Fatalf("删除应计入管理动作: %+v", adminStats.Stats)
	}
	// 墓碑目标对所有人不可见：管理员的"修订条数"不包含它（可见性口径），与 audit_actions 的差别是刻意的。
	if adminStats.Stats.RevisionsCount != 0 {
		t.Fatalf("墓碑目标不该计入 revisions_count: %+v", adminStats.Stats)
	}
	if after := read(&author, "works", 1, 20); after.Total != 2 {
		t.Fatalf("删除后本人作品数应为 2（另一作品 + 草稿）: %d", after.Total)
	}

	// 非法参数：Store 层按 respond 的判据返回（sql.ErrNoRows → 404 not_found / 400 invalid_tab）。
	if _, err := f.s.UserContributions(ctx, "not-a-uuid", "all", 1, 20, nil); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("非法 user id 应按 not_found 处理: %v", err)
	}
	if _, err := f.s.UserContributions(ctx, author.ID, "topics", 1, 20, nil); err == nil || err.Error() != "invalid_tab" {
		t.Fatalf("目录不服务的 tab 应报 invalid_tab: %v", err)
	}
	// 不存在的用户：目录不查账号服务（账号表归账号服务），"没有这个人"与"有这个人但零贡献"
	// 分不出来，两者都是 200 + 空列表；404 只留给"这个 id 根本不是 uuid"。
	ghost, err := f.s.UserContributions(ctx, fixtureUser("member").ID, "all", 1, 20, nil)
	if err != nil || ghost.Total != 0 || len(ghost.Items) != 0 {
		t.Fatalf("无贡献用户应返回空列表: %+v %v", ghost, err)
	}
}
