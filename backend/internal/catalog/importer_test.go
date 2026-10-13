package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	// 预览 DTO 直接装箱 Go 的 int ID。
	if v := person.Artist.ExternalIDs["bangumi_person"]; v != 9 && v != "9" && v != float64(9) {
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

func TestNormalizeImporterSource(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "bangumi"},
		{"auto", "bangumi"},
		{"Bangumi", "bangumi"},
		{"dlsite", "dlsite"},
		{"DMM", "dmm"},
	} {
		got, err := normalizeImporterSource(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("%q: got %q %v want %q", tc.in, got, err, tc.want)
		}
	}
	if _, err := normalizeImporterSource("tmdb"); err == nil {
		t.Fatal("tmdb should be not_supported")
	}
	if detectImporterSource("https://www.dlsite.com/maniax/work/=/product_id/RJ123.html") != "dlsite" {
		t.Fatal("dlsite detect")
	}
	if detectImporterSource("https://www.dmm.co.jp/dc/doujin/-/detail/=/cid=d_123/") != "dmm" {
		t.Fatal("dmm detect")
	}
	if detectImporterSource("https://bgm.tv/subject/1") != "bangumi" {
		t.Fatal("bangumi detect")
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
