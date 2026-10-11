package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubBangumi 替换 Bangumi 公开 API，覆盖 subject/person/character 三端点。
func stubBangumi(t *testing.T) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/subjects/7", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"type":2,"name":"テスト作品","name_cn":"测试作品","summary":"简介","date":"2024-01-05","platform":"TV","images":{"large":"https://example.com/cover.jpg"},"tags":[{"name":"日常","count":10}]}`))
	})
	mux.HandleFunc("/v0/persons/9", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9,"name":"John Doe","name_cn":"约翰","type":1,"career":["原作"],"summary":"作家","images":{"large":"https://example.com/avatar.jpg"}}`))
	})
	mux.HandleFunc("/v0/characters/11", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":11,"name":"小鸟游","name_cn":"","summary":"角色","images":{"large":""}}`))
	})
	// 分集端点：subject 7 有两话本篇 + 一首 OP（type=2）。其它 subject 返回空列表。
	// 按 type 分别返回，覆盖"遍历多类型并按 episode id 去重"的行为。
	mux.HandleFunc("/v0/episodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("subject_id") != "7" {
			_, _ = w.Write([]byte(`{"data":[],"total":0}`))
			return
		}
		switch r.URL.Query().Get("type") {
		case "0":
			_, _ = w.Write([]byte(`{"data":[{"id":101,"type":0,"name":"第一话","name_cn":"第一话","sort":1,"ep":1,"duration":"24m"},{"id":102,"type":0,"name":"第二話","name_cn":"第二话","sort":2,"ep":2,"duration":"24m"}],"total":2}`))
		case "2":
			_, _ = w.Write([]byte(`{"data":[{"id":201,"type":2,"name":"オープニング","name_cn":"片头曲","sort":1,"ep":0,"duration":"1m30s"}],"total":1}`))
		default:
			_, _ = w.Write([]byte(`{"data":[],"total":0}`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() {
		srv.Close()
		bangumiAPIBase = "https://api.bgm.tv"
	})
	bangumiAPIBase = srv.URL
}

func TestImporterPreviewValidation(t *testing.T) {
	// 纯参数校验不触库：Store 为空即可。
	s := &Store{}
	ctx := context.Background()

	if _, err := s.Preview(ctx, "musicbrainz", "123", "work"); err == nil || err.Error() != "not_supported" {
		t.Fatalf("unsupported source accepted: %v", err)
	}
	if _, err := s.Preview(ctx, "tmdb", "1", "work"); err == nil || err.Error() != "not_supported" {
		t.Fatalf("unsupported source accepted: %v", err)
	}
	if _, err := s.Preview(ctx, "bangumi", "not-an-id", "work"); err == nil {
		t.Fatal("invalid bangumi id accepted")
	}
	if _, err := s.Preview(ctx, "bangumi", "7", "nope"); err == nil || err.Error() != "invalid_entity_type" {
		t.Fatalf("invalid entity type accepted: %v", err)
	}
	// 非 Bangumi 站点 URL 明确拒绝，不伪造。
	// 来源主机白名单按域名后缀匹配：仿冒域不接受（notbgm.tv 曾因 Contains 而通过），
	// 官方子域照旧可用。出站请求本身永远走常量 base + 数字 ID。
	if _, err := parseBangumiRef("https://notbgm.tv/subject/7"); err == nil || err.Error() != "not_supported" {
		t.Fatalf("lookalike host must be rejected: %v", err)
	}
	if ref, err := parseBangumiRef("https://www.bgm.tv:443/subject/7"); err != nil || ref.ID != 7 {
		t.Fatalf("official subdomain with port must parse: %+v %v", ref, err)
	}
	if _, err := s.Preview(ctx, "bangumi", "https://evil.example/bgm.tv/subject/7", "work"); err == nil || err.Error() != "not_supported" {
		t.Fatalf("path-embedded host must not be treated as a bangumi URL: %v", err)
	}
	if _, err := s.Preview(ctx, "bangumi", "https://example.com/item/1", "work"); err == nil || err.Error() != "not_supported" {
		t.Fatalf("foreign url accepted: %v", err)
	}
}

func TestImporterRefParsing(t *testing.T) {
	cases := []struct {
		raw, entityType, kind string
		id                    int
	}{
		{"7", "work", "subject", 7},
		{"9", "artist", "person", 9},
		{"11", "character", "character", 11},
		{"https://bgm.tv/subject/7", "work", "subject", 7},
		{"bgm.tv/person/9", "artist", "person", 9},
		{"https://bangumi.tv/character/11", "character", "character", 11},
		{"https://bgm.tv/subject/7/ep/3", "work", "subject", 7},
	}
	for _, c := range cases {
		ref, err := resolveBangumiRef(c.raw, c.entityType)
		if err != nil || ref.Kind != c.kind || ref.ID != c.id {
			t.Fatalf("parse %q: got %+v %v", c.raw, ref, err)
		}
	}
	if _, err := parseBangumiRef(""); err == nil {
		t.Fatal("empty id accepted")
	}
	if _, err := parseBangumiRef("https://bgm.tv/subject/abc"); err == nil {
		t.Fatal("non-numeric id accepted")
	}

	// source / entity_type / link_mode 归一化。
	if src, err := normalizeImporterSource(""); err != nil || src != "bangumi" {
		t.Fatalf("default source: %q %v", src, err)
	}
	if src, err := normalizeImporterSource("AUTO"); err != nil || src != "bangumi" {
		t.Fatalf("auto source: %q %v", src, err)
	}
	if _, err := normalizeImporterSource("douban"); err == nil {
		t.Fatal("douban should be not_supported")
	}
	if et, err := normalizeImporterEntityType(""); err != nil || et != "work" {
		t.Fatalf("default entity type: %q %v", et, err)
	}
	if _, err := normalizeImporterEntityType("album"); err == nil {
		t.Fatal("album should be invalid_entity_type")
	}
	if m, err := normalizeImporterLinkMode(""); err != nil || m != "new_work" {
		t.Fatalf("default link mode: %q %v", m, err)
	}
	if _, err := normalizeImporterLinkMode("merge_everything"); err == nil {
		t.Fatal("unknown link mode accepted")
	}

	// subject type → work type 映射（Bangumi 文档：1 书籍 2 动画 3 音乐 4 游戏 6 三次元）。
	for in, want := range map[int]string{1: "novel", 2: "animation", 3: "music", 4: "game", 6: "personal", 0: "", 9: ""} {
		if got := bangumiWorkType(in); got != want {
			t.Fatalf("work type %d: got %q want %q", in, got, want)
		}
	}
	for in, want := range map[int]string{1: "person", 2: "organization", 3: "group", 0: "person"} {
		if got := bangumiAgentType(in); got != want {
			t.Fatalf("agent type %d: got %q want %q", in, got, want)
		}
	}

	// 幂等键由 url_or_id 本地解析，不发网络。
	key, ok := importDedupKey("bangumi", ImporterImportRequest{URLOrID: "https://bgm.tv/subject/7"}, "work")
	if !ok || key != "bangumi:subject:7" {
		t.Fatalf("bad dedup key: %q %v", key, ok)
	}
	if kind, id := splitDedupKey(key); kind != "subject" || id != "7" {
		t.Fatalf("bad dedup split: %q %q", kind, id)
	}
	if _, ok := importDedupKey("tmdb", ImporterImportRequest{URLOrID: "1"}, "work"); ok {
		t.Fatal("non-bangumi dedup key generated")
	}

	// Import 参数校验（触库前失败，无需 DB）。
	ctx := context.Background()
	s := &Store{}
	me := User{ID: "00000000-0000-0000-0000-000000000000"}
	if _, err := s.Import(ctx, ImporterImportRequest{EntityType: "work", Source: "vndb", URLOrID: "1", Work: &ImporterWorkPreview{Title: "x"}}, me); err == nil || !strings.Contains(err.Error(), "not_supported") {
		t.Fatalf("unsupported import accepted: %v", err)
	}
	if _, err := s.Import(ctx, ImporterImportRequest{EntityType: "work", Source: "bangumi", LinkMode: "bogus", Work: &ImporterWorkPreview{Title: "x"}}, me); err == nil {
		t.Fatal("invalid link mode import accepted")
	}
	if _, err := s.Import(ctx, ImporterImportRequest{EntityType: "work", Source: "bangumi", LinkMode: "append_release_to_work"}, me); err == nil {
		t.Fatal("append without target accepted")
	}
	// merge_translations 已从契约移除：它曾与 append_release_to_work 同义，词典承诺的
	// "补译名/简介/外部 ID"从未实现，必须显式拒绝而不是继续静默当同义词。
	if _, err := s.Import(ctx, ImporterImportRequest{EntityType: "work", Source: "bangumi", LinkMode: "merge_translations", TargetWorkID: "00000000-0000-0000-0000-0000000000ff", Work: &ImporterWorkPreview{Title: "x"}}, me); err == nil || err.Error() != "invalid_link_mode" {
		t.Fatalf("merge_translations must be rejected with invalid_link_mode: %v", err)
	}
	if _, err := buildWorkEntity(&ImporterWorkPreview{}, "bangumi", "", "", false, ""); err == nil {
		t.Fatal("empty work title accepted")
	}
}

func TestImporterPreviewBangumi(t *testing.T) {
	stubBangumi(t)
	f := newFixture(t)
	ctx := context.Background()

	work, err := f.s.Preview(ctx, "bangumi", "https://bgm.tv/subject/7", "work")
	if err != nil {
		t.Fatal(err)
	}
	if work.Source != "bangumi" || work.EntityType != "work" || work.ExternalID != "7" || work.Work == nil {
		t.Fatalf("bad subject preview: %+v", work)
	}
	if work.Work.Title != "测试作品" || work.Work.OriginalTitle != "テスト作品" || work.MediaType != "animation" {
		t.Fatalf("bad subject mapping: %+v", work.Work)
	}
	if len(work.Work.Translations) != 2 || work.ExternalURL != "https://bgm.tv/subject/7" {
		t.Fatalf("bad subject translations/url: %+v", work.Work.Translations)
	}
	if len(work.Tags) != 1 || work.Tags[0] != "日常" {
		t.Fatalf("bad subject tags: %v", work.Tags)
	}

	person, err := f.s.Preview(ctx, "auto", "https://bgm.tv/person/9", "artist")
	if err != nil {
		t.Fatal(err)
	}
	if person.Source != "bangumi" || person.EntityType != "artist" || person.Artist == nil {
		t.Fatalf("bad person preview: %+v", person)
	}
	if person.Artist.Name != "约翰" || person.Artist.OriginalName != "John Doe" || person.Artist.EntityType != "person" {
		t.Fatalf("bad person mapping: %+v", person.Artist)
	}
	// 预览 DTO 直接装箱 Go 的 int ID；经 JSON 往返后为 float64。两种形态都应保留 ID。
	if v := person.Artist.ExternalIDs["bangumi_person"]; replayNumEqual(v, 9) == false && v != "9" {
		t.Fatalf("bad person external_ids: %v", person.Artist.ExternalIDs)
	}

	character, err := f.s.Preview(ctx, "bangumi", "https://bgm.tv/character/11", "character")
	if err != nil {
		t.Fatal(err)
	}
	if character.EntityType != "character" || character.Artist == nil || character.Artist.Name != "小鸟游" {
		t.Fatalf("bad character preview: %+v", character)
	}

	digit, err := f.s.Preview(ctx, "bangumi", "9", "artist")
	if err != nil || digit.Artist == nil || digit.EntityType != "artist" {
		t.Fatalf("numeric person id unresolved: %+v %v", digit, err)
	}
}

func TestImporterImportIdempotent(t *testing.T) {
	stubBangumi(t)
	f := newFixture(t)
	ctx := context.Background()

	req := ImporterImportRequest{
		EntityType: "work",
		Source:     "bangumi",
		URLOrID:    "https://bgm.tv/subject/7",
		Work: &ImporterWorkPreview{
			Title: "测试作品", OriginalTitle: "テスト作品",
			OriginalLanguage: "zh-CN",
			Translations:     []ImporterTranslationItem{{Locale: "zh-CN", Title: "测试作品", Summary: "简介"}},
			CatalogMetadata:  map[string]any{"bangumi_type": float64(2)},
		},
		CanonicalEntries: []ImporterCanonicalEntryPreview{
			{Title: "第一话", Position: 1},
			{Title: "第二话", Position: 2},
		},
		Release:  &ImporterReleasePreview{EditionName: "初回版", EditionDate: "2024-01-05", Country: "JP"},
		Mediums:  []ImporterMediumPreview{{Position: 0, Name: "Disc 1", Format: "bd", Role: "primary", Tracks: []ImporterTrackPreview{{Position: 1, Title: "第一话"}, {Position: 2, Title: "第二话"}}}},
		EditNote: "导入测试",
		SourceURLs: []string{
			"https://bgm.tv/subject/7",
		},
	}
	first, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Success || first.WorkID == "" || first.ReleaseID == "" {
		t.Fatalf("bad import result: %+v", first)
	}
	if first.ImportedCounts.Mediums != 1 || first.ImportedCounts.Tracks != 2 {
		t.Fatalf("bad imported counts: %+v", first.ImportedCounts)
	}
	if first.RedirectURL != "/releases/"+first.ReleaseID {
		t.Fatalf("bad redirect url: %s", first.RedirectURL)
	}

	before := len(mustList(t, f, ListOptions{Kind: "work"}))
	second, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if second.WorkID != first.WorkID {
		t.Fatalf("idempotency broken: %s != %s", second.WorkID, first.WorkID)
	}
	if after := len(mustList(t, f, ListOptions{Kind: "work"})); after != before {
		t.Fatalf("duplicate work created: %d -> %d", before, after)
	}

	// append_release_to_work 挂靠已有 work，只建发行链。
	req2 := req
	req2.LinkMode = "append_release_to_work"
	req2.TargetWorkID = first.WorkID
	req2.Mediums = []ImporterMediumPreview{{Position: 0, Name: "Disc 2", Format: "dvd", Tracks: []ImporterTrackPreview{{Position: 1, Title: "第一话"}}}}
	appended, err := f.s.Import(ctx, req2, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if appended.WorkID != first.WorkID || appended.ReleaseID == first.ReleaseID {
		t.Fatalf("bad append result: %+v", appended)
	}
	if appended.RedirectURL != "/releases/"+appended.ReleaseID {
		t.Fatalf("bad append redirect: %s", appended.RedirectURL)
	}

	// agent 幂等。
	agentReq := ImporterImportRequest{
		EntityType: "artist", Source: "bangumi", URLOrID: "https://bgm.tv/person/9",
		Artist:   &ImporterArtistPreview{Name: "约翰", OriginalName: "John Doe", EntityType: "person"},
		EditNote: "导入测试", SourceURLs: []string{"https://bgm.tv/person/9"},
	}
	a1, err := f.s.Import(ctx, agentReq, f.u)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := f.s.Import(ctx, agentReq, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if a1.ArtistID == "" || a2.ArtistID != a1.ArtistID || a1.RedirectURL != "/artists/"+a1.ArtistID {
		t.Fatalf("agent idempotency broken: %+v %+v", a1, a2)
	}

	// 不支持的来源明确报错。
	if _, err := f.s.Import(ctx, ImporterImportRequest{EntityType: "work", Source: "tmdb", URLOrID: "1", Work: &ImporterWorkPreview{Title: "x"}}, f.u); err == nil || !strings.Contains(err.Error(), "not_supported") {
		t.Fatalf("unsupported import accepted: %v", err)
	}
}

// TestImporterImportNormalizesSource：Import 与 Preview 共用 normalizeImporterSource。
// Import 曾自带一套归一化（"" → bangumi，但 "auto" 原样保留），于是 source=auto 的落库
// 拿不到 importDedupKey 的幂等键，还把 external_ids 的键名写成 "auto"（不在注册表预设里，
// 带 external_id 时会被预检以 invalid_external_key: auto 拒）。三种写法现在必须同一条记录。
func TestImporterImportNormalizesSource(t *testing.T) {
	stubBangumi(t)
	f := newFixture(t)
	ctx := context.Background()

	importWith := func(source string) ImporterImportResponse {
		t.Helper()
		out, err := f.s.Import(ctx, ImporterImportRequest{
			EntityType: "work",
			Source:     source,
			URLOrID:    "https://bgm.tv/subject/7",
			Work:       &ImporterWorkPreview{Title: "来源归一化"},
			EditNote:   "来源归一化测试",
			SourceURLs: []string{"https://bgm.tv/subject/7"},
		}, f.u)
		if err != nil {
			t.Fatalf("source=%q 导入失败：%v", source, err)
		}
		return out
	}
	auto := importWith("auto")
	empty := importWith("")
	bangumi := importWith("bangumi")
	if auto.WorkID == "" {
		t.Fatal("source=auto 没有返回作品 id")
	}
	// 幂等键取归一化后的来源：三次调用必须命中同一条记录，而不是各建一份。
	if empty.WorkID != auto.WorkID || bangumi.WorkID != auto.WorkID {
		t.Fatalf("同一来源的不同写法未命中同一条记录：auto=%s empty=%s bangumi=%s", auto.WorkID, empty.WorkID, bangumi.WorkID)
	}
	// external_ids 的键名同样按归一化后的来源写。
	got, err := f.s.Get(ctx, auto.WorkID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExternalIDs["metafusion_import"] != "bangumi:subject:7" {
		t.Errorf("幂等键未按 bangumi 落库：%v", got.ExternalIDs)
	}
	if got.ExternalIDs["bangumi"] != "7" {
		t.Errorf("bangumi external id 缺失：%v", got.ExternalIDs)
	}
	if _, ok := got.ExternalIDs["auto"]; ok {
		t.Errorf("external_ids 里不该出现 auto：%v", got.ExternalIDs)
	}

	// 幂等键取不到时（url_or_id 不是 Bangumi 引用）走的是 external_id 那条写路径：
	// 手工载荷同样必须落到归一化后的来源，而不是带着 "auto" 去撞注册表预设。
	manual, err := f.s.Import(ctx, ImporterImportRequest{
		EntityType: "work",
		Source:     "auto",
		URLOrID:    "manual-payload",
		ExternalID: "999",
		Work:       &ImporterWorkPreview{Title: "手工载荷"},
		EditNote:   "手工载荷来源归一化测试",
		SourceURLs: []string{"https://bgm.tv/subject/999"},
	}, f.u)
	if err != nil {
		t.Fatalf("手工载荷导入失败：%v", err)
	}
	manualWork, err := f.s.Get(ctx, manual.WorkID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if manualWork.ExternalIDs["bangumi"] != "999" {
		t.Errorf("external_id 未按归一化后的来源落库：%v", manualWork.ExternalIDs)
	}
	if _, ok := manualWork.ExternalIDs["auto"]; ok {
		t.Errorf("external_ids 里不该出现 auto：%v", manualWork.ExternalIDs)
	}
}

// 归一化函数本身的口径（Preview 与 Import 共用）：大小写/空白归一，空值/auto 默认 bangumi。
// 未实现适配器的来源（包括注册表里的 douban / bangumi_person）返回 not_supported，
// 不因为"注册表认识"就放行；Preview 的 auto 探测在调用此函数前完成。
func TestNormalizeImporterSourceSharedByBothEndpoints(t *testing.T) {
	for _, in := range []string{"", "auto", "AUTO", "  Auto  ", "bangumi", "Bangumi"} {
		if got, err := normalizeImporterSource(in); err != nil || got != "bangumi" {
			t.Errorf("normalizeImporterSource(%q)=%q,%v，want bangumi,nil", in, got, err)
		}
	}
	for _, in := range []string{"tmdb", "musicbrainz", "douban", "bangumi_person", "plugin:x"} {
		if _, err := normalizeImporterSource(in); err == nil || err.Error() != "not_supported" {
			t.Errorf("normalizeImporterSource(%q) 未按 not_supported 拒绝：%v", in, err)
		}
	}
	// 未知来源在 Import 里于任何写之前就被拒：空 Store（无库）也能拿到同一个错误码。
	s := &Store{}
	_, err := s.Import(context.Background(), ImporterImportRequest{
		EntityType: "work", Source: "tmdb", URLOrID: "1", Work: &ImporterWorkPreview{Title: "x"},
	}, User{ID: "u1"})
	if err == nil || err.Error() != "not_supported" {
		t.Fatalf("未知来源未被落库端点拒绝：%v", err)
	}
}

func mustList(t *testing.T, f fixture, o ListOptions) []Entity {
	t.Helper()
	items, err := f.s.List(context.Background(), o, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// tracksOfRelease 先列载体再按 MediumID 查曲目：List 的 ReleaseID 过滤只命中
// catalog.mediums，直接用它查 track 会得到 0 条。
func tracksOfRelease(t *testing.T, f fixture, releaseID string) []Entity {
	t.Helper()
	var tracks []Entity
	for _, m := range mustList(t, f, ListOptions{Kind: "medium", ReleaseID: releaseID}) {
		tracks = append(tracks, mustList(t, f, ListOptions{Kind: "track", MediumID: m.ID})...)
	}
	return tracks
}

// TestImporterImportMultiDiscExpressionMatching：多盘发行的表达对齐。
// 跨盘同轨号不再误判为同一内容；同一份载荷内声明的 canonical 表达按唯一同名绑定；
// 曲目携带的 ISRC 落录音本体（expression.external_ids），不写 track.attributes。
func TestImporterImportMultiDiscExpressionMatching(t *testing.T) {
	stubBangumi(t)
	f := newFixture(t)
	ctx := context.Background()

	req := ImporterImportRequest{
		EntityType: "work",
		Source:     "bangumi",
		URLOrID:    "https://bgm.tv/subject/7",
		Work: &ImporterWorkPreview{
			Title:            "多盘作品",
			OriginalTitle:    "テスト作品",
			OriginalLanguage: "zh-CN",
			CatalogMetadata:  map[string]any{"bangumi_type": float64(2)},
		},
		CanonicalEntries: []ImporterCanonicalEntryPreview{
			{Title: "夜航", Position: 1},
			{Title: "星海", Position: 2},
		},
		Release: &ImporterReleasePreview{EditionName: "双盘限定"},
		Mediums: []ImporterMediumPreview{
			{Position: 0, Name: "Disc 1", Format: "cd", Tracks: []ImporterTrackPreview{
				{Position: 1, Title: "夜航"},
				{Position: 2, Title: "星海"},
			}},
			{Position: 1, Name: "Disc 2", Format: "bd", Tracks: []ImporterTrackPreview{
				// 轨号与 Disc 1 Track 1 相同，但内容不同：不得复用"夜航"的表达。
				{Position: 1, Title: "幕间映像", ISRC: "JPB992600010"},
				// 同名曲跨盘收录：应复用"夜航"的既有表达。
				// recording_mbid 必须是合法 MBID（预检与 Store.Save 同口径的 UUID 正则）；
				// 这里只作查找线索，命中同名条目表达后不会写库。
				{Position: 2, Title: "夜航", RecordingMBID: "11111111-2222-3333-4444-555555555555"},
			}},
		},
		EditNote:   "多盘对齐测试",
		SourceURLs: []string{"https://bgm.tv/subject/7"},
	}
	out, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Success || out.ReleaseID == "" {
		t.Fatalf("bad import result: %+v", out)
	}
	if out.ImportedCounts.Mediums != 2 || out.ImportedCounts.Tracks != 4 {
		t.Fatalf("bad imported counts: %+v", out.ImportedCounts)
	}

	exprs := mustList(t, f, ListOptions{Kind: "expression", WorkID: out.WorkID})
	if len(exprs) != 3 {
		t.Fatalf("expected 3 expressions, got %d", len(exprs))
	}
	exprIDByTitle := map[string]string{}
	for _, e := range exprs {
		exprIDByTitle[e.Title] = e.ID
	}

	// track 属于 medium（release→medium→track 两级）：List 的 ReleaseID 过滤只命中
	// mediums 表，须先列载体再按 MediumID 查曲目。
	meds := mustList(t, f, ListOptions{Kind: "medium", ReleaseID: out.ReleaseID})
	if len(meds) != 2 {
		t.Fatalf("expected 2 mediums, got %d", len(meds))
	}
	var tracks []Entity
	for _, m := range meds {
		trs := mustList(t, f, ListOptions{Kind: "track", MediumID: m.ID})
		tracks = append(tracks, trs...)
	}
	if len(tracks) != 4 {
		t.Fatalf("expected 4 tracks, got %d", len(tracks))
	}
	exprOf := map[string]string{}
	for _, tr := range tracks {
		full, err := f.s.Get(ctx, tr.ID, &f.u)
		if err != nil {
			t.Fatal(err)
		}
		if len(full.Contents) != 1 {
			t.Fatalf("track %q has %d contents, want 1", full.Title, len(full.Contents))
		}
		exprOf[full.Title] = full.Contents[0].ExpressionID
		// ISRC 属于录音本体，不再写进 track.attributes（track 定义只有 duration/role）。
		if _, ok := full.Attributes["isrc"]; ok {
			t.Fatalf("ISRC must not be stored on track.attributes: %v", full.Attributes)
		}
	}
	// 跨盘同名曲复用同一表达。
	if exprOf["夜航"] == "" || exprOf["夜航"] != exprIDByTitle["夜航"] {
		t.Fatalf("Disc2 夜航 did not reuse canonical expression: %q vs %q", exprOf["夜航"], exprIDByTitle["夜航"])
	}
	if exprOf["幕间映像"] == exprIDByTitle["夜航"] {
		t.Fatal("Disc2 Track 1 wrongly reused Disc1 Track 1 expression by position")
	}
	if exprOf["幕间映像"] != exprIDByTitle["幕间映像"] {
		t.Fatalf("幕间映像 linked to unexpected expression: %q", exprOf["幕间映像"])
	}
	// ISRC 应落在新建表达的 external_ids 上，供后续权威匹配复用。
	for _, e := range exprs {
		if e.Title != "幕间映像" {
			continue
		}
		if e.ExternalIDs["isrc"] != "JPB992600010" {
			t.Fatalf("ISRC not stored on expression external_ids: %v", e.ExternalIDs)
		}
	}
}

// character_in 的番位落 attributes.character_rank（definitions 的角色番位字段），
// 不再写 role（载体用途/收录内容词表）；原始番位文本仍在 credit_role，检索与保真兼得。
func TestImporterImportCharacterRankAttribute(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	req := ImporterImportRequest{
		EntityType: "work",
		Source:     "bangumi",
		URLOrID:    "9",
		Work: &ImporterWorkPreview{
			Title:            "番位落点",
			OriginalLanguage: "zh-CN",
			CatalogMetadata:  map[string]any{"bangumi_type": float64(2)},
		},
		StaffAssociations: []ImporterStaffAssociation{
			{ParsedName: "登场角色", EntityType: "character", ParsedRole: "主角", RelationType: "character_in", RelationRole: "main"},
		},
		EditNote:   "番位落点测试",
		SourceURLs: []string{"https://bgm.tv/character/1"},
	}
	out, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	rels, err := f.s.Relations(ctx, out.WorkID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rels {
		if r.Type != "character_in" {
			continue
		}
		found = true
		if got := r.Attributes["character_rank"]; got != "main" {
			t.Fatalf("character_rank not stored: %v", r.Attributes)
		}
		if _, ok := r.Attributes["role"]; ok {
			t.Fatalf("rank must not be written to role: %v", r.Attributes)
		}
		if got := r.Attributes["credit_role"]; got != "主角" {
			t.Fatalf("raw rank text must stay in credit_role: %v", r.Attributes)
		}
	}
	if !found {
		t.Fatal("character_in relation not created")
	}
}

// 发行复用必须校验该发行是否声明了目标作品：同一来源身份撞上同一发行幂等键、
// 但目标作品不同（换了 target_work_id）时拒绝复用，不把本次载体/曲目挂进别人的发行。
func TestImporterReleaseReuseRequiresWorkDeclaration(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := ImporterImportRequest{
		EntityType: "work", Source: "bangumi", URLOrID: "https://bgm.tv/subject/7",
		Work:       &ImporterWorkPreview{Title: "作品甲", OriginalLanguage: "zh-CN", CatalogMetadata: map[string]any{"bangumi_type": float64(2)}},
		Release:    &ImporterReleasePreview{EditionName: "初回版"},
		Mediums:    []ImporterMediumPreview{{Position: 0, Name: "Disc 1", Format: "cd", Tracks: []ImporterTrackPreview{{Position: 1, Title: "第一话"}}}},
		EditNote:   "发行复用校验",
		SourceURLs: []string{"https://bgm.tv/subject/7"},
	}
	out, err := f.s.Import(ctx, first, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if out.ReleaseID == "" {
		t.Fatal("first import must build the release chain")
	}
	before := len(mustList(t, f, ListOptions{Kind: "medium", ReleaseID: out.ReleaseID}))
	// 同一来源身份挂到另一个目标作品：发行基础键相同，但该发行只声明了"作品甲"。
	target := f.save(Entity{Kind: "work", Title: "作品乙"})
	second := first
	second.LinkMode = "append_release_to_work"
	second.TargetWorkID = target.ID
	if _, err := f.s.Import(ctx, second, f.u); err == nil || !strings.Contains(err.Error(), "undeclared_release_subject") {
		t.Fatalf("reusing a release that does not declare the work must fail: %v", err)
	}
	if after := len(mustList(t, f, ListOptions{Kind: "medium", ReleaseID: out.ReleaseID})); after != before {
		t.Fatalf("failed reuse must not touch the other release: %d -> %d", before, after)
	}
}

// 预检零写入：外部编号非法（与 Store.Save 同一套正则/分类校验）、篇目缺标题、
// create_relation 关系码非法，都必须在建 work/release/medium 之前失败，不留半成品。
func TestImporterPreflightKeepsZeroWrites(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	works := func() int { return len(mustList(t, f, ListOptions{Kind: "work"})) }

	badExternal := ImporterImportRequest{
		EntityType: "work", Source: "bangumi", URLOrID: "https://bgm.tv/subject/7",
		Work:             &ImporterWorkPreview{Title: "外部编号预检", OriginalLanguage: "zh-CN", CatalogMetadata: map[string]any{"bangumi_type": float64(2)}},
		CanonicalEntries: []ImporterCanonicalEntryPreview{{Title: "第一话", ExternalIDs: map[string]any{"isrc": "NOT-AN-ISRC"}}},
		EditNote:         "零写入探针", SourceURLs: []string{"https://bgm.tv/subject/7"},
	}
	before := works()
	if _, err := f.s.Import(ctx, badExternal, f.u); err == nil || !strings.Contains(err.Error(), "invalid_external_id") {
		t.Fatalf("bad external id must fail preflight, got %v", err)
	}
	if after := works(); after != before {
		t.Fatalf("preflight failure left partial data: works %d -> %d", before, after)
	}

	missingTitle := badExternal
	missingTitle.CanonicalEntries = []ImporterCanonicalEntryPreview{{Title: "  "}}
	if _, err := f.s.Import(ctx, missingTitle, f.u); err == nil || !strings.Contains(err.Error(), "canonical_entries[0].title") {
		t.Fatalf("missing entry title must fail preflight, got %v", err)
	}
	if after := works(); after != before {
		t.Fatalf("title preflight failure left partial data: works %d -> %d", before, after)
	}

	badRelation := badExternal
	badRelation.CanonicalEntries = nil
	badRelation.LinkMode = "create_relation"
	badRelation.TargetWorkID = "00000000-0000-0000-0000-0000000000ff"
	badRelation.RelationType = "no_such_relation"
	if _, err := f.s.Import(ctx, badRelation, f.u); err == nil || !strings.Contains(err.Error(), "importer_mapping_stale") {
		t.Fatalf("unknown relation type must fail preflight, got %v", err)
	}
	if after := works(); after != before {
		t.Fatalf("relation preflight failure left partial data: works %d -> %d", before, after)
	}
}

// importerApplyFieldSwitches 的两条现行行为：has_release 与 mediums 必须自洽，
// download_cover 显式 false 不引用远端封面/头像（未传保持既有透传）。
func TestImporterApplyFieldSwitches(t *testing.T) {
	base := func() ImporterImportRequest {
		return ImporterImportRequest{
			Work:              &ImporterWorkPreview{Title: "作品", CoverImageURL: "https://example.com/cover.jpg"},
			Artist:            &ImporterArtistPreview{Name: "甲", AvatarURL: "https://example.com/avatar.jpg"},
			Artists:           []ImporterArtistPreview{{Name: "乙", AvatarURL: "https://example.com/avatar2.jpg"}},
			StaffAssociations: []ImporterStaffAssociation{{ParsedName: "丙", AvatarURL: "https://example.com/avatar3.jpg"}},
			Release:           &ImporterReleasePreview{EditionName: "初回", CoverImageURL: "https://example.com/rel.jpg"},
		}
	}
	// 未传 download_cover：保持既有透传行为
	got, err := importerApplyFieldSwitches(base())
	if err != nil {
		t.Fatal(err)
	}
	if got.Work.CoverImageURL == "" || got.Artist.AvatarURL == "" || got.Artists[0].AvatarURL == "" || got.StaffAssociations[0].AvatarURL == "" {
		t.Fatalf("absent download_cover must keep remote picture references: %+v", got)
	}
	// 显式 false：不引用远端封面/头像
	req := base()
	no := false
	req.DownloadCover = &no
	got, err = importerApplyFieldSwitches(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Work.CoverImageURL != "" || got.Release.CoverImageURL != "" || got.Artist.AvatarURL != "" || got.Artists[0].AvatarURL != "" || got.StaffAssociations[0].AvatarURL != "" {
		t.Fatalf("download_cover=false must drop remote pictures: %+v", got)
	}
	if req.Work.CoverImageURL == "" {
		t.Fatal("caller payload must not be mutated in place")
	}
	// has_release 与 mediums 必须自洽
	claimed := base()
	claimed.HasRelease = true
	if _, err := importerApplyFieldSwitches(claimed); err == nil || !strings.Contains(err.Error(), "has_release") {
		t.Fatalf("has_release without mediums must be rejected: %v", err)
	}
	claimed.Mediums = []ImporterMediumPreview{{Format: "cd"}}
	if _, err := importerApplyFieldSwitches(claimed); err != nil {
		t.Fatalf("has_release with mediums must pass: %v", err)
	}
}

// download_cover 的落库效果（DB）：显式 false 不写 Picture，未传时保持远端 URL 引用；
// has_release 与 mediums 不一致时零写入。
func TestImporterImportHonoursDownloadCoverSwitch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	works := func() int { return len(mustList(t, f, ListOptions{Kind: "work"})) }
	before := works()

	no := false
	req := ImporterImportRequest{
		EntityType: "work", Source: "bangumi", URLOrID: "https://bgm.tv/subject/7",
		Work: &ImporterWorkPreview{
			Title: "不要封面", OriginalLanguage: "zh-CN",
			CoverImageURL:   "https://example.com/cover.jpg",
			CatalogMetadata: map[string]any{"bangumi_type": float64(2)},
		},
		DownloadCover: &no,
		EditNote:      "封面开关",
		SourceURLs:    []string{"https://bgm.tv/subject/7"},
	}
	out, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Work.Pictures) != 0 {
		t.Fatalf("download_cover=false must not store pictures: %+v", out.Work.Pictures)
	}

	// 未传（nil）时保持既有行为：远端封面仍作为 URL 引用落库
	withCover := req
	withCover.URLOrID = "https://bgm.tv/subject/8"
	withCover.DownloadCover = nil
	withCover.Work = &ImporterWorkPreview{
		Title: "要封面", OriginalLanguage: "zh-CN",
		CoverImageURL:   "https://example.com/cover2.jpg",
		CatalogMetadata: map[string]any{"bangumi_type": float64(2)},
	}
	out, err = f.s.Import(ctx, withCover, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Work.Pictures) != 1 || out.Work.Pictures[0].URL != "https://example.com/cover2.jpg" {
		t.Fatalf("absent download_cover must keep the remote cover reference: %+v", out.Work.Pictures)
	}

	// has_release=true 却不带载体：明确拒绝且不建作品
	inconsistent := req
	inconsistent.URLOrID = "https://bgm.tv/subject/9"
	inconsistent.HasRelease = true
	if _, err := f.s.Import(ctx, inconsistent, f.u); err == nil || !strings.Contains(err.Error(), "has_release") {
		t.Fatalf("has_release without mediums must be rejected: %v", err)
	}
	if after := works(); after != before+2 {
		t.Fatalf("rejected payload must not create entities: works %d -> %d", before, after)
	}
}

// TestImporterPreviewEpisodes：动画条目预览应带分集 canonical entries
// （entry_kind=content_unit、带官方集号与来源时长），无分集的条目为空。
// Preview 只走上游 HTTP、不触库，因此无需数据库夹具。
func TestImporterPreviewEpisodes(t *testing.T) {
	stubBangumi(t)
	s := &Store{}
	ctx := context.Background()

	work, err := s.Preview(ctx, "bangumi", "7", "work")
	if err != nil {
		t.Fatal(err)
	}
	if len(work.CanonicalEntries) != 3 {
		t.Fatalf("expected 3 episode entries (2 main + 1 OP), got %d", len(work.CanonicalEntries))
	}
	first := work.CanonicalEntries[0]
	if first.EntryKind != "content_unit" || first.Number != "1" || first.Title != "第一话" {
		t.Fatalf("bad episode entry: %+v", first)
	}
	if first.DurationSeconds != 24*60 {
		t.Fatalf("episode duration not parsed: %v", first.DurationSeconds)
	}
	if work.CanonicalEntries[1].Number != "2" {
		t.Fatalf("second episode number wrong: %+v", work.CanonicalEntries[1])
	}
	// OP（type=2）不应被 type=0 的固定查询漏掉，role 应映射为非本篇。
	var op *ImporterCanonicalEntryPreview
	for i := range work.CanonicalEntries {
		if work.CanonicalEntries[i].ExternalIDs["bangumi_episode"] == 201 {
			op = &work.CanonicalEntries[i]
		}
	}
	if op == nil {
		t.Fatal("OP episode (type=2) missing from preview")
	}
	if op.EntryRole == "main" {
		t.Fatalf("OP episode role should not be main: %+v", op)
	}
}

// TestImporterImportEpisodeTree：分集导入应落 ContentUnit 树并通过 work_id 归属，
// 且曲目命中篇目标题时表达挂到该单元下；重复导入不重复建篇目。
func TestImporterImportEpisodeTree(t *testing.T) {
	stubBangumi(t)
	f := newFixture(t)
	ctx := context.Background()

	work, err := f.s.Preview(ctx, "bangumi", "7", "work")
	if err != nil {
		t.Fatal(err)
	}
	req := ImporterImportRequest{
		EntityType: "work",
		Source:     "bangumi",
		URLOrID:    "https://bgm.tv/subject/7",
		Work: &ImporterWorkPreview{
			Title: "测试作品", OriginalTitle: "テスト作品", OriginalLanguage: "ja",
			CatalogMetadata: map[string]any{"bangumi_type": float64(2)},
		},
		// 无 Mediums：只建章节树，验证 content_unit 归属。
		CanonicalEntries: work.CanonicalEntries,
		EditNote:         "分集导入测试",
		SourceURLs:       []string{"https://bgm.tv/subject/7"},
	}
	out, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if out.ImportedCounts.ContentUnits != 3 {
		t.Fatalf("expected 3 content units (2 main + 1 OP), got %d", out.ImportedCounts.ContentUnits)
	}
	units := mustList(t, f, ListOptions{Kind: "content_unit", WorkID: out.WorkID})
	if len(units) != 3 {
		t.Fatalf("expected 3 content units in store, got %d", len(units))
	}
	for _, u := range units {
		if u.WorkID != out.WorkID {
			t.Fatalf("content unit not scoped to work: %+v", u)
		}
	}
	// 重复导入：篇目按来源 ID 复用，不重复建。
	again, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if again.WorkID != out.WorkID {
		t.Fatalf("idempotency broken: %s != %s", again.WorkID, out.WorkID)
	}
	if n := len(mustList(t, f, ListOptions{Kind: "content_unit", WorkID: out.WorkID})); n != 3 {
		t.Fatalf("duplicate content units created: %d", n)
	}
	// entry_role 必须真正落库：OP（bangumi_episode=201）应带 opening，而非被丢掉。
	unitsAfter := mustList(t, f, ListOptions{Kind: "content_unit", WorkID: out.WorkID})
	roleByExternal := map[string]string{}
	for _, u := range unitsAfter {
		full, gerr := f.s.Get(ctx, u.ID, &f.u)
		if gerr != nil {
			t.Fatal(gerr)
		}
		roleByExternal[scalarString(full.ExternalIDs["bangumi_episode"])] = scalarString(full.Attributes["entry_role"])
	}
	if roleByExternal["201"] != "opening" {
		t.Fatalf("OP entry_role not persisted: %+v", roleByExternal)
	}
	if roleByExternal["101"] != "main" {
		t.Fatalf("main entry_role not persisted: %+v", roleByExternal)
	}
}

// TestImporterImportRepeatKeepsExpressionCount：重复导入不得重复建录音。
// 旧实现先建 canonical 表达、之后才按轨位跳过已存在的曲目，重试会攒下一批
// 无实际收录的孤儿表达；验收必须同时检查 Work/Release/Track 与 Expression 数量及引用。
func TestImporterImportRepeatKeepsExpressionCount(t *testing.T) {
	stubBangumi(t)
	f := newFixture(t)
	ctx := context.Background()

	req := ImporterImportRequest{
		EntityType: "work",
		Source:     "bangumi",
		URLOrID:    "https://bgm.tv/subject/7",
		Work: &ImporterWorkPreview{
			Title: "重试作品", OriginalTitle: "リトライ作品", OriginalLanguage: "ja",
			CatalogMetadata: map[string]any{"bangumi_type": float64(2)},
		},
		// 无权威外部编号的曲目：最容易被重试重复建表达。
		CanonicalEntries: []ImporterCanonicalEntryPreview{{Title: "片头曲A", Position: 1}, {Title: "片头曲B", Position: 2}},
		Release:          &ImporterReleasePreview{EditionName: "通常版", CatalogNumber: "JP-001", Country: "JP"},
		Mediums: []ImporterMediumPreview{{Position: 0, Name: "Disc 1", Format: "cd", Tracks: []ImporterTrackPreview{
			{Position: 1, Title: "片头曲A"},
			{Position: 2, Title: "片头曲B"},
		}}},
		EditNote:   "重复导入表达计数测试",
		SourceURLs: []string{"https://bgm.tv/subject/7"},
	}
	first, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	exprsFirst := mustList(t, f, ListOptions{Kind: "expression", WorkID: first.WorkID})
	tracksFirst := tracksOfRelease(t, f, first.ReleaseID)
	if len(exprsFirst) == 0 || len(tracksFirst) != 2 {
		t.Fatalf("bad first import: exprs=%d tracks=%d", len(exprsFirst), len(tracksFirst))
	}
	refsFirst := map[string]string{}
	for _, tr := range tracksFirst {
		full, gerr := f.s.Get(ctx, tr.ID, &f.u)
		if gerr != nil {
			t.Fatal(gerr)
		}
		if len(full.Contents) != 1 {
			t.Fatalf("track %q has %d contents", full.Title, len(full.Contents))
		}
		refsFirst[full.Title] = full.Contents[0].ExpressionID
	}

	second, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if second.ReleaseID != first.ReleaseID || second.WorkID != first.WorkID {
		t.Fatalf("repeat import created new chain: %s/%s vs %s/%s", second.WorkID, second.ReleaseID, first.WorkID, first.ReleaseID)
	}
	exprsSecond := mustList(t, f, ListOptions{Kind: "expression", WorkID: first.WorkID})
	tracksSecond := tracksOfRelease(t, f, first.ReleaseID)
	if len(exprsSecond) != len(exprsFirst) {
		t.Fatalf("repeat import duplicated expressions: %d -> %d", len(exprsFirst), len(exprsSecond))
	}
	if len(tracksSecond) != len(tracksFirst) {
		t.Fatalf("repeat import duplicated tracks: %d -> %d", len(tracksFirst), len(tracksSecond))
	}
	// 引用必须完全不变。
	for _, tr := range tracksSecond {
		full, gerr := f.s.Get(ctx, tr.ID, &f.u)
		if gerr != nil {
			t.Fatal(gerr)
		}
		if len(full.Contents) != 1 || full.Contents[0].ExpressionID != refsFirst[full.Title] {
			t.Fatalf("track %q binding changed on retry: %+v", full.Title, full.Contents)
		}
	}
}

// TestImporterRegionVariantsNotMerged：同作品下追加的不同地区同名发行是两个发行。
// 版名/曲目结构一致但品番与地区不同（JP-001 vs TW-002）时不得被幂等键误并。
func TestImporterRegionVariantsNotMerged(t *testing.T) {
	stubBangumi(t)
	f := newFixture(t)
	ctx := context.Background()

	base := ImporterImportRequest{
		EntityType:       "work",
		Source:           "bangumi",
		URLOrID:          "https://bgm.tv/subject/7",
		Work:             &ImporterWorkPreview{Title: "地区版作品", OriginalLanguage: "ja", CatalogMetadata: map[string]any{"bangumi_type": float64(2)}},
		CanonicalEntries: []ImporterCanonicalEntryPreview{{Title: "主题曲", Position: 1}},
		Mediums:          []ImporterMediumPreview{{Position: 0, Name: "Disc 1", Format: "cd", Tracks: []ImporterTrackPreview{{Position: 1, Title: "主题曲"}}}},
		EditNote:         "地区版测试",
		SourceURLs:       []string{"https://bgm.tv/subject/7"},
	}
	jpReq := base
	jpReq.Release = &ImporterReleasePreview{EditionName: "原声集", CatalogNumber: "JP-001", Country: "JP"}
	jp, err := f.s.Import(ctx, jpReq, f.u)
	if err != nil {
		t.Fatal(err)
	}
	twReq := base
	twReq.LinkMode = "append_release_to_work"
	twReq.TargetWorkID = jp.WorkID
	twReq.Release = &ImporterReleasePreview{EditionName: "原声集", CatalogNumber: "TW-002", Country: "TW"}
	tw, err := f.s.Import(ctx, twReq, f.u)
	if err != nil {
		t.Fatal(err)
	}
	if tw.ReleaseID == jp.ReleaseID {
		t.Fatalf("different region/catalog variant merged into one release: %s", tw.ReleaseID)
	}
	if n := len(mustList(t, f, ListOptions{Kind: "release", WorkID: jp.WorkID})); n != 2 {
		t.Fatalf("expected 2 releases for two region variants, got %d", n)
	}
}

// TestImporterEntryIndexStructuralBinding：曲目按 entry_index 绑定清单内对应条目，
// 标题相同也能区分（不再靠标题传递绑定）。
func TestImporterEntryIndexStructuralBinding(t *testing.T) {
	stubBangumi(t)
	f := newFixture(t)
	ctx := context.Background()

	zero, one := 0, 1
	req := ImporterImportRequest{
		EntityType: "work",
		Source:     "bangumi",
		URLOrID:    "https://bgm.tv/subject/7",
		Work:       &ImporterWorkPreview{Title: "结构绑定作品", OriginalLanguage: "ja", CatalogMetadata: map[string]any{"bangumi_type": float64(2)}},
		// 两条同名条目（不同来源编号），曲目各自指向不同下标。
		CanonicalEntries: []ImporterCanonicalEntryPreview{
			{Title: "同名曲", Position: 1, ExternalIDs: map[string]any{"bangumi_episode": 301}},
			{Title: "同名曲", Position: 2, ExternalIDs: map[string]any{"bangumi_episode": 302}},
		},
		Release: &ImporterReleasePreview{EditionName: "双版", CatalogNumber: "JP-9"},
		Mediums: []ImporterMediumPreview{{Position: 0, Name: "Disc 1", Format: "cd", Tracks: []ImporterTrackPreview{
			{Position: 1, Title: "同名曲", EntryIndex: &one},
			{Position: 2, Title: "同名曲", EntryIndex: &zero},
		}}},
		EditNote:   "结构绑定测试",
		SourceURLs: []string{"https://bgm.tv/subject/7"},
	}
	out, err := f.s.Import(ctx, req, f.u)
	if err != nil {
		t.Fatal(err)
	}
	exprs := mustList(t, f, ListOptions{Kind: "expression", WorkID: out.WorkID})
	byEpisode := map[string]string{}
	for _, e := range exprs {
		byEpisode[scalarString(e.ExternalIDs["bangumi_episode"])] = e.ID
	}
	if len(byEpisode) != 2 {
		t.Fatalf("expected two distinct expressions for same-titled entries: %+v", byEpisode)
	}
	tracks := tracksOfRelease(t, f, out.ReleaseID)
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	for _, tr := range tracks {
		full, gerr := f.s.Get(ctx, tr.ID, &f.u)
		if gerr != nil {
			t.Fatal(gerr)
		}
		if len(full.Contents) != 1 {
			t.Fatalf("track %q has %d contents", full.Title, len(full.Contents))
		}
		wantEpisode := "302" // Track 1 → 下标 1
		if full.Position == 2 {
			wantEpisode = "301" // Track 2 → 下标 0
		}
		if full.Contents[0].ExpressionID != byEpisode[wantEpisode] {
			t.Fatalf("track pos %d bound wrong expression: got %s want %s(%s)", full.Position, full.Contents[0].ExpressionID, wantEpisode, byEpisode[wantEpisode])
		}
	}
}

// M04 删后禁自动重导完整流程：导入→删除→重导直接返回稳定业务错误 import_deleted
// （带墓碑恢复入口），不删墓碑、不复活、不撞唯一索引循环报错、不新建。
func TestPostgresDeletedReimportReturnsStableError(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	req := importerValuePayload()
	first, err := f.s.Import(ctx, req, f.u)
	if err != nil || first.WorkID == "" {
		t.Fatalf("首导应成功：%+v %v", first, err)
	}
	cur, err := f.s.Get(ctx, first.WorkID, &f.u)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.Lifecycle(ctx, first.WorkID, LifecycleEdit{
		ExpectedVersion: cur.Version, EditNote: "m04 delete", Sources: fixtureSources(),
	}, f.u); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.s.findImported(ctx, "bangumi:subject:7", &f.u); ok {
		t.Fatal("已删除键必须视为未命中")
	}
	before := countEntities(t, f, "")
	_, err = f.s.Import(ctx, req, f.u)
	if err == nil || !strings.Contains(err.Error(), "import_deleted") {
		t.Fatalf("删后重导应报稳定业务错误 import_deleted，实际 %v", err)
	}
	if !strings.Contains(err.Error(), first.WorkID) {
		t.Fatalf("错误应带墓碑恢复入口（墓碑 id），实际 %v", err)
	}
	if after := countEntities(t, f, ""); after != before {
		t.Fatalf("删后重导不得新建：实体总数 %d -> %d", before, after)
	}
	tomb, gerr := f.s.Get(ctx, first.WorkID, &f.u)
	if gerr != nil || tomb.Status != "deleted" {
		t.Fatalf("墓碑必须保留且不得被复活：%+v %v", tomb, gerr)
	}
}
