package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// parseDMMRef 各形态。
func TestParseDMMRef(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		channel string
		cid     string
		wantErr string
	}{
		{"数字同人 URL", "https://www.dmm.co.jp/dc/doujin/-/detail/=/cid=d_123456/", "dc/doujin", "d_123456", ""},
		{"实体同人 URL", "https://www.dmm.co.jp/mono/doujin/-/detail/=/cid=b_654321/", "mono/doujin", "b_654321", ""},
		{"下载动画 URL", "https://www.dmm.co.jp/digital/anim/-/detail/=/cid=654abc/", "digital/anim", "654abc", ""},
		{"视频 URL", "https://www.dmm.co.jp/digital/vug2/-/detail/=/cid=194abc/", "digital/vug2", "194abc", ""},
		{"漫画 URL", "https://www.dmm.co.jp/digital/comic/-/detail/=/cid=cm_001/", "digital/comic", "cm_001", ""},
		{"dlsoft URL", "https://dlsoft.dmm.co.jp/detail/abc_001/", "dlsoft", "abc_001", ""},
		{"裸 cid", "d_777777", "dc/doujin", "d_777777", ""},
		{"仿冒域名", "https://www.notdmm.co.jp/dc/doujin/cid=d_1/", "", "", "not_supported"},
		{"URL 无 cid", "https://www.dmm.co.jp/dc/doujin/-/detail/", "", "", "invalid_payload"},
		{"空输入", "", "", "", "invalid_payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := parseDMMRef(tc.input)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err=%v, want %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if ref.Channel != tc.channel || ref.CID != tc.cid {
				t.Fatalf("ref=%+v, want channel=%s cid=%s", ref, tc.channel, tc.cid)
			}
		})
	}
}

// dmmWorkType 与 medium format 映射。
func TestDMMWorkTypeAndFormat(t *testing.T) {
	workCases := []struct {
		channel, form string
		genres        []string
		want          string
	}{
		{"dc/doujin", "", []string{"ボイス・ASMR"}, "audio"},
		{"dc/doujin", "", []string{"ゲーム"}, "indie_game"},
		{"dc/doujin", "", []string{"コミック"}, "comic"},
		{"dc/doujin", "", []string{"アニメ"}, "animation"},
		{"dc/doujin", "", []string{"その他"}, "personal"},
		{"dc/doujin", "コミック", nil, "comic"},
		{"dc/doujin", "ボイス・ASMR", nil, "audio"},
		{"mono/doujin", "", nil, "novel"},
		{"digital/comic", "", nil, "comic"},
		{"digital/book", "", nil, "novel"},
		{"digital/anim", "", nil, "animation"},
		{"mono/anime", "", nil, "animation"},
		{"digital/vug2", "", nil, "film"},
		{"mono/game", "", nil, "game"},
		{"dlsoft", "ゲーム", nil, "game"},
		{"unknown/x", "", nil, "personal"},
	}
	for _, tc := range workCases {
		if got := dmmWorkType(tc.channel, tc.form, tc.genres); got != tc.want {
			t.Errorf("channel=%s form=%s got=%s want=%s", tc.channel, tc.form, got, tc.want)
		}
	}
	formatCases := map[string]string{
		"mono/doujin": "paper", "mono/anime": "dvd", "mono/game": "",
		"dc/doujin": "digital", "dlsoft": "digital", "digital/vug2": "digital",
	}
	for channel, want := range formatCases {
		if got := dmmMediumFormat(channel); got != want {
			t.Errorf("channel=%s format=%s want=%s", channel, got, want)
		}
	}
}

// dmmJSONLDPage：JSON-LD 缺日期/标签，由 informationList 兜底。
const dmmJSONLDPage = `<!doctype html><html><head>
<script type="application/ld+json">
{"@type":"Product","name":"テスト同人音声","image":["https://pics.dmm.co.jp/digital/doujin/d_123456/package.jpg"],"description":"商品の説明文","brand":{"@type":"Brand","name":"テストサークル"},"sku":"d_123456"}
</script>
</head><body>
<dl><dt class="informationList__ttl">配信開始日</dt><dd class="informationList__txt">2022/11/20 00:00</dd></dl>
<dl><dt class="informationList__ttl">作品形式</dt><dd class="informationList__txt">ボイス・ASMR</dd></dl>
<dl><dt class="informationList__ttl">ジャンル</dt><dd class="informationList__item"><ul class="genreTagList"><li><a href="x" class="genreTag__txt">ボイス・ASMR</a></li><li><a href="x" class="genreTag__txt">癒やし</a></li></ul></dd></dl>
</body></html>`

const dmmOGOnlyPage = `<!doctype html><html><head>
<meta property="og:title" content="OGタイトル">
<meta property="og:description" content="OG説明文">
<meta property="og:image" content="https://pics.dmm.co.jp/ogcover.jpg">
</head><body></body></html>`

// newDMMTestServer 模拟年龄门：/age_check 下发 cookie 后 302 回 rurl，
// 其余路径返回给定页面。
func newDMMTestServer(page string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/age_check/") {
			http.SetCookie(w, &http.Cookie{Name: "age_check_done", Value: "1"})
			http.Redirect(w, r, r.URL.Query().Get("rurl"), http.StatusFound)
			return
		}
		_, _ = w.Write([]byte(page))
	}))
}

func swapDMMBases(srv *httptest.Server) func() {
	old1, old2, old3 := dmmCoJPBase, dmmSoftBase, dmmGateBase
	dmmCoJPBase, dmmSoftBase, dmmGateBase = srv.URL, srv.URL, srv.URL
	return func() { dmmCoJPBase, dmmSoftBase, dmmGateBase = old1, old2, old3 }
}

// previewDMM 经年龄门解析 JSON-LD + informationList。
func TestPreviewDMM(t *testing.T) {
	srv := newDMMTestServer(dmmJSONLDPage)
	defer srv.Close()
	restore := swapDMMBases(srv)
	defer restore()

	res, err := previewDMM(context.Background(), dmmRef{Channel: "dc/doujin", CID: "d_123456"})
	if err != nil {
		t.Fatalf("preview err: %v", err)
	}
	if res.Source != "dmm" || res.ExternalID != "d_123456" {
		t.Fatalf("source/id wrong: %+v", res)
	}
	if res.Work.Title != "テスト同人音声" {
		t.Errorf("title=%s", res.Work.Title)
	}
	if res.Work.ReleaseDate != "2022-11-20" {
		t.Errorf("date=%s", res.Work.ReleaseDate)
	}
	if res.Work.CoverImageURL != "https://pics.dmm.co.jp/digital/doujin/d_123456/package.jpg" {
		t.Errorf("cover=%s", res.Work.CoverImageURL)
	}
	if !contains(res.Work.Tags, "ボイス・ASMR") || !contains(res.Work.Tags, "癒やし") || !contains(res.Work.Tags, "R-18") {
		t.Errorf("tags=%v", res.Work.Tags)
	}
	if res.MediaType != "audio" {
		t.Errorf("media type=%s", res.MediaType)
	}
	if len(res.Artists) != 1 || res.Artists[0].Name != "テストサークル" || res.Artists[0].RelationType != "created_by" {
		t.Fatalf("artists=%+v", res.Artists)
	}
	if len(res.Mediums) != 1 || res.Mediums[0].Format != "digital" {
		t.Fatalf("mediums=%+v", res.Mediums)
	}
}

// JSON-LD 缺失时 og:* 兜底。
func TestPreviewDMMOgFallback(t *testing.T) {
	srv := newDMMTestServer(dmmOGOnlyPage)
	defer srv.Close()
	restore := swapDMMBases(srv)
	defer restore()

	res, err := previewDMM(context.Background(), dmmRef{Channel: "mono/doujin", CID: "b_000001"})
	if err != nil {
		t.Fatalf("preview err: %v", err)
	}
	if res.Work.Title != "OGタイトル" || res.Work.Summary != "OG説明文" {
		t.Errorf("work=%+v", res.Work)
	}
	if res.Work.CoverImageURL != "https://pics.dmm.co.jp/ogcover.jpg" {
		t.Errorf("cover=%s", res.Work.CoverImageURL)
	}
	// mono/doujin：实体纸介质，mono/doujin 属成人频道带 R-18。
	if len(res.Mediums) != 1 || res.Mediums[0].Format != "paper" {
		t.Fatalf("mediums=%+v", res.Mediums)
	}
	if res.Release.DistributionChannel != "physical" {
		t.Errorf("channel=%s", res.Release.DistributionChannel)
	}
}

// 页面无任何可用名称 → upstream_error。
func TestPreviewDMMEmpty(t *testing.T) {
	srv := newDMMTestServer("<html><head></head><body></body></html>")
	defer srv.Close()
	restore := swapDMMBases(srv)
	defer restore()
	if _, err := previewDMM(context.Background(), dmmRef{Channel: "dc/doujin", CID: "d_000002"}); err == nil || !strings.Contains(err.Error(), "upstream_error") {
		t.Fatalf("err=%v, want upstream_error", err)
	}
}
